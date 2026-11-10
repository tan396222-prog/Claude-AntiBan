import { mkdirSync, readFileSync, writeFileSync, renameSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID, createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
const run = promisify(execFile);
const extensionID = /^[a-p]{32}$/;
const idFor = value => createHash('sha256').update(value).digest('hex').slice(0, 32)
  .replace(/./g, char => String.fromCharCode(97 + parseInt(char, 16)));
export const extensionUploadLimit = 32 << 20;

export class BrowserExtensions {
  constructor({ directory, cdp, control, protectedID, extractArchive, idForPath = idFor }) {
    this.root = join(directory, 'extensions');
    this.cdp = cdp; this.control = control; this.protectedID = protectedID;
    this.idForPath = idForPath;
    this.extractArchive = extractArchive || (async (archive, destination) => {
      const { stdout } = await run('/usr/bin/python3', [fileURLToPath(new URL('./browser-extension-archive.py', import.meta.url)), archive, destination], { maxBuffer: 1 << 20, timeout: 15000 });
      return JSON.parse(stdout);
    });
    this.records = new Map(); this.pending = new Map();
    mkdirSync(join(this.root, 'packages'), { recursive: true, mode: 0o700 });
    rmSync(join(this.root, 'staging'), { recursive: true, force: true });
    mkdirSync(join(this.root, 'staging'), { mode: 0o700 });
    try {
      for (const entry of JSON.parse(readFileSync(join(this.root, 'registry.json'), 'utf8'))) {
        if (extensionID.test(entry.id) && /^[a-f0-9-]{36}$/.test(entry.package) &&
            typeof entry.relative === 'string' && entry.relative !== '..' && /^[^/\\]+$/.test(entry.relative))
          this.records.set(entry.id, entry);
      }
    } catch {}
  }
  persist() {
    const temporary = join(this.root, 'registry.json.tmp');
    writeFileSync(temporary, JSON.stringify([...this.records.values()]), { mode: 0o600 });
    renameSync(temporary, join(this.root, 'registry.json'));
  }
  path(entry) { return join(this.root, 'packages', entry.package, entry.relative); }
  async management(input) {
    const evaluate = async repair => this.cdp('Runtime.evaluate', {
      expression: `manageCageExtensions(${JSON.stringify(input)})`, awaitPromise: true,
      returnByValue: true, userGesture: true,
    }, await this.control(repair), 15000);
    let response;
    try { response = await evaluate(false); }
    catch { response = await evaluate(true); }
    if (response.exceptionDetails && /not defined|context|session/i.test(response.exceptionDetails.exception?.description || response.exceptionDetails.text || '')) response = await evaluate(true);
    if (response.exceptionDetails || !response.result?.value) throw new Error('扩展操作未完成，请重试或打开 Chromium 扩展管理');
    return response.result.value;
  }
  async restore() {
    for (const entry of this.records.values()) {
      if (!entry.enabled) continue;
      try {
        const { id } = await this.cdp('Extensions.loadUnpacked', { path: this.path(entry) });
        if (id !== entry.id) throw new Error('扩展标识已变化');
        entry.error = '';
      } catch { entry.error = '扩展未能加载，请检查扩展文件或重新安装'; }
    }
  }
  async upload(stream) {
    if (this.pending.size >= 4) throw new Error('请先安装或取消之前选择的扩展');
    const token = randomUUID(), directory = join(this.root, 'staging', token), archive = directory + '.zip';
    const chunks = []; let size = 0;
    try {
      for await (const chunk of stream) {
        size += chunk.length;
        if (size > extensionUploadLimit) throw new Error('扩展 ZIP 不能超过 32 MB');
        chunks.push(chunk);
      }
      writeFileSync(archive, Buffer.concat(chunks), { mode: 0o600 });
      mkdirSync(directory, { mode: 0o700 });
      const entry = await this.extractArchive(archive, directory);
      this.pending.set(token, { ...entry, directory });
      return { session: token, name: entry.name, version: entry.version, description: entry.description,
        permissions: entry.permissions, hostPermissions: entry.hostPermissions };
    } catch (error) {
      rmSync(directory, { recursive: true, force: true });
      throw new Error(error.stderr?.trim().split('\n').at(-1)?.replace(/^ValueError: /, '') || error.message);
    } finally { rmSync(archive, { force: true }); }
  }
  async request(input) {
    const mode = input.mode || 'list';
    if (mode === 'cancel' || mode === 'install') {
      const entry = this.pending.get(input.session);
      if (!entry) throw new Error('扩展选择已过期，请重新选择 ZIP');
      if (mode === 'cancel') {
        rmSync(entry.directory, { recursive: true, force: true }); this.pending.delete(input.session);
        return { ok: true };
      }
      const packageID = input.session, directory = join(this.root, 'packages', packageID);
      if (input.target && this.records.size && [...this.records.values()].some(item => item.source === input.target)) throw new Error('此本机扩展已经导入');
      const expectedID = entry.key ? idFor(Buffer.from(entry.key, 'base64')) : this.idForPath(join(directory, entry.relative));
      if (expectedID === this.protectedID || this.records.has(expectedID)) throw new Error('此扩展已经安装，请先移除原有版本');
      renameSync(entry.directory, directory); this.pending.delete(input.session);
      let id, inserted = false;
      try {
        if (input.enabled === false) id = expectedID;
        else ({ id } = await this.cdp('Extensions.loadUnpacked', { path: join(directory, entry.relative) }));
        if (id === this.protectedID || this.records.has(id)) throw new Error('此扩展标识已存在');
        const { directory: unused, key: publicKey, ...metadata } = entry;
        this.records.set(id, { ...metadata, id, package: packageID, enabled: input.enabled !== false,
          ...(typeof input.target === 'string' && /^[a-f0-9]{32}:[a-p]{32}$/.test(input.target) ? { source: input.target } : {}),
        });
        inserted = true;
        this.persist();
      } catch (error) {
        if (inserted) this.records.delete(id);
        if (id && id !== this.protectedID && !this.records.has(id)) await this.cdp('Extensions.uninstall', { id }).catch(() => {});
        rmSync(directory, { recursive: true, force: true }); throw error;
      }
      return this.request({ mode: 'list' });
    }
    if (!['list', 'enable', 'disable', 'remove', 'options'].includes(mode)) throw new Error('扩展操作无效');
    if (mode !== 'list' && (!extensionID.test(input.id || '') || input.id === this.protectedID)) throw new Error('扩展标识无效');
    const record = this.records.get(input.id);
    if (mode === 'enable' && record) {
      const loaded = await this.cdp('Extensions.loadUnpacked', { path: this.path(record) });
      if (loaded.id !== input.id) {
        await this.cdp('Extensions.uninstall', { id: loaded.id }).catch(() => {});
        throw new Error('扩展标识已变化，请重新安装');
      }
    }
    if (mode === 'remove') {
      if (!record) return { native: true, id: input.id };
      const installed = (await this.management({ mode: 'list' })).items.some(item => item.id === input.id);
      if (installed) await this.cdp('Extensions.uninstall', { id: input.id });
      this.records.delete(input.id);
      try { this.persist(); } catch (error) { this.records.set(input.id, record); throw error; }
      rmSync(join(this.root, 'packages', record.package), { recursive: true, force: true });
    } else if (mode !== 'list') {
      const result = await this.management({ mode, id: input.id });
      if (mode === 'options') return result;
      if (record) { record.enabled = mode === 'enable'; record.error = ''; this.persist(); }
    }
    const { items } = await this.management({ mode: 'list' });
    const byID = new Map(items.map(item => [item.id, { ...item, managed: this.records.has(item.id) }]));
    for (const entry of this.records.values()) {
      if (!byID.has(entry.id)) byID.set(entry.id, { ...entry, enabled: false, managed: true, optionsUrl: '' });
      if (entry.source) byID.get(entry.id).source = entry.source;
      if (entry.error) byID.get(entry.id).error = entry.error;
    }
    return { items: [...byID.values()].map(({ directory, package: path, relative, ...entry }) => entry) };
  }
}
