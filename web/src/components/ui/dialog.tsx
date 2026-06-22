// Примитив модального окна (Dialog) дизайн-системы портала (ADR-0017). Гарантии
// доступности: focus-trap (фокус заперт внутри окна), возврат фокуса на инициатор
// после закрытия, закрытие по ESC и по клику на overlay, блокировка прокрутки
// фона на время открытия, ARIA (`role="dialog"`, `aria-modal`, связь заголовка
// через `aria-labelledby`). Уважает `prefers-reduced-motion` (переходы заданы
// классами, которые ОС может редуцировать). Формы внутри — на react-hook-form +
// zod, как и раньше.
import { useCallback, useEffect, useId, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";

import { cn } from "@/lib/utils";

export type DialogProps = {
  open: boolean;
  // onClose — закрытие окна (ESC/overlay/крестик/после сабмита).
  onClose: () => void;
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  // footer — область действий (кнопки) внизу окна.
  footer?: ReactNode;
  // className — дополнительные классы контейнера окна (например, ширина).
  className?: string;
};

// FOCUSABLE — селектор фокусируемых элементов для focus-trap.
const FOCUSABLE =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

export function Dialog({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  className,
}: DialogProps) {
  const panelRef = useRef<HTMLDivElement>(null);
  // previouslyFocused — элемент, на который вернём фокус после закрытия.
  const previouslyFocused = useRef<HTMLElement | null>(null);
  const titleId = useId();
  const descId = useId();

  // onKeyDown реализует закрытие по ESC и focus-trap по Tab.
  const onKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
        return;
      }
      if (e.key !== "Tab") return;
      const panel = panelRef.current;
      if (!panel) return;
      const items = panel.querySelectorAll<HTMLElement>(FOCUSABLE);
      if (items.length === 0) {
        e.preventDefault();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      const active = document.activeElement as HTMLElement | null;
      if (e.shiftKey && (active === first || !panel.contains(active))) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && active === last) {
        e.preventDefault();
        first.focus();
      }
    },
    [onClose],
  );

  // При открытии: запоминаем фокус, переносим его внутрь окна, блокируем скролл.
  // При закрытии: возвращаем фокус и скролл.
  useEffect(() => {
    if (!open) return;
    previouslyFocused.current = document.activeElement as HTMLElement | null;
    const panel = panelRef.current;
    const firstFocusable = panel?.querySelector<HTMLElement>(FOCUSABLE);
    (firstFocusable ?? panel)?.focus();

    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    return () => {
      document.body.style.overflow = prevOverflow;
      previouslyFocused.current?.focus?.();
    };
  }, [open]);

  if (!open) return null;

  return createPortal(
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      {/* Overlay: клик закрывает окно. */}
      <button
        type="button"
        aria-label="Закрыть"
        tabIndex={-1}
        className="absolute inset-0 bg-black/50 motion-safe:transition-opacity"
        onClick={onClose}
      />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descId : undefined}
        tabIndex={-1}
        onKeyDown={onKeyDown}
        className={cn(
          "relative z-10 flex w-full max-w-lg flex-col gap-4 rounded-xl border border-border bg-card p-5 text-card-foreground shadow-lg outline-none",
          className,
        )}
      >
        <div className="flex items-start justify-between gap-4">
          <div className="flex flex-col gap-1">
            <h2 id={titleId} className="text-base font-semibold">
              {title}
            </h2>
            {description && (
              <p id={descId} className="text-sm text-muted-foreground">
                {description}
              </p>
            )}
          </div>
          <button
            type="button"
            aria-label="Закрыть"
            onClick={onClose}
            className="rounded-md p-1 text-muted-foreground outline-none transition-colors hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
          >
            <X className="size-4" />
          </button>
        </div>

        {children && <div className="flex flex-col gap-3">{children}</div>}

        {footer && <div className="flex justify-end gap-2">{footer}</div>}
      </div>
    </div>,
    document.body,
  );
}
