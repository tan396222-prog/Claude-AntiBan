import { reportDiagnostic } from "./diagnostics.mjs";
import { httpErrorMessage, connectionErrorMessage, operationName, nativeFailureMessage } from "./error-message.mjs";
import { assertConnection, connectionHeaders, getClientContext, isProjectSharesPath } from "./client-context.mjs";
import { installAgentNativeConsumer } from "./agent-native.js";
let sessionRenewal;
class WorkbenchUnreachable extends Error {}
async function responseError(response, path, method = "GET") {
  const error = new Error(
    httpErrorMessage(path, response.status, await response.text(), response.headers.get("Content-Type") || "", method),
  );
  error.status = response.status;
  error.recovery = response.headers.get("X-Cage-Recovery") || "";
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
      if (!response.ok) throw await responseError(response, "/session");
    })().finally(() => {
      sessionRenewal = null;
    });
  }
  return sessionRenewal;
}
export async function api(path, body, method, headers = {}, { signal } = {}) {
  const connection = getClientContext().connection_id;
  const options = {
    method: method || (body === undefined ? "GET" : "POST"),
    headers: { ...connectionHeaders(path), ...(body === undefined ? {} : { "Content-Type": "application/json" }), ...headers },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  };
  const send = async () => {
    try { return await fetch("/api" + path, options); }
    catch (error) {
      if (signal?.aborted) throw signal.reason;
      reportDiagnostic("frontend", error);
      const message = connectionErrorMessage(path, options.method);
      if (error instanceof TypeError) throw new WorkbenchUnreachable(message, { cause: error });
      throw new Error(message, { cause: error });
    }
  };
  let response = await send();
  // Authentication fails before a handler runs, so retrying this rejected
  // request once does not repeat an accepted mutation.
  if (response.status === 401 && path !== "/session" && nativeAvailable()) {
    await renewSession();
    response = await send();
  }
  if (response.status === 423 && !path.startsWith("/client/")) window.dispatchEvent(new Event("cage-account-locked"));
  if (!response.ok) throw await responseError(response, path, options.method);
  let value;
  try { value = await response.json(); }
  catch (error) { reportDiagnostic("frontend", error); throw new Error(`${operationName(path, options.method)}失败：后台返回的结果格式无效。请重新连接；仍出现时在设置中导出诊断包。`, { cause: error }); }
  if (path !== "/session" && !path.startsWith("/agent/native") && (!path.startsWith("/client/") || isProjectSharesPath(path))) assertConnection(connection);
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
    request.onerror = () => { cleanup(); reject(new Error("上传文件失败：与 Cage 后台的连接中断。请恢复连接后核对目标是否已收到文件，再重新选择文件。")); };
    request.onabort = () => { cleanup(); reject(new DOMException("上传已取消", "AbortError")); };
    request.onload = () => {
      cleanup();
      if (request.status === 423) window.dispatchEvent(new Event("cage-account-locked"));
      if (request.status !== 200) {
        const error = new Error(httpErrorMessage(path, request.status, request.responseText, request.getResponseHeader("Content-Type") || ""));
        error.status = request.status; reject(error); return;
      }
      try { assertConnection(connection); }
      catch (error) { reject(error); return; }
      try { resolve(JSON.parse(request.responseText)); }
      catch { reject(new Error("上传文件后未收到有效确认。请先核对目标是否已收到文件，避免重复上传；仍失败时导出诊断包。")); }
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
const nativeBridge = () =>
  window.cageNativeBridge || window.webkit?.messageHandlers?.cage;
export const nativeAvailable = () => Boolean(nativeBridge());
const lostLocalView = (context) =>
  context.desktop_gateway && context.mode === "local" &&
  context.connection?.state === "failed" && context.connection.error_code === "connection_lost";

async function suspendBeforeQuit(context) {
  if (context.mode !== "local" || context.client_only || lostLocalView(context)) return;
  try {
    await api("/account/suspend", {});
  } catch (error) {
    if (error.status === 423) return;
    // The first request after CLI shutdown can fail before the next context poll.
    if (context.desktop_gateway && [409, 502].includes(error.status)) {
      const latest = await api("/client/context");
      if (latest.connection_id === context.connection_id && lostLocalView(latest)) return;
    }
    throw error;
  }
}
export async function native(action, extra = {}) {
  const context = getClientContext();
  const connection = context.connection_id;
  if (action === "quitNow") {
    try { await suspendBeforeQuit(context); }
    catch (error) { if (!(error instanceof WorkbenchUnreachable)) throw error; }
    assertConnection(connection);
  }
  try {
    const result = await nativeBridge().postMessage({ action, ...extra, connection_id: connection });
    assertConnection(connection);
    return result;
  }
  catch (error) {
    reportDiagnostic("frontend", error);
    const original = error?.message || String(error);
    const message = nativeFailureMessage(action, original);
    if (message === original) throw error;
    throw new Error(message, { cause: error });
  }
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

installAgentNativeConsumer({ api, native, nativeAvailable, getContext: getClientContext });
