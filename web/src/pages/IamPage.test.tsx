// Тесты раздела «Роли и доступы» (IAM-админка): happy-path (просмотр ролей и
// субъектов), отказ доступа 403 (содержимое скрыто), назначение/снятие роли,
// структурные мутации каталога (создание/удаление роли, attach/detach права),
// read-only системных ролей и клиентская валидация формы.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";

import { IamPage } from "./IamPage";

// Мокаем клиент периметра, сохраняя реальные zod-схемы.
const {
  listRoles,
  listPermissions,
  listSubjects,
  getRolePermissions,
  assignRole,
  revokeRole,
  createRole,
  deleteRole,
  createPermission,
  deletePermission,
  attachPermission,
  detachPermission,
  searchDirectorySubjects,
} = vi.hoisted(() => ({
  listRoles: vi.fn(),
  listPermissions: vi.fn(),
  listSubjects: vi.fn(),
  getRolePermissions: vi.fn(),
  assignRole: vi.fn(),
  revokeRole: vi.fn(),
  createRole: vi.fn(),
  deleteRole: vi.fn(),
  createPermission: vi.fn(),
  deletePermission: vi.fn(),
  attachPermission: vi.fn(),
  detachPermission: vi.fn(),
  searchDirectorySubjects: vi.fn(),
}));
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return {
    ...actual,
    apiClient: {
      listRoles,
      listPermissions,
      listSubjects,
      getRolePermissions,
      assignRole,
      revokeRole,
      createRole,
      deleteRole,
      createPermission,
      deletePermission,
      attachPermission,
      detachPermission,
      searchDirectorySubjects,
    },
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
    vi.clearAllMocks();
    // Разумные значения по умолчанию; конкретные тесты переопределяют при нужде.
    listPermissions.mockResolvedValue({ permissions: [] });
    listSubjects.mockResolvedValue({ subjects: [], next_page_token: "" });
  });

  it("happy-path: показывает роли и субъектов с их ролями", async () => {
    listRoles.mockResolvedValue({
      roles: [
        { name: "iam-admin", system: true },
        { name: "project-creator", system: true },
      ],
    });
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

    renderPage();

    expect(await screen.findByText(/Доступ к разделу запрещён/i)).toBeInTheDocument();
    expect(screen.queryByText("iam-admin")).not.toBeInTheDocument();
  });

  it("назначение роли → вызов периметра (выбор пользователя из каталога)", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "iam-admin", system: true }] });
    searchDirectorySubjects.mockResolvedValue({
      subjects: [
        {
          subject: "alice-sub",
          username: "alice",
          email: "alice@example.com",
          display_name: "Alice Ivanova",
          enabled: true,
          found: true,
        },
      ],
      next_cursor: "",
    });
    assignRole.mockResolvedValue({ subject: "alice-sub", roles: ["iam-admin"] });

    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: "iam-admin" });
    await user.type(screen.getByLabelText(/Поиск пользователя/i), "alice");
    await waitFor(() => expect(searchDirectorySubjects).toHaveBeenCalled());
    await user.click(await screen.findByText("Alice Ivanova"));
    await user.selectOptions(screen.getByLabelText(/^Роль/i), "iam-admin");
    await user.click(screen.getByRole("button", { name: /Назначить/i }));

    await waitFor(() => expect(assignRole).toHaveBeenCalledTimes(1));
    expect(assignRole).toHaveBeenCalledWith(undefined, {
      params: { subject: "alice-sub", role: "iam-admin" },
    });
  });

  it("валидация формы: пользователь не выбран → запрос не уходит", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "iam-admin", system: true }] });

    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: "iam-admin" });
    await user.click(screen.getByRole("button", { name: /Назначить/i }));

    expect(await screen.findByText(/Выберите пользователя/i)).toBeInTheDocument();
    expect(assignRole).not.toHaveBeenCalled();
  });

  it("создание роли → вызов createRole с именем", async () => {
    listRoles.mockResolvedValue({ roles: [] });
    createRole.mockResolvedValue({ name: "reviewers", system: false });

    const user = userEvent.setup();
    renderPage();

    const nameInput = await screen.findByLabelText(/Новая роль/i);
    const roleForm = nameInput.closest("form") as HTMLElement;
    await user.type(nameInput, "reviewers");
    await user.click(within(roleForm).getByRole("button", { name: /Создать/i }));

    await waitFor(() => expect(createRole).toHaveBeenCalledTimes(1));
    expect(createRole).toHaveBeenCalledWith({ name: "reviewers" });
  });

  it("удаление пользовательской роли → вызов deleteRole; системная роль read-only", async () => {
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
    // У системной роли нет кнопки удаления, у пользовательской — есть.
    expect(screen.queryByLabelText(/Удалить роль iam-admin/i)).not.toBeInTheDocument();
    await user.click(screen.getByLabelText(/Удалить роль reviewers/i));

    await waitFor(() => expect(deleteRole).toHaveBeenCalledTimes(1));
    expect(deleteRole).toHaveBeenCalledWith(undefined, { params: { role: "reviewers" } });
  });

  it("attach права к пользовательской роли → вызов attachPermission", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "reviewers", system: false }] });
    listPermissions.mockResolvedValue({
      permissions: [{ action: "read", resource: "iam:global", system: true }],
    });
    getRolePermissions.mockResolvedValue({ permissions: [] });
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

  it("detach права у пользовательской роли → вызов detachPermission через query", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "reviewers", system: false }] });
    getRolePermissions.mockResolvedValue({
      permissions: [{ action: "read", resource: "iam:global", system: true }],
    });
    detachPermission.mockResolvedValue({ role: "reviewers", permissions: [] });

    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "reviewers" }));
    await user.click(await screen.findByLabelText(/Открепить право read iam:global/i));

    await waitFor(() => expect(detachPermission).toHaveBeenCalledTimes(1));
    expect(detachPermission).toHaveBeenCalledWith(undefined, {
      params: { role: "reviewers" },
      queries: { action: "read", resource: "iam:global" },
    });
  });

  it("системная роль: состав прав не редактируется (нет привязки/откреплений)", async () => {
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

  it("пикер: поиск с debounce, выбор подставляет канонический subject", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "iam-admin", system: true }] });
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
    assignRole.mockResolvedValue({ subject: "11111111-1111-1111-1111-111111111111", roles: ["iam-admin"] });

    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: "iam-admin" });
    await user.type(screen.getByLabelText(/Поиск пользователя/i), "dev");

    // Debounce: после задержки уходит запрос и появляется результат.
    await waitFor(() => expect(searchDirectorySubjects).toHaveBeenCalled());
    await user.click(await screen.findByText("Dev User"));

    // После выбора поле поиска заменяется чипом выбранного пользователя.
    await waitFor(() =>
      expect(screen.queryByLabelText(/Поиск пользователя/i)).not.toBeInTheDocument(),
    );
    expect(screen.getByText("Dev User")).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText(/^Роль/i), "iam-admin");
    await user.click(screen.getByRole("button", { name: /Назначить/i }));
    await waitFor(() => expect(assignRole).toHaveBeenCalledTimes(1));
    expect(assignRole).toHaveBeenCalledWith(undefined, {
      params: { subject: "11111111-1111-1111-1111-111111111111", role: "iam-admin" },
    });
  });

  it("список субъектов: показывает имя/почту обогащённого субъекта", async () => {
    listRoles.mockResolvedValue({ roles: [] });
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
    listRoles.mockResolvedValue({ roles: [] });
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

  it("нет права на справочник (403): пикер скрыт, показан отказ", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "iam-admin", system: true }] });
    searchDirectorySubjects.mockRejectedValue(httpError(403));

    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: "iam-admin" });
    // Провоцируем запрос к справочнику, чтобы получить 403.
    expect(screen.getByLabelText(/Поиск пользователя/i)).toBeInTheDocument();
    await user.type(screen.getByLabelText(/Поиск пользователя/i), "dev");
    await waitFor(() => expect(searchDirectorySubjects).toHaveBeenCalled());

    // После 403 пикер скрывается, показывается отказ доступа к каталогу.
    await waitFor(() =>
      expect(screen.queryByLabelText(/Поиск пользователя/i)).not.toBeInTheDocument(),
    );
    expect(screen.getByText(/Нет доступа к каталогу пользователей/i)).toBeInTheDocument();
  });

  it("каталог недоступен (503): показана индикация, поиск остаётся", async () => {
    listRoles.mockResolvedValue({ roles: [{ name: "iam-admin", system: true }] });
    searchDirectorySubjects.mockRejectedValue(httpError(503));

    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: "iam-admin" });
    await user.type(screen.getByLabelText(/Поиск пользователя/i), "dev");
    await waitFor(() => expect(searchDirectorySubjects).toHaveBeenCalled());

    expect(await screen.findByText(/Каталог недоступен/i)).toBeInTheDocument();
    // Поле поиска остаётся (503 ретраебелен), пикер не превращается в отказ.
    expect(screen.getByLabelText(/Поиск пользователя/i)).toBeInTheDocument();
  });
});
