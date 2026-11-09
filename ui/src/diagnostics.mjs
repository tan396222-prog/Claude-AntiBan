// Error reports deliberately omit request bodies, arguments, page URLs and DOM.
let windowStarted = 0;
let sent = 0;
const pending = [];
let sending = false;
let retryTimer;
async function flush() {
  if (sending) return;
  clearTimeout(retryTimer);
  retryTimer = undefined;
  sending = true;
  try {
    while (pending.length) {
      const response = await fetch("/api/diagnostics/events", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify(pending[0]),
      });
      if (!response.ok) break;
      pending.shift();
    }
  } catch { /* Retry after the local controller or session recovers. */ }
  finally {
    sending = false;
    if (pending.length) {
      retryTimer = setTimeout(flush, 5000);
      retryTimer.unref?.();
    }
  }
}
export function reportDiagnostic(source, error) {
  const now = Date.now();
  if (now - windowStarted > 60_000) { windowStarted = now; sent = 0; }
  if (sent >= 20 || pending.length >= 20) return;
  sent++;
  let message;
  if (error instanceof Error) message = `${error.name}: ${error.message}\n${error.stack || ""}`;
  else if (typeof error === "string") message = error;
  else message = "异步操作发生异常，未收集原始对象";
  // The backend redacts before persisting. Never report the report's own failure.
  pending.push({ source, level: "error", message: message.slice(0, 4096) });
  void flush();
}
export function installDiagnostics(target = window) {
  target.addEventListener("error", event => {
    if (event.error) reportDiagnostic("frontend", event.error);
    else reportDiagnostic("frontend", event.message || "界面资源加载失败");
  });
  target.addEventListener("unhandledrejection", event => reportDiagnostic("frontend", event.reason));
}
