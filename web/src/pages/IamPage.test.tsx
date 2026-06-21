// Тесты раздела «Роли и доступы» (IAM-админка): happy-path (просмотр ролей и
// субъектов), отказ доступа 403 (содержимое скрыто), назначение/снятие роли
// (мутация + индикация) и клиентская валидация формы.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";

import { IamPage } from "./IamPage";

// Мокаем клиент периметра, сохраняя реальные zod-схемы.
const { listRoles, listSubjects, getRolePermissions, assignRole, revokeRole } = vi.hoisted(() => ({
  listRoles: vi.fn(),
  listSubjects: vi.fn(),
  getRolePermissions: vi.fn(),
  assignRole: vi.fn(),
  revokeRole: vi.fn(),
}));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return {
    ...actual,
    apiClient: { listRoles, listSubjects, getRolePermissions, assignRole, revokeRole },
  };
});

// httpError имитирует ошибку zodios/axios с HTTP-статусом.
function httpError(status: number) {
  return { response: { status } };
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/iam"]}>
        <IamPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("IamPage", () => {
  beforeEach(() => {
    listRoles.mockReset();
    listSubjects.mockReset();
    getRolePermissions.mockReset();
    assignRole.mockReset();
    revokeRole.mockReset();
  });

  it("happy-path: показывает роли и субъектов с их ролями", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "iam-admin" }, { name: "project-creator" }] });
    listSubjects.mockResolvedValue({
      subjects: [{ subject: "demo-user", roles: ["iam-admin"] }],
      next_page_token: "",
    });

    renderPage();

    expect(await screen.findByRole("button", { name: "iam-admin" })).toBeInTheDocument();
    expect(await screen.findByText("demo-user")).toBeInTheDocument();
    expect(listSubjects).toHaveBeenCalled();
  });

  it("403 на каталоге → раздел скрыт, показан отказ", async () => {
    listRoles.mockRejectedValue(httpError(403));
    listSubjects.mockRejectedValue(httpError(403));

    renderPage();

    expect(await screen.findByText(/Доступ к разделу запрещён/i)).toBeInTheDocument();
    // Содержимое каталога не отображается.
    expect(screen.queryByText("iam-admin")).not.toBeInTheDocument();
  });

  it("назначение роли → вызов периметра и инвалидация", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "iam-admin" }] });
    listSubjects.mockResolvedValue({ subjects: [], next_page_token: "" });
    assignRole.mockResolvedValue({ subject: "alice", roles: ["iam-admin"] });

    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: "iam-admin" });
    await user.type(screen.getByLabelText(/Субъект/i), "alice");
    await user.selectOptions(screen.getByLabelText(/Роль/i), "iam-admin");
    await user.click(screen.getByRole("button", { name: /Назначить/i }));

    await waitFor(() => expect(assignRole).toHaveBeenCalledTimes(1));
    expect(assignRole).toHaveBeenCalledWith(undefined, {
      params: { subject: "alice", role: "iam-admin" },
    });
  });

  it("снятие роли → вызов revokeRole с subject и role", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "iam-admin" }] });
    listSubjects.mockResolvedValue({
      subjects: [{ subject: "demo-user", roles: ["iam-admin"] }],
      next_page_token: "",
    });
    revokeRole.mockResolvedValue({ subject: "demo-user", roles: [] });

    const user = userEvent.setup();
    renderPage();

    const subjectRow = (await screen.findByText("demo-user")).closest("li") as HTMLElement;
    await user.click(within(subjectRow).getByLabelText(/Снять роль iam-admin/i));

    await waitFor(() => expect(revokeRole).toHaveBeenCalledTimes(1));
    expect(revokeRole).toHaveBeenCalledWith(undefined, {
      params: { subject: "demo-user", role: "iam-admin" },
    });
  });

  it("валидация формы: пустой субъект → запрос не уходит", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "iam-admin" }] });
    listSubjects.mockResolvedValue({ subjects: [], next_page_token: "" });

    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: "iam-admin" });
    await user.click(screen.getByRole("button", { name: /Назначить/i }));

    expect(await screen.findByText(/Укажите субъекта/i)).toBeInTheDocument();
    expect(assignRole).not.toHaveBeenCalled();
  });
});
