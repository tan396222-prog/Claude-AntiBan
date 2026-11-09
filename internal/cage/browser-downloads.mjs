import { closeSync, existsSync, fsyncSync, lstatSync, mkdirSync, openSync, readSync, renameSync, unlinkSync, writeFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { basename, join } from "node:path";

const historyLimit = 50;
const activeLimit = 50;
const guidPattern = /^[a-f\d]{8}(?:-[a-f\d]{4}){3}-[a-f\d]{12}$/i;
const terminalStates = new Set(["saved", "failed", "canceled"]);

export class DownloadManager {
  constructor({ directory, save, changed = () => {}, cancel = () => {} }) {
    this.directory = directory;
    this.manifest = join(directory, "download-state.json");
    this.save = save;
    this.changed = changed;
    this.cancel = cancel;
    this.records = new Map();
    this.queue = Promise.resolve();
    this.lastProgress = 0;
    mkdirSync(join(directory, "Downloads"), { recursive: true });
    this.restore();
  }

  sourceExists(id) {
    try {
      return guidPattern.test(id) && lstatSync(join(this.directory, "Downloads", id)).isFile();
    } catch { return false; }
  }

  restore() {
    try {
      if (!existsSync(this.manifest)) return;
      const fd = openSync(this.manifest, "r");
      let data;
      try {
        const buffer = Buffer.alloc((1 << 20) + 1);
        let size = 0, n;
        while (size < buffer.length && (n = readSync(fd, buffer, size, buffer.length - size, null)) > 0) size += n;
        if (size > 1 << 20) throw new Error("Download manifest too large");
        data = buffer.subarray(0, size);
      } finally { closeSync(fd); }
      const records = JSON.parse(data);
      if (!Array.isArray(records) || records.length > activeLimit + historyLimit) throw new Error("Invalid download manifest");
      for (const item of records) {
        if (!guidPattern.test(item.id) || typeof item.name !== "string" || item.name.length > 1024 || basename(item.name) !== item.name) continue;
        const record = { id: item.id, name: item.name, state: item.state,
          receivedBytes: Number(item.receivedBytes) || 0, totalBytes: Number(item.totalBytes) || 0,
          updatedAt: item.updatedAt, sourceReady: item.sourceReady === true,
          ...(typeof item.path === "string" && item.path.length < 8192 ? { path: item.path } : {}),
          ...(typeof item.error === "string" ? { error: item.error.slice(0, 300) } : {}) };
        if (record.state === "downloading") {
          record.state = "failed";
          record.sourceReady = false;
          record.error = "下载被中断，请从原网页重新下载。";
        } else if (record.state === "saving") {
          record.state = "failed";
          record.error = "保存过程被中断。源文件仍在时可以重试；否则请检查 Downloads 中的文件。";
        }
        if (!terminalStates.has(record.state)) continue;
        record.retryable = record.state === "failed" && record.sourceReady && this.sourceExists(record.id);
        this.records.set(record.id, record);
      }
      this.persist();
    } catch {
      // Do not trust or expose arbitrary content from a damaged manifest.
      this.records.clear();
    }
  }

  prune() {
    const finished = [...this.records.values()].filter(r => terminalStates.has(r.state));
    for (const record of finished.slice(0, Math.max(0, finished.length - historyLimit))) this.records.delete(record.id);
  }

  persist() {
    this.prune();
    const temporary = this.manifest + "." + randomUUID() + ".tmp";
    let fd;
    try {
      fd = openSync(temporary, "wx", 0o600);
      writeFileSync(fd, JSON.stringify([...this.records.values()]));
      fsyncSync(fd);
      closeSync(fd);
      fd = undefined;
      renameSync(temporary, this.manifest);
      return true;
    } catch {
      if (fd !== undefined) closeSync(fd);
      try { unlinkSync(temporary); } catch {}
      return false;
    }
  }

  publish(record, persist = true) {
    record.updatedAt = new Date().toISOString();
    const saved = !persist || this.persist();
    if (!saved) {
      record.error = "无法保存下载记录，请检查客机可用空间。";
    }
    this.changed();
    return saved;
  }

  begin(id, name) {
    if (!guidPattern.test(id) || typeof name !== "string" || !name || name.length > 1024 || basename(name) !== name) return;
    const active = [...this.records.values()].filter(r => !terminalStates.has(r.state)).length;
    const record = { id, name, state: "downloading", receivedBytes: 0, totalBytes: 0, retryable: false, sourceReady: false };
    if (active >= activeLimit) {
      record.state = "canceled";
      record.error = "同时下载的文件过多，请等待部分下载完成后重试。";
      void Promise.resolve(this.cancel(id)).catch(() => {});
    }
    this.records.set(id, record);
    this.publish(record);
  }

  progress({ guid, state, receivedBytes, totalBytes }) {
    const record = this.records.get(guid);
    if (!record || record.state !== "downloading") return;
    record.receivedBytes = Number(receivedBytes) || 0;
    record.totalBytes = Number(totalBytes) || 0;
    if (state === "completed") {
      record.sourceReady = true;
      this.enqueue(record);
    } else if (state === "canceled") {
      record.state = "canceled";
      record.error = "下载已取消，请从原网页重新下载。";
      this.publish(record);
    } else if (Date.now() - this.lastProgress >= 500) {
      this.lastProgress = Date.now();
      this.publish(record, false);
    }
  }

  retry(id) {
    const record = this.records.get(id);
    if (record?.state === "saving") return { ok: true };
    if (!record || record.state !== "failed" || !record.sourceReady || !this.sourceExists(id)) {
      if (record) {
        record.retryable = false;
        record.error = "下载源文件已不可用，请从原网页重新下载。";
        this.publish(record);
      }
      throw new Error("下载源文件已不可用，请从原网页重新下载。");
    }
    this.enqueue(record);
    return { ok: true };
  }

  enqueue(record) {
    if (!this.sourceExists(record.id)) {
      record.state = "failed";
      record.retryable = false;
      record.error = "下载源文件不可用，请从原网页重新下载。";
      this.publish(record);
      return;
    }
    record.state = "saving";
    record.retryable = false;
    delete record.error;
    if (!this.publish(record)) {
      record.state = "failed";
      record.retryable = true;
      this.changed();
      return;
    }
    this.queue = this.queue.then(async () => {
      try {
        const result = await this.save(record.id, record.name);
        if (!result?.ok || typeof result.path !== "string") throw new Error(result?.error || "保存结果无效，请检查共享目录后重试。");
        record.state = "saved";
        record.path = result.path;
        record.name = result.name || record.name;
        record.receivedBytes = result.bytes ?? record.receivedBytes;
        if (result.warning) record.error = result.warning;
      } catch (error) {
        record.state = "failed";
        record.error = String(error.message || "保存失败，请检查共享目录后重试。").slice(0, 300);
        record.retryable = this.sourceExists(record.id);
      }
      this.publish(record);
    });
  }

  snapshot() {
    return [...this.records.values()].reverse().map(({ sourceReady, ...record }) => record);
  }

  sourceFile(id) {
    const record = this.records.get(id);
    if (!record?.sourceReady || record.state === "downloading" || record.state === "saving" || !this.sourceExists(id))
      throw new Error("下载源文件不可用或尚未完成");
    return join(this.directory, "Downloads", id);
  }
}
