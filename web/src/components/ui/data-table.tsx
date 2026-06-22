// Переиспользуемый компонент таблицы (DataTable) дизайн-системы портала
// (ADR-0017). Доступная семантическая разметка (`<table>` с заголовками и
// `aria-sort`), выравнивание колонок по типу данных, регулируемая плотность строк,
// опциональная «липкая» шапка, единые состояния loading (скелет)/empty/error и
// КУРСОРНАЯ пагинация по ADR-0009 (курсор пробрасывается владельцем без
// интерпретации; DataTable лишь вызывает «загрузить ещё», пока есть следующая
// страница). Клиентская сортировка — только для колонок, у которых задан
// `sortValue` (для полностью загруженных небольших наборов).
import { useMemo, useState, type ReactNode } from "react";
import { ArrowDown, ArrowUp, ChevronsUpDown, AlertTriangle, Inbox } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// Align — выравнивание содержимого колонки (текст — влево, числа — вправо и т.п.).
export type Align = "left" | "right" | "center";

// Density — плотность строк: комфортная (по умолчанию) и компактная.
export type Density = "comfortable" | "compact";

// ColumnDef описывает одну колонку таблицы.
export type ColumnDef<T> = {
  // id — стабильный ключ колонки (для сортировки и React-ключей).
  id: string;
  // header — заголовок колонки.
  header: ReactNode;
  // cell — рендер ячейки строки.
  cell: (row: T) => ReactNode;
  // align — выравнивание содержимого (и заголовка).
  align?: Align;
  // sortValue — если задан, колонка клиентски сортируема по этому значению.
  sortValue?: (row: T) => string | number;
  // width — необязательная ширина колонки (CSS-значение).
  width?: string;
};

// CursorPagination — управление курсорной пагинацией (ADR-0009).
export type CursorPagination = {
  // hasNextPage — есть ли следующая страница (false при пустом курсоре → конец).
  hasNextPage: boolean;
  // isFetchingNextPage — идёт ли дозагрузка следующей страницы.
  isFetchingNextPage?: boolean;
  // onLoadMore — запросить следующую страницу (владелец сам прокидывает курсор).
  onLoadMore: () => void;
};

export type DataTableProps<T> = {
  columns: ColumnDef<T>[];
  rows: T[];
  // rowKey — стабильный ключ строки.
  rowKey: (row: T) => string;
  // caption — подпись таблицы для скринридеров.
  caption?: string;
  isLoading?: boolean;
  isError?: boolean;
  // errorMessage — единое сообщение об ошибке (без сырых внутренних деталей).
  errorMessage?: string;
  // emptyMessage — единое сообщение пустого набора.
  emptyMessage?: string;
  density?: Density;
  stickyHeader?: boolean;
  // skeletonRows — число строк-скелетов в состоянии загрузки.
  skeletonRows?: number;
  pagination?: CursorPagination;
  // onRowClick — переход по строке (доступен с клавиатуры: Enter/Space).
  onRowClick?: (row: T) => void;
};

// SortState — текущая сортировка (колонка + направление).
type SortState = { columnId: string; dir: "asc" | "desc" } | null;

// alignClass переводит выравнивание в Tailwind-класс.
function alignClass(align: Align | undefined): string {
  if (align === "right") return "text-right";
  if (align === "center") return "text-center";
  return "text-left";
}

// ariaSortOf возвращает значение aria-sort для заголовка колонки.
function ariaSortOf(sort: SortState, columnId: string): "ascending" | "descending" | "none" {
  if (!sort || sort.columnId !== columnId) return "none";
  return sort.dir === "asc" ? "ascending" : "descending";
}

