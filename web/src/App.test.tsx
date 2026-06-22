// Тесты маршрутизации раздела «Роли и доступы»: редирект старого /iam на /iam/roles и
// прямой переход на под-разделы (страницы рендерятся в глобальном каркасе).
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";

import { App } from "./App";
import { ToastProvider } from "@/components/ui/toast";
import { ThemeProvider } from "@/lib/theme";

// Мокаем клиент периметра: страницы вызывают apiClient при монтировании.
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return {
    ...actual,
    apiClient: {
      listRoles: vi.fn().mockResolvedValue({ roles: [] }),
      listPermissions: vi.fn().mockResolvedValue({ permissions: [] }),
      listSubjects: vi.fn().mockResolvedValue({ subjects: [], next_page_token: "" }),
      getRolePermissions: vi.fn().mockResolvedValue({ permissions: [] }),
      searchDirectorySubjects: vi.fn().mockResolvedValue({ subjects: [], next_cursor: "" }),
      listServices: vi.fn().mockResolvedValue({ services: [], next_page_token: "" }),
    },
  };
});

function renderApp(initialPath: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ThemeProvider>
        <ToastProvider>
          <MemoryRouter initialEntries={[initialPath]}>
            <App />
          </MemoryRouter>
        </ToastProvider>
      </ThemeProvider>
    </QueryClientProvider>,
  );
}

describe("Маршрутизация IAM", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("/iam редиректит на /iam/roles (страница «Роли»)", async () => {
    renderApp("/iam");
    expect(await screen.findByRole("heading", { level: 1, name: "Роли" })).toBeInTheDocument();
  });

  it("/iam/permissions открывает страницу «Права»", async () => {
    renderApp("/iam/permissions");
    expect(await screen.findByRole("heading", { level: 1, name: "Права" })).toBeInTheDocument();
  });

  it("/iam/users открывает страницу «Пользователи»", async () => {
    renderApp("/iam/users");
    expect(
      await screen.findByRole("heading", { level: 1, name: "Пользователи" }),
    ).toBeInTheDocument();
  });
});
