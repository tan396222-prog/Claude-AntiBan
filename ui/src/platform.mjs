export const isWindows = /Win/i.test(globalThis.navigator?.platform || "");
export const isMac = /Mac/i.test(globalThis.navigator?.platform || "");
export const usesControl = !isMac;
export const primaryModifier = (event, control = usesControl) =>
  control ? event.ctrlKey && !event.altKey && !event.metaKey : event.metaKey;
export const terminalModifier = (event, control = usesControl) =>
  control ? primaryModifier(event, true) && event.shiftKey : event.metaKey;
export function shortcut(mac, control = usesControl) {
  return control
    ? mac.replaceAll("⌘", "Ctrl+").replaceAll("⇧", "Shift+").replaceAll("↵", "Enter")
    : mac;
}
