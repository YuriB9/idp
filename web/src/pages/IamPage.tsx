// Раздел «Роли и доступы» (IAM-админка, ADR-0014). Горизонтальный маршрут (не
// project-scoped): просмотр ролей и их прав (read-only), список субъектов с их
// ролями и форма назначить/снять роль субъекту. Все данные идут через периметр с
// рантайм-валидацией ответов zod; отказ доступа (403) скрывает содержимое
// (fail-closed на UI), внутренние ошибки клиенту не раскрываются.
import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { AlertTriangle, KeyRound, Loader2, ShieldX, Trash2, UserCog } from "lucide-react";
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

export function IamPage() {
  const queryClient = useQueryClient();
  const [selectedRole, setSelectedRole] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  // Каталог ролей. Ошибка 403 здесь означает отсутствие права на админку —
  // содержимое раздела не показываем (fail-closed на UI).
  const rolesQuery = useQuery({
    queryKey: ["iam", "roles"],
    queryFn: () => apiClient.listRoles(),
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

  // Инвалидация списков после мутации, чтобы UI отразил актуальные роли.
  const invalidateSubjects = () =>
    queryClient.invalidateQueries({ queryKey: ["iam", "subjects"] });

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
  const subjects = subjectsQuery.data?.pages.flatMap((p) => p.subjects) ?? [];

  return (
    <section className="flex flex-col gap-5">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Роли и доступы</h1>
        <p className="text-sm text-muted-foreground">
          Просмотр ролей и прав, управление назначением ролей субъектам (IAM-админка)
        </p>
      </div>

      {actionError && (
        <p className="flex items-center gap-2 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <AlertTriangle className="size-4 shrink-0" />
          {actionError}
        </p>
      )}

      {/* Роли и их права (read-only) */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <KeyRound className="size-4" /> Роли
          </CardTitle>
          <CardDescription>
            Роли сидируются миграциями; здесь они только отображаются. Выберите
            роль, чтобы увидеть её права.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {rolesQuery.isLoading && <p className="text-sm text-muted-foreground">Загрузка…</p>}
          {rolesQuery.isError && httpStatusOf(rolesQuery.error) !== 403 && (
            <p className="text-sm text-destructive">Не удалось загрузить роли.</p>
          )}
          <div className="flex flex-wrap gap-2">
            {roles.map((r) => (
              <button
                key={r.name}
                type="button"
                onClick={() => setSelectedRole(r.name)}
                className={
                  "rounded-md border px-3 py-1.5 text-sm transition-colors " +
                  (selectedRole === r.name
                    ? "border-primary bg-accent text-accent-foreground"
                    : "border-border hover:bg-accent/50")
                }
              >
                {r.name}
              </button>
            ))}
          </div>

          {selectedRole !== null && (
            <div className="rounded-md border border-border p-3">
              <p className="mb-2 text-sm font-medium">Права роли «{selectedRole}»</p>
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
                    <li key={`${p.action}:${p.resource}`} className="font-mono text-xs">
                      {p.action} @ {p.resource}
                    </li>
                  ))}
                </ul>
              )}
            </div>
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

// mutationMessage переводит ошибку мутации в стабильное пользовательское сообщение
// (без раскрытия внутренних деталей сервера).
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
