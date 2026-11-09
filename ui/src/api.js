import { reportDiagnostic } from "./diagnostics.mjs";
import { assertConnection, connectionHeaders, getClientContext } from "./client-context.mjs";
let sessionRenewal;
async function responseError(response) {
  const error = new Error(
    (await response.text()).trim() || "连接未完成，请重试",
  );
  error.status = response.status;
  return error;
}
async function renewSession() {
  if (!sessionRenewal) {
    sessionRenewal = (async () => {
      const { url } = await native("refreshSession");
      const entry = new URL(url);
      if (entry.origin !== location.origin) {
        await native("reconnect");
        throw new Error("工作台地址已更新，连接恢复后请重试刚才的操作。");
      }
      const response = await fetch("/api/session", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token: entry.hash.slice(1) }),
      });
      if (!response.ok) throw await responseError(response);
    })().finally(() => {
      sessionRenewal = null;
    });
  }
  return sessionRenewal;
}
export async function api(path, body, method) {
  const connection = getClientContext().connection_id;
  const options = {
    method: method || (body === undefined ? "GET" : "POST"),
    headers: { ...connectionHeaders(path), ...(body === undefined ? {} : { "Content-Type": "application/json" }) },
    body: body === undefined ? undefined : JSON.stringify(body),
  };
  const send = async () => {
    try { return await fetch("/api" + path, options); }
    catch (error) { reportDiagnostic("frontend", error); throw error; }
  };
  let response = await send();
  // Authentication fails before a handler runs, so retrying this rejected
  // request once does not repeat an accepted mutation.
  if (response.status === 401 && path !== "/session" && nativeAvailable()) {
    await renewSession();
    response = await send();
  }
  if (response.status === 423 && !path.startsWith("/client/")) window.dispatchEvent(new Event("cage-account-locked"));
  if (!response.ok) throw await responseError(response);
  const value = await response.json();
  if (path !== "/session" && !path.startsWith("/client/")) assertConnection(connection);
  return value;
}
export async function uploadFile(path, file, { signal, onProgress } = {}) {
  const headers = connectionHeaders(path);
  const connection = getClientContext().connection_id;
  const send = () => new Promise((resolve, reject) => {
    if (signal?.aborted) { reject(new DOMException("上传已取消", "AbortError")); return; }
    const request = new XMLHttpRequest();
    const abort = () => request.abort();
    const cleanup = () => signal?.removeEventListener("abort", abort);
    request.open("POST", "/api" + path);
    request.setRequestHeader("Content-Type", "application/octet-stream");
    for (const [name, value] of Object.entries(headers)) request.setRequestHeader(name, value);
    request.upload.onprogress = event => onProgress?.(event.loaded, event.total || file.size);
    request.onerror = () => { cleanup(); reject(new Error("文件传递中断，请重试")); };
    request.onabort = () => { cleanup(); reject(new DOMException("上传已取消", "AbortError")); };
    request.onload = () => {
      cleanup();
      if (request.status === 423) window.dispatchEvent(new Event("cage-account-locked"));
      if (request.status !== 200) {
        const error = new Error(request.responseText.trim() || "文件传递未完成");
        error.status = request.status; reject(error); return;
      }
      try { assertConnection(connection); resolve(JSON.parse(request.responseText)); }
      catch { reject(new Error("文件传递响应无效")); }
    };
    signal?.addEventListener("abort", abort, { once: true });
    request.send(file);
  });
  try { return await send(); }
  catch (error) {
    if (error.status !== 401 || !nativeAvailable() || signal?.aborted) throw error;
    await renewSession();
    return send();
  }
}
export const nativeAvailable = () =>
  Boolean(window.webkit?.messageHandlers?.cage);
export async function native(action, extra = {}) {
  const connection = getClientContext().connection_id;
  if (action === "quitNow" && getClientContext().mode === "local" && !getClientContext().client_only) {
    try { await api("/account/suspend", {}); }
    catch (error) { if (error.status !== 423) throw error; }
  }
  try {
    const result = await window.webkit.messageHandlers.cage.postMessage({ action, ...extra, connection_id: connection });
    assertConnection(connection);
    return result;
  }
  catch (error) { reportDiagnostic("frontend", error); throw error; }
}
export const readClipboard = () =>
  nativeAvailable() ? native("clipboardRead") : navigator.clipboard.readText();
export const writeClipboard = (text) =>
  nativeAvailable()
    ? native("clipboardWrite", { text })
    : navigator.clipboard.writeText(text);
export function normalizeURL(text) {
  const value = text.trim();
  if (!value || value === "about:blank") return "about:blank";
  if (/^https?:\/\//i.test(value)) return new URL(value).href;
  if (/^[a-z][a-z\d+.-]*:/i.test(value) && !/^localhost:\d+/i.test(value))
    throw new Error("地址仅支持 http 或 https");
  if (
    !/\s/.test(value) &&
    (value.includes(".") || /^localhost(?::\d+)?(?:\/|$)/i.test(value))
  )
    return new URL(
      (/^localhost|^127\./.test(value) ? "http://" : "https://") + value,
    ).href;
  return "https://www.google.com/search?q=" + encodeURIComponent(value);
}
