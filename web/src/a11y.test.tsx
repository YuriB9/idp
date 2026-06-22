// Сквозные базовые проверки доступности (ADR-0017): ARIA-контракты ключевых
// примитивов (таблица, диалог, тосты, меню) и уважение prefers-reduced-motion.
// Фокус/клавиатура и состояния подробно покрыты в тестах самих примитивов; здесь —
// единый регрессионный страж ARIA-разметки и редукции анимаций.
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { Boxes, Layers } from "lucide-react";

import { DataTable, type ColumnDef } from "./components/ui/data-table";
import { Dialog } from "./components/ui/dialog";
import { ToastProvider, useToast } from "./components/ui/toast";
import { Sidebar, type SidebarGroup } from "./components/Sidebar";

type Row = { id: string; name: string };
const columns: ColumnDef<Row>[] = [
  { id: "name", header: "Имя", cell: (r) => r.name, sortValue: (r) => r.name },
  { id: "plain", header: "Прочее", cell: () => "—" },
];

describe("a11y: DataTable", () => {
  it("семантическая таблица с заголовками и aria-sort на сортируемой колонке", () => {
    render(
      <DataTable columns={columns} rows={[{ id: "1", name: "a" }]} rowKey={(r) => r.id} />,
    );
    expect(screen.getByRole("table")).toBeInTheDocument();
    const headers = screen.getAllByRole("columnheader");
    expect(headers[0]).toHaveAttribute("aria-sort", "none");
    expect(headers[0]).toHaveAttribute("scope", "col");
    // Несортируемая колонка не объявляет aria-sort.
    expect(headers[1]).not.toHaveAttribute("aria-sort");
  });
});

describe("a11y: Dialog", () => {
  it("role=dialog, aria-modal и связь заголовка через aria-labelledby", () => {
    render(
      <Dialog open onClose={() => {}} title="Заголовок">
        <p>тело</p>
      </Dialog>,
    );
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-modal", "true");
    const labelledBy = dialog.getAttribute("aria-labelledby");
    expect(labelledBy).toBeTruthy();
    // Заголовок существует и связан с диалогом.
    expect(document.getElementById(labelledBy as string)).toHaveTextContent("Заголовок");
  });
});

describe("a11y: Toast", () => {
  function Harness() {
    const toast = useToast();
    return (
      <button type="button" onClick={() => toast.success("ок")}>
        go
      </button>
    );
  }

  it("контейнер — live-region; тост уважает prefers-reduced-motion (motion-safe)", async () => {
    const user = userEvent.setup();
    render(
      <ToastProvider>
        <Harness />
      </ToastProvider>,
    );
    await user.click(screen.getByRole("button", { name: "go" }));
    const toast = await screen.findByRole("status");
    // Контейнер уведомлений — live-region.
    const region = toast.closest("[aria-live]");
    expect(region).toHaveAttribute("aria-live", "polite");
    // Анимация появления — только в motion-safe (редуцируется при reduced-motion).
    expect(toast.className).toMatch(/motion-safe:/);
  });
});

describe("a11y: Sidebar", () => {
  const groups: SidebarGroup[] = [
    { label: "Платформа", items: [{ label: "Сервисы", to: "/services", icon: Layers }] },
  ];

  it("навигация имеет aria-label, кнопка сворачивания — aria-pressed", () => {
    render(
      <MemoryRouter>
        <Sidebar brand="IDP" brandIcon={Boxes} groups={groups} />
      </MemoryRouter>,
    );
    expect(screen.getByRole("navigation", { name: /Основная навигация/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Свернуть меню/i })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });
});
