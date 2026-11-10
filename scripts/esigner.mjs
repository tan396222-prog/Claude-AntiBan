// Windows Authenticode signing via SSL.com's official Java tool. No release mutations.
import { createHash } from 'node:crypto';
import { execFileSync, spawn } from 'node:child_process';
import { copyFile, mkdir, mkdtemp, readFile, rm, stat, writeFile, access, open } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createInterface } from 'node:readline/promises';
import { Writable } from 'node:stream';

const root = fileURLToPath(new URL('../', import.meta.url));
const pin = JSON.parse(await readFile(new URL('../packaging/windows/signing.json', import.meta.url), 'utf8'));
const configPath = 'conf/code_sign_tool.properties';
const secretNames = ['CAGE_ESIGNER_PASSWORD', 'CAGE_ESIGNER_TOTP_SECRET'];
const providerCodes = ['server_error', 'invalid_grant', 'invalid_client', 'access_denied', 'unauthorized', 'invalid_request', 'invalid_otp', 'malware_detected'];
const rejectedOrUncertain = /timeout|timed out|abort|cancel|invalid_grant|invalid_client|access_denied|unauthorized|(?:otp|totp|authentication|authorization|credential|password)[^\n]*(?:invalid|expired|failed|rejected)|(?:invalid|expired|failed|rejected)[^\n]*(?:otp|totp|authentication|credential|password)/i;
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const toolDirectory = () => resolve(process.env.CAGE_ESIGNER_TOOL_DIR || join(root, '_output/esigner', `CodeSignTool-v${pin.version}`));

export function javaArgument(value) {
  // Java launcher @files, not a shell. Quotes/backslashes must round-trip literally.
  if (/[\r\n\0]/u.test(value)) throw new Error('Signing arguments must not contain newlines or NUL.');
  return `"${value.replaceAll('\\', '\\\\').replaceAll('"', '\\"')}"`;
}

export function redact(text, secrets) {
  for (const secret of secrets.filter(Boolean).sort((a, b) => b.length - a.length)) text = text.replaceAll(secret, '[redacted]');
  return text;
}

export async function verifyTool(directory) {
  for (const [name, digest] of [[pin.jar, pin.jarSHA256], [configPath, pin.configSHA256]]) {
    if (hash(await readFile(join(directory, name))) !== digest) throw new Error(`CodeSignTool checksum mismatch: ${name}`);
  }
}

async function installTool() {
  const directory = toolDirectory();
  try { await verifyTool(directory); return directory; }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
  await mkdir(dirname(directory), { recursive: true });
  const temp = await mkdtemp(join(dirname(directory), 'download-'));
  try {
    const archive = join(temp, 'CodeSignTool.zip');
    execFileSync(process.platform === 'win32' ? 'curl.exe' : 'curl',
      ['--fail', '--location', '--silent', '--show-error', '--max-time', '180', '--output', archive, pin.url], { stdio: 'inherit' });
    if (hash(await readFile(archive)) !== pin.archiveSHA256) throw new Error('CodeSignTool ZIP checksum mismatch. Review a new vendor version before changing the pin.');
    const extracted = join(temp, 'tool');
    await mkdir(extracted);
    execFileSync('tar', ['-xf', archive, '-C', extracted]);
    await verifyTool(extracted);
    // Copy only the pinned files. Vendor launchers and logs are never executed or reused.
    for (const name of [pin.jar, configPath]) {
      await mkdir(dirname(join(directory, name)), { recursive: true });
      await copyFile(join(extracted, name), join(directory, name));
    }
    await verifyTool(directory);
    return directory;
  } finally { await rm(temp, { recursive: true, force: true }); }
}

