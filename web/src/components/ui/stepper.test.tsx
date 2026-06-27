// Тесты примитива ступенчатого прогресса (ADR-0017): отрисовка состояний шагов,
// пометка точки невозврата, сопроводительное сообщение и доступность (role/
// aria-live). Motion-safe-анимация задаётся классом motion-safe:* — уважение
// prefers-reduced-motion обеспечивается CSS (статичная индикация без motion-safe).
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { Stepper } from "./stepper";
import type { Step } from "@/lib/workflow-steps";

const steps: Step[] = [
  { key: "a", label: "Шаг А", state: "done" },
  { key: "b", label: "Шаг Б", state: "running" },
  { key: "c", label: "Шаг В", state: "pending" },
];

describe("Stepper", () => {
  it("отрисовывает все шаги списком с доступной подписью", () => {
    render(<Stepper steps={steps} label="Создание сервиса" />);
    const list = screen.getByRole("list", { name: "Создание сервиса" });
    expect(list).toBeInTheDocument();
    expect(screen.getByText("Шаг А")).toBeInTheDocument();
    expect(screen.getByText("Шаг Б")).toBeInTheDocument();
    expect(screen.getByText("Шаг В")).toBeInTheDocument();
  });

  it("оборачивает шаги в live-region (aria-live=polite)", () => {
    const { container } = render(<Stepper steps={steps} />);
    expect(container.querySelector('[aria-live="polite"]')).not.toBeNull();
  });

  it("идущий шаг использует motion-safe-анимацию (статична при reduced-motion)", () => {
    const { container } = render(<Stepper steps={steps} />);
    expect(container.querySelector(".motion-safe\\:animate-spin")).not.toBeNull();
  });

  it("показывает пометку точки невозврата", () => {
    render(<Stepper steps={steps} irreversible />);
    expect(screen.getByText(/точку невозврата|точка невозврата|необратим/i)).toBeInTheDocument();
  });

  it("показывает сопроводительное сообщение (например, факт отката)", () => {
    render(
      <Stepper
        steps={[{ key: "a", label: "Шаг А", state: "failed" }]}
        note="Создание завершилось ошибкой — выполнен откат (Saga)."
      />,
    );
    expect(screen.getByText(/выполнен откат \(Saga\)/)).toBeInTheDocument();
  });
});
