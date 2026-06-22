// Тесты страницы «Роли»: happy-path, 403 admin (fail-closed), создание/удаление роли,
// read-only системной роли, attach/detach права, deep-link выбранной роли через
// маршрут, базовая a11y (заголовок h1).
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import { RolesPage } from "./RolesPage";
import { ToastProvider } from "@/components/ui/toast";

const {
  listRoles,
  listPermissions,
  getRolePermissions,
  createRole,
  deleteRole,
  attachPermission,
  detachPermission,
} = vi.hoisted(() => ({
  listRoles: vi.fn(),
  listPermissions: vi.fn(),
  getRolePermissions: vi.fn(),
  createRole: vi.fn(),
  deleteRole: vi.fn(),
  attachPermission: vi.fn(),
  detachPermission: vi.fn(),
}));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return {
    ...actual,
    apiClient: {
      listRoles,
      listPermissions,
      getRolePermissions,
      createRole,
      deleteRole,
      attachPermission,
      detachPermission,
    },
  };
});

function httpError(status: number) {
  return { response: { status } };
}

function renderPage(initialPath = "/iam/roles") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <MemoryRouter initialEntries={[initialPath]}>
          <Routes>
            <Route path="/iam/roles" element={<RolesPage />} />
            <Route path="/iam/roles/:role" element={<RolesPage />} />
          </Routes>
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("RolesPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listPermissions.mockResolvedValue({ permissions: [] });
    getRolePermissions.mockResolvedValue({ permissions: [] });
  });

  it("happy-path: показывает роли и заголовок (h1)", async () => {
    listRoles.mockResolvedValue({
      roles: [
        { name: "iam-admin", system: true },
        { name: "reviewers", system: false },
      ],
    });
    renderPage();
    expect(await screen.findByRole("button", { name: "iam-admin" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "Роли" })).toBeInTheDocument();
  });

  it("403 на каталоге → раздел скрыт, показан отказ", async () => {
    listRoles.mockRejectedValue(httpError(403));
    renderPage();
    expect(await screen.findByText(/Доступ к разделу запрещён/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "iam-admin" })).not.toBeInTheDocument();
  });

  it("создание роли → вызов createRole с именем", async () => {
    listRoles.mockResolvedValue({ roles: [] });
    createRole.mockResolvedValue({ name: "reviewers", system: false });

    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: /Создать роль/i }));
    await user.type(await screen.findByLabelText(/Новая роль/i), "reviewers");
    await user.click(screen.getByRole("button", { name: /^Создать$/ }));

    await waitFor(() => expect(createRole).toHaveBeenCalledTimes(1));
    expect(createRole).toHaveBeenCalledWith({ name: "reviewers" });
  });

  it("удаление пользовательской роли → deleteRole; системная роль read-only", async () => {
    listRoles.mockResolvedValue({
      roles: [
        { name: "iam-admin", system: true },
        { name: "reviewers", system: false },
      ],
    });
    deleteRole.mockResolvedValue({ name: "reviewers", system: false });

    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: "reviewers" });
    expect(screen.queryByLabelText(/Удалить роль iam-admin/i)).not.toBeInTheDocument();
    await user.click(screen.getByLabelText(/Удалить роль reviewers/i));
    await user.click(await screen.findByRole("button", { name: /Подтвердить/i }));

    await waitFor(() => expect(deleteRole).toHaveBeenCalledTimes(1));
    expect(deleteRole).toHaveBeenCalledWith(undefined, { params: { role: "reviewers" } });
  });

  it("attach права к пользовательской роли → attachPermission", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "reviewers", system: false }] });
    listPermissions.mockResolvedValue({
      permissions: [{ action: "read", resource: "iam:global", system: true }],
    });
    attachPermission.mockResolvedValue({ role: "reviewers", permissions: [] });

    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "reviewers" }));
    const attachSelect = await screen.findByLabelText(/Прикрепить право/i);
    await user.selectOptions(
      attachSelect,
      within(attachSelect).getByRole("option", { name: /read.*iam:global/i }),
    );
    await user.click(screen.getByRole("button", { name: /Прикрепить/i }));

    await waitFor(() => expect(attachPermission).toHaveBeenCalledTimes(1));
    expect(attachPermission).toHaveBeenCalledWith(
      { action: "read", resource: "iam:global" },
      { params: { role: "reviewers" } },
    );
  });

  it("detach права у пользовательской роли → detachPermission через query", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "reviewers", system: false }] });
    getRolePermissions.mockResolvedValue({
      permissions: [{ action: "read", resource: "iam:global", system: true }],
    });
    detachPermission.mockResolvedValue({ role: "reviewers", permissions: [] });

    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "reviewers" }));
    await user.click(await screen.findByLabelText(/Открепить право read iam:global/i));
    await user.click(await screen.findByRole("button", { name: /Подтвердить/i }));

    await waitFor(() => expect(detachPermission).toHaveBeenCalledTimes(1));
    expect(detachPermission).toHaveBeenCalledWith(undefined, {
      params: { role: "reviewers" },
      queries: { action: "read", resource: "iam:global" },
    });
  });

  it("системная роль: состав прав не редактируется", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "iam-admin", system: true }] });
    getRolePermissions.mockResolvedValue({
      permissions: [{ action: "manage", resource: "iam:global", system: true }],
    });

    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "iam-admin" }));
    expect(await screen.findByText(/состав прав системной роли фиксирован/i)).toBeInTheDocument();
    expect(screen.queryByLabelText(/Прикрепить право/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/Открепить право/i)).not.toBeInTheDocument();
  });

  it("deep-link выбранной роли через маршрут показывает её права", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "reviewers", system: false }] });
    getRolePermissions.mockResolvedValue({
      permissions: [{ action: "deploy", resource: "project:demo", system: false }],
    });

    renderPage("/iam/roles/reviewers");

    expect(await screen.findByText(/Права роли «reviewers»/i)).toBeInTheDocument();
    expect(await screen.findByText(/deploy @ project:demo/i)).toBeInTheDocument();
    expect(getRolePermissions).toHaveBeenCalledWith({ params: { role: "reviewers" } });
  });
});
