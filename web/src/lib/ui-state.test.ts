// Тесты безопасного хранения состояния UI (ADR-0017): состояние раскрытых подменю
// читается/пишется корректно, деградирует при недоступности localStorage и игнорирует
// некорректный JSON/мусор.
import { describe, expect, it, beforeEach, vi } from "vitest";

import {
  SIDEBAR_SUBMENUS_STORAGE_KEY,
  readSidebarSubmenus,
  writeSidebarSubmenus,
} from "./ui-state";

describe("ui-state: состояние подменю", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("дефолт — пустой список (ничего не сохранено)", () => {
    expect(readSidebarSubmenus()).toEqual([]);
  });

  it("запись и чтение круговым рейсом сохраняют список ключей", () => {
    writeSidebarSubmenus(["iam"]);
    expect(readSidebarSubmenus()).toEqual(["iam"]);
    expect(localStorage.getItem(SIDEBAR_SUBMENUS_STORAGE_KEY)).toBe('["iam"]');
  });

  it("некорректный JSON → дефолт (пусто), без исключения", () => {
    localStorage.setItem(SIDEBAR_SUBMENUS_STORAGE_KEY, "{не json");
    expect(readSidebarSubmenus()).toEqual([]);
  });

  it("не-массив или нестроковые элементы → дефолт (пусто)", () => {
    localStorage.setItem(SIDEBAR_SUBMENUS_STORAGE_KEY, JSON.stringify([1, 2]));
    expect(readSidebarSubmenus()).toEqual([]);
    localStorage.setItem(SIDEBAR_SUBMENUS_STORAGE_KEY, JSON.stringify({ a: 1 }));
    expect(readSidebarSubmenus()).toEqual([]);
  });

  it("недоступность localStorage не ломает чтение/запись", () => {
    const getItem = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    const setItem = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(() => writeSidebarSubmenus(["iam"])).not.toThrow();
    expect(readSidebarSubmenus()).toEqual([]);
    getItem.mockRestore();
    setItem.mockRestore();
  });
});
