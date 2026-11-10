export async function copyGuestText({ request, sendCopy, valid }) {
  const before = await request({ phase: "prepare" });
  if (!valid() || typeof before.revision !== "number" || typeof before.session !== "string") throw new Error("复制窗口已变化，请重新选择文本。");
  await sendCopy();
  if (!valid()) throw new Error("复制窗口已变化，请重新选择文本。");
  const result = await request({ phase: "read", revision: before.revision, session: before.session });
  if (!valid() || result.session !== before.session || result.revision === before.revision || typeof result.text !== "string") {
    throw new Error("未收到新的选中文本，请重新选择后复制。");
  }
  return { text: result.text };
}

// One connection owns clipboard synchronization for the workbench window,
// including while its remote canvas is hidden or the window is unfocused.
export function shareClipboard({ connect, native, onStatus, setActive, interval = 300 }) {
  let socket, timer, retry, disposed = false, revision, generation = 0, guestReady = false, guestSession;
  let polling, writes = Promise.resolve(), lastPayload = "", serial = 0, errorShown = false, acknowledgedRevision;
  let sessionGeneration = 0;
  let sessionController = new AbortController();
  const acknowledgements = new Map();
  const fileReferences = new Map();
  const retainFiles = value => {
    const id = value?.files;
    if (typeof id !== "string") return () => {};
    fileReferences.set(id, (fileReferences.get(id) || 0) + 1);
    return () => {
      const remaining = fileReferences.get(id) - 1;
      if (remaining) { fileReferences.set(id, remaining); return; }
      fileReferences.delete(id);
      void native("clipboardSyncRelease", { files: id }).catch(() => {});
    };
  };
  const disconnected = () => new Error("剪贴板连接已变化，请确认客机已连接后重新粘贴。");
  const invalidateSession = () => {
    generation++; sessionGeneration++;
    sessionController.abort(); sessionController = new AbortController();
    guestReady = false; revision = undefined; acknowledgedRevision = undefined; lastPayload = "";
    polling = undefined; writes = Promise.resolve();
    for (const pending of acknowledgements.values()) pending.reject();
    acknowledgements.clear();
    setActive(false);
    void native("clipboardSyncCancel").catch(() => {});
  };
  const requireSession = signal => {
    if (disposed || signal.aborted || !guestReady || socket?.readyState !== 1) throw disconnected();
  };
  const waitForSession = (work, signal) => new Promise((resolve, reject) => {
    const abort = () => { signal.removeEventListener("abort", abort); reject(disconnected()); };
    Promise.resolve(work).then(resolve, reject).finally(() => signal.removeEventListener("abort", abort));
    if (signal.aborted) abort();
    else signal.addEventListener("abort", abort, { once: true });
  });
  const warn = () => { if (!disposed && !errorShown) { errorShown = true; onStatus("剪贴板共享暂未完成，请检查文件是否仍存在及大小限制。"); } };
  const send = (connection, content) => new Promise((resolve, reject) => {
    const id = ++serial;
    const timeout = setTimeout(() => { acknowledgements.delete(id); reject(new Error("Clipboard acknowledgement timed out")); }, 8000);
    acknowledgements.set(id, { resolve: () => { clearTimeout(timeout); resolve(); }, reject: () => { clearTimeout(timeout); reject(new Error("Clipboard disconnected")); } });
    try { connection.send(JSON.stringify({ ...content, serial: id })); }
    catch { acknowledgements.get(id)?.reject(); acknowledgements.delete(id); }
  });
  const payload = value => typeof value.text === "string" ? { text: value.text }
    : typeof value.png === "string" ? { png: value.png }
      : typeof value.files === "string" ? { files: value.files } : null;
  const readHost = async (initial = false) => {
    if (disposed || !guestReady || socket?.readyState !== 1) return;
    const currentSocket = socket, currentGeneration = generation, session = sessionGeneration;
    let releaseFiles = () => {};
    try {
      const current = await native("clipboardRevision");
      if (typeof current !== "number") throw new Error("Clipboard revision unavailable");
      if (!initial && current === revision && current === acknowledgedRevision) return;
      const value = await native("clipboardSyncRead");
      releaseFiles = retainFiles(value);
      const content = payload(value);
      const finalRevision = await native("clipboardRevision");
      if (disposed || !guestReady || socket !== currentSocket || currentGeneration !== generation ||
          value.revision !== finalRevision) return;
      revision = value.revision;
      if (!content) throw new Error("Clipboard content unavailable");
      const encoded = JSON.stringify(content);
      if (initial || value.revision !== acknowledgedRevision || encoded !== lastPayload) await send(currentSocket, content);
      if (disposed || !guestReady || socket !== currentSocket || currentGeneration !== generation) return;
      acknowledgedRevision = value.revision;
      lastPayload = encoded;
      setActive(true);
      errorShown = false;
    } catch {
      if (disposed || socket !== currentSocket || session !== sessionGeneration) return;
      warn();
      setActive(false);
      revision = undefined; lastPayload = "";
      currentSocket.close();
      throw new Error("Clipboard synchronization failed");
    } finally { releaseFiles(); }
  };
  const poll = (initial = false) => {
    if (!polling) {
      const current = readHost(initial).finally(() => { if (polling === current) polling = undefined; });
      polling = current;
    }
    return polling;
  };
  const receive = (value) => {
    if (value.hello) {
      const hello = value.hello;
      const supported = hello.version === 1 && ["linux", "windows", "darwin"].includes(hello.os) &&
        typeof hello.session === "string" && hello.session.length > 0 &&
        ["text", "png", "files"].every(format => hello.formats?.includes(format));
      const previouslyReady = guestReady;
      if (previouslyReady && (guestSession !== hello.session || !supported || !hello.available)) invalidateSession();
      guestSession = hello.session;
      guestReady = supported && hello.available === true;
      if (!guestReady) setActive(false);
      if (guestReady && (!previouslyReady || revision === undefined)) void poll(true).catch(warn);
      return;
    }
    if (value.ack) {
      acknowledgements.get(value.ack)?.resolve(); acknowledgements.delete(value.ack); return;
    }
    if (value.error) { warn(); socket?.close(); return; }
    const content = payload(value), sequence = ++generation, currentSocket = socket, session = sessionGeneration;
    if (!content) return;
    const releaseFiles = retainFiles(content);
    writes = writes.then(async () => {
      if (disposed || !guestReady || socket !== currentSocket || sequence !== generation) return;
      const current = await native("clipboardRevision");
      if (disposed || !guestReady || socket !== currentSocket || sequence !== generation) return;
      if (revision === undefined || current !== revision) {
        // A newer local copy wins over an update that was already in transit.
        void poll().catch(warn);
        return;
      }
      if (JSON.stringify(content) === lastPayload) return;
      const result = await native("clipboardSyncWrite", { ...content, revision });
      if (disposed || socket !== currentSocket || session !== sessionGeneration) return;
      if (result.superseded) { void poll().catch(warn); return; }
      revision = result.revision;
      acknowledgedRevision = result.revision;
      lastPayload = JSON.stringify(content);
      onStatus(content.files ? "文件已复制到本机，可在本机粘贴" : "已复制到本机");
    }).catch(() => {
      if (session === sessionGeneration) warn();
    }).finally(releaseFiles);
  };
  const open = () => {
    if (disposed) return;
    revision = undefined; lastPayload = ""; guestReady = false;
    socket = connect();
    const connection = socket;
    socket.onopen = () => {
      if (disposed || socket !== connection) return;
      timer = setInterval(() => void poll().catch(warn), interval);
    };
    socket.onmessage = event => {
      if (disposed || socket !== connection) return;
      try { receive(JSON.parse(event.data)); } catch { /* Invalid transport data is not a clipboard change. */ }
    };
    socket.onclose = () => {
      if (disposed || socket !== connection) return;
      invalidateSession(); clearInterval(timer);
      retry = setTimeout(open, 1500);
    };
    socket.onerror = () => { /* onclose owns reconnects. */ };
  };
  open();
  return { poll, async sync() {
    const signal = sessionController.signal;
    requireSession(signal);
    for (;;) {
      await waitForSession(poll(), signal);
      const pendingWrites = writes;
      await waitForSession(pendingWrites, signal);
      const sequence = generation;
      const current = await waitForSession(native("clipboardRevision"), signal);
      requireSession(signal);
      if (typeof current !== "number") throw new Error("无法确认当前剪贴板版本，请重新粘贴。");
      if (current === acknowledgedRevision && sequence === generation && pendingWrites === writes) return;
    }
  }, dispose() {
    disposed = true; invalidateSession(); clearInterval(timer); clearTimeout(retry);
    socket?.close();
  } };
}
