// Система уведомлений (toast) дизайн-системы портала (ADR-0017). Показывает
// результат действий (успех/ошибка) вместо разрозненного инлайнового текста.
// Ошибки переводятся ЕДИНЫМ маппингом кодов периметра (lib/errors:
// 400/403/404/409/422/503) в понятные сообщения без раскрытия сырых внутренних
// деталей. Доступность: контейнер — live-region (`aria-live`), успех —
// `role="status"`, ошибка — `role="alert"`. Анимации появления — только в
// `motion-safe` (уважение `prefers-reduced-motion`).
import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { CheckCircle2, AlertTriangle, X } from "lucide-react";

import { cn } from "@/lib/utils";
import { perimeterErrorMessage, type PerimeterErrorOptions } from "@/lib/errors";

type ToastKind = "success" | "error" | "info";

type ToastItem = { id: number; kind: ToastKind; message: string };

type ToastApi = {
  // success показывает уведомление об успехе.
  success: (message: string) => void;
  // error показывает уведомление об ошибке, переводя код периметра в сообщение.
  error: (err: unknown, options?: PerimeterErrorOptions) => void;
  // info показывает нейтральное уведомление.
  info: (message: string) => void;
};

const ToastContext = createContext<ToastApi | null>(null);

// AUTO_DISMISS_MS — автозакрытие уведомления (мс).
const AUTO_DISMISS_MS = 5000;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const nextId = useRef(1);

  const dismiss = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const push = useCallback(
    (kind: ToastKind, message: string) => {
      const id = nextId.current++;
      setToasts((prev) => [...prev, { id, kind, message }]);
      setTimeout(() => dismiss(id), AUTO_DISMISS_MS);
    },
    [dismiss],
  );

  const api = useMemo<ToastApi>(
    () => ({
      success: (message) => push("success", message),
      info: (message) => push("info", message),
      error: (err, options) => push("error", perimeterErrorMessage(err, options)),
    }),
    [push],
  );

  return (
    <ToastContext.Provider value={api}>
      {children}
      {createPortal(
        <div
          className="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-full max-w-sm flex-col gap-2"
          aria-live="polite"
          aria-atomic="false"
        >
          {toasts.map((t) => (
            <div
              key={t.id}
              role={t.kind === "error" ? "alert" : "status"}
              className={cn(
                "pointer-events-auto flex items-start gap-2 rounded-lg border px-3 py-2.5 text-sm shadow-md motion-safe:transition-all",
                t.kind === "success" && "border-success/30 bg-success/10 text-success",
                t.kind === "error" && "border-destructive/30 bg-destructive/10 text-destructive",
                t.kind === "info" && "border-border bg-card text-card-foreground",
              )}
            >
              {t.kind === "success" && <CheckCircle2 className="mt-0.5 size-4 shrink-0" />}
              {t.kind === "error" && <AlertTriangle className="mt-0.5 size-4 shrink-0" />}
              <span className="flex-1">{t.message}</span>
              <button
                type="button"
                aria-label="Закрыть уведомление"
                onClick={() => dismiss(t.id)}
                className="rounded p-0.5 opacity-70 outline-none transition-opacity hover:opacity-100 focus-visible:ring-2 focus-visible:ring-ring"
              >
                <X className="size-3.5" />
              </button>
            </div>
          ))}
        </div>,
        document.body,
      )}
    </ToastContext.Provider>
  );
}

// useToast возвращает API уведомлений; вне ToastProvider — ошибка.
export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (ctx === null) {
    throw new Error("useToast должен использоваться внутри ToastProvider");
  }
  return ctx;
}
