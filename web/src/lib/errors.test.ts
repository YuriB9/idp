// Тесты единого маппинга кодов периметра (ADR-0017): каждый код → понятное
// сообщение; 503 распознаётся как retryable; сырые детали не раскрываются.
import { describe, expect, it } from "vitest";

import { httpStatusOf, perimeterErrorMessage, isRetryable } from "./errors";

// makeErr имитирует ошибку zodios/axios с заданным HTTP-статусом.
function makeErr(status: number) {
  return { response: { status } };
}

describe("perimeterErrorMessage", () => {
  it("маппит каждый стабилизированный код в понятное сообщение", () => {
    for (const code of [400, 403, 404, 409, 422, 503]) {
      const msg = perimeterErrorMessage(makeErr(code));
      expect(msg).toBeTruthy();
      // Сырой статус/«error» из тела наружу не просачивается.
      expect(msg).not.toMatch(/undefined|\[object/);
    }
  });

  it("403 и 422 дают разные сообщения", () => {
    expect(perimeterErrorMessage(makeErr(403))).not.toEqual(
      perimeterErrorMessage(makeErr(422)),
    );
  });

  it("overrides имеют приоритет над дефолтом", () => {
    const msg = perimeterErrorMessage(makeErr(409), {
      overrides: { 409: "Имя занято." },
    });
    expect(msg).toBe("Имя занято.");
  });

  it("неизвестный код использует фолбэк с действием", () => {
    const msg = perimeterErrorMessage(new Error("boom"), { action: "назначить роль" });
    expect(msg).toMatch(/назначить роль/);
  });
});

describe("isRetryable / httpStatusOf", () => {
  it("503 — retryable, прочие — нет", () => {
    expect(isRetryable(makeErr(503))).toBe(true);
    expect(isRetryable(makeErr(403))).toBe(false);
  });

  it("httpStatusOf достаёт статус, иначе undefined", () => {
    expect(httpStatusOf(makeErr(404))).toBe(404);
    expect(httpStatusOf(new Error("x"))).toBeUndefined();
  });
});
