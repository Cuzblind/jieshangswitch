import { useEffect, useState } from "react";

export type ThemeMode = "system" | "light" | "dark";
const KEY = "jieshangai:theme";

function readMode(): ThemeMode {
  try {
    const saved = localStorage.getItem(KEY);
    if (saved === "light" || saved === "dark" || saved === "system") return saved;
  } catch {
    /* Storage can be unavailable. */
  }
  return "system";
}

function systemDark(): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia("(prefers-color-scheme: dark)").matches;
}

export function applyTheme(mode: ThemeMode) {
  const dark = mode === "dark" || (mode === "system" && systemDark());
  document.documentElement.classList.toggle("dark", dark);
}

export function useTheme() {
  const [mode, setModeState] = useState<ThemeMode>(readMode);
  useEffect(() => {
    applyTheme(mode);
    if (mode !== "system" || typeof window.matchMedia !== "function") return;
    const query = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => applyTheme("system");
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, [mode]);
  const setMode = (next: ThemeMode) => {
    try {
      localStorage.setItem(KEY, next);
    } catch {
      /* Preference just won't persist. */
    }
    setModeState(next);
  };
  return { mode, setMode };
}
