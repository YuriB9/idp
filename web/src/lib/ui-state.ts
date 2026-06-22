// Безопасное хранение состояния UI в localStorage (ADR-0017), согласованное со
// схемой темы (ключ `idp-theme`). Чтение защищено от недоступности localStorage
// (приватный режим) и от рассинхронизации: значение валидируется по белому списку,
// иначе используется дефолт. SSR нет (SPA), поэтому проверок окружения не требуется.

// Ключи состояния UI (стабильные строки, единый префикс `idp-`).
export const SIDEBAR_STORAGE_KEY = "idp-sidebar";
export const DENSITY_STORAGE_KEY = "idp-density";

// SidebarState — свёрнуто/развёрнуто левое меню.
export type SidebarState = "collapsed" | "expanded";

// Density — плотность таблиц (согласована с типом DataTable).
export type Density = "comfortable" | "compact";

// readEnum читает значение по ключу и валидирует его по списку допустимых; при
// недоступности localStorage или неизвестном значении возвращает дефолт.
function readEnum<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  try {
    const stored = localStorage.getItem(key);
    if (stored !== null && (allowed as readonly string[]).includes(stored)) {
      return stored as T;
    }
  } catch {
    // Игнорируем недоступность localStorage — используем дефолт.
  }
  return fallback;
}

// writeValue сохраняет значение, молча игнорируя недоступность localStorage.
function writeValue(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Сохранение необязательно: значение применено на этой сессии.
  }
}

// readSidebarState возвращает сохранённое состояние меню (дефолт — развёрнуто).
export function readSidebarState(): SidebarState {
  return readEnum(SIDEBAR_STORAGE_KEY, ["collapsed", "expanded"] as const, "expanded");
}

// writeSidebarState сохраняет состояние меню.
export function writeSidebarState(state: SidebarState): void {
  writeValue(SIDEBAR_STORAGE_KEY, state);
}

// readDensity возвращает сохранённую плотность таблиц (дефолт — comfortable).
export function readDensity(): Density {
  return readEnum(DENSITY_STORAGE_KEY, ["comfortable", "compact"] as const, "comfortable");
}

// writeDensity сохраняет плотность таблиц.
export function writeDensity(density: Density): void {
  writeValue(DENSITY_STORAGE_KEY, density);
}
