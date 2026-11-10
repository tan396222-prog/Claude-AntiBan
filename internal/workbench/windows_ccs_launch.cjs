'use strict';

// Runs inside the real CCS parent. Only its fixed native Claude spawn is
// wrapped; all proxy and temporary-profile lifetime remains owned by CCS.
const cp = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');

const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-[1-5][a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/i;
const profile = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/;
function readObject(filename) {
  const info = fs.lstatSync(filename);
  if (!info.isFile() || info.isSymbolicLink() || info.size > 1 << 20) throw new Error('ccs_launch_file_invalid');
  const value = JSON.parse(fs.readFileSync(filename, 'utf8'));
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('ccs_launch_file_invalid');
  return value;
}
function samePath(a, b) {
  const normalize = value => process.platform === 'win32' ? path.resolve(value).toLowerCase() : path.resolve(value);
  return typeof a === 'string' && typeof b === 'string' && normalize(a) === normalize(b);
}
function settingsSource(args) {
  let source = '';
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--settings') { if (typeof args[++i] !== 'string' || source) return ''; source = args[i]; }
    else if (args[i].startsWith('--settings=')) { if (source) return ''; source = args[i].slice(11); }
  }
  return source && !source.trimStart().startsWith('{') && path.isAbsolute(source) ? path.resolve(source) : '';
}
function install(context, originalSpawn = cp.spawn) {
  const previousSpawn = cp.spawn;
  if (!uuid.test(context.id) || !uuid.test(context.session_id) || !profile.test(context.profile)) throw new Error('ccs_launch_context_invalid');
  const folder = path.resolve(context.folder);
  if (fs.lstatSync(folder).isSymbolicLink()) throw new Error('ccs_launch_directory_invalid');
  let captured = false;
  cp.spawn = function (command, args, options) {
    if (!samePath(command, context.binary) || !Array.isArray(args) || options?.shell ||
        !args.some((arg, index) => (arg === '--session-id' || arg === '--resume') && args[index + 1] === context.session_id)) {
      return originalSpawn.apply(this, arguments);
    }
    if (captured) throw new Error('ccs_duplicate_claude_spawn');
    captured = true;
    if (args.length > 1024 || args.some(arg => typeof arg !== 'string' || arg.includes('\0'))) throw new Error('ccs_launch_arguments_invalid');
    const source = settingsSource(args);
    const metadata = {id:context.id, session_id:context.session_id, profile:context.profile,
      actual_args:args, settings_source:source, expected_settings_source:context.settings_source || '',
      profile_type:options?.env?.CCS_PROFILE_TYPE || ''};
    const filename = path.join(folder, `ccs-actual-${crypto.randomUUID()}.json`);
    if (Buffer.byteLength(JSON.stringify(metadata)) > 1 << 20) throw new Error('ccs_launch_arguments_limit');
    fs.writeFileSync(filename, JSON.stringify(metadata), {flag:'wx', mode:0o600});
    let child;
    try {
      child = originalSpawn(context.python, ['-X','utf8','-I',context.helper,'ccs-live',filename],
        {...options, env:{...(options?.env || process.env), CAGE_CCS_ACTUAL:'1'}});
    } catch (error) { fs.unlinkSync(filename); throw error; }
    const cleanup = () => { try { fs.unlinkSync(filename); } catch (error) { if (error.code !== 'ENOENT') throw error; } };
    child.once('exit', cleanup); child.once('error', cleanup);
    return child;
  };
  return () => { cp.spawn = previousSpawn; };
}

if (process.env.CAGE_CCS_LAUNCH_FILE) {
  const filename = process.env.CAGE_CCS_LAUNCH_FILE;
  const context = readObject(filename);
  if (!samePath(path.dirname(filename), context.folder) || path.basename(filename) !== 'ccs-launch.json') throw new Error('ccs_launch_context_invalid');
  // This reads only the selected guest-owned CCS profile. No host configuration
  // or credentials are imported. The actual child remains authoritative.
  try {
    const manager = require(path.join(path.dirname(context.main), 'utils', 'config-manager.js'));
    const source = manager.getSettingsPath(context.profile);
    if (typeof source === 'string' && path.isAbsolute(source)) context.settings_source = source;
  } catch { context.settings_source = ''; }
  install(context);
}
module.exports = {install, readObject, samePath, settingsSource};
