export function deviceCommand(action, fields = {}) {
  const message = { action, ...fields };
  if (window.webkit?.messageHandlers?.cageDevices) {
    window.webkit.messageHandlers.cageDevices.postMessage(message);
    return true;
  } else if (window.CageDevices?.postMessage) {
    window.CageDevices.postMessage(JSON.stringify(message));
    return true;
  }
  return false;
}
