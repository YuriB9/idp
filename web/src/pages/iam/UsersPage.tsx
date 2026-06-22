// Страница «Пользователи» раздела «Роли и доступы» (ADR-0014/0016). Список субъектов с
// ролями на DataTable с КУРСОРНОЙ пагинацией (next_page_token, ADR-0009). Назначение
// роли — ТОЛЬКО выбором пользователя из справочника Keycloak через пикер с поиском
// (debounce, username/email, канонический subject). Снятие роли — ConfirmDialog.
// «Осиротевшие» субъекты (found=false) помечаются «нет в каталоге». 403 на админку —
// fail-closed (IamGuard); 403 на iam:directory — пикер скрыт с отказом; 503 каталога —
// индикация, поиск остаётся. Ответы валидируются zod в общих хуках.
import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { AlertTriangle, Loader2, Search, ShieldX, Trash2, UserCog } from "lucide-react";
import { z } from "zod";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { DataTable, type ColumnDef } from "@/components/ui/data-table";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader } from "@/components/PageHeader";
import { httpStatusOf } from "@/lib/errors";
import { IamGuard } from "./IamGuard";
import {
  useAssignRole,
  useDebounced,
  useDirectorySearch,
  useRevokeRole,
  useRolesQuery,
  useSubjectsInfiniteQuery,
} from "./hooks";

// assignFormSchema — валидация ввода формы назначения роли (subject + role).
const assignFormSchema = z.object({
  subject: z.string().min(1, "Выберите пользователя"),
  role: z.string().min(1, "Выберите роль"),
});
type AssignFormValues = z.infer<typeof assignFormSchema>;

// ConfirmState — подтверждение снятия роли с субъекта.
type ConfirmState = { subject: string; role: string } | null;

export function UsersPage() {
  const [subjectSearch, setSubjectSearch] = useState<string>("");
  const debouncedSearch = useDebounced(subjectSearch, 300);
  const [picked, setPicked] = useState<{ label: string; email: string } | null>(null);
  const [confirm, setConfirm] = useState<ConfirmState>(null);

  const rolesQuery = useRolesQuery();
  const subjectsQuery = useSubjectsInfiniteQuery();
  const directoryQuery = useDirectorySearch(debouncedSearch);
  const directoryForbidden = httpStatusOf(directoryQuery.error) === 403;
  const directoryUnavailable = httpStatusOf(directoryQuery.error) === 503;

  const assignMutation = useAssignRole();
  const revokeMutation = useRevokeRole();

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

  const roles = rolesQuery.data?.roles ?? [];
  const subjects = subjectsQuery.data?.pages.flatMap((p) => p.subjects) ?? [];

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
                onClick={() => setConfirm({ subject: s.subject, role })}
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
        title="Пользователи"
        description="Роли и доступы / Пользователи — субъекты с ролями и назначение роли"
      />

      <IamGuard error={subjectsQuery.error}>
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
              onSubmit={handleSubmit((values) =>
                assignMutation.mutate(values, {
                  onSuccess: () => {
                    reset();
                    setPicked(null);
                  },
                }),
              )}
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
      </IamGuard>

      {/* Confirm снятия роли */}
      <ConfirmDialog
        open={confirm !== null}
        onClose={() => setConfirm(null)}
        onConfirm={() => {
          if (confirm) revokeMutation.mutate({ subject: confirm.subject, role: confirm.role });
          setConfirm(null);
        }}
        title="Снять роль с пользователя?"
        description={confirm ? `Роль «${confirm.role}» будет снята с пользователя.` : ""}
        confirmLabel="Подтвердить"
      />
    </section>
  );
}
