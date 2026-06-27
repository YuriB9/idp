// Тесты экрана сервиса с ЕДИНЫМ ступенчатым прогрессом (ADR-0022): создание
// показывает шаги воркфлоу степпером (а не однострочной панелью); терминальные
// исходы несут сообщение (успех/откат Saga).
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import { ServiceProgressPage } from "./ServiceProgressPage";
import { ToastProvider } from "@/components/ui/toast";

// Мокаем клиент периметра, сохраняя реальные zod-схемы.
const { getService } = vi.hoisted(() => ({ getService: vi.fn() }));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return { ...actual, apiClient: { getService } };
});

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <MemoryRouter initialEntries={["/projects/demo/services/svc"]}>
          <Routes>
            <Route
              path="/projects/:project/services/:name"
              element={<ServiceProgressPage />}
            />
          </Routes>
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("ServiceProgressPage", () => {
  beforeEach(() => {
    getService.mockReset();
  });

  it("создание (creating) показывает шаги воркфлоу степпером", async () => {
    getService.mockResolvedValue({
      project: "demo",
      name: "svc",
      status: "creating",
      owners: ["alice"],
      owners_version: 1,
    });
    renderPage();

    expect(await screen.findByText(/Создание репозитория GitLab/i)).toBeInTheDocument();
    expect(screen.getByRole("list", { name: /Создание сервиса/i })).toBeInTheDocument();
  });

  it("active → степпер завершён, сообщение об активном сервисе", async () => {
    getService.mockResolvedValue({
      project: "demo",
      name: "svc",
      status: "active",
      owners: ["alice"],
      owners_version: 1,
    });
    renderPage();

    expect(await screen.findByText(/Сервис создан и активен/i)).toBeInTheDocument();
  });

  it("failed → сообщение о выполненном откате (Saga)", async () => {
    getService.mockResolvedValue({
      project: "demo",
      name: "svc",
      status: "failed",
      owners: ["alice"],
      owners_version: 1,
    });
    renderPage();

    expect(await screen.findByText(/выполнен откат \(Saga\)/i)).toBeInTheDocument();
  });
});
