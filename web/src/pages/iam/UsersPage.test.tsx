// Тесты страницы «Пользователи»: happy-path списка, обогащённый/осиротевший субъект,
// назначение роли через пикер (debounce, канонический subject), снятие роли,
// валидация формы, 403 admin (fail-closed), 403/503 справочника, курсорная пагинация.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";

import { UsersPage } from "./UsersPage";
import { ToastProvider } from "@/components/ui/toast";

const { listRoles, listSubjects, assignRole, revokeRole, searchDirectorySubjects } = vi.hoisted(
  () => ({
    listRoles: vi.fn(),
    listSubjects: vi.fn(),
    assignRole: vi.fn(),
    revokeRole: vi.fn(),
    searchDirectorySubjects: vi.fn(),
  }),
);
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return {
    ...actual,
    apiClient: { listRoles, listSubjects, assignRole, revokeRole, searchDirectorySubjects },
  };
});

function httpError(status: number) {
  return { response: { status } };
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <MemoryRouter initialEntries={["/iam/users"]}>
          <UsersPage />
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("UsersPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listRoles.mockResolvedValue({ roles: [{ name: "iam-admin", system: true }] });
    listSubjects.mockResolvedValue({ subjects: [], next_page_token: "" });
  });

  it("happy-path: показывает субъектов с ролями и заголовок (h1)", async () => {
    listSubjects.mockResolvedValue({
      subjects: [{ subject: "demo-user", roles: ["iam-admin"] }],
      next_page_token: "",
    });
    renderPage();
    expect(await screen.findByText("demo-user")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "Пользователи" })).toBeInTheDocument();
  });

  it("403 на subjects → раздел скрыт, показан отказ", async () => {
    listSubjects.mockRejectedValue(httpError(403));
    renderPage();
    expect(await screen.findByText(/Доступ к разделу запрещён/i)).toBeInTheDocument();
  });

  it("обогащённый субъект: показывает имя/почту", async () => {
    listSubjects.mockResolvedValue({
      subjects: [
        {
          subject: "11111111-1111-1111-1111-111111111111",
          roles: ["iam-admin"],
          identity: {
            subject: "11111111-1111-1111-1111-111111111111",
            username: "dev",
            email: "dev@example.com",
            display_name: "Dev User",
            enabled: true,
            found: true,
          },
        },
      ],
      next_page_token: "",
    });
    renderPage();
    expect(await screen.findByText("Dev User")).toBeInTheDocument();
    expect(screen.getByText("dev@example.com")).toBeInTheDocument();
  });

  it("осиротевший субъект: помечен «нет в каталоге»", async () => {
    listSubjects.mockResolvedValue({
      subjects: [
        {
          subject: "orphan-sub",
          roles: ["viewer"],
          identity: {
            subject: "orphan-sub",
            username: "",
            email: "",
            display_name: "",
            enabled: false,
            found: false,
          },
        },
      ],
      next_page_token: "",
    });
    renderPage();
    expect(await screen.findByText("orphan-sub")).toBeInTheDocument();
    expect(screen.getByText(/нет в каталоге/i)).toBeInTheDocument();
  });

  it("назначение роли через пикор с debounce → канонический subject", async () => {
    searchDirectorySubjects.mockResolvedValue({
      subjects: [
        {
          subject: "11111111-1111-1111-1111-111111111111",
          username: "dev",
          email: "dev@example.com",
          display_name: "Dev User",
          enabled: true,
          found: true,
        },
      ],
      next_cursor: "",
    });
    assignRole.mockResolvedValue({
      subject: "11111111-1111-1111-1111-111111111111",
      roles: ["iam-admin"],
    });

    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: /Назначить/i });
    await user.type(screen.getByLabelText(/Поиск пользователя/i), "dev");
    await waitFor(() => expect(searchDirectorySubjects).toHaveBeenCalled());
    await user.click(await screen.findByText("Dev User"));
    await waitFor(() =>
      expect(screen.queryByLabelText(/Поиск пользователя/i)).not.toBeInTheDocument(),
    );
    await user.selectOptions(screen.getByLabelText(/^Роль/i), "iam-admin");
    await user.click(screen.getByRole("button", { name: /Назначить/i }));

    await waitFor(() => expect(assignRole).toHaveBeenCalledTimes(1));
    expect(assignRole).toHaveBeenCalledWith(undefined, {
      params: { subject: "11111111-1111-1111-1111-111111111111", role: "iam-admin" },
    });
  });

  it("валидация формы: пользователь не выбран → запрос не уходит", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: /Назначить/i }));
    expect(await screen.findByText(/Выберите пользователя/i)).toBeInTheDocument();
    expect(assignRole).not.toHaveBeenCalled();
  });

  it("снятие роли → revokeRole после подтверждения", async () => {
    listSubjects.mockResolvedValue({
      subjects: [{ subject: "demo-user", roles: ["iam-admin"] }],
      next_page_token: "",
    });
    revokeRole.mockResolvedValue({ subject: "demo-user", roles: [] });

    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByLabelText(/Снять роль iam-admin с demo-user/i));
    await user.click(await screen.findByRole("button", { name: /Подтвердить/i }));

    await waitFor(() => expect(revokeRole).toHaveBeenCalledTimes(1));
    expect(revokeRole).toHaveBeenCalledWith(undefined, {
      params: { subject: "demo-user", role: "iam-admin" },
    });
  });

  it("нет права на справочник (403): пикер скрыт, показан отказ", async () => {
    searchDirectorySubjects.mockRejectedValue(httpError(403));
    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: /Назначить/i });
    await user.type(screen.getByLabelText(/Поиск пользователя/i), "dev");
    await waitFor(() => expect(searchDirectorySubjects).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.queryByLabelText(/Поиск пользователя/i)).not.toBeInTheDocument(),
    );
    expect(screen.getByText(/Нет доступа к каталогу пользователей/i)).toBeInTheDocument();
  });

  it("каталог недоступен (503): индикация, поиск остаётся", async () => {
    searchDirectorySubjects.mockRejectedValue(httpError(503));
    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: /Назначить/i });
    await user.type(screen.getByLabelText(/Поиск пользователя/i), "dev");
    await waitFor(() => expect(searchDirectorySubjects).toHaveBeenCalled());

    expect(await screen.findByText(/Каталог недоступен/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Поиск пользователя/i)).toBeInTheDocument();
  });

  it("курсорная пагинация: «Показать ещё» догружает следующую страницу", async () => {
    listSubjects.mockImplementation(({ queries }: { queries: { page_token?: string } }) => {
      if (!queries.page_token) {
        return Promise.resolve({
          subjects: [{ subject: "user-1", roles: ["iam-admin"] }],
          next_page_token: "cursor-2",
        });
      }
      return Promise.resolve({
        subjects: [{ subject: "user-2", roles: ["iam-admin"] }],
        next_page_token: "",
      });
    });

    const user = userEvent.setup();
    renderPage();

    expect(await screen.findByText("user-1")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Показать ещё/i }));
    expect(await screen.findByText("user-2")).toBeInTheDocument();
    expect(listSubjects).toHaveBeenLastCalledWith({ queries: { page_token: "cursor-2" } });
  });
});
