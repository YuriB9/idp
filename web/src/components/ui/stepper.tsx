// Переиспользуемый примитив ступенчатого прогресса дизайн-системы портала
// (ADR-0017). Презентационный: принимает готовый список шагов с состояниями и
// только отрисовывает их единым стилем (цвет + иконка на состояние, по образцу
// StatusBadge). Доступность: контейнер шагов — список с обёрткой-live-region
// (aria-live="polite"), смена текущего шага объявляется без перехвата фокуса.
// Анимации — только в motion-safe (уважение prefers-reduced-motion). Сырые
// внутренние ошибки не показываются — только подписи шагов и сообщение note.
import { CheckCircle2, Circle, Loader2, XCircle, AlertTriangle } from "lucide-react";

import { cn } from "@/lib/utils";
import type { Step, StepState } from "@/lib/workflow-steps";

// meta — оформление строки шага по состоянию: иконка, цвет, кручение спиннера.
const meta: Record<
  StepState,
  { Icon: typeof CheckCircle2; className: string; spin?: boolean }
> = {
  pending: { Icon: Circle, className: "text-muted-foreground" },
  running: { Icon: Loader2, className: "text-warning", spin: true },
  done: { Icon: CheckCircle2, className: "text-success" },
  failed: { Icon: XCircle, className: "text-destructive" },
};

type StepperProps = {
  // steps — упорядоченные шаги прогресса.
  steps: Step[];
  // irreversible — пометка точки невозврата (decommission/transfer, ADR-0012/0013).
  irreversible?: boolean;
  // note — сопроводительное сообщение (например, факт отката Saga при failed).
  note?: string;
  // label — доступная подпись группы шагов (что за операция).
  label?: string;
};

export function Stepper({ steps, irreversible, note, label }: StepperProps) {
  // hasFailed — есть ли упавший шаг (влияет на стиль сообщения note).
  const hasFailed = steps.some((s) => s.state === "failed");

  return (
    <div className="flex flex-col gap-3" aria-live="polite">
      {irreversible && (
        <p className="flex items-center gap-1.5 text-xs font-medium text-warning">
          <AlertTriangle className="size-3.5 shrink-0" />
          Операция содержит необратимые шаги (точка невозврата).
        </p>
      )}

      <ol role="list" aria-label={label} className="flex flex-col gap-2.5">
        {steps.map((step) => {
          const m = meta[step.state];
          return (
            <li key={step.key} className="flex items-center gap-2.5 text-sm">
              <m.Icon
                className={cn(
                  "size-4 shrink-0",
                  m.className,
                  m.spin && "motion-safe:animate-spin",
                )}
              />
              <span
                className={cn(
                  step.state === "pending" && "text-muted-foreground",
                  step.state === "done" && "text-foreground",
                )}
              >
                {step.label}
              </span>
            </li>
          );
        })}
      </ol>

      {note && (
        <p
          className={cn(
            "text-sm",
            hasFailed ? "text-destructive" : "text-muted-foreground",
          )}
        >
          {note}
        </p>
      )}
    </div>
  );
}
