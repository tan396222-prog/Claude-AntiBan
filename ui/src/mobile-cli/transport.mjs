import { httpErrorMessage, connectionErrorMessage } from "../error-message.mjs";

// A desktop tab is bound to the workload selected when it was opened. The
// native phone bridge serves its UI at / and authenticates its own peer.
const desktopConnection = location.pathname === "/mobile.html"
  ? sessionStorage.getItem("cage-cli-local-connection") : null;

async function requestAPI(path, { body, signal, keepalive } = {}) {
  let response;
  try {
    response = await fetch(path, {
      keepalive,
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: { ...(desktopConnection ? { "X-Cage-Connection": desktopConnection } : {}),
        ...(body === undefined ? {} : { "Content-Type": "application/json" }) },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
  } catch (error) {
    if (signal?.aborted) throw error;
    throw new Error(connectionErrorMessage(path), { cause: error });
  }
  if (!response.ok) {
    let message;
    if (response.status === 401) message = "连接授权已失效，请从 Cage 重新打开此页面。";
    else if (response.status === 423) message = "Cage 已锁定。请在电脑上解锁，再重新读取。";
    else if ((response.headers.get("Content-Type") || "").startsWith("text/plain")) {
      message = (await response.text()).trim().slice(0, 1200);
    }
    const error = new Error(message || httpErrorMessage(path, response.status));
    error.status = response.status;
    throw error;
  }
  if (response.status === 204) return null;
  try { return await response.json(); }
  catch (error) { throw new Error("读取会话或画面结果失败：Cage 返回的数据格式无效。请重新读取；持续出现时从电脑上的 Cage 导出诊断包。", { cause: error }); }
}

export const request = (path, options) => requestAPI("/api/mobile/cli" + path, options);
export const browserRequest = (path, options) => requestAPI("/api/mobile/browser" + path, options);

export const desktopRequest = (path, options) => requestAPI("/api/mobile/desktop" + path, options);

export const sessionPath = (id) => "/sessions/" + encodeURIComponent(id);
export const accessDenied = (error) => error.status === 401 || error.status === 423;
