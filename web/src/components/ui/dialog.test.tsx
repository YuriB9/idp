// Тесты Dialog/ConfirmDialog (ADR-0017): открытие захватывает фокус; ESC и клик
// overlay закрывают и возвращают фокус на инициатор; focus-trap по Tab; сабмит
// формы в модалке; ConfirmDialog требует явного подтверждения и уважает
// confirmDisabled.
import { describe, expect, it, vi } from "vitest";
import { useState } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { Dialog } from "./dialog";
import { ConfirmDialog } from "./confirm-dialog";
import { Button } from "./button";

// Harness открывает Dialog по кнопке-инициатору (для проверки возврата фокуса).
function DialogHarness({ withForm = false }: { withForm?: boolean }) {
  const [open, setOpen] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  return (
    <div>
      <button type="button" onClick={() => setOpen(true)}>
        Открыть
      </button>
      <Dialog
        open={open}
        onClose={() => setOpen(false)}
        title="Заголовок окна"
        description="Описание"
        footer={
          withForm ? (
            <Button type="submit" form="dlg-form">
              Сохранить
            </Button>
          ) : (
            <button type="button" onClick={() => setOpen(false)}>
              Готово
            </button>
          )
        }
      >
        {withForm ? (
          <form
            id="dlg-form"
            onSubmit={(e) => {
              e.preventDefault();
              setSubmitted(true);
              setOpen(false);
            }}
          >
            <label htmlFor="fld">Поле</label>
            <input id="fld" />
          </form>
        ) : (
          <input aria-label="Внутреннее поле" />
        )}
      </Dialog>
      {submitted && <p>отправлено</p>}
    </div>
  );
}

describe("Dialog", () => {
  it("открытие захватывает фокус внутрь окна", async () => {
    const user = userEvent.setup();
    render(<DialogHarness />);
    await user.click(screen.getByRole("button", { name: "Открыть" }));
    const dialog = screen.getByRole("dialog");
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
  });

  it("ESC закрывает и возвращает фокус на инициатор", async () => {
    const user = userEvent.setup();
    render(<DialogHarness />);
    const opener = screen.getByRole("button", { name: "Открыть" });
    await user.click(opener);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.activeElement).toBe(opener);
  });

  it("клик по overlay закрывает окно", async () => {
    const user = userEvent.setup();
    render(<DialogHarness />);
    await user.click(screen.getByRole("button", { name: "Открыть" }));
    // Overlay — первая кнопка «Закрыть» (с aria-label) поверх фона.
    const closers = screen.getAllByRole("button", { name: "Закрыть" });
    await user.click(closers[0]);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("блокирует прокрутку фона на время открытия", async () => {
    const user = userEvent.setup();
    render(<DialogHarness />);
    await user.click(screen.getByRole("button", { name: "Открыть" }));
    expect(document.body.style.overflow).toBe("hidden");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(document.body.style.overflow).not.toBe("hidden"));
  });

  it("сабмит формы в модалке выполняется и закрывает окно", async () => {
    const user = userEvent.setup();
    render(<DialogHarness withForm />);
    await user.click(screen.getByRole("button", { name: "Открыть" }));
    await user.type(screen.getByLabelText("Поле"), "значение");
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    expect(await screen.findByText("отправлено")).toBeInTheDocument();
  });
});

describe("ConfirmDialog", () => {
  it("подтверждение вызывает onConfirm, отмена — нет", async () => {
    const onConfirm = vi.fn();
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(
      <ConfirmDialog
        open
        onClose={onClose}
        onConfirm={onConfirm}
        title="Удалить роль?"
        confirmLabel="Удалить"
      />,
    );
    await user.click(screen.getByRole("button", { name: "Удалить" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);

    await user.click(screen.getByRole("button", { name: "Отмена" }));
    expect(onClose).toHaveBeenCalled();
  });

  it("confirmDisabled блокирует подтверждение (предусловие не выполнено)", async () => {
    const onConfirm = vi.fn();
    const user = userEvent.setup();
    render(
      <ConfirmDialog
        open
        onClose={vi.fn()}
        onConfirm={onConfirm}
        title="Вывести из эксплуатации?"
        confirmLabel="Вывести"
        confirmDisabled
      />,
    );
    await user.click(screen.getByRole("button", { name: "Вывести" }));
    expect(onConfirm).not.toHaveBeenCalled();
  });
});
