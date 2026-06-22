// Confirm-диалог для деструктивных операций (ADR-0017): удаление роли/права,
// открепление права, decommission, transfer. Деструктивное действие НЕ выполняется
// без явного подтверждения. Наследует все a11y-гарантии Dialog (focus-trap, ESC,
// возврат фокуса, блокировка скролла). Для особо рискованных/необратимых операций
// можно потребовать ввод подтверждающего текста (например, имени сервиса) и
// дополнительные предусловия через слот `children` + флаг `confirmDisabled`.
import { type ReactNode } from "react";
import { Loader2 } from "lucide-react";

import { Dialog } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";

export type ConfirmDialogProps = {
  open: boolean;
  onClose: () => void;
  // onConfirm — подтверждение деструктивного действия.
  onConfirm: () => void;
  title: ReactNode;
  description?: ReactNode;
  // children — дополнительные поля подтверждения (ввод имени, предусловия).
  children?: ReactNode;
  // confirmLabel — подпись кнопки подтверждения.
  confirmLabel?: string;
  cancelLabel?: string;
  // destructive — оформить кнопку подтверждения как деструктивную (по умолчанию да).
  destructive?: boolean;
  // confirmDisabled — заблокировать подтверждение, пока предусловия не выполнены.
  confirmDisabled?: boolean;
  // pending — идёт выполнение действия (показываем индикатор, блокируем кнопку).
  pending?: boolean;
};

export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  description,
  children,
  confirmLabel = "Подтвердить",
  cancelLabel = "Отмена",
  destructive = true,
  confirmDisabled = false,
  pending = false,
}: ConfirmDialogProps) {
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={title}
      description={description}
      footer={
        <>
          <Button type="button" variant="ghost" onClick={onClose} disabled={pending}>
            {cancelLabel}
          </Button>
          <Button
            type="button"
            variant={destructive ? "destructive" : "default"}
            disabled={confirmDisabled || pending}
            onClick={onConfirm}
          >
            {pending && <Loader2 className="size-4 animate-spin" />}
            {confirmLabel}
          </Button>
        </>
      }
    >
      {children}
    </Dialog>
  );
}
