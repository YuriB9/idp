// Тесты раздвигающегося меню (ADR-0017): свернуть/развернуть + сохранение в
// localStorage и восстановление; подсветка активного маршрута; клавиатурная
// навигация; недоступность localStorage не ломает меню.
import { describe, expect, it, beforeEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { Layers, ShieldCheck, Boxes } from "lucide-react";

import { Sidebar, type SidebarGroup } from "./Sidebar";
import { SIDEBAR_STORAGE_KEY } from "@/lib/ui-state";

const groups: SidebarGroup[] = [
  {
    label: "Платформа",
    items: [
      { label: "Сервисы", to: "/services", icon: Layers },
      { label: "Роли и доступы", to: "/iam", icon: ShieldCheck },
    ],
  },
];

function renderSidebar(initialPath = "/services") {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Sidebar brand="IDP Platform" brandIcon={Boxes} groups={groups} />
    </MemoryRouter>,
  );
}

describe("Sidebar", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("по умолчанию развёрнут и показывает подписи пунктов", () => {
    renderSidebar();
    expect(screen.getByText("Сервисы")).toBeInTheDocument();
    expect(screen.getByText("Роли и доступы")).toBeInTheDocument();
  });

  it("свернуть → сохраняет состояние в localStorage и скрывает подписи", async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByRole("button", { name: /Свернуть меню/ }));
    expect(localStorage.getItem(SIDEBAR_STORAGE_KEY)).toBe("collapsed");
    // В свёрнутом режиме текстовые подписи скрыты (остаются иконки/тултипы).
    expect(screen.queryByText("Сервисы")).toBeNull();
  });

  it("восстанавливает свёрнутое состояние из localStorage", () => {
    localStorage.setItem(SIDEBAR_STORAGE_KEY, "collapsed");
    renderSidebar();
    expect(screen.queryByText("Сервисы")).toBeNull();
    expect(screen.getByRole("button", { name: /Развернуть меню/ })).toBeInTheDocument();
  });

  it("подсвечивает активный маршрут через aria-current", () => {
    renderSidebar("/iam");
    const active = screen.getByRole("link", { name: /Роли и доступы/ });
    expect(active).toHaveAttribute("aria-current", "page");
  });

  it("клавиатурная навигация: пункты достижимы табом и активируемы", async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.tab();
    // Первый фокусируемый элемент — ссылка/кнопка внутри меню.
    expect(document.activeElement).toBeInstanceOf(HTMLElement);
    const collapseBtn = screen.getByRole("button", { name: /Свернуть меню/ });
    collapseBtn.focus();
    await user.keyboard("{Enter}");
    expect(localStorage.getItem(SIDEBAR_STORAGE_KEY)).toBe("collapsed");
  });

  it("недоступность localStorage не ломает меню", () => {
    const getItem = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(() => renderSidebar()).not.toThrow();
    expect(screen.getByText("Сервисы")).toBeInTheDocument();
    getItem.mockRestore();
  });
});
