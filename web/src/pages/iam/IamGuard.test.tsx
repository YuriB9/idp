// Тесты guard fail-closed раздела «Роли и доступы»: 403 → отказ без содержимого;
// иные ошибки/успех → дети рендерятся (страница сама обрабатывает свои состояния).
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { IamGuard, isIamForbidden } from "./IamGuard";

// fakeHttpError имитирует ошибку периметра с http-статусом (как zodios/axios).
function fakeHttpError(status: number): unknown {
  return { response: { status } };
}

describe("IamGuard", () => {
  it("403 → показывает отказ, содержимое скрыто", () => {
    render(
      <IamGuard error={fakeHttpError(403)}>
        <p>секретный каталог</p>
      </IamGuard>,
    );
    expect(screen.getByText("Доступ к разделу запрещён")).toBeInTheDocument();
    expect(screen.queryByText("секретный каталог")).toBeNull();
  });

  it("нет ошибки → рендерит детей", () => {
    render(
      <IamGuard error={null}>
        <p>каталог</p>
      </IamGuard>,
    );
    expect(screen.getByText("каталог")).toBeInTheDocument();
  });

  it("иная ошибка (503) → рендерит детей (страница обрабатывает сама)", () => {
    render(
      <IamGuard error={fakeHttpError(503)}>
        <p>каталог</p>
      </IamGuard>,
    );
    expect(screen.getByText("каталог")).toBeInTheDocument();
  });

  it("isIamForbidden распознаёт только 403", () => {
    expect(isIamForbidden(fakeHttpError(403))).toBe(true);
    expect(isIamForbidden(fakeHttpError(404))).toBe(false);
    expect(isIamForbidden(null)).toBe(false);
  });
});
