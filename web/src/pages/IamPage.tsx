// Раздел «Роли и доступы» (IAM-админка, ADR-0014/0015). Горизонтальный маршрут (не
// project-scoped): просмотр ролей и их прав, управление назначением ролей субъектам
// (write) и СТРУКТУРНОЕ управление каталогом (manage) — создание/удаление ролей и
// прав, правка набора прав роли (attach/detach). Системные (сидированные) роли/права
// помечены бейджем «системная» и доступны только для чтения (кнопки удаления/правки
// скрыты). Все данные идут через периметр с рантайм-валидацией ответов zod; отказ
// доступа (403) скрывает содержимое (fail-closed на UI), внутренние ошибки клиенту
// не раскрываются.
import { useState } from "react";
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
  ShieldX,
  Trash2,
  UserCog,
} from "lucide-react";
import { z } from "zod";

import { apiClient } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

// httpStatusOf аккуратно достаёт HTTP-статус из ошибки zodios/axios.
function httpStatusOf(err: unknown): number | undefined {
  if (typeof err === "object" && err !== null && "response" in err) {
    return (err as { response?: { status?: number } }).response?.status;
  }
  return undefined;
}

// assignFormSchema — валидация ввода формы назначения роли (subject + role).
const assignFormSchema = z.object({
  subject: z.string().min(1, "Укажите субъекта"),
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

export function IamPage() {
  const queryClient = useQueryClient();
  const [selectedRole, setSelectedRole] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [attachPerm, setAttachPerm] = useState<string>("");

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

  const {
    register,
    handleSubmit,
    reset,
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
      setActionError(null);
      reset();
      void invalidateSubjects();
    },
    onError: (err) => setActionError(mutationMessage(err, "назначить")),
  });

  const revokeMutation = useMutation({
    mutationFn: (vars: { subject: string; role: string }) =>
      apiClient.revokeRole(undefined, { params: { subject: vars.subject, role: vars.role } }),
    onSuccess: () => {
      setActionError(null);
      void invalidateSubjects();
    },
    onError: (err) => setActionError(mutationMessage(err, "снять")),
  });

  const createRoleMutation = useMutation({
    mutationFn: (values: CreateRoleValues) => apiClient.createRole({ name: values.name }),
    onSuccess: () => {
      setActionError(null);
      createRoleForm.reset();
      void invalidateRoles();
    },
    onError: (err) => setActionError(catalogMessage(err, "создать роль")),
  });

  const deleteRoleMutation = useMutation({
    mutationFn: (role: string) => apiClient.deleteRole(undefined, { params: { role } }),
    onSuccess: (_data, role) => {
      setActionError(null);
      if (selectedRole === role) setSelectedRole(null);
      void invalidateRoles();
      void invalidateSubjects();
    },
    onError: (err) => setActionError(catalogMessage(err, "удалить роль")),
  });

  const createPermMutation = useMutation({
    mutationFn: (values: CreatePermissionValues) =>
      apiClient.createPermission({ action: values.action, resource: values.resource }),
    onSuccess: () => {
      setActionError(null);
      createPermForm.reset();
      void invalidatePermissions();
    },
    onError: (err) => setActionError(catalogMessage(err, "создать право")),
  });

  const deletePermMutation = useMutation({
    mutationFn: (vars: { action: string; resource: string }) =>
      apiClient.deletePermission(undefined, { queries: vars }),
    onSuccess: () => {
      setActionError(null);
      void invalidatePermissions();
      void invalidateRolePerms();
    },
    onError: (err) => setActionError(catalogMessage(err, "удалить право")),
  });

  const attachMutation = useMutation({
    mutationFn: (vars: { role: string; action: string; resource: string }) =>
      apiClient.attachPermission(
        { action: vars.action, resource: vars.resource },
        { params: { role: vars.role } },
      ),
    onSuccess: () => {
      setActionError(null);
      setAttachPerm("");
      void invalidateRolePerms();
    },
    onError: (err) => setActionError(catalogMessage(err, "прикрепить право")),
  });

  const detachMutation = useMutation({
    mutationFn: (vars: { role: string; action: string; resource: string }) =>
      apiClient.detachPermission(undefined, {
        params: { role: vars.role },
        queries: { action: vars.action, resource: vars.resource },
      }),
    onSuccess: () => {
      setActionError(null);
      void invalidateRolePerms();
    },
    onError: (err) => setActionError(catalogMessage(err, "открепить право")),
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

  return (
    <section className="flex flex-col gap-5">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Роли и доступы</h1>
        <p className="text-sm text-muted-foreground">
          Просмотр и управление каталогом ролей/прав и назначением ролей субъектам
          (IAM-админка)
        </p>
      </div>

      {actionError && (
        <p className="flex items-center gap-2 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <AlertTriangle className="size-4 shrink-0" />
          {actionError}
        </p>
      )}

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
          <div className="flex flex-wrap gap-2">
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
                    disabled={deleteRoleMutation.isPending}
                    onClick={() => {
                      setActionError(null);
                      deleteRoleMutation.mutate(r.name);
                    }}
                  >
                    <Trash2 className="size-3.5" />
                  </button>
                )}
              </span>
            ))}
          </div>

          {/* Форма создания роли */}
          <form
            className="flex items-end gap-2"
            onSubmit={createRoleForm.handleSubmit((values) => {
              setActionError(null);
              createRoleMutation.mutate(values);
            })}
          >
            <div className="flex flex-col gap-1.5">
              <label htmlFor="new-role" className="text-sm font-medium">
                Новая роль
              </label>
              <Input
                id="new-role"
                placeholder="например, reviewers"
                aria-invalid={Boolean(createRoleForm.formState.errors.name)}
                {...createRoleForm.register("name")}
              />
            </div>
            <Button type="submit" variant="outline" disabled={createRoleMutation.isPending}>
              <Plus className="size-4" /> Создать
            </Button>
          </form>
          {createRoleForm.formState.errors.name && (
            <p className="text-sm text-destructive">
              {createRoleForm.formState.errors.name.message}
            </p>
          )}

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
                          disabled={detachMutation.isPending}
                          onClick={() => {
                            setActionError(null);
                            detachMutation.mutate({
                              role: selectedRole,
                              action: p.action,
                              resource: p.resource,
                            });
                          }}
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
                          value={`${p.action} ${p.resource}`}
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
                      const [action, resource] = attachPerm.split(" ");
                      if (!action || !resource) return;
                      setActionError(null);
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
          <CardTitle className="flex items-center gap-2">
            <KeyRound className="size-4" /> Права
          </CardTitle>
          <CardDescription>
            Каталог прав (пара действие/ресурс). Системные права защищены от
            удаления.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {permissionsQuery.isLoading && (
            <p className="text-sm text-muted-foreground">Загрузка…</p>
          )}
          {permissionsQuery.isError && (
            <p className="text-sm text-destructive">Не удалось загрузить права.</p>
          )}
          <ul className="flex flex-col gap-1 text-sm">
            {permissions.map((p) => (
              <li
                key={`${p.action}:${p.resource}`}
                className="flex items-center gap-2 font-mono text-xs"
              >
                <span>
                  {p.action} @ {p.resource}
                </span>
                {p.system ? (
                  <span
                    className="flex items-center gap-0.5 text-muted-foreground"
                    title="Системное право — защищено от удаления"
                  >
                    <Lock className="size-3" />
                  </span>
                ) : (
                  <button
                    type="button"
                    aria-label={`Удалить право ${p.action} ${p.resource}`}
                    className="text-muted-foreground transition-colors hover:text-destructive"
                    disabled={deletePermMutation.isPending}
                    onClick={() => {
                      setActionError(null);
                      deletePermMutation.mutate({ action: p.action, resource: p.resource });
                    }}
                  >
                    <Trash2 className="size-3.5" />
                  </button>
                )}
              </li>
            ))}
          </ul>

          {/* Форма создания права */}
          <form
            className="flex items-end gap-2"
            onSubmit={createPermForm.handleSubmit((values) => {
              setActionError(null);
              createPermMutation.mutate(values);
            })}
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
            <Button type="submit" variant="outline" disabled={createPermMutation.isPending}>
              <Plus className="size-4" /> Создать
            </Button>
          </form>
          {(createPermForm.formState.errors.action || createPermForm.formState.errors.resource) && (
            <p className="text-sm text-destructive">
              {createPermForm.formState.errors.action?.message ??
                createPermForm.formState.errors.resource?.message}
            </p>
          )}
        </CardContent>
      </Card>

      {/* Форма назначения роли */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <UserCog className="size-4" /> Назначить роль субъекту
          </CardTitle>
          <CardDescription>
            Выдача существующей роли субъекту (sub из JWT). Операция идемпотентна.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="flex flex-col gap-4"
            onSubmit={handleSubmit((values) => {
              setActionError(null);
              assignMutation.mutate(values);
            })}
          >
            <div className="flex flex-col gap-1.5">
              <label htmlFor="subject" className="text-sm font-medium">
                Субъект
              </label>
              <Input
                id="subject"
                placeholder="например, demo-user"
                aria-invalid={Boolean(errors.subject)}
                {...register("subject")}
              />
              {errors.subject && (
                <p className="text-sm text-destructive">{errors.subject.message}</p>
              )}
            </div>
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
              <Button type="submit" disabled={assignMutation.isPending}>
                {assignMutation.isPending && <Loader2 className="size-4 animate-spin" />}
                {assignMutation.isPending ? "Назначаем…" : "Назначить"}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>

      {/* Субъекты с их ролями */}
      <Card>
        <CardHeader>
          <CardTitle>Субъекты</CardTitle>
          <CardDescription>
            Субъекты с назначенными ролями. Субъекты без ролей в системе не видны.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {subjectsQuery.isLoading && <p className="text-sm text-muted-foreground">Загрузка…</p>}
          {subjectsQuery.isError && (
            <p className="text-sm text-destructive">Не удалось загрузить субъектов.</p>
          )}
          {subjects.length === 0 && subjectsQuery.data && (
            <p className="text-sm text-muted-foreground">Нет субъектов с ролями.</p>
          )}
          <ul className="flex flex-col gap-2">
            {subjects.map((s) => (
              <li
                key={s.subject}
                className="flex flex-col gap-2 rounded-md border border-border p-3"
              >
                <span className="font-medium">{s.subject}</span>
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
                        disabled={revokeMutation.isPending}
                        onClick={() => {
                          setActionError(null);
                          revokeMutation.mutate({ subject: s.subject, role });
                        }}
                      >
                        <Trash2 className="size-3.5" />
                      </button>
                    </span>
                  ))}
                </div>
              </li>
            ))}
          </ul>
          {subjectsQuery.hasNextPage && (
            <div className="flex justify-center">
              <Button
                variant="outline"
                disabled={subjectsQuery.isFetchingNextPage}
                onClick={() => void subjectsQuery.fetchNextPage()}
              >
                {subjectsQuery.isFetchingNextPage ? "Загрузка…" : "Показать ещё"}
              </Button>
            </div>
          )}
        </CardContent>
      </Card>
    </section>
  );
}

// mutationMessage переводит ошибку мутации назначения/снятия роли в стабильное
// пользовательское сообщение (без раскрытия внутренних деталей сервера).
function mutationMessage(err: unknown, verb: string): string {
  switch (httpStatusOf(err)) {
    case 403:
      return "Недостаточно прав для управления ролями (нужно право write на iam:global).";
    case 404:
      return "Роль не найдена.";
    case 400:
      return "Некорректные данные запроса.";
    default:
      return `Не удалось ${verb} роль. Повторите попытку.`;
  }
}

// catalogMessage переводит ошибку структурной мутации каталога (manage) в стабильное
// пользовательское сообщение, разводя 403/404/409/422.
function catalogMessage(err: unknown, verb: string): string {
  switch (httpStatusOf(err)) {
    case 403:
      return "Недостаточно прав для изменения каталога (нужно право manage на iam:global).";
    case 404:
      return "Роль или право не найдены.";
    case 409:
      return "Такая роль или право уже существуют.";
    case 422:
      return "Системные роли и права защищены от изменения.";
    case 400:
      return "Некорректные данные запроса.";
    default:
      return `Не удалось ${verb}. Повторите попытку.`;
  }
}
