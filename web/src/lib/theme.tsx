// Управление темой портала (светлая/тёмная). Токены обеих тем уже заданы в
// index.css (:root — светлая, .dark — тёмная); здесь — выбор активной темы,
// сохранение в localStorage и проставление класса .dark на <html>. Платформенный
// дефолт — тёмная тема (единый визуальный язык), но выбор пользователя сохраняется.
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

export type Theme = "light" | "dark";

// storageKey — ключ сохранённого выбора темы в localStorage.
const storageKey = "idp-theme";

// applyTheme проставляет/снимает класс .dark на корне документа (Tailwind-вариант
// dark завязан на `.dark *`). Вызывается синхронно до первого рендера, чтобы не
// было мигания темы (FOUC).
export function applyTheme(theme: Theme): void {
  document.documentElement.classList.toggle("dark", theme === "dark");
}

// readInitialTheme берёт сохранённый выбор; при его отсутствии — платформенный
// дефолт (тёмная тема). localStorage может быть недоступен (приватный режим) —
// тогда тоже дефолт.
export function readInitialTheme(): Theme {
  try {
    const stored = localStorage.getItem(storageKey);
    if (stored === "light" || stored === "dark") {
      return stored;
    }
  } catch {
    // Игнорируем недоступность localStorage — используем дефолт.
  }
  return "dark";
}

type ThemeContextValue = {
  theme: Theme;
  setTheme: (theme: Theme) => void;
  toggleTheme: () => void;
};

const ThemeContext = createContext<ThemeContextValue | null>(null);

// ThemeProvider хранит активную тему, применяет её к документу и сохраняет выбор.
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(readInitialTheme);

  useEffect(() => {
    applyTheme(theme);
    try {
      localStorage.setItem(storageKey, theme);
    } catch {
      // Сохранение необязательно: тема всё равно применена на этой сессии.
    }
  }, [theme]);

  const value: ThemeContextValue = {
    theme,
    setTheme: setThemeState,
    toggleTheme: () => setThemeState((prev) => (prev === "dark" ? "light" : "dark")),
  };

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

// useTheme возвращает текущую тему и переключатель; вне ThemeProvider — ошибка.
export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (ctx === null) {
    throw new Error("useTheme должен использоваться внутри ThemeProvider");
  }
  return ctx;
}
