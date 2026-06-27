// Тесты карточки владельцев (ADR-0017): отображение состава; изменение через
// модалку (Dialog) с формой react-hook-form + zod; успех поднимает единый
// ступенчатый прогресс (onStarted), а не тост; ошибки (409/403) — через тосты.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { OwnersCard, parseOwners } from "./OwnersCard";
import { ToastProvider } from "./ui/toast";

// Мокаем клиент периметра, сохраняя реальные zod-схемы.
const { setServiceOwners } = vi.hoisted(() => ({ setServiceOwners: vi.fn() }));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return { ...actual, apiClient: { setServiceOwners } };
});

function renderCard(onStarted?: () => void) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <OwnersCard
          project="demo"
          name="svc"
          owners={["alice"]}
          ownersVersion={4}
          onStarted={onStarted}
        />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("parseOwners", () => {
  it("нормализует: trim, дедуп, сортировка, отбрасывание пустых", () => {
    expect(parseOwners(" bob , alice\n\nbob ")).toEqual(["alice", "bob"]);
  });
});

describe("OwnersCard", () => {
  beforeEach(() => {
    setServiceOwners.mockReset();
  });

  it("отображает текущих владельцев", () => {
    renderCard();
    expect(screen.getByLabelText(/Текущие владельцы/i)).toHaveTextContent("alice");
  });

  it("happy-path: нормализованный набор и версия уходят в периметр, успех → onStarted (без тоста)", async () => {
    setServiceOwners.mockResolvedValue({ owners: ["alice", "bob"], owners_version: 5 });
    const onStarted = vi.fn();
    const user = userEvent.setup();
    renderCard(onStarted);

    await user.click(screen.getByRole("button", { name: /Изменить владельцев/i }));
    const textarea = screen.getByLabelText(/Новый состав владельцев/i);
    await user.clear(textarea);
    await user.type(textarea, "bob, alice");
    await user.click(screen.getByRole("button", { name: /Сохранить владельцев/i }));

    await waitFor(() => expect(setServiceOwners).toHaveBeenCalledTimes(1));
    expect(setServiceOwners).toHaveBeenCalledWith(
      { owners: ["alice", "bob"], owners_version: 4 },
      { params: { project: "demo", name: "svc" } },
    );
    // Успех поднимает единый ступенчатый прогресс (onStarted), а не тост успеха.
    await waitFor(() => expect(onStarted).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("конфликт версии (409) → тост с понятным сообщением", async () => {
    setServiceOwners.mockRejectedValue({ response: { status: 409 } });
    const user = userEvent.setup();
    renderCard();

    await user.click(screen.getByRole("button", { name: /Изменить владельцев/i }));
    await user.click(screen.getByRole("button", { name: /Сохранить владельцев/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/изменился в другом месте/i);
  });

  it("отказ доступа (403) → тост с понятным сообщением", async () => {
    setServiceOwners.mockRejectedValue({ response: { status: 403 } });
    const user = userEvent.setup();
    renderCard();

    await user.click(screen.getByRole("button", { name: /Изменить владельцев/i }));
    await user.click(screen.getByRole("button", { name: /Сохранить владельцев/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/Недостаточно прав/i);
  });
});
