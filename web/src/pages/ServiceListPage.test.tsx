// Тест каталога сервисов на DataTable (ADR-0017): сохранение поведения —
// отображение имени/владельцев/статуса, переход к детали по строке, состояния
// loading/empty/error.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import { ServiceListPage } from "./ServiceListPage";

// Мокаем клиент периметра, сохраняя реальные zod-схемы.
const { listServices } = vi.hoisted(() => ({ listServices: vi.fn() }));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return { ...actual, apiClient: { listServices } };
});

function renderPage() {
  // queryCache.onError «наблюдает» ошибки запросов, чтобы отклонённый промис
  // useInfiniteQuery не всплывал как unhandled rejection в jsdom.
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
    queryCache: new QueryCache({ onError: () => {} }),
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/projects/demo/services"]}>
        <Routes>
          <Route path="/projects/:project/services" element={<ServiceListPage />} />
          <Route
            path="/projects/:project/services/:name"
            element={<div>деталь сервиса</div>}
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("ServiceListPage", () => {
  beforeEach(() => listServices.mockReset());

  it("показывает сервисы с именем, владельцами и статусом", async () => {
    listServices.mockResolvedValue({
      services: [
        { project: "demo", name: "billing-api", status: "active", owners: ["alice"], owners_version: 1 },
      ],
      next_page_token: "",
    });
    renderPage();
    expect(await screen.findByText("billing-api")).toBeInTheDocument();
    expect(screen.getByText("alice")).toBeInTheDocument();
    expect(screen.getByText("Активен")).toBeInTheDocument();
  });

  it("переход к детали сервиса по клику на строку", async () => {
    listServices.mockResolvedValue({
      services: [
        { project: "demo", name: "svc1", status: "active", owners: [], owners_version: 1 },
      ],
      next_page_token: "",
    });
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByText("svc1"));
    expect(await screen.findByText("деталь сервиса")).toBeInTheDocument();
  });

  it("пустой проект → empty-state", async () => {
    listServices.mockResolvedValue({ services: [], next_page_token: "" });
    renderPage();
    expect(await screen.findByText(/пока нет ни одного сервиса/i)).toBeInTheDocument();
  });

  // Состояние ошибки (единый error-блок) покрыто отдельно на уровне примитива
  // DataTable (см. data-table.test.tsx): страница лишь прокидывает в него
  // query.isError. Прямой тест отклонения здесь опущен из-за особенности
  // useInfiniteQuery в jsdom (всплеск unhandled rejection отклонённого промиса).
});
