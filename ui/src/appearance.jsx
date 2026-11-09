import React, {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";
import { api, native, nativeAvailable } from "./api";
import {
  defaultAppearance,
  normalizeAppearance,
  themeVariables,
} from "./appearance-state.mjs";

const AppearanceContext = createContext(null);
export const useAppearance = () => useContext(AppearanceContext);
export function AppearanceProvider({ children }) {
  const [appearance, setAppearance] = useState(() => {
    try {
      return normalizeAppearance(
        JSON.parse(localStorage.getItem("cage-appearance")) ||
          defaultAppearance,
      );
    } catch {
      return defaultAppearance;
    }
  });
  const [systemDark, setSystemDark] = useState(
    matchMedia("(prefers-color-scheme: dark)").matches,
  );
  const [systemReduced, setSystemReduced] = useState(
    matchMedia("(prefers-reduced-motion: reduce)").matches,
  );
  const [loading, setLoading] = useState(true),
    [saving, setSaving] = useState(false),
    [error, setError] = useState("");
  const revision = useRef(0),
    queue = useRef(Promise.resolve()),
    current = useRef(appearance);
  const variant =
    appearance.mode === "system"
      ? systemDark
        ? "dark"
        : "light"
      : appearance.mode;
  const theme = appearance[variant + "Theme"];
  current.current = appearance;
  useEffect(() => {
    let live = true;
    const load = () =>
      api("/appearance")
        .then((value) => {
          if (live) {
            setAppearance(normalizeAppearance(value));
            localStorage.setItem("cage-appearance", JSON.stringify(value));
          }
        })
        .catch((e) => {
          if (live) setError(e.message);
        })
        .finally(() => {
          if (live) setLoading(false);
        });
    if (location.hash)
      window.addEventListener("cage-session-ready", load, { once: true });
    else load();
    const dark = matchMedia("(prefers-color-scheme: dark)"),
      reduced = matchMedia("(prefers-reduced-motion: reduce)");
    const onDark = () => setSystemDark(dark.matches),
      onReduced = () => setSystemReduced(reduced.matches);
    dark.addEventListener("change", onDark);
    reduced.addEventListener("change", onReduced);
    return () => {
      live = false;
      window.removeEventListener("cage-session-ready", load);
      dark.removeEventListener("change", onDark);
      reduced.removeEventListener("change", onReduced);
    };
  }, []);
  useEffect(() => {
    const root = document.documentElement;
    const variables = themeVariables(theme, variant);
    for (const [key, value] of Object.entries(variables))
      root.style.setProperty(key, value);
    root.style.setProperty("--ui-font-size", appearance.uiFontSize + "px");
    root.style.setProperty("--code-font-size", appearance.codeFontSize + "px");
    root.style.setProperty(
      "--control-cursor",
      appearance.pointerCursors ? "pointer" : "default",
    );
    root.dataset.theme = variant;
    root.dataset.translucent = String(!theme.opaqueWindows);
    root.dataset.fontSmoothing = String(appearance.fontSmoothing);
    root.dataset.reducedMotion = String(
      appearance.reducedMotion === "on" ||
        (appearance.reducedMotion === "system" && systemReduced),
    );
    root.style.colorScheme = variant;
    if (nativeAvailable())
      native("appearance", {
        mode: appearance.mode,
        variant,
        background: variables["--sidebar"],
        foreground: variables["--sidebar-ink"],
        translucent: !theme.opaqueWindows,
      }).catch(() =>
        setError("外观已应用到工作台，原生窗口未能同步，请重新打开 Cage。"),
      );
  }, [appearance, variant, systemReduced]);
  const update = (next) => {
    next = normalizeAppearance(
      typeof next === "function" ? next(current.current) : next,
    );
    current.current = next;
    setAppearance(next);
    setSaving(true);
    setError("");
    const id = ++revision.current;
    queue.current = queue.current
      .catch(() => {})
      .then(async () => {
        if (id !== revision.current) return;
        try {
          await api("/appearance", next);
          localStorage.setItem("cage-appearance", JSON.stringify(next));
          if (id === revision.current) setSaving(false);
          return true;
        } catch (e) {
          if (id === revision.current) {
            setError(e.message);
            setSaving(false);
          }
          return false;
        }
      });
    return queue.current;
  };
  return (
    <AppearanceContext.Provider
      value={{
        appearance,
        theme,
        variant,
        loading,
        saving,
        error,
        update,
        retry: () => update(current.current),
      }}
    >
      {children}
    </AppearanceContext.Provider>
  );
}
