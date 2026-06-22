// Тесты системы уведомлений (ADR-0017): тост успеха объявляется (role=status);
// ошибка переводится единым маппингом кодов периметра (role=alert) без сырых
// деталей; 503 даёт сообщение о временной недоступности.
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { ToastProvider, useToast } from "./toast";

// makeErr имитирует ошибку периметра с заданным кодом.
function makeErr(status: number) {
  return { response: { status } };
}

// Harness даёт кнопки, дёргающие toast.success / toast.error.
function Harness() {
  const toast = useToast();
  return (
    <div>
      <button type="button" onClick={() => toast.success("Готово!")}>
        ok
      </button>
      <button type="button" onClick={() => toast.error(makeErr(403))}>
        err403
      </button>
      <button type="button" onClick={() => toast.error(makeErr(503))}>
        err503
      </button>
    </div>
  );
}

function renderHarness() {
  return render(
    <ToastProvider>
      <Harness />
    </ToastProvider>,
  );
}

describe("Toast", () => {
  it("тост успеха объявляется через role=status", async () => {
    const user = userEvent.setup();
    renderHarness();
    await user.click(screen.getByRole("button", { name: "ok" }));
    const toast = await screen.findByRole("status");
    expect(toast).toHaveTextContent("Готово!");
  });

  it("ошибка 403 переводится в понятное сообщение (role=alert)", async () => {
    const user = userEvent.setup();
    renderHarness();
    await user.click(screen.getByRole("button", { name: "err403" }));
    const toast = await screen.findByRole("alert");
    expect(toast).toHaveTextContent(/прав/i);
    // Сырой статус наружу не просачивается.
    expect(toast).not.toHaveTextContent("403");
  });

  it("503 даёт сообщение о временной недоступности", async () => {
    const user = userEvent.setup();
    renderHarness();
    await user.click(screen.getByRole("button", { name: "err503" }));
    const toast = await screen.findByRole("alert");
    expect(toast).toHaveTextContent(/временно недоступн|позже/i);
  });

  it("уведомление закрывается по кнопке", async () => {
    const user = userEvent.setup();
    renderHarness();
    await user.click(screen.getByRole("button", { name: "ok" }));
    await screen.findByRole("status");
    await user.click(screen.getByRole("button", { name: "Закрыть уведомление" }));
    expect(screen.queryByRole("status")).toBeNull();
  });
});
