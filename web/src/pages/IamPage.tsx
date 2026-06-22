// Раздел «Роли и доступы» (IAM-админка, ADR-0014/0015/0016) в подаче дизайн-системы
// (ADR-0017). Просмотр ролей и их прав, управление назначением ролей субъектам
// (write) и СТРУКТУРНОЕ управление каталогом (manage) — создание/удаление ролей и
// прав, правка набора прав роли (attach/detach). Системные (сидированные) роли/права
// помечены и доступны только для чтения. Все данные идут через периметр с рантайм-
// валидацией ответов zod; отказ доступа (403) скрывает содержимое (fail-closed на
// UI), внутренние ошибки клиенту не раскрываются. Подача: PageHeader, таблицы
// (DataTable), действия — модалки/ConfirmDialog, результаты — тосты. Пикер
// пользователя из справочника Keycloak и обработка 403/503/«осиротевших» сохранены.
import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import {
  AlertTriangle,
  KeyRound,
  Loader2,
  Lock,
  Plus,
  Search,
  ShieldX,
  Trash2,
  UserCog,
} from "lucide-react";
import { z } from "zod";

import { apiClient } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { DataTable, type ColumnDef } from "@/components/ui/data-table";
import { Dialog } from "@/components/ui/dialog";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader } from "@/components/PageHeader";
import { useToast } from "@/components/ui/toast";
import { httpStatusOf } from "@/lib/errors";

// Единый маппинг кодов для действий назначения/снятия роли (write на iam:global).
const ASSIGN_OVERRIDES: Record<number, string> = {
  403: "Недостаточно прав для управления ролями (нужно право write на iam:global).",
  404: "Роль не найдена.",
  400: "Некорректные данные запроса.",
};

// Единый маппинг кодов для структурных мутаций каталога (manage на iam:global).
const CATALOG_OVERRIDES: Record<number, string> = {
  403: "Недостаточно прав для изменения каталога (нужно право manage на iam:global).",
  404: "Роль или право не найдены.",
  409: "Такая роль или право уже существуют.",
  422: "Системные роли и права защищены от изменения.",
  400: "Некорректные данные запроса.",
};

// assignFormSchema — валидация ввода формы назначения роли (subject + role).
const assignFormSchema = z.object({
  subject: z.string().min(1, "Выберите пользователя"),
  role: z.string().min(1, "Выберите роль"),
});
type AssignFormValues = z.infer<typeof assignFormSchema>;

// createRoleSchema — валидация ввода формы создания роли.
const createRoleSchema = z.object({ name: z.string().min(1, "Укажите имя роли") });
type CreateRoleValues = z.infer<typeof createRoleSchema>;

// createPermissionSchema — валидация ввода формы создания права.
const createPermissionSchema = z.object({
  action: z.string().min(1, "Укажите действие"),
  resource: z.string().min(1, "Укажите ресурс"),
});
type CreatePermissionValues = z.infer<typeof createPermissionSchema>;

// ConfirmState — единое состояние подтверждения деструктивного действия.
type ConfirmState =
  | { kind: "delete-role"; role: string }
  | { kind: "delete-permission"; action: string; resource: string }
  | { kind: "detach"; role: string; action: string; resource: string }
  | { kind: "revoke"; subject: string; role: string }
  | null;

