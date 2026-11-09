import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { assertWindowsExecutable, invokeJava, javaArgument, prepareVendorInput, redact, verifyTool } from './esigner.mjs';

test('Java arguments reject line injection and redact overlapping secrets', () => {
  for (const value of ['line\nbreak', 'line\rbreak', 'a\0b']) assert.throws(() => javaArgument(value));
  assert.equal(redact('secret-long and secret', ['secret', 'secret-long']), '[redacted] and [redacted]');
});

test('a real JVM receives quoted Unicode arguments, no secrets in process arguments, and OTP through stdin', async t => {
  const temp = await mkdtemp(join(tmpdir(), 'cage-esigner-test-'));
  t.after(() => rm(temp, { recursive: true, force: true }));
  const source = join(temp, 'Signer.java');
  const config = join(temp, 'config.properties');
  await writeFile(config, 'TEST=true\n');
  await writeFile(source, `
import java.nio.file.*;
import java.util.*;
public class Signer {
  public static void main(String[] args) throws Exception {
    String temp = System.getProperty("user.dir");
    Files.writeString(Path.of(args[0]), temp);
    String command = ProcessHandle.current().info().commandLine().orElse("");
    if (!command.contains("@") || command.contains("password=") || command.contains("totp_secret=")) System.exit(13);
    if (System.getenv("CAGE_ESIGNER_PASSWORD") != null || System.getenv("CAGE_ESIGNER_TOTP_SECRET") != null) System.exit(14);
    if (!Files.readString(Path.of("conf", "code_sign_tool.properties")).contains("TEST=true")) System.exit(15);
    for (int i = 1; i < args.length; i++) System.out.println("ARG[" + i + "]=" + args[i]);
    Files.createDirectories(Path.of("logs"));
    Files.writeString(Path.of("logs", "sensitive.log"), "must be removed");
    System.out.print("Enter the OTP - Press enter to continue:");
    System.out.flush();
    String otp = new Scanner(System.in).nextLine();
    if (!otp.equals("123456")) System.exit(16);
    System.out.println(otp);
    if (Arrays.asList(args).contains("fail")) { System.out.println("Error: dummy-password"); System.exit(4); }
  }
}
`);
  execFileSync('javac', ['-d', temp, source]);
  const jar = join(temp, 'fixture.jar');
  execFileSync('jar', ['--create', '--file', jar, '--main-class', 'Signer', '-C', temp, 'Signer.class']);
  const values = [' spaces ', 'quote"slash\\tab\t', '中文路径/空 格.exe', '#hash', '@value', '-password=dummy-password'];
  for (const fail of [false, true]) {
    const marker = join(temp, fail ? 'failure-marker' : 'success-marker');
    let calls = 0;
    const previous = process.env.CAGE_ESIGNER_PASSWORD;
    process.env.CAGE_ESIGNER_PASSWORD = 'dummy-password';
    try {
      const operation = invokeJava({ jar, config, arguments: [marker, ...values, ...(fail ? ['fail'] : [])],
        otp: async () => { calls++; return '123456'; }, secrets: ['dummy-password'] });
      if (fail) {
        await assert.rejects(operation, error => !error.message.includes('dummy-password') && !error.message.includes('123456') && /exit 4/u.test(error.message));
      } else {
        const output = await operation;
        for (const [i, value] of values.entries()) assert.ok(output.includes(`ARG[${i + 1}]=${redact(value, ['dummy-password'])}`));
        assert.ok(!output.includes('123456'));
      }
      assert.equal(calls, 1);
      const working = await readFile(marker, 'utf8');
      await assert.rejects(readFile(join(working, 'java.args')), { code: 'ENOENT' });
      await assert.rejects(readFile(join(working, 'logs/sensitive.log')), { code: 'ENOENT' });
    } finally {
      if (previous === undefined) delete process.env.CAGE_ESIGNER_PASSWORD;
      else process.env.CAGE_ESIGNER_PASSWORD = previous;
    }
  }
});

test('modified vendor binaries are refused before executing Java', async t => {
  const temp = await mkdtemp(join(tmpdir(), 'cage-esigner-tampered-'));
  t.after(() => rm(temp, { recursive: true, force: true }));
  const { mkdir } = await import('node:fs/promises');
  await mkdir(join(temp, 'jar'));
  await writeFile(join(temp, 'jar/code_sign_tool-1.3.3.jar'), 'modified');
  await assert.rejects(verifyTool(temp), /checksum mismatch/u);
});

