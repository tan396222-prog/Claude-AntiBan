export const sessionRetryLimit = 5;
export const sessionRetryDelay = attempt => Math.min(8000, 1000 * 2 ** (attempt - 1));
export const retryableSessionError = error => !error.status || error.status >= 500 || [408, 425, 429].includes(error.status);

export function sessionFailureMessage(error) {
  return error.status ? error.message : "手机与电脑的连接暂时中断。";
}

function pause(ms, signal) {
  return new Promise((resolve, reject) => {
    if (signal.aborted) { reject(signal.reason); return; }
    const abort = () => { clearTimeout(timer); reject(signal.reason); };
    const timer = setTimeout(() => { signal.removeEventListener("abort", abort); resolve(); }, ms);
    signal.addEventListener("abort", abort, {once:true});
  });
}

// Read first: a lost HTTP receipt does not mean the message was rejected.
// Resends keep the original payload, using the host's durable admission ledger.
export async function recoverPendingMessage({signal, pending, read, send, progress, wait = pause}) {
  for (let attempt = 1; attempt <= sessionRetryLimit; attempt++) {
    progress(attempt);
    await wait(sessionRetryDelay(attempt), signal);
    if (signal.aborted) return false;
    try {
      const snapshot = await read();
      if (signal.aborted) return false;
      const message = pending();
      if (!message) return true;
      const busy = ["running", "queued", "stopping"].includes(snapshot?.turns?.at(-1)?.status);
      if (snapshot && snapshot.can_send !== false && !busy) {
        await send(message);
        if (signal.aborted) return false;
        if (!pending()) return true;
      }
    } catch (error) {
      if (signal.aborted) return false;
      if (!retryableSessionError(error) && error.status !== 409) throw error;
    }
  }
  return false;
}
