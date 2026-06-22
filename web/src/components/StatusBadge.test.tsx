// Тесты семантики статусов и критичности (ADR-0017): бейдж кодирует значение
// тремя признаками (цвет + иконка + текст) и безопасно деградирует на неизвестном
// значении, не ломая отрисовку.
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { StatusBadge } from "./StatusBadge";
import { CriticalityBadge } from "./CriticalityBadge";

describe("StatusBadge", () => {
  it("кодирует статус цветом, иконкой и текстом одновременно", () => {
    const { container } = render(<StatusBadge status="failed" />);
    // Текст статуса присутствует.
    expect(screen.getByText("Ошибка")).toBeInTheDocument();
    // Иконка (svg) присутствует — признак, отличный от цвета.
    expect(container.querySelector("svg")).not.toBeNull();
    // Цветовой класс деструктивного статуса присутствует.
    const badge = container.firstElementChild as HTMLElement;
    expect(badge.className).toMatch(/destructive/);
  });

  it("неизвестный статус деградирует в нейтральный бейдж с исходной строкой", () => {
    const { container } = render(<StatusBadge status="weird-state" />);
    expect(screen.getByText("weird-state")).toBeInTheDocument();
    // Отрисовка не падает, иконка-заглушка есть.
    expect(container.querySelector("svg")).not.toBeNull();
  });
});

describe("CriticalityBadge", () => {
  it("кодирует уровень критичности цветом, иконкой и текстом", () => {
    const { container } = render(<CriticalityBadge level="critical" />);
    expect(screen.getByText("Критическая")).toBeInTheDocument();
    expect(container.querySelector("svg")).not.toBeNull();
    const badge = container.firstElementChild as HTMLElement;
    expect(badge.className).toMatch(/criticality-critical/);
  });

  it("неизвестный уровень деградирует безопасно", () => {
    const { container } = render(<CriticalityBadge level="bogus" />);
    expect(screen.getByText("bogus")).toBeInTheDocument();
    expect(container.querySelector("svg")).not.toBeNull();
  });
});
