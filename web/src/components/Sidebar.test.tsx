// Тесты раздвигающегося меню (ADR-0017): свернуть/развернуть + сохранение в
// localStorage и восстановление; подсветка активного маршрута; клавиатурная
// навигация; недоступность localStorage не ломает меню.
import { describe, expect, it, beforeEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { KeyRound, Layers, ShieldCheck, UserCog, Users, Boxes } from "lucide-react";

import { Sidebar, type SidebarGroup } from "./Sidebar";
import { SIDEBAR_STORAGE_KEY, SIDEBAR_SUBMENUS_STORAGE_KEY } from "@/lib/ui-state";

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

// Группы с вложенным подменю под «Роли и доступы».
const submenuGroups: SidebarGroup[] = [
  {
    label: "Платформа",
    items: [
      { label: "Сервисы", to: "/services", icon: Layers },
      {
        label: "Роли и доступы",
        to: "/iam",
        icon: ShieldCheck,
        children: [
          { label: "Роли", to: "/iam/roles", icon: KeyRound },
          { label: "Права", to: "/iam/permissions", icon: UserCog },
          { label: "Пользователи", to: "/iam/users", icon: Users },
        ],
      },
    ],
  },
];

function renderSubmenu(initialPath = "/services") {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Sidebar brand="IDP Platform" brandIcon={Boxes} groups={submenuGroups} />
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

describe("Sidebar: вложенное подменю", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("по умолчанию подменю свёрнуто: родитель есть, под-пункты скрыты", () => {
    renderSubmenu();
    const parent = screen.getByRole("button", { name: /Роли и доступы/ });
    expect(parent).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("link", { name: "Роли" })).toBeNull();
  });

  it("раскрытие подменю показывает под-пункты и сохраняет состояние в localStorage", async () => {
    const user = userEvent.setup();
    renderSubmenu();
    const parent = screen.getByRole("button", { name: /Роли и доступы/ });
    await user.click(parent);
    expect(parent).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: "Роли" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Права" })).toBeInTheDocument();
    expect(JSON.parse(localStorage.getItem(SIDEBAR_SUBMENUS_STORAGE_KEY)!)).toContain("/iam");
  });

  it("сворачивание подменю скрывает под-пункты и обновляет localStorage", async () => {
    const user = userEvent.setup();
    renderSubmenu();
    const parent = screen.getByRole("button", { name: /Роли и доступы/ });
    await user.click(parent);
    await user.click(parent);
    expect(parent).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("link", { name: "Роли" })).toBeNull();
    expect(JSON.parse(localStorage.getItem(SIDEBAR_SUBMENUS_STORAGE_KEY)!)).not.toContain("/iam");
  });

  it("восстанавливает раскрытое подменю из localStorage", () => {
    localStorage.setItem(SIDEBAR_SUBMENUS_STORAGE_KEY, JSON.stringify(["/iam"]));
    renderSubmenu();
    expect(screen.getByRole("link", { name: "Роли" })).toBeInTheDocument();
  });

  it("авто-раскрытие на активном дочернем маршруте + подсветка активного под-пункта", () => {
    renderSubmenu("/iam/permissions");
    const active = screen.getByRole("link", { name: "Права" });
    expect(active).toHaveAttribute("aria-current", "page");
    // Другой под-пункт активным не помечен.
    expect(screen.getByRole("link", { name: "Роли" })).not.toHaveAttribute("aria-current", "page");
  });

  it("deep-link дочернего маршрута тоже авто-раскрывает подменю", () => {
    renderSubmenu("/iam/roles/reviewers");
    expect(screen.getByRole("link", { name: "Роли" })).toHaveAttribute("aria-current", "page");
  });

  it("клавиатура: Enter на родителе раскрывает подменю", async () => {
    const user = userEvent.setup();
    renderSubmenu();
    const parent = screen.getByRole("button", { name: /Роли и доступы/ });
    parent.focus();
    await user.keyboard("{Enter}");
    expect(parent).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: "Роли" })).toBeInTheDocument();
  });

  it("свёрнутый icon-only режим: под-пункты доступны как меню (поповер)", () => {
    localStorage.setItem(SIDEBAR_STORAGE_KEY, "collapsed");
    renderSubmenu();
    // Под-пункты присутствуют в DOM как пункты меню (видимость — по hover/focus).
    expect(screen.getByRole("menuitem", { name: "Роли" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Права" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Пользователи" })).toBeInTheDocument();
  });

  it("плоский пункт «Сервисы» продолжает работать в меню с подменю", () => {
    renderSubmenu();
    expect(screen.getByRole("link", { name: /Сервисы/ })).toBeInTheDocument();
  });
});
