// Тесты карточки владельцев: happy-path (нормализованный набор уходит в периметр),
// конфликт версии (409) и отказ доступа (403) показывают понятные сообщения.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { OwnersCard, parseOwners } from "./OwnersCard";

// Мокаем клиент периметра, сохраняя реальные zod-схемы.
const { setServiceOwners } = vi.hoisted(() => ({ setServiceOwners: vi.fn() }));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return { ...actual, apiClient: { setServiceOwners } };
});

function renderCard() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OwnersCard project="demo" name="svc" owners={["alice"]} ownersVersion={4} />
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

  it("happy-path: нормализованный набор и версия уходят в периметр", async () => {
    setServiceOwners.mockResolvedValue({ owners: ["alice", "bob"], owners_version: 5 });
    const user = userEvent.setup();
    renderCard();

    const textarea = screen.getByLabelText(/Новый состав владельцев/i);
    await user.clear(textarea);
    await user.type(textarea, "bob, alice");
    await user.click(screen.getByRole("button", { name: /Сохранить владельцев/i }));

    await waitFor(() => expect(setServiceOwners).toHaveBeenCalledTimes(1));
    expect(setServiceOwners).toHaveBeenCalledWith(
      { owners: ["alice", "bob"], owners_version: 4 },
      { params: { project: "demo", name: "svc" } },
    );
  });

  it("конфликт версии (409) → понятное сообщение", async () => {
    setServiceOwners.mockRejectedValue({ response: { status: 409 } });
    const user = userEvent.setup();
    renderCard();

    await user.click(screen.getByRole("button", { name: /Сохранить владельцев/i }));

    expect(await screen.findByText(/изменился в другом месте/i)).toBeInTheDocument();
  });

  it("отказ доступа (403) → понятное сообщение", async () => {
    setServiceOwners.mockRejectedValue({ response: { status: 403 } });
    const user = userEvent.setup();
    renderCard();

    await user.click(screen.getByRole("button", { name: /Сохранить владельцев/i }));

    expect(await screen.findByText(/Недостаточно прав/i)).toBeInTheDocument();
  });
});