export function IamPage() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [selectedRole, setSelectedRole] = useState<string | null>(null);
  const [attachPerm, setAttachPerm] = useState<string>("");
  // Поиск пользователя в каталоге (пикер назначения роли, ADR-0016). Debounce,
  // чтобы не дёргать справочник на каждый символ.
  const [subjectSearch, setSubjectSearch] = useState<string>("");
  const debouncedSearch = useDebounced(subjectSearch, 300);
  // Выбранный в пикере пользователь (для отображения; ключ — в поле формы subject).
  const [picked, setPicked] = useState<{ label: string; email: string } | null>(null);
  // Открытые модалки создания и состояние подтверждения деструктивного действия.
  const [createRoleOpen, setCreateRoleOpen] = useState(false);
  const [createPermOpen, setCreatePermOpen] = useState(false);
  const [confirm, setConfirm] = useState<ConfirmState>(null);

  // Каталог ролей. Ошибка 403 здесь означает отсутствие права на админку —
  // содержимое раздела не показываем (fail-closed на UI).
  const rolesQuery = useQuery({
    queryKey: ["iam", "roles"],
    queryFn: () => apiClient.listRoles(),
    retry: false,
  });

  // Каталог прав — нужен для выбора права при прикреплении к роли и для управления.
  const permissionsQuery = useQuery({
    queryKey: ["iam", "permissions"],
    queryFn: () => apiClient.listPermissions(),
    retry: false,
  });

  const rolePermsQuery = useQuery({
    queryKey: ["iam", "role-permissions", selectedRole],
    queryFn: () => apiClient.getRolePermissions({ params: { role: selectedRole ?? "" } }),
    enabled: selectedRole !== null,
    retry: false,
  });

  const subjectsQuery = useInfiniteQuery({
    queryKey: ["iam", "subjects"],
    initialPageParam: "",
    queryFn: ({ pageParam }) =>
      apiClient.listSubjects({ queries: { page_token: pageParam || undefined } }),
    getNextPageParam: (lastPage) => lastPage.next_page_token || undefined,
    retry: false,
  });

  // Справочник субъектов из каталога Keycloak (ADR-0016): поиск под право
  // (read, iam:directory). 403 — нет права на PII (пикер скрываем, назначение по
  // строке остаётся); 503 — каталог недоступен (показываем индикацию).
  const directoryQuery = useQuery({
    queryKey: ["iam", "directory", debouncedSearch],
    queryFn: () =>
      apiClient.searchDirectorySubjects({ queries: { search: debouncedSearch } }),
    enabled: debouncedSearch.trim().length >= 2,
    retry: false,
  });
  const directoryForbidden = httpStatusOf(directoryQuery.error) === 403;
  const directoryUnavailable = httpStatusOf(directoryQuery.error) === 503;

  const {
    register,
    handleSubmit,
    reset,
    setValue,
    formState: { errors },
  } = useForm<AssignFormValues>({
    resolver: zodResolver(assignFormSchema),
    defaultValues: { subject: "", role: "" },
  });

  const createRoleForm = useForm<CreateRoleValues>({
    resolver: zodResolver(createRoleSchema),
    defaultValues: { name: "" },
  });

  const createPermForm = useForm<CreatePermissionValues>({
    resolver: zodResolver(createPermissionSchema),
    defaultValues: { action: "", resource: "" },
  });

  // Инвалидация списков после мутации, чтобы UI отразил актуальное состояние.
  const invalidateSubjects = () =>
    queryClient.invalidateQueries({ queryKey: ["iam", "subjects"] });
  const invalidateRoles = () => queryClient.invalidateQueries({ queryKey: ["iam", "roles"] });
  const invalidatePermissions = () =>
    queryClient.invalidateQueries({ queryKey: ["iam", "permissions"] });
  const invalidateRolePerms = () =>
    queryClient.invalidateQueries({ queryKey: ["iam", "role-permissions"] });

  const assignMutation = useMutation({
    mutationFn: (values: AssignFormValues) =>
      apiClient.assignRole(undefined, { params: { subject: values.subject, role: values.role } }),
    onSuccess: () => {
      toast.success("Роль назначена.");
      reset();
      setPicked(null);
      void invalidateSubjects();
    },
    onError: (err) =>
      toast.error(err, { action: "назначить роль", overrides: ASSIGN_OVERRIDES }),
  });

  const revokeMutation = useMutation({
    mutationFn: (vars: { subject: string; role: string }) =>
      apiClient.revokeRole(undefined, { params: { subject: vars.subject, role: vars.role } }),
    onSuccess: () => {
      toast.success("Роль снята.");
      void invalidateSubjects();
    },
    onError: (err) =>
      toast.error(err, { action: "снять роль", overrides: ASSIGN_OVERRIDES }),
  });

  const createRoleMutation = useMutation({
    mutationFn: (values: CreateRoleValues) => apiClient.createRole({ name: values.name }),
    onSuccess: () => {
      toast.success("Роль создана.");
      createRoleForm.reset();
      setCreateRoleOpen(false);
      void invalidateRoles();
    },
    onError: (err) =>
      toast.error(err, { action: "создать роль", overrides: CATALOG_OVERRIDES }),
  });

  const deleteRoleMutation = useMutation({
    mutationFn: (role: string) => apiClient.deleteRole(undefined, { params: { role } }),
    onSuccess: (_data, role) => {
      toast.success("Роль удалена.");
      if (selectedRole === role) setSelectedRole(null);
      void invalidateRoles();
      void invalidateSubjects();
    },
    onError: (err) =>
      toast.error(err, { action: "удалить роль", overrides: CATALOG_OVERRIDES }),
  });

  const createPermMutation = useMutation({
    mutationFn: (values: CreatePermissionValues) =>
      apiClient.createPermission({ action: values.action, resource: values.resource }),
    onSuccess: () => {
      toast.success("Право создано.");
      createPermForm.reset();
      setCreatePermOpen(false);
      void invalidatePermissions();
    },
    onError: (err) =>
      toast.error(err, { action: "создать право", overrides: CATALOG_OVERRIDES }),
  });

  const deletePermMutation = useMutation({
    mutationFn: (vars: { action: string; resource: string }) =>
      apiClient.deletePermission(undefined, { queries: vars }),
    onSuccess: () => {
      toast.success("Право удалено.");
      void invalidatePermissions();
      void invalidateRolePerms();
    },
    onError: (err) =>
      toast.error(err, { action: "удалить право", overrides: CATALOG_OVERRIDES }),
  });

  const attachMutation = useMutation({
    mutationFn: (vars: { role: string; action: string; resource: string }) =>
      apiClient.attachPermission(
        { action: vars.action, resource: vars.resource },
        { params: { role: vars.role } },
      ),
    onSuccess: () => {
      toast.success("Право прикреплено.");
      setAttachPerm("");
      void invalidateRolePerms();
    },
    onError: (err) =>
      toast.error(err, { action: "прикрепить право", overrides: CATALOG_OVERRIDES }),
  });

  const detachMutation = useMutation({
    mutationFn: (vars: { role: string; action: string; resource: string }) =>
      apiClient.detachPermission(undefined, {
        params: { role: vars.role },
        queries: { action: vars.action, resource: vars.resource },
      }),
    onSuccess: () => {
      toast.success("Право откреплено.");
      void invalidateRolePerms();
    },
    onError: (err) =>
      toast.error(err, { action: "открепить право", overrides: CATALOG_OVERRIDES }),
  });

  // 403 на загрузке каталога — нет права на админку: показываем отказ, без содержимого.
  if (httpStatusOf(rolesQuery.error) === 403) {
    return (
      <Card className="flex flex-col items-center gap-2 p-10 text-center">
        <ShieldX className="size-8 text-muted-foreground" />
        <p className="text-sm font-medium">Доступ к разделу запрещён</p>
        <p className="text-sm text-muted-foreground">
          У вас нет права на управление ролями и доступами (IAM-админка).
        </p>
      </Card>
    );
  }

  const roles = rolesQuery.data?.roles ?? [];
  const permissions = permissionsQuery.data?.permissions ?? [];
  const subjects = subjectsQuery.data?.pages.flatMap((p) => p.subjects) ?? [];
  const selectedRoleObj = roles.find((r) => r.name === selectedRole) ?? null;
  const selectedRoleIsSystem = selectedRoleObj?.system ?? false;

  // Колонки каталога прав (DataTable).
  type PermRow = (typeof permissions)[number];
  const permColumns: ColumnDef<PermRow>[] = [
    {
      id: "perm",
      header: "Право",
      sortValue: (p) => `${p.action} ${p.resource}`,
      cell: (p) => (
        <span className="font-mono text-xs">
          {p.action} @ {p.resource}
        </span>
      ),
    },
    {
      id: "type",
      header: "Тип",
      cell: (p) =>
        p.system ? (
          <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
            <Lock className="size-3" /> системное
          </span>
        ) : (
          <span className="text-xs text-muted-foreground">пользовательское</span>
        ),
    },
    {
      id: "actions",
      header: "",
      align: "right",
      cell: (p) =>
        p.system ? null : (
          <button
            type="button"
            aria-label={`Удалить право ${p.action} ${p.resource}`}
            className="text-muted-foreground transition-colors hover:text-destructive"
            onClick={() =>
              setConfirm({ kind: "delete-permission", action: p.action, resource: p.resource })
            }
          >
            <Trash2 className="size-3.5" />
          </button>
        ),
    },
  ];

  // Колонки списка субъектов с ролями (DataTable).
  type SubjectRow = (typeof subjects)[number];
  const subjectColumns: ColumnDef<SubjectRow>[] = [
    {
      id: "user",
      header: "Пользователь",
      cell: (s) =>
        s.identity && s.identity.found ? (
          <span className="flex flex-col">
            <span className="font-medium">
              {s.identity.display_name || s.identity.username || s.subject}
            </span>
            {s.identity.email && (
              <span className="text-xs text-muted-foreground">{s.identity.email}</span>
            )}
            <span className="font-mono text-xs text-muted-foreground">{s.subject}</span>
          </span>
        ) : (
          <span className="flex items-center gap-2">
            <span className="font-mono text-sm">{s.subject}</span>
            {s.identity && !s.identity.found && (
              <span className="flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
                <AlertTriangle className="size-3" /> нет в каталоге
              </span>
            )}
          </span>
        ),
    },
    {
      id: "roles",
      header: "Роли",
      cell: (s) => (
        <div className="flex flex-wrap gap-2">
          {s.roles.map((role) => (
            <span
              key={role}
              className="flex items-center gap-1 rounded-md bg-muted px-2 py-1 text-xs"
            >
              {role}
              <button
                type="button"
                aria-label={`Снять роль ${role} с ${s.subject}`}
                className="text-muted-foreground transition-colors hover:text-destructive"
                onClick={() => setConfirm({ kind: "revoke", subject: s.subject, role })}
              >
                <Trash2 className="size-3.5" />
              </button>
            </span>
          ))}
        </div>
      ),
    },
  ];

  return (
    <section className="flex flex-col gap-5">
      <PageHeader
        title="Роли и доступы"
        description="Просмотр и управление каталогом ролей/прав и назначением ролей субъектам (IAM-админка)"
      />

      {/* Роли и их права */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <KeyRound className="size-4" /> Роли
          </CardTitle>
          <CardDescription>
            Выберите роль, чтобы увидеть и изменить её права. Системные роли
            защищены от удаления и правки.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {rolesQuery.isLoading && <p className="text-sm text-muted-foreground">Загрузка…</p>}
          {rolesQuery.isError && httpStatusOf(rolesQuery.error) !== 403 && (
            <p className="text-sm text-destructive">Не удалось загрузить роли.</p>
          )}
          <div className="flex flex-wrap items-center gap-2">
            {roles.map((r) => (
              <span
                key={r.name}
                className={
                  "flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-sm transition-colors " +
                  (selectedRole === r.name
                    ? "border-primary bg-accent text-accent-foreground"
                    : "border-border")
                }
              >
                <button type="button" onClick={() => setSelectedRole(r.name)}>
                  {r.name}
                </button>
                {r.system ? (
                  <span
                    className="flex items-center gap-0.5 text-xs text-muted-foreground"
                    title="Системная роль — защищена от удаления"
                  >
                    <Lock className="size-3" />
                  </span>
                ) : (
                  <button
                    type="button"
                    aria-label={`Удалить роль ${r.name}`}
                    className="text-muted-foreground transition-colors hover:text-destructive"
                    onClick={() => setConfirm({ kind: "delete-role", role: r.name })}
                  >
                    <Trash2 className="size-3.5" />
                  </button>
                )}
              </span>
            ))}
            <Button type="button" variant="outline" size="sm" onClick={() => setCreateRoleOpen(true)}>
              <Plus className="size-4" /> Создать роль
            </Button>
          </div>

          {selectedRole !== null && (
            <div className="rounded-md border border-border p-3">
              <p className="mb-2 flex items-center gap-2 text-sm font-medium">
                Права роли «{selectedRole}»
                {selectedRoleIsSystem && (
                  <span className="flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
                    <Lock className="size-3" /> системная
                  </span>
                )}
              </p>
              {rolePermsQuery.isLoading && (
                <p className="text-sm text-muted-foreground">Загрузка прав…</p>
              )}
              {rolePermsQuery.isError && (
                <p className="text-sm text-destructive">
                  {httpStatusOf(rolePermsQuery.error) === 404
                    ? "Роль не найдена."
                    : "Не удалось загрузить права роли."}
                </p>
              )}
              {rolePermsQuery.data && rolePermsQuery.data.permissions.length === 0 && (
                <p className="text-sm text-muted-foreground">У роли нет прав.</p>
              )}
              {rolePermsQuery.data && rolePermsQuery.data.permissions.length > 0 && (
                <ul className="flex flex-col gap-1 text-sm">
                  {rolePermsQuery.data.permissions.map((p) => (
                    <li
                      key={`${p.action}:${p.resource}`}
                      className="flex items-center gap-2 font-mono text-xs"
                    >
                      <span>
                        {p.action} @ {p.resource}
                      </span>
                      {!selectedRoleIsSystem && (
                        <button
                          type="button"
                          aria-label={`Открепить право ${p.action} ${p.resource}`}
                          className="text-muted-foreground transition-colors hover:text-destructive"
                          onClick={() =>
                            setConfirm({
                              kind: "detach",
                              role: selectedRole,
                              action: p.action,
                              resource: p.resource,
                            })
                          }
                        >
                          <Trash2 className="size-3.5" />
                        </button>
                      )}
                    </li>
                  ))}
                </ul>
              )}

              {/* Прикрепление права к пользовательской роли */}
              {!selectedRoleIsSystem && (
                <div className="mt-3 flex items-end gap-2">
                  <div className="flex flex-col gap-1.5">
                    <label htmlFor="attach-perm" className="text-sm font-medium">
                      Прикрепить право
                    </label>
                    <select
                      id="attach-perm"
                      className="h-9 rounded-md border border-input bg-transparent px-3 text-sm"
                      value={attachPerm}
                      onChange={(e) => setAttachPerm(e.target.value)}
                    >
                      <option value="">— выберите право —</option>
                      {permissions.map((p) => (
                        <option
                          key={`${p.action}:${p.resource}`}
                          value={`${p.action} ${p.resource}`}
                        >
                          {p.action} @ {p.resource}
                        </option>
                      ))}
                    </select>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    disabled={attachMutation.isPending || attachPerm === ""}
                    onClick={() => {
                      const [action, resource] = attachPerm.split(" ");
                      if (!action || !resource) return;
                      attachMutation.mutate({ role: selectedRole, action, resource });
                    }}
                  >
                    <Plus className="size-4" /> Прикрепить
                  </Button>
                </div>
              )}
              {selectedRoleIsSystem && (
                <p className="mt-2 text-xs text-muted-foreground">
                  Состав прав системной роли фиксирован и не редактируется.
                </p>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      {/* Каталог прав */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between gap-2">
            <span className="flex items-center gap-2">
              <KeyRound className="size-4" /> Права
            </span>
            <Button type="button" variant="outline" size="sm" onClick={() => setCreatePermOpen(true)}>
              <Plus className="size-4" /> Создать право
            </Button>
          </CardTitle>
          <CardDescription>
            Каталог прав (пара действие/ресурс). Системные права защищены от
            удаления.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <DataTable
            columns={permColumns}
            rows={permissions}
            rowKey={(p) => `${p.action}:${p.resource}`}
            caption="Каталог прав"
            density="compact"
            isLoading={permissionsQuery.isLoading}
            isError={permissionsQuery.isError}
            errorMessage="Не удалось загрузить права."
            emptyMessage="Каталог прав пуст."
          />
        </CardContent>
      </Card>

      {/* Форма назначения роли */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <UserCog className="size-4" /> Назначить роль пользователю
          </CardTitle>
          <CardDescription>
            Выдача существующей роли пользователю из каталога. Найдите пользователя
            и выберите его. Операция идемпотентна.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="flex flex-col gap-4"
            onSubmit={handleSubmit((values) => assignMutation.mutate(values))}
          >
            {/* Назначение роли возможно только выбором пользователя из каталога
                (ADR-0016): нужно право (read, iam:directory). Без него пикер скрыт. */}
            {directoryForbidden ? (
              <p className="flex items-center gap-2 rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground">
                <ShieldX className="size-4 shrink-0" /> Нет доступа к каталогу
                пользователей (нужно право read на iam:directory).
              </p>
            ) : (
              <div className="flex flex-col gap-1.5">
                <label htmlFor="subject-search" className="text-sm font-medium">
                  Поиск пользователя
                </label>
                {/* Скрытое поле формы хранит канонический subject выбранного. */}
                <input type="hidden" {...register("subject")} />
                {picked ? (
                  <div className="flex items-center justify-between gap-2 rounded-md border border-border px-3 py-2 text-sm">
                    <span className="flex flex-col">
                      <span className="font-medium">{picked.label}</span>
                      {picked.email && (
                        <span className="text-xs text-muted-foreground">{picked.email}</span>
                      )}
                    </span>
                    <button
                      type="button"
                      aria-label="Сбросить выбор пользователя"
                      className="text-muted-foreground transition-colors hover:text-destructive"
                      onClick={() => {
                        setPicked(null);
                        setValue("subject", "", { shouldValidate: false });
                      }}
                    >
                      <Trash2 className="size-3.5" />
                    </button>
                  </div>
                ) : (
                  <>
                    <div className="relative">
                      <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
                      <Input
                        id="subject-search"
                        className="pl-8"
                        placeholder="имя, логин или email"
                        value={subjectSearch}
                        onChange={(e) => setSubjectSearch(e.target.value)}
                      />
                    </div>
                    {directoryUnavailable && (
                      <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
                        <AlertTriangle className="size-3.5" /> Каталог недоступен —
                        попробуйте позже.
                      </p>
                    )}
                    {directoryQuery.isFetching && !directoryUnavailable && (
                      <p className="text-sm text-muted-foreground">Поиск…</p>
                    )}
                    {directoryQuery.data && directoryQuery.data.subjects.length > 0 && (
                      <ul className="flex flex-col gap-1 rounded-md border border-border p-1">
                        {directoryQuery.data.subjects.map((u) => (
                          <li key={u.subject}>
                            <button
                              type="button"
                              className="flex w-full flex-col items-start rounded px-2 py-1 text-left text-sm hover:bg-accent"
                              onClick={() => {
                                setValue("subject", u.subject, { shouldValidate: true });
                                setPicked({
                                  label: u.display_name || u.username || u.subject,
                                  email: u.email,
                                });
                                setSubjectSearch("");
                              }}
                            >
                              <span className="font-medium">
                                {u.display_name || u.username || u.subject}
                              </span>
                              {u.email && (
                                <span className="text-xs text-muted-foreground">{u.email}</span>
                              )}
                            </button>
                          </li>
                        ))}
                      </ul>
                    )}
                    {directoryQuery.data &&
                      directoryQuery.data.subjects.length === 0 &&
                      debouncedSearch.trim().length >= 2 && (
                        <p className="text-sm text-muted-foreground">Никого не найдено.</p>
                      )}
                  </>
                )}
                {errors.subject && (
                  <p className="text-sm text-destructive">{errors.subject.message}</p>
                )}
              </div>
            )}
            <div className="flex flex-col gap-1.5">
              <label htmlFor="role" className="text-sm font-medium">
                Роль
              </label>
              <select
                id="role"
                className="h-9 rounded-md border border-input bg-transparent px-3 text-sm"
                aria-invalid={Boolean(errors.role)}
                {...register("role")}
              >
                <option value="">— выберите роль —</option>
                {roles.map((r) => (
                  <option key={r.name} value={r.name}>
                    {r.name}
                  </option>
                ))}
              </select>
              {errors.role && <p className="text-sm text-destructive">{errors.role.message}</p>}
            </div>
            <div className="flex justify-end">
              <Button type="submit" disabled={assignMutation.isPending || directoryForbidden}>
                {assignMutation.isPending && <Loader2 className="size-4 animate-spin" />}
                {assignMutation.isPending ? "Назначаем…" : "Назначить"}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>

      {/* Пользователи с их ролями */}
      <Card>
        <CardHeader>
          <CardTitle>Пользователи</CardTitle>
          <CardDescription>
            Пользователи с назначенными ролями. Пользователи без ролей в системе не
            видны.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <DataTable
            columns={subjectColumns}
            rows={subjects}
            rowKey={(s) => s.subject}
            caption="Пользователи с ролями"
            isLoading={subjectsQuery.isLoading}
            isError={subjectsQuery.isError}
            errorMessage="Не удалось загрузить пользователей."
            emptyMessage="Нет пользователей с ролями."
            pagination={{
              hasNextPage: Boolean(subjectsQuery.hasNextPage),
              isFetchingNextPage: subjectsQuery.isFetchingNextPage,
              onLoadMore: () => void subjectsQuery.fetchNextPage(),
            }}
          />
        </CardContent>
      </Card>

      {/* Модалка создания роли */}
      <Dialog
        open={createRoleOpen}
        onClose={() => setCreateRoleOpen(false)}
        title="Создать роль"
        description="Имя новой пользовательской роли."
        footer={
          <>
            <Button type="button" variant="ghost" onClick={() => setCreateRoleOpen(false)}>
              Отмена
            </Button>
            <Button type="submit" form="create-role-form" disabled={createRoleMutation.isPending}>
              {createRoleMutation.isPending && <Loader2 className="size-4 animate-spin" />}
              Создать
            </Button>
          </>
        }
      >
        <form
          id="create-role-form"
          className="flex flex-col gap-1.5"
          onSubmit={createRoleForm.handleSubmit((values) => createRoleMutation.mutate(values))}
        >
          <label htmlFor="new-role" className="text-sm font-medium">
            Новая роль
          </label>
          <Input
            id="new-role"
            placeholder="например, reviewers"
            aria-invalid={Boolean(createRoleForm.formState.errors.name)}
            {...createRoleForm.register("name")}
          />
          {createRoleForm.formState.errors.name && (
            <p className="text-sm text-destructive">
              {createRoleForm.formState.errors.name.message}
            </p>
          )}
        </form>
      </Dialog>

      {/* Модалка создания права */}
      <Dialog
        open={createPermOpen}
        onClose={() => setCreatePermOpen(false)}
        title="Создать право"
        description="Пара действие/ресурс нового пользовательского права."
        footer={
          <>
            <Button type="button" variant="ghost" onClick={() => setCreatePermOpen(false)}>
              Отмена
            </Button>
            <Button type="submit" form="create-perm-form" disabled={createPermMutation.isPending}>
              {createPermMutation.isPending && <Loader2 className="size-4 animate-spin" />}
              Создать
            </Button>
          </>
        }
      >
        <form
          id="create-perm-form"
          className="flex flex-col gap-3"
          onSubmit={createPermForm.handleSubmit((values) => createPermMutation.mutate(values))}
        >
          <div className="flex flex-col gap-1.5">
            <label htmlFor="new-action" className="text-sm font-medium">
              Действие
            </label>
            <Input
              id="new-action"
              placeholder="например, deploy"
              aria-invalid={Boolean(createPermForm.formState.errors.action)}
              {...createPermForm.register("action")}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label htmlFor="new-resource" className="text-sm font-medium">
              Ресурс
            </label>
            <Input
              id="new-resource"
              placeholder="например, project:demo"
              aria-invalid={Boolean(createPermForm.formState.errors.resource)}
              {...createPermForm.register("resource")}
            />
          </div>
          {(createPermForm.formState.errors.action || createPermForm.formState.errors.resource) && (
            <p className="text-sm text-destructive">
              {createPermForm.formState.errors.action?.message ??
                createPermForm.formState.errors.resource?.message}
            </p>
          )}
        </form>
      </Dialog>

      {/* Единый confirm-диалог деструктивных действий */}
      <ConfirmDialog
        open={confirm !== null}
        onClose={() => setConfirm(null)}
        onConfirm={() => {
          if (!confirm) return;
          if (confirm.kind === "delete-role") deleteRoleMutation.mutate(confirm.role);
          else if (confirm.kind === "delete-permission")
            deletePermMutation.mutate({ action: confirm.action, resource: confirm.resource });
          else if (confirm.kind === "detach")
            detachMutation.mutate({
              role: confirm.role,
              action: confirm.action,
              resource: confirm.resource,
            });
          else if (confirm.kind === "revoke")
            revokeMutation.mutate({ subject: confirm.subject, role: confirm.role });
          setConfirm(null);
        }}
        title={confirmTitle(confirm)}
        description={confirmDescription(confirm)}
        confirmLabel="Подтвердить"
      />
    </section>
  );
}

// confirmTitle — заголовок confirm-диалога по виду действия.
function confirmTitle(c: ConfirmState): string {
  switch (c?.kind) {
    case "delete-role":
      return "Удалить роль?";
    case "delete-permission":
      return "Удалить право?";
    case "detach":
      return "Открепить право от роли?";
    case "revoke":
      return "Снять роль с пользователя?";
    default:
      return "";
  }
}

// confirmDescription — пояснение confirm-диалога по виду действия.
function confirmDescription(c: ConfirmState): string {
  switch (c?.kind) {
    case "delete-role":
      return `Роль «${c.role}» будет удалена у всех носителей. Действие необратимо.`;
    case "delete-permission":
      return `Право «${c.action} @ ${c.resource}» будет удалено из каталога и откреплено от ролей.`;
    case "detach":
      return `Право «${c.action} @ ${c.resource}» будет откреплено от роли «${c.role}».`;
    case "revoke":
      return `Роль «${c.role}» будет снята с пользователя.`;
    default:
      return "";
  }
}

// useDebounced возвращает значение с задержкой: пикер пользователя не дёргает
// справочник на каждый набранный символ (ADR-0016).
function useDebounced<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState<T>(value);
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(id);
  }, [value, delayMs]);
  return debounced;
}
