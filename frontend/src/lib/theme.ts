import { useEffect, useState } from "react"

export type ThemeMode = "light" | "dark" | "system"

const STORAGE_KEY = "forja_theme"
const CHANGE_EVENT = "forja-theme-change"

const ORDER: ThemeMode[] = ["light", "dark", "system"]

export function getStoredTheme(): ThemeMode {
  const v = localStorage.getItem(STORAGE_KEY)
  return v === "light" || v === "dark" || v === "system" ? v : "system"
}

export function resolveTheme(mode: ThemeMode): "light" | "dark" {
  if (mode !== "system") return mode
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"
}

export function applyTheme(mode: ThemeMode) {
  const resolved = resolveTheme(mode)
  document.documentElement.classList.toggle("dark", resolved === "dark")
  document.documentElement.style.colorScheme = resolved
}

export function setTheme(mode: ThemeMode) {
  localStorage.setItem(STORAGE_KEY, mode)
  applyTheme(mode)
  window.dispatchEvent(new Event(CHANGE_EVENT))
}

export function initTheme() {
  applyTheme(getStoredTheme())
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (getStoredTheme() === "system") applyTheme("system")
  })
}

export function useTheme() {
  const [mode, setMode] = useState<ThemeMode>(getStoredTheme)

  useEffect(() => {
    const sync = () => setMode(getStoredTheme())
    window.addEventListener(CHANGE_EVENT, sync)
    return () => window.removeEventListener(CHANGE_EVENT, sync)
  }, [])

  return {
    mode,
    cycle: () => setTheme(ORDER[(ORDER.indexOf(getStoredTheme()) + 1) % ORDER.length]),
  }
}
