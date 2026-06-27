// Тесты карточки переноса сервиса (ADR-0017): действие через ConfirmDialog
// (открыть → целевой проект + имя → подтвердить); успех поднимает единый
// ступенчатый прогресс (onStarted), а не тост; ошибки (403/409/422) — через
// тосты; блокировка для неактивного/переносимого сервиса.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { TransferCard } from "./TransferCard";
import { ToastProvider } from "./ui/toast";

// Мокаем клиент периметра, сохраняя реальные zod-схемы.
const { transferService } = vi.hoisted(() => ({ transferService: vi.fn() }));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return { ...actual, apiClient: { transferService } };
});

function renderCard(status = "active", onStarted?: () => void) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <TransferCard project="demo" name="svc" status={status} onStarted={onStarted} />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

// openAndConfirm открывает модалку, заполняет подтверждение и нажимает «Перенести».
async function openAndConfirm(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /Перенести сервис/i }));
  await user.type(screen.getByLabelText(/Целевой проект/i), "demo2");
  await user.type(screen.getByLabelText(/Имя сервиса для подтверждения/i), "svc");
  await user.click(screen.getByRole("button", { name: /^Перенести$/i }));
}

describe("TransferCard", () => {
  beforeEach(() => {
    transferService.mockReset();
  });

  it("блокирует действие для переносимого сервиса", () => {
    renderCard("transferring");
    expect(screen.getByText(/уже переносится/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Перенести сервис/i })).toBeNull();
  });

  it("блокирует действие для неактивного сервиса (creating)", () => {
    renderCard("creating");
    expect(screen.getByText(/доступен только для активного/i)).toBeInTheDocument();
  });

  it("в модалке подтверждение неактивно без имени и целевого проекта", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(screen.getByRole("button", { name: /Перенести сервис/i }));
    expect(screen.getByRole("button", { name: /^Перенести$/i })).toBeDisabled();
  });

  it("happy-path: target_project уходит в периметр, успех → onStarted (без тоста)", async () => {
    transferService.mockResolvedValue({
      project: "demo",
      name: "svc",
      status: "transferring",
      owners: [],
      owners_version: 0,
    });
    const onStarted = vi.fn();
    const user = userEvent.setup();
    renderCard("active", onStarted);

    await openAndConfirm(user);

    await waitFor(() => expect(transferService).toHaveBeenCalledTimes(1));
    expect(transferService).toHaveBeenCalledWith(
      { target_project: "demo2" },
      { params: { project: "demo", name: "svc" } },
    );
    // Ход и исход показывает единый степпер (onStarted), а не тост успеха.
    await waitFor(() => expect(onStarted).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("отказ доступа (403) → тост с понятным сообщением", async () => {
    transferService.mockRejectedValue({ response: { status: 403 } });
    const user = userEvent.setup();
    renderCard();
    await openAndConfirm(user);
    expect(await screen.findByRole("alert")).toHaveTextContent(/Недостаточно прав/i);
  });

  it("занятое имя в target (409) → тост с понятным сообщением", async () => {
    transferService.mockRejectedValue({ response: { status: 409 } });
    const user = userEvent.setup();
    renderCard();
    await openAndConfirm(user);
    expect(await screen.findByRole("alert")).toHaveTextContent(/уже занято в целевом проекте/i);
  });

  it("недопустимый исходный статус (422) → тост с понятным сообщением", async () => {
    transferService.mockRejectedValue({ response: { status: 422 } });
    const user = userEvent.setup();
    renderCard();
    await openAndConfirm(user);
    expect(await screen.findByRole("alert")).toHaveTextContent(/только для активного сервиса/i);
  });
});
