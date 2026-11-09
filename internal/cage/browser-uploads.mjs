import { randomUUID } from "node:crypto";
import { mkdir, open, rm } from "node:fs/promises";
import { basename, join } from "node:path";

export const uploadLimit = 2 * 1024 * 1024 * 1024;
export const validFileName = (name) =>
  typeof name === "string" &&
  name.length > 0 &&
  Buffer.byteLength(name) <= 255 &&
  basename(name) === name &&
  !/[\\\x00-\x1f]/.test(name) &&
  ![".", ".."].includes(name);

export class UploadManager {
  constructor({ directory, cdp, changed = () => {} }) {
    this.directory = directory;
    this.cdp = cdp;
    this.changed = changed;
    this.requests = new Map();
    this.retained = new Map();
  }

  async opened(tabId, sessionId, event) {
    if (!event.backendNodeId) return;
    this.cancelTab(tabId, false);
    const id = randomUUID();
    this.requests.set(id, {
      id,
      tabId,
      sessionId,
      node: event.backendNodeId,
      multiple: event.mode === "selectMultiple",
      files: [],
      busy: false,
      kind: "chooser",
    });
    this.changed();
  }

  snapshot() {
    return [...this.requests.values()].map(({ id, tabId, multiple, kind }) => ({
      id,
      tabId,
      multiple,
      kind,
    }));
  }

  start(tabId, sessionId, { kind, x, y }) {
    if (!["drop", "paste"].includes(kind)) throw new Error("文件传递方式无效");
    if (kind === "drop" && ![x, y].every(v => Number.isFinite(v) && v >= 0 && v <= 1))
      throw new Error("拖入位置无效");
    this.cancelTab(tabId, false);
    const id = randomUUID();
    this.requests.set(id, { id, tabId, sessionId, kind, x, y, multiple: true, files: [], busy: false });
    this.changed();
    return { id };
  }

  request(id) {
    const request = this.requests.get(id);
    if (!request)
      throw new Error("上传窗口已关闭或网页已刷新，请重新点击网页的上传按钮");
    return request;
  }

  async receive(id, name, stream) {
    const request = this.request(id);
    if (!validFileName(name)) throw new Error("文件名无效");
    if (request.busy || request.files.length >= (request.multiple ? 100 : 1))
      throw new Error("请选择允许数量的文件");
    request.busy = true;
    const directory = join(this.directory, id, randomUUID());
    const path = join(directory, name);
    let file,
      size = 0,
      accepted = false;
    try {
      await mkdir(directory, { recursive: true, mode: 0o700 });
      file = await open(path, "wx", 0o600);
      for await (const chunk of stream) {
        size += chunk.length;
        if (size > uploadLimit) throw new Error("每个文件不能超过 2 GB");
        if (this.requests.get(id) !== request) throw new Error("上传已取消");
        await file.writeFile(chunk);
      }
      await file.close();
      file = null;
      if (this.requests.get(id) !== request) throw new Error("上传已取消");
      request.files.push(path);
      accepted = true;
      return { ok: true, name, size };
    } finally {
      await file?.close();
      if (!accepted) await rm(directory, { recursive: true, force: true });
      request.busy = false;
    }
  }

  async finish(id) {
    const request = this.request(id);
    if (request.busy || !request.files.length)
      throw new Error("请先选择并传递文件");
    request.busy = true;
    try {
      if (request.kind === "chooser") {
        await this.cdp("DOM.setFileInputFiles", { backendNodeId: request.node, files: request.files }, request.sessionId);
      } else {
        const { result, exceptionDetails } = await this.cdp("Runtime.evaluate", {
          expression: `(() => {
            const e = document.activeElement, r = e?.getBoundingClientRect();
            return { width: innerWidth, height: innerHeight,
              x: r?.width ? Math.max(0, Math.min(innerWidth - 1, r.left + r.width / 2)) : innerWidth / 2,
              y: r?.height ? Math.max(0, Math.min(innerHeight - 1, r.top + r.height / 2)) : innerHeight / 2 };
          })()`, returnByValue: true,
        }, request.sessionId);
        if (exceptionDetails || !result?.value) throw new Error("无法确定文件接收位置");
        const viewport = result.value;
        const x = request.kind === "drop" ? Math.min(viewport.width - 1, request.x * viewport.width) : viewport.x;
        const y = request.kind === "drop" ? Math.min(viewport.height - 1, request.y * viewport.height) : viewport.y;
        for (const type of ["dragEnter", "dragOver", "drop"]) {
          if (this.requests.get(id) !== request) throw new Error("上传已取消");
          await this.cdp("Input.dispatchDragEvent", { type, x, y, data: { items: [], files: request.files, dragOperationsMask: 1 } }, request.sessionId);
        }
      }
      if (this.requests.get(id) !== request) throw new Error("上传已取消");
      this.requests.delete(id);
      // Chromium File objects can read lazily. Retain bytes until the document
      // is replaced or the tab closes, rather than deleting during submission.
      const retained = this.retained.get(request.tabId) || [];
      retained.push(id);
      this.retained.set(request.tabId, retained);
      this.changed();
      return { ok: true };
    } catch (error) {
      this.cancel(id);
      throw error;
    }
  }

  cancel(id) {
    if (!this.requests.delete(id)) return;
    void rm(join(this.directory, id), { recursive: true, force: true }).catch(
      () => {},
    );
    this.changed();
  }

  cancelTab(tabId, removeRetained = true) {
    for (const request of this.requests.values())
      if (request.tabId === tabId) this.cancel(request.id);
    if (removeRetained) {
      for (const id of this.retained.get(tabId) || [])
        void rm(join(this.directory, id), {
          recursive: true,
          force: true,
        }).catch(() => {});
      this.retained.delete(tabId);
    }
  }
}
