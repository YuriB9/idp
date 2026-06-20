// Тесты карточки вывода из эксплуатации: happy-path (load_drained уходит в
// периметр после подтверждения именем), предусловие (422), конфликт (409), отказ
// доступа (403), блокировка для неактивного/уже выведенного сервиса.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { DecommissionCard } from "./DecommissionCard";

// Мокаем клиент периметра, сохраняя реальные zod-схемы.
const { decommissionService } = vi.hoisted(() => ({ decommissionService: vi.fn() }));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return { ...actual, apiClient: { decommissionService } };
});

function renderCard(status = "active") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <DecommissionCard project="demo" name="svc" status={status} />
    </QueryClientProvider>,
  );
}

// confirm заполняет подтверждение (отметка нагрузки + точное имя сервиса).
async function confirm(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("checkbox"));
  await user.type(screen.getByLabelText(/Имя сервиса для подтверждения/i), "svc");
}

describe("DecommissionCard", () => {
  beforeEach(() => {
    decommissionService.mockReset();
  });

  it("блокирует действие для уже выведенного сервиса", () => {
    renderCard("decommissioned");
    expect(screen.getByText(/уже выведен из эксплуатации/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Вывести из эксплуатации/i })).toBeNull();
  });

  it("блокирует действие для неактивного сервиса (creating)", () => {
    renderCard("creating");
    expect(screen.getByText(/доступен только для активного/i)).toBeInTheDocument();
  });

  it("кнопка неактивна без подтверждения именем", () => {
    renderCard();
    expect(screen.getByRole("button", { name: /Вывести из эксплуатации/i })).toBeDisabled();
  });

  it("happy-path: load_drained уходит в периметр после подтверждения", async () => {
    decommissionService.mockResolvedValue({ service: { project: "demo", name: "svc", status: "decommissioned", owners: [], owners_version: 0 } });
    const user = userEvent.setup();
    renderCard();

    await confirm(user);
    await user.click(screen.getByRole("button", { name: /Вывести из эксплуатации/i }));

    await waitFor(() => expect(decommissionService).toHaveBeenCalledTimes(1));
    expect(decommissionService).toHaveBeenCalledWith(
      { load_drained: true },
      { params: { project: "demo", name: "svc" } },
    );
  });

  it("предусловие не выполнено (422) → понятное сообщение", async () => {
    decommissionService.mockRejectedValue({ response: { status: 422 } });
    const user = userEvent.setup();
    renderCard();

    await confirm(user);
    await user.click(screen.getByRole("button", { name: /Вывести из эксплуатации/i }));

    expect(await screen.findByText(/нагрузка снята из K8s/i)).toBeInTheDocument();
  });

  it("конкурентный конфликт (409) → понятное сообщение", async () => {
    decommissionService.mockRejectedValue({ response: { status: 409 } });
    const user = userEvent.setup();
    renderCard();

    await confirm(user);
    await user.click(screen.getByRole("button", { name: /Вывести из эксплуатации/i }));

    expect(await screen.findByText(/изменился в другом месте/i)).toBeInTheDocument();
  });

  it("отказ доступа (403) → понятное сообщение", async () => {
    decommissionService.mockRejectedValue({ response: { status: 403 } });
    const user = userEvent.setup();
    renderCard();

    await confirm(user);
    await user.click(screen.getByRole("button", { name: /Вывести из эксплуатации/i }));

    expect(await screen.findByText(/Недостаточно прав/i)).toBeInTheDocument();
  });
});
