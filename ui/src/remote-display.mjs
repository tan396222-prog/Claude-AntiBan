// Guest applications render at 2x. Zoom changes their logical workspace size,
// while noVNC fits the resulting framebuffer into the available CSS area.
export function displayGeometry({ width, height }, { desktop = false, zoom = 1 } = {}) {
  if (width < (desktop ? 320 : 160) || height < 240) return null;
  const ratio = 2 / zoom;
  const limit = Math.min(1, 8192 / (width * ratio), 8192 / (height * ratio));
  return {
    width: Math.max(320, Math.floor(width * ratio * limit)),
    height: Math.max(desktop ? 240 : 480, Math.floor(height * ratio * limit)),
  };
}

// Serialize guest changes: a slow old request must finish before the newest
// size is applied. Failed changes get bounded retries even without another
// window resize. Disposing never starts another request.
export function displaySizeSync(resize, { onError = () => {}, onSuccess = () => {}, delay = 160 } = {}) {
  let desired, applied = "", timer, busy = false, disposed = false, retries = 0;
  const key = (size) => size ? `${size.width}x${size.height}` : "";
  const schedule = (ms) => { clearTimeout(timer); timer = setTimeout(apply, ms); };
  async function apply() {
    timer = null;
    if (disposed || busy || !desired || key(desired) === applied) return;
    const target = desired;
    let retryDelay;
    busy = true;
    try {
      await resize(target);
      if (!disposed) {
        applied = key(target);
        if (key(desired) === applied) { retries = 0; onSuccess(); }
      }
    } catch (error) {
      if (!disposed && key(desired) === key(target)) {
        onError(error);
        if (retries < 3) retryDelay = 500 * 2 ** retries++;
      }
    } finally {
      busy = false;
      if (!disposed) {
        if (key(desired) !== key(target)) schedule(0);
        else if (retryDelay !== undefined) schedule(retryDelay);
      }
    }
  }
  return {
    update(size) {
      if (disposed || !size || key(size) === key(desired)) return;
      desired = size;
      retries = 0;
      schedule(delay);
    },
    retry() { if (!disposed) { applied = ""; retries = 0; schedule(0); } },
    dispose() { disposed = true; clearTimeout(timer); },
  };
}