async function prompt(label, hidden = false, signal) {
  if (!process.stdin.isTTY) throw new Error(`Missing ${label}. Run this command in your own terminal, or configure the signing environment secrets.`);
  const sink = new Writable({ write(_chunk, _encoding, callback) { callback(); } });
  const rl = createInterface({ input: process.stdin, output: hidden ? sink : process.stdout, terminal: true });
  const controller = new AbortController();
  const abort = () => controller.abort();
  rl.once('SIGINT', abort);
  signal?.addEventListener('abort', abort, { once: true });
  try {
    if (signal?.aborted) controller.abort();
    if (hidden) process.stdout.write(`${label}: `);
    return await rl.question(hidden ? '' : `${label}: `, { signal: controller.signal });
  } finally {
    signal?.removeEventListener('abort', abort);
    rl.close();
    if (hidden) process.stdout.write('\n');
  }
}

// Exported for exercising the process boundary with an offline Java fixture.
export async function invokeJava({ java = 'java', jar, config, arguments: args, otp, secrets = [] }) {
  const temp = await mkdtemp(join(tmpdir(), 'cage-esigner-'));
  let child;
  const controller = new AbortController();
  const knownSecrets = [...secrets];
  try {
    if (process.platform !== 'win32') await (await import('node:fs/promises')).chmod(temp, 0o700);
    await mkdir(join(temp, 'conf'));
    await copyFile(config, join(temp, configPath));
    const argumentFile = join(temp, 'java.args');
    await writeFile(argumentFile, ['-Xmx2048m', '-jar', resolve(jar), ...args].map(javaArgument).join('\n'), { mode: 0o600, flag: 'wx' });
    const env = { ...process.env };
    for (const name of secretNames) delete env[name];
    // Only an argument-file path appears in the child process command line.
    child = spawn(java, [`@${argumentFile}`], { cwd: temp, env, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true });
    child.stdin.on('error', () => {}); // A failed signer can close stdin before an OTP arrives.
    let stdout = '', stderr = '', otpRequested = false, otpError;
    let otpTask = Promise.resolve();
    child.stdout.on('data', chunk => {
      stdout += chunk.toString();
      if (!otpRequested && stdout.includes('Enter the OTP')) {
        otpRequested = true;
        otpTask = (async () => {
          try {
            const value = (await otp(controller.signal)).trim();
            if (!/^\d{6}$/u.test(value)) throw new Error('The authenticator code must contain six digits.');
            knownSecrets.push(value);
            child.stdin.end(`${value}\n`);
          } catch (error) { otpError = error; child.kill(); }
        })();
      }
    });
    child.stderr.on('data', chunk => { stderr += chunk.toString(); });
    // Closing or interrupting a command must not leave a signer waiting indefinitely.
    const stop = () => { controller.abort(); child.kill(); };
    const timeout = setTimeout(stop, 15 * 60 * 1000);
    process.once('SIGINT', stop);
    process.once('SIGTERM', stop);
    let code;
    try {
      code = await new Promise((done, reject) => { child.once('error', reject); child.once('close', done); });
      controller.abort();
      await otpTask;
    } finally { clearTimeout(timeout); process.off('SIGINT', stop); process.off('SIGTERM', stop); }
    if (otpError) throw otpError;
    const output = redact(`${stdout}\n${stderr}`, knownSecrets);
    if (code !== 0 || /\b(?:Error:|Exception|Missing required option|Unmatched argument)/u.test(output)) {
      let privateLog = '';
      try { privateLog = await readFile(join(temp, 'logs', 'code_signing_tool.log'), 'utf8'); } catch { /* Optional diagnostic only. */ }
      const stages = { getMalwareScanResponse: 'malware-scan', getScanSettingsResponse: 'scan-settings', getCredentialsAuthorize: 'credentials-authorize', signHash: 'sign-hash', isOtpTypeOnline: 'otp-type', sendOtp: 'send-otp', getCredentialInfo: 'credentials-info', getCredentialIDs: 'credentials-list' };
      const method = /^\s*at com\.ssl\.code\.signing\.tool\.csc\.CscApi\.(getMalwareScanResponse|getScanSettingsResponse|getCredentialsAuthorize|signHash|isOtpTypeOnline|sendOtp|getCredentialInfo|getCredentialIDs)\(/m.exec(privateLog)?.[1];
      const failureStage = stages[method] || 'unknown';
      const rawCode = /"error"\s*:\s*"([a-z_]+)"/.exec(output)?.[1];
      const providerCode = providerCodes.includes(rawCode) ? rawCode : 'unclassified';
      const quotaExceeded = /QuotaExceededError/.test(privateLog + output);
      // Never attach the raw response or vendor log to the exception. These
      // fixed classifications are the only diagnostic values leaving memory.
      throw Object.assign(new Error(`CodeSignTool failed (exit ${code}). stage=${failureStage}; error=${providerCode}; quotaExceeded=${quotaExceeded}.`), {
        failureStage, providerCode, quotaExceeded, nonRetryable: quotaExceeded || rejectedOrUncertain.test(output),
      });
    }
    return output;
  } finally {
    controller.abort();
    child?.kill();
    // This also removes vendor logs, which may contain authentication responses.
    await rm(temp, { recursive: true, force: true });
  }
}

export async function assertWindowsExecutable(path) {
  if (!(await stat(path)).isFile()) throw new Error('Provide a Windows PE executable.');
  // NSIS passes a temporary uninstaller with no .exe suffix. Check the file format.
  const file = await open(path, 'r');
  try {
    const header = Buffer.alloc(64);
    const pe = Buffer.alloc(4);
    const first = await file.read(header, 0, 64, 0);
    if (first.bytesRead !== 64 || header.readUInt16LE(0) !== 0x5a4d) throw new Error('Input is not a Windows PE executable.');
    const signature = await file.read(pe, 0, 4, header.readUInt32LE(60));
    if (signature.bytesRead !== 4 || pe.readUInt32LE(0) !== 0x4550) throw new Error('Input is not a Windows PE executable.');
  } finally { await file.close(); }
}

export async function prepareVendorInput(input, stage) {
  // CodeSignTool chooses its signing format by extension, while NSIS omits it.
  // Give the vendor a separate .exe copy; keep the caller's filename and bytes intact.
  const path = join(stage, 'input', 'payload.exe');
  const output = join(stage, 'signed');
  await mkdir(dirname(path));
  await mkdir(output);
  await copyFile(input, path);
  return { path, output };
}

async function credentials() {
  const username = process.env.CAGE_ESIGNER_USERNAME || await prompt('SSL.com username');
  const password = process.env.CAGE_ESIGNER_PASSWORD || await prompt('SSL.com password', true);
  const credential = process.env.CAGE_ESIGNER_CREDENTIAL_ID || await prompt('eSigner credential ID');
  if (!username || !password || !/^[a-f0-9-]{36}$/iu.test(credential)) throw new Error('A username, password and valid eSigner credential ID are required.');
  return { username, password, credential, totp: process.env.CAGE_ESIGNER_TOTP_SECRET };
}

// A provider-declared server error is a definite failure. Timeouts, OTP and
// authentication errors are not retried. Any output proceeds to the caller's
// existing Authenticode verification; it must never trigger another signature.
export async function invokeSigning({ invoke, outputPath, pause = ms => new Promise(done => setTimeout(done, ms)) }) {
  for (let attempt = 0; attempt < 3; attempt++) {
    try { return await invoke(); }
    catch (error) {
      try { await access(outputPath); return ''; }
      catch (outputError) { if (outputError.code !== 'ENOENT') throw error; }
      const uncertainOrRejected = error.nonRetryable || error.quotaExceeded || rejectedOrUncertain.test(error.message) || /QuotaExceededError/.test(error.message);
      const serverError = error.providerCode === 'server_error' || /"error"\s*:\s*"server_error"/.test(error.message);
      if (attempt === 2 || uncertainOrRejected || !/^CodeSignTool failed \(exit 11\)/.test(error.message) || !serverError) throw error;
      console.error(`Signing service returned server_error; retrying in ${(attempt + 1) * 15} seconds.`);
      await pause((attempt + 1) * 15000);
    }
  }
}

// Diagnostic output is an allowlist. Never return vendor JSON, identities,
// certificate data, tokens or free-form error messages to CI logs.
export function credentialDiagnostic(error) {
  if (!error) return { operation: 'credential_info', success: true, stage: 'csc-credential-info', exitCode: 0, error: '' };
  const match = /^CodeSignTool failed \(exit ([0-9]{1,3})\)/.exec(error.message);
  const exitCode = match && Number(match[1]) <= 255 ? Number(match[1]) : null;
  const code = error.providerCode || /"error"\s*:\s*"([a-z_]+)"/.exec(error.message)?.[1];
  return { operation: 'credential_info', success: false, stage: exitCode === 1 ? 'oauth-token' : exitCode === 2 ? 'csc-credential-info' : 'client', exitCode, error: providerCodes.includes(code) ? code : 'unclassified' };
}

async function main(argv) {
  const [command, input, flag, destination] = argv;
  if (command === 'install' && argv.length === 1) { console.log(`Verified CodeSignTool ${pin.version}: ${await installTool()}`); return; }
  if (!['info', 'diagnose', 'sign'].includes(command) || (['info', 'diagnose'].includes(command) && argv.length !== 1) ||
    (command === 'sign' && (argv.length !== 4 || flag !== '--output-dir'))) {
    throw new Error('Usage: node scripts/esigner.mjs install | info | diagnose | sign INPUT.exe --output-dir DIRECTORY');
  }
  if (command === 'sign') await assertWindowsExecutable(resolve(input));
  const directory = await installTool();
  const auth = await credentials();
  if (command === 'sign' && !auth.totp && !process.stdin.isTTY) throw new Error('Non-interactive signing requires CAGE_ESIGNER_TOTP_SECRET.');
  const args = [command === 'sign' ? 'sign' : 'credential_info', `-username=${auth.username}`, `-password=${auth.password}`, `-credential_id=${auth.credential}`];
  let staged, output, vendor;
  try {
    if (command === 'sign') {
      const path = resolve(input);
      await mkdir(resolve(destination), { recursive: true });
      output = join(resolve(destination), basename(path));
      try { await access(output); throw new Error('Signed output already exists; use a fresh output directory.'); }
      catch (error) { if (error.code !== 'ENOENT') throw error; }
      staged = await mkdtemp(join(resolve(destination), '.sign-'));
      vendor = await prepareVendorInput(path, staged);
      args.push(`-input_file_path=${vendor.path}`, `-output_dir_path=${vendor.output}`);
      if (auth.totp) args.push(`-totp_secret=${auth.totp}`);
    }
    const invoke = () => invokeJava({ jar: join(directory, pin.jar), config: join(directory, configPath), arguments: args,
      otp: signal => prompt('Current six-digit authenticator code', true, signal), secrets: [auth.password, auth.totp] });
    const result = command === 'sign' ? await invokeSigning({ invoke, outputPath: join(vendor.output, basename(vendor.path)) }) : await invoke();
    if (command === 'sign') {
      const signed = join(vendor.output, basename(vendor.path));
      const bytes = await readFile(signed);
      if (!bytes.length || hash(bytes) === hash(await readFile(resolve(input)))) throw new Error('CodeSignTool did not produce a changed signed file.');
      await copyFile(signed, output, (await import('node:fs')).constants.COPYFILE_EXCL);
      console.log(`Signed copy: ${output}\nWindows trust and publisher verification are still required.`);
    } else if (command === 'diagnose') console.log(JSON.stringify(credentialDiagnostic()));
    else console.log(result.trim());
  } finally { if (staged) await rm(staged, { recursive: true, force: true }); }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).catch(error => {
    if (process.argv[2] === 'diagnose') console.error(JSON.stringify(credentialDiagnostic(error)));
    else console.error(redact(error.message, secretNames.map(name => process.env[name])));
    process.exitCode = 1;
  });
}