test('NSIS temporary executables without an extension are accepted, unrelated files are refused', async t => {
  const temp = await mkdtemp(join(tmpdir(), 'cage-esigner-pe-'));
  t.after(() => rm(temp, { recursive: true, force: true }));
  const path = join(temp, 'makensis123');
  const bytes = Buffer.alloc(132);
  bytes.writeUInt16LE(0x5a4d, 0);
  bytes.writeUInt32LE(128, 60);
  bytes.writeUInt32LE(0x4550, 128);
  await writeFile(path, bytes);
  await assertWindowsExecutable(path);
  const prepared = await prepareVendorInput(path, temp);
  assert.ok(prepared.path.endsWith('.exe'), 'The vendor requires an extension to choose Authenticode.');
  assert.deepEqual(await readFile(prepared.path), bytes);
  assert.deepEqual(await readFile(path), bytes);
  bytes.writeUInt32LE(0, 128);
  await writeFile(path, bytes);
  await assert.rejects(assertWindowsExecutable(path), /not a Windows PE/u);
  await writeFile(path, 'plain text');
  await assert.rejects(assertWindowsExecutable(path), /not a Windows PE/u);
});

test('release readiness accepts eSigner without a PFX, requires all secrets and rejects unknown providers', () => {
  const script = fileURLToPath(new URL('./check-release-readiness.mjs', import.meta.url));
  const env = {
    ...process.env,
    CAGE_MACOS_P12_BASE64: 'fixture', CAGE_MACOS_P12_PASSWORD: 'fixture', CAGE_MACOS_SIGNING_IDENTITY: 'Developer ID Application: Fixture',
    CAGE_MACOS_NOTARY_KEY_BASE64: 'fixture', CAGE_MACOS_NOTARY_KEY_ID: 'fixture', CAGE_MACOS_NOTARY_ISSUER_ID: 'fixture',
    CAGE_WINDOWS_QEMU_SOURCE_URL: 'https://example.invalid/source.zip', CAGE_WINDOWS_QEMU_SOURCE_SHA256: 'a'.repeat(64),
    CAGE_WINDOWS_SIGNING_PROVIDER: 'esigner', CAGE_ESIGNER_USERNAME: 'fixture', CAGE_ESIGNER_PASSWORD: 'private-fixture-password',
    CAGE_ESIGNER_CREDENTIAL_ID: '11111111-2222-3333-4444-555555555555', CAGE_ESIGNER_TOTP_SECRET: 'private-fixture-seed',
    CAGE_ESIGNER_CERTIFICATE_SERIAL: '7'.repeat(32), CAGE_WINDOWS_PFX_BASE64: '', CAGE_WINDOWS_PFX_PASSWORD: '',
  };
  const run = change => spawnSync(process.execPath, [script], { env: { ...env, ...change }, encoding: 'utf8' });
  assert.equal(run({}).status, 0);
  for (const name of ['CAGE_ESIGNER_USERNAME', 'CAGE_ESIGNER_PASSWORD', 'CAGE_ESIGNER_CREDENTIAL_ID', 'CAGE_ESIGNER_TOTP_SECRET', 'CAGE_ESIGNER_CERTIFICATE_SERIAL']) {
    const result = run({ [name]: '' });
    assert.equal(result.status, 1);
    assert.ok(result.stderr.includes(name));
    assert.ok(!result.stderr.includes('private-fixture-password') && !result.stderr.includes('private-fixture-seed'));
  }
  assert.equal(run({ CAGE_WINDOWS_SIGNING_PROVIDER: 'typo' }).status, 1);
  assert.equal(run({ CAGE_ESIGNER_CERTIFICATE_SERIAL: 'bad' }).status, 1);
  assert.equal(run({ CAGE_WINDOWS_SIGNING_PROVIDER: '', CAGE_WINDOWS_PFX_BASE64: 'fixture', CAGE_WINDOWS_PFX_PASSWORD: 'fixture' }).status, 0);
  assert.equal(run({ CAGE_WINDOWS_SIGNING_PROVIDER: '' }).status, 1);
});
