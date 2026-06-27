// Тесты карточки вывода из эксплуатации (ADR-0017): действие выполняется через
// ConfirmDialog (открыть → отметка нагрузки + ввод имени → подтвердить), результат
// и ошибки (422/409/403) — через тосты; блокировка для неактивного/уже выведенного
// сервиса.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { DecommissionCard } from "./DecommissionCard";
import { ToastProvider } from "./ui/toast";

// Мокаем клиент периметра, сохраняя реальные zod-схемы.
const { decommissionService } = vi.hoisted(() => ({ decommissionService: vi.fn() }));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return { ...actual, apiClient: { decommissionService } };
});

function renderCard(status = "active", onStarted?: () => void) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <DecommissionCard project="demo" name="svc" status={status} onStarted={onStarted} />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

// openAndConfirm открывает модалку, заполняет подтверждение и нажимает «Вывести».
async function openAndConfirm(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /Вывести из эксплуатации/i }));
  await user.click(screen.getByRole("checkbox"));
  await user.type(screen.getByLabelText(/Имя сервиса для подтверждения/i), "svc");
  await user.click(screen.getByRole("button", { name: /^Вывести$/i }));
}

describe("DecommissionCard", () => {
  beforeEach(() => {
    decommissionService.mockReset();
  });

  it("блокирует действие для уже выведенного сервиса", () => {
    renderCard("decommissioned");
    expect(screen.getByText(/уже выведен из эксплуатации/i)).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Вывести из эксплуатации/i }),
    ).toBeNull();
  });

  it("блокирует действие для неактивного сервиса (creating)", () => {
    renderCard("creating");
    expect(screen.getByText(/доступен только для активного/i)).toBeInTheDocument();
  });

  it("в модалке подтверждение неактивно без имени и отметки нагрузки", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(screen.getByRole("button", { name: /Вывести из эксплуатации/i }));
    expect(screen.getByRole("button", { name: /^Вывести$/i })).toBeDisabled();
  });

  it("happy-path: load_drained уходит в периметр, успех → onStarted (без тоста)", async () => {
    decommissionService.mockResolvedValue({
      project: "demo",
      name: "svc",
      status: "decommissioned",
      owners: [],
      owners_version: 0,
    });
    const onStarted = vi.fn();
    const user = userEvent.setup();
    renderCard("active", onStarted);

    await openAndConfirm(user);

    await waitFor(() => expect(decommissionService).toHaveBeenCalledTimes(1));
    expect(decommissionService).toHaveBeenCalledWith(
      { load_drained: true },
      { params: { project: "demo", name: "svc" } },
    );
    // Ход и исход показывает единый степпер (onStarted), а не тост успеха.
    await waitFor(() => expect(onStarted).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("предусловие не выполнено (422) → тост с понятным сообщением", async () => {
    decommissionService.mockRejectedValue({ response: { status: 422 } });
    const user = userEvent.setup();
    renderCard();
    await openAndConfirm(user);
    expect(await screen.findByRole("alert")).toHaveTextContent(/нагрузка снята из K8s/i);
  });

  it("конкурентный конфликт (409) → тост с понятным сообщением", async () => {
    decommissionService.mockRejectedValue({ response: { status: 409 } });
    const user = userEvent.setup();
    renderCard();
    await openAndConfirm(user);
    expect(await screen.findByRole("alert")).toHaveTextContent(/изменился в другом месте/i);
  });

  it("отказ доступа (403) → тост с понятным сообщением", async () => {
    decommissionService.mockRejectedValue({ response: { status: 403 } });
    const user = userEvent.setup();
    renderCard();
    await openAndConfirm(user);
    expect(await screen.findByRole("alert")).toHaveTextContent(/Недостаточно прав/i);
  });
});
