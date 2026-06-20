// Тесты карточки переноса сервиса: happy-path (target_project уходит в периметр
// после подтверждения именем), отказ доступа (403), конфликт занятого имени (409),
// недопустимый статус (422), блокировка для неактивного/переносимого сервиса.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { TransferCard } from "./TransferCard";

// Мокаем клиент периметра, сохраняя реальные zod-схемы.
const { transferService } = vi.hoisted(() => ({ transferService: vi.fn() }));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return { ...actual, apiClient: { transferService } };
});

function renderCard(status = "active") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <TransferCard project="demo" name="svc" status={status} />
    </QueryClientProvider>,
  );
}

// confirm заполняет подтверждение (целевой проект + точное имя сервиса).
async function confirm(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText(/Целевой проект/i), "demo2");
  await user.type(screen.getByLabelText(/Имя сервиса для подтверждения/i), "svc");
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

  it("кнопка неактивна без подтверждения именем и целевым проектом", () => {
    renderCard();
    expect(screen.getByRole("button", { name: /Перенести сервис/i })).toBeDisabled();
  });

  it("happy-path: target_project уходит в периметр после подтверждения", async () => {
    transferService.mockResolvedValue({ project: "demo", name: "svc", status: "transferring", owners: [], owners_version: 0 });
    const user = userEvent.setup();
    renderCard();

    await confirm(user);
    await user.click(screen.getByRole("button", { name: /Перенести сервис/i }));

    await waitFor(() => expect(transferService).toHaveBeenCalledTimes(1));
    expect(transferService).toHaveBeenCalledWith(
      { target_project: "demo2" },
      { params: { project: "demo", name: "svc" } },
    );
  });

  it("отказ доступа (403) → понятное сообщение", async () => {
    transferService.mockRejectedValue({ response: { status: 403 } });
    const user = userEvent.setup();
    renderCard();

    await confirm(user);
    await user.click(screen.getByRole("button", { name: /Перенести сервис/i }));

    expect(await screen.findByText(/Недостаточно прав/i)).toBeInTheDocument();
  });

  it("занятое имя в target (409) → понятное сообщение", async () => {
    transferService.mockRejectedValue({ response: { status: 409 } });
    const user = userEvent.setup();
    renderCard();

    await confirm(user);
    await user.click(screen.getByRole("button", { name: /Перенести сервис/i }));

    expect(await screen.findByText(/уже занято в целевом проекте/i)).toBeInTheDocument();
  });

  it("недопустимый исходный статус (422) → понятное сообщение", async () => {
    transferService.mockRejectedValue({ response: { status: 422 } });
    const user = userEvent.setup();
    renderCard();

    await confirm(user);
    await user.click(screen.getByRole("button", { name: /Перенести сервис/i }));

    expect(await screen.findByText(/только для активного сервиса/i)).toBeInTheDocument();
  });
});
