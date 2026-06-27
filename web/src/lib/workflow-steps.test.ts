// Тесты клиентской модели шагов воркфлоу (вариант B, ADR-0022): чистые функции
// фаз и разрешения прогресса по грубому статусу и доменным полям.
import { describe, expect, it } from "vitest";

import {
  buildSteps,
  createPhase,
  decommissionPhase,
  isProgressActive,
  noteFor,
  OPERATION_STEPS,
  ownersPhase,
  resolveProgress,
  transferPhase,
  type ActiveOp,
} from "./workflow-steps";

describe("buildSteps", () => {
  it("применяет фазу единообразно ко всем шагам операции", () => {
    const steps = buildSteps("create", "running");
    expect(steps).toHaveLength(OPERATION_STEPS.create.length);
    expect(steps.every((s) => s.state === "running")).toBe(true);
  });

  it("смена владельцев — вырожденный одношаговый степпер", () => {
    expect(buildSteps("change-owners", "done")).toEqual([
      { key: "owners", label: "Применение состава владельцев", state: "done" },
    ]);
  });
});

describe("createPhase", () => {
  it.each([
    ["creating", "running"],
    ["active", "done"],
    ["failed", "failed"],
  ])("статус %s → фаза %s", (status, phase) => {
    expect(createPhase(status)).toBe(phase);
  });
});

describe("ownersPhase", () => {
  it("running, пока версия не выросла; done после роста", () => {
    expect(ownersPhase(4, 4)).toBe("running");
    expect(ownersPhase(4, 5)).toBe("done");
  });
});

describe("decommissionPhase", () => {
  it.each([
    ["active", "running"],
    ["decommissioned", "done"],
    ["failed", "failed"],
  ])("статус %s → фаза %s", (status, phase) => {
    expect(decommissionPhase(status)).toBe(phase);
  });
});

describe("transferPhase", () => {
  it("transferring → running", () => {
    expect(transferPhase("transferring", false)).toBe("running");
  });
  it("active до наблюдения transferring → running (мост)", () => {
    expect(transferPhase("active", false)).toBe("running");
  });
  it("active после наблюдавшегося transferring → done", () => {
    expect(transferPhase("active", true)).toBe("done");
  });
  it("failed → failed", () => {
    expect(transferPhase("failed", true)).toBe("failed");
  });
});

describe("noteFor", () => {
  it("failed несёт сообщение об откате (Saga) без атрибуции шага", () => {
    expect(noteFor("create", "failed")).toMatch(/откат \(Saga\)/);
  });
  it("running без сообщения", () => {
    expect(noteFor("transfer", "running")).toBeUndefined();
  });
});

describe("resolveProgress", () => {
  it("нет данных → null", () => {
    expect(resolveProgress(undefined, null, false)).toBeNull();
  });

  it("без активной операции выводит фазу из статуса", () => {
    expect(resolveProgress({ status: "creating" }, null, false)).toMatchObject({
      operation: "create",
      phase: "running",
    });
    expect(resolveProgress({ status: "transferring" }, null, false)).toMatchObject({
      operation: "transfer",
      phase: "running",
      irreversible: true,
    });
    expect(resolveProgress({ status: "decommissioned" }, null, false)).toMatchObject({
      operation: "decommission",
      phase: "done",
      irreversible: true,
    });
  });

  it("активная смена владельцев: running → done по росту версии", () => {
    const op: ActiveOp = { operation: "change-owners", ownersBaseline: 4 };
    expect(resolveProgress({ status: "active", owners_version: 4 }, op, false)).toMatchObject({
      operation: "change-owners",
      phase: "running",
      irreversible: false,
    });
    expect(resolveProgress({ status: "active", owners_version: 5 }, op, false)).toMatchObject({
      phase: "done",
    });
  });

  it("активный decommission помечает точку невозврата и failed-откат", () => {
    const op: ActiveOp = { operation: "decommission" };
    const failed = resolveProgress({ status: "failed" }, op, false);
    expect(failed).toMatchObject({ operation: "decommission", phase: "failed", irreversible: true });
    expect(failed?.note).toMatch(/откат \(Saga\)/);
  });
});

describe("isProgressActive", () => {
  it("true для running/pending, false для терминальных", () => {
    expect(isProgressActive({ operation: "create", phase: "running", irreversible: false })).toBe(true);
    expect(isProgressActive({ operation: "create", phase: "done", irreversible: false })).toBe(false);
    expect(isProgressActive(null)).toBe(false);
  });
});
