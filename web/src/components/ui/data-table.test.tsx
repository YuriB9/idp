// Тесты DataTable (ADR-0017): состояния loading(скелет)/empty/error, курсорная
// пагинация (следующая страница и пустой курсор → нет управления), клиентская
// сортировка с отражением направления в aria-sort и доступность с клавиатуры.
import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { DataTable, type ColumnDef } from "./data-table";

type Row = { id: string; name: string; count: number };

const rows: Row[] = [
  { id: "b", name: "bravo", count: 3 },
  { id: "a", name: "alpha", count: 1 },
  { id: "c", name: "charlie", count: 2 },
];

const columns: ColumnDef<Row>[] = [
  { id: "name", header: "Имя", cell: (r) => r.name, sortValue: (r) => r.name },
  { id: "count", header: "Счётчик", align: "right", cell: (r) => r.count },
];

function renderTable(props: Partial<React.ComponentProps<typeof DataTable<Row>>> = {}) {
  return render(
    <DataTable
      columns={columns}
      rows={rows}
      rowKey={(r) => r.id}
      caption="Тестовая таблица"
      {...props}
    />,
  );
}

describe("DataTable состояния", () => {
  it("loading показывает скелет, а не пустой экран/ошибку", () => {
    const { container } = renderTable({ isLoading: true, rows: [], skeletonRows: 3 });
    expect(container.querySelectorAll(".animate-pulse").length).toBeGreaterThan(0);
    expect(screen.queryByText(/Нет данных/)).toBeNull();
    expect(screen.queryByText(/Не удалось/)).toBeNull();
  });

  it("empty показывает единый empty-state", () => {
    renderTable({ rows: [], emptyMessage: "Пусто тут" });
    expect(screen.getByText("Пусто тут")).toBeInTheDocument();
  });

  it("error показывает единый блок ошибки", () => {
    renderTable({ isError: true, rows: [], errorMessage: "Ошибка загрузки" });
    expect(screen.getByText("Ошибка загрузки")).toBeInTheDocument();
  });
});

describe("DataTable курсорная пагинация", () => {
  it("показывает «Показать ещё» при hasNextPage и вызывает onLoadMore", async () => {
    const onLoadMore = vi.fn();
    const user = userEvent.setup();
    renderTable({ pagination: { hasNextPage: true, onLoadMore } });
    const btn = screen.getByRole("button", { name: /Показать ещё/ });
    await user.click(btn);
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });

  it("пустой курсор (hasNextPage=false) скрывает управление «дальше»", () => {
    renderTable({ pagination: { hasNextPage: false, onLoadMore: vi.fn() } });
    expect(screen.queryByRole("button", { name: /Показать ещё/ })).toBeNull();
  });
});

describe("DataTable сортировка", () => {
  it("сортирует по колонке и отражает направление в aria-sort", async () => {
    const user = userEvent.setup();
    renderTable();
    const nameHeader = screen.getByRole("columnheader", { name: /Имя/ });
    // По умолчанию — без сортировки.
    expect(nameHeader).toHaveAttribute("aria-sort", "none");

    await user.click(within(nameHeader).getByRole("button"));
    expect(nameHeader).toHaveAttribute("aria-sort", "ascending");

    // Первая строка данных после сортировки по возрастанию — alpha.
    const firstDataRow = screen.getAllByRole("row")[1];
    expect(within(firstDataRow).getByText("alpha")).toBeInTheDocument();

    // Повторный клик — по убыванию.
    await user.click(within(nameHeader).getByRole("button"));
    expect(nameHeader).toHaveAttribute("aria-sort", "descending");
    const firstDesc = screen.getAllByRole("row")[1];
    expect(within(firstDesc).getByText("charlie")).toBeInTheDocument();
  });

  it("несортируемая колонка не имеет кнопки сортировки", () => {
    renderTable();
    const countHeader = screen.getByRole("columnheader", { name: /Счётчик/ });
    expect(within(countHeader).queryByRole("button")).toBeNull();
  });
});

describe("DataTable строки-клики", () => {
  it("вызывает onRowClick по Enter с клавиатуры", async () => {
    const onRowClick = vi.fn();
    const user = userEvent.setup();
    renderTable({ onRowClick });
    const firstDataRow = screen.getAllByRole("row")[1];
    firstDataRow.focus();
    await user.keyboard("{Enter}");
    expect(onRowClick).toHaveBeenCalledTimes(1);
  });
});
