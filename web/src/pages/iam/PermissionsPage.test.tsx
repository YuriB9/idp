// Тесты страницы «Права»: happy-path каталога, 403 admin (fail-closed), создание
// права, удаление пользовательского права, read-only системного права, базовая a11y.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";

import { PermissionsPage } from "./PermissionsPage";
import { ToastProvider } from "@/components/ui/toast";

const { listPermissions, createPermission, deletePermission } = vi.hoisted(() => ({
  listPermissions: vi.fn(),
  createPermission: vi.fn(),
  deletePermission: vi.fn(),
}));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return {
    ...actual,
    apiClient: { listPermissions, createPermission, deletePermission },
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
        <MemoryRouter initialEntries={["/iam/permissions"]}>
          <PermissionsPage />
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("PermissionsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
  });

  it("happy-path: показывает каталог прав и заголовок (h1)", async () => {
    listPermissions.mockResolvedValue({
      permissions: [
        { action: "read", resource: "iam:global", system: true },
        { action: "deploy", resource: "project:demo", system: false },
      ],
    });
    renderPage();
    expect(await screen.findByText(/read @ iam:global/i)).toBeInTheDocument();
    expect(screen.getByText(/deploy @ project:demo/i)).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "Права" })).toBeInTheDocument();
  });

  it("403 на каталоге → раздел скрыт, показан отказ", async () => {
    listPermissions.mockRejectedValue(httpError(403));
    renderPage();
    expect(await screen.findByText(/Доступ к разделу запрещён/i)).toBeInTheDocument();
  });

  it("создание права → вызов createPermission", async () => {
    listPermissions.mockResolvedValue({ permissions: [] });
    createPermission.mockResolvedValue({ action: "deploy", resource: "project:demo", system: false });

    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: /Создать право/i }));
    await user.type(await screen.findByLabelText(/Действие/i), "deploy");
    await user.type(screen.getByLabelText(/Ресурс/i), "project:demo");
    await user.click(screen.getByRole("button", { name: /^Создать$/ }));

    await waitFor(() => expect(createPermission).toHaveBeenCalledTimes(1));
    expect(createPermission).toHaveBeenCalledWith({ action: "deploy", resource: "project:demo" });
  });

  it("удаление пользовательского права → deletePermission; системное read-only", async () => {
    listPermissions.mockResolvedValue({
      permissions: [
        { action: "read", resource: "iam:global", system: true },
        { action: "deploy", resource: "project:demo", system: false },
      ],
    });
    deletePermission.mockResolvedValue(undefined);

    const user = userEvent.setup();
    renderPage();

    await screen.findByText(/deploy @ project:demo/i);
    // У системного права нет кнопки удаления.
    expect(screen.queryByLabelText(/Удалить право read iam:global/i)).not.toBeInTheDocument();
    await user.click(screen.getByLabelText(/Удалить право deploy project:demo/i));
    await user.click(await screen.findByRole("button", { name: /Подтвердить/i }));

    await waitFor(() => expect(deletePermission).toHaveBeenCalledTimes(1));
    expect(deletePermission).toHaveBeenCalledWith(undefined, {
      queries: { action: "deploy", resource: "project:demo" },
    });
  });
});