export function DataTable<T>({
  columns,
  rows,
  rowKey,
  caption,
  isLoading = false,
  isError = false,
  errorMessage = "Не удалось загрузить данные.",
  emptyMessage = "Нет данных для отображения.",
  density = "comfortable",
  stickyHeader = false,
  skeletonRows = 5,
  pagination,
  onRowClick,
}: DataTableProps<T>) {
  const [sort, setSort] = useState<SortState>(null);

  // Клиентская сортировка применяется только при активной сортируемой колонке.
  const sortedRows = useMemo(() => {
    if (!sort) return rows;
    const col = columns.find((c) => c.id === sort.columnId);
    if (!col?.sortValue) return rows;
    const getVal = col.sortValue;
    const factor = sort.dir === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => {
      const va = getVal(a);
      const vb = getVal(b);
      if (va < vb) return -1 * factor;
      if (va > vb) return 1 * factor;
      return 0;
    });
  }, [rows, sort, columns]);

  // toggleSort циклически переключает направление сортировки по колонке.
  const toggleSort = (columnId: string) => {
    setSort((prev) => {
      if (!prev || prev.columnId !== columnId) return { columnId, dir: "asc" };
      if (prev.dir === "asc") return { columnId, dir: "desc" };
      return null;
    });
  };

  const cellPad = density === "compact" ? "px-3 py-1.5" : "px-4 py-2.5";
  const colCount = columns.length;

  return (
    <div className="overflow-hidden rounded-xl border border-border">
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-sm" role="table">
          {caption && <caption className="sr-only">{caption}</caption>}
          <thead className={cn("bg-muted/40", stickyHeader && "sticky top-0 z-10")}>
            <tr className="border-b border-border">
              {columns.map((col) => {
                const sortable = Boolean(col.sortValue);
                const sortVal = ariaSortOf(sort, col.id);
                return (
                  <th
                    key={col.id}
                    scope="col"
                    aria-sort={sortable ? sortVal : undefined}
                    style={col.width ? { width: col.width } : undefined}
                    className={cn(
                      "font-medium text-muted-foreground",
                      cellPad,
                      alignClass(col.align),
                    )}
                  >
                    {sortable ? (
                      <button
                        type="button"
                        onClick={() => toggleSort(col.id)}
                        className={cn(
                          "inline-flex items-center gap-1 rounded outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
                          col.align === "right" && "flex-row-reverse",
                        )}
                      >
                        {col.header}
                        {sortVal === "ascending" ? (
                          <ArrowUp className="size-3.5" aria-hidden="true" />
                        ) : sortVal === "descending" ? (
                          <ArrowDown className="size-3.5" aria-hidden="true" />
                        ) : (
                          <ChevronsUpDown className="size-3.5 opacity-50" aria-hidden="true" />
                        )}
                      </button>
                    ) : (
                      col.header
                    )}
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody>
            {/* Состояние загрузки: скелет строк (а не пустой экран/ошибка). */}
            {isLoading &&
              Array.from({ length: skeletonRows }).map((_, i) => (
                <tr key={`skeleton-${i}`} className="border-b border-border/60">
                  {columns.map((col) => (
                    <td key={col.id} className={cellPad}>
                      <div
                        className="h-4 w-full max-w-[12rem] animate-pulse rounded bg-muted"
                        aria-hidden="true"
                      />
                    </td>
                  ))}
                </tr>
              ))}

            {/* Состояние ошибки: единый блок без сырых внутренних деталей. */}
            {!isLoading && isError && (
              <tr>
                <td colSpan={colCount} className="px-4 py-10 text-center">
                  <span className="flex flex-col items-center gap-2 text-sm text-destructive">
                    <AlertTriangle className="size-6" aria-hidden="true" />
                    {errorMessage}
                  </span>
                </td>
              </tr>
            )}

            {/* Пустой набор: единый empty-state. */}
            {!isLoading && !isError && sortedRows.length === 0 && (
              <tr>
                <td colSpan={colCount} className="px-4 py-10 text-center">
                  <span className="flex flex-col items-center gap-2 text-sm text-muted-foreground">
                    <Inbox className="size-6" aria-hidden="true" />
                    {emptyMessage}
                  </span>
                </td>
              </tr>
            )}

            {/* Данные. */}
            {!isLoading &&
              !isError &&
              sortedRows.map((row) => {
                const clickable = Boolean(onRowClick);
                return (
                  <tr
                    key={rowKey(row)}
                    className={cn(
                      "border-b border-border/60 last:border-0",
                      clickable &&
                        "cursor-pointer outline-none transition-colors hover:bg-muted/50 focus-visible:bg-muted/50",
                    )}
                    tabIndex={clickable ? 0 : undefined}
                    onClick={clickable ? () => onRowClick?.(row) : undefined}
                    onKeyDown={
                      clickable
                        ? (e) => {
                            if (e.key === "Enter" || e.key === " ") {
                              e.preventDefault();
                              onRowClick?.(row);
                            }
                          }
                        : undefined
                    }
                  >
                    {columns.map((col) => (
                      <td
                        key={col.id}
                        className={cn(cellPad, alignClass(col.align))}
                      >
                        {col.cell(row)}
                      </td>
                    ))}
                  </tr>
                );
              })}
          </tbody>
        </table>
      </div>

      {/* Курсорная пагинация (ADR-0009): кнопка скрыта, когда следующей страницы
          нет (пустой курсор → конец выборки). */}
      {pagination?.hasNextPage && (
        <div className="flex justify-center border-t border-border bg-muted/20 p-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={pagination.isFetchingNextPage}
            onClick={() => pagination.onLoadMore()}
          >
            {pagination.isFetchingNextPage ? "Загрузка…" : "Показать ещё"}
          </Button>
        </div>
      )}
    </div>
  );
}
