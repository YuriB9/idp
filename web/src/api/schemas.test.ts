// Тесты рантайм-валидации ответов периметра по сгенерированным из OpenAPI
// zod-схемам: валидный ответ проходит, а дрейф контракта падает явно.
import { describe, expect, it } from "vitest";

import { schemas } from "./index";

describe("zod-валидация ответов периметра", () => {
  it("валидный ServiceSummary проходит .parse", () => {
    const ok = {
      project: "demo",
      name: "svc",
      status: "creating",
      owners: ["alice", "bob"],
      owners_version: 4,
    };
    expect(schemas.ServiceSummary.parse(ok)).toEqual(ok);
  });

  it("валидный ServiceList проходит .parse", () => {
    const ok = {
      services: [
        { project: "demo", name: "svc", status: "active", owners: [], owners_version: 0 },
      ],
      next_page_token: "",
    };
    expect(schemas.ServiceList.parse(ok)).toEqual(ok);
  });

  it("неизвестный статус (дрейф контракта) падает явно", () => {
    const drift = { project: "demo", name: "svc", status: "weird", owners: [], owners_version: 0 };
    expect(() => schemas.ServiceSummary.parse(drift)).toThrow();
  });

  it("отсутствие обязательного поля падает явно", () => {
    const broken = { project: "demo", status: "active", owners: [], owners_version: 0 };
    expect(() => schemas.ServiceSummary.parse(broken)).toThrow();
  });

  it("CreateServiceRequest отклоняет пустое имя", () => {
    expect(() => schemas.CreateServiceRequest.parse({ name: "" })).toThrow();
  });

  it("валидный SetServiceOwnersRequest проходит .parse", () => {
    const ok = { owners: ["alice", "bob"], owners_version: 4 };
    expect(schemas.SetServiceOwnersRequest.parse(ok)).toEqual(ok);
  });

  it("SetServiceOwnersRequest отклоняет пустого владельца", () => {
    expect(() =>
      schemas.SetServiceOwnersRequest.parse({ owners: [""], owners_version: 1 }),
    ).toThrow();
  });
});
