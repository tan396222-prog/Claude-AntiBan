import { useSyncExternalStore } from "react";

const visible = () =>
  document.visibilityState !== "hidden" && window.cageWindowVisible !== false;

function subscribe(notify) {
  document.addEventListener("visibilitychange", notify);
  window.addEventListener("cage-window-visibility", notify);
  return () => {
    document.removeEventListener("visibilitychange", notify);
    window.removeEventListener("cage-window-visibility", notify);
  };
}

export function useWindowVisibility() {
  return useSyncExternalStore(subscribe, visible);
}
