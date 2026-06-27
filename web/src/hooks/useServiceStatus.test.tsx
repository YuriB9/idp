// Тесты хука поллинга статуса сервиса: предикат терминальности (остановка/
// продолжение опроса) и проброс ошибки (явное падение при сбое/дрейфе ответа).
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { isTerminalStatus, useServiceStatus } from "./useServiceStatus";

// Мокаем клиент периметра, сохраняя реальные zod-схемы.
const { getService } = vi.hoisted(() => ({ getService: vi.fn() }));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return { ...actual, apiClient: { getService } };
});

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

describe("isTerminalStatus", () => {
  it("терминальные статусы (по умолчанию для создания) останавливают опрос", () => {
    expect(isTerminalStatus("active")).toBe(true);
    expect(isTerminalStatus("failed")).toBe(true);
    expect(isTerminalStatus("decommissioned")).toBe(true);
  });

  it("нетерминальные статусы продолжают опрос", () => {
    expect(isTerminalStatus("creating")).toBe(false);
    expect(isTerminalStatus("transferring")).toBe(false);
    expect(isTerminalStatus(undefined)).toBe(false);
  });
});

describe("useServiceStatus", () => {
  beforeEach(() => {
    getService.mockReset();
  });

  it("отдаёт статус из ответа периметра", async () => {
    getService.mockResolvedValue({ status: "creating", owners: [], owners_version: 1 });
    const { result } = renderHook(() => useServiceStatus("demo", "svc"), { wrapper });
    await waitFor(() => expect(result.current.status).toBe("creating"));
  });

  it("явно падает при сбое/дрейфе ответа (isError)", async () => {
    getService.mockRejectedValue(new Error("несоответствие схемы"));
    const { result } = renderHook(() => useServiceStatus("demo", "svc"), { wrapper });
    await waitFor(() => expect(result.current.isError).toBe(true));
  });
});
