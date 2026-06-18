// Тест формы создания: happy-path (валидное имя → вызов периметра и переход на
// экран прогресса) и клиентская валидация (пустое имя → запрос не уходит).
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import { CreateServicePage } from "./CreateServicePage";

// Мокаем клиент периметра, сохраняя реальные zod-схемы (источник валидации).
// vi.hoisted нужен, т.к. фабрика vi.mock поднимается в начало файла.
const { createService } = vi.hoisted(() => ({ createService: vi.fn() }));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return { ...actual, apiClient: { createService } };
});

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/projects/demo/services/new"]}>
        <Routes>
          <Route path="/projects/:project/services/new" element={<CreateServicePage />} />
          <Route path="/projects/:project/services/:name" element={<div>экран прогресса: svc</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("CreateServicePage", () => {
  beforeEach(() => {
    createService.mockReset();
  });

  it("happy-path: валидное имя → вызов периметра и переход на прогресс", async () => {
    createService.mockResolvedValue({ id: "id-1", status: "creating" });
    const user = userEvent.setup();
    renderPage();

    await user.type(screen.getByLabelText(/Имя сервиса/i), "svc");
    await user.click(screen.getByRole("button", { name: /Создать/i }));

    await waitFor(() => expect(createService).toHaveBeenCalledTimes(1));
    expect(createService).toHaveBeenCalledWith({ name: "svc" }, { params: { project: "demo" } });
    expect(await screen.findByText(/экран прогресса/i)).toBeInTheDocument();
  });

  it("пустое имя → клиентская валидация, запрос не уходит", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole("button", { name: /Создать/i }));

    await screen.findByText(/String must contain at least 1 character|Некорректное имя|Required/i);
    expect(createService).not.toHaveBeenCalled();
  });
});
