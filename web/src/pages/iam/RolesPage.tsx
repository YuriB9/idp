// Страница «Роли» раздела «Роли и доступы» (ADR-0014/0015). Каталог ролей и права
// ВЫБРАННОЙ роли (attach/detach из каталога прав). Выбранная роль живёт в маршруте
// (/iam/roles/:role) — состояние шеринг-абельно и переживает перезагрузку. Создание
// роли — модалка (react-hook-form + zod), удаление пользовательской роли —
// ConfirmDialog. Системные роли read-only. 403 на админку — fail-closed (IamGuard);
// 403 на manage — структурные действия скрыты/заблокированы периметром, результат —
// тост. Ответы валидируются zod в общих хуках.
import { useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { KeyRound, Loader2, Lock, Plus, Trash2 } from "lucide-react";
import { z } from "zod";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Dialog } from "@/components/ui/dialog";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader } from "@/components/PageHeader";
import { httpStatusOf } from "@/lib/errors";
import { IamGuard } from "./IamGuard";
import {
  useAttachPermission,
  useCreateRole,
  useDeleteRole,
  useDetachPermission,
  usePermissionsQuery,
  useRolePermissionsQuery,
  useRolesQuery,
} from "./hooks";

// createRoleSchema — валидация ввода формы создания роли.
const createRoleSchema = z.object({ name: z.string().min(1, "Укажите имя роли") });
type CreateRoleValues = z.infer<typeof createRoleSchema>;

// ConfirmState — подтверждение деструктивного действия на странице «Роли».
type ConfirmState =
  | { kind: "delete-role"; role: string }
  | { kind: "detach"; role: string; action: string; resource: string }
  | null;

export function RolesPage() {
  const navigate = useNavigate();
  // Выбранная роль — из маршрута (/iam/roles/:role), а не локального стейта.
  const { role: routeRole } = useParams();
  const selectedRole = routeRole ?? null;

  const [attachPerm, setAttachPerm] = useState<string>("");
  const [createRoleOpen, setCreateRoleOpen] = useState(false);
  const [confirm, setConfirm] = useState<ConfirmState>(null);

  const rolesQuery = useRolesQuery();
  const permissionsQuery = usePermissionsQuery();
  const rolePermsQuery = useRolePermissionsQuery(selectedRole);

  const createRoleForm = useForm<CreateRoleValues>({
    resolver: zodResolver(createRoleSchema),
    defaultValues: { name: "" },
  });

  const createRoleMutation = useCreateRole();
  const deleteRoleMutation = useDeleteRole();
  const attachMutation = useAttachPermission();
  const detachMutation = useDetachPermission();

  const roles = rolesQuery.data?.roles ?? [];
  const permissions = permissionsQuery.data?.permissions ?? [];
  const selectedRoleObj = roles.find((r) => r.name === selectedRole) ?? null;
  const selectedRoleIsSystem = selectedRoleObj?.system ?? false;

  // selectRole переводит выбор роли в маршрут (deep-link).
  const selectRole = (name: string) => navigate(`/iam/roles/${encodeURIComponent(name)}`);

  return (
    <section className="flex flex-col gap-5">
      <PageHeader
        title="Роли"
        description="Роли и доступы / Роли — каталог ролей и состав их прав (IAM-админка)"
        actions={
          <Button type="button" variant="outline" size="sm" onClick={() => setCreateRoleOpen(true)}>
            <Plus className="size-4" /> Создать роль
          </Button>
        }
      />

      <IamGuard error={rolesQuery.error}>
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
                  <button type="button" onClick={() => selectRole(r.name)}>
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
                        attachMutation.mutate(
                          { role: selectedRole, action, resource },
                          { onSuccess: () => setAttachPerm("") },
                        );
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
      </IamGuard>

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
          onSubmit={createRoleForm.handleSubmit((values) =>
            createRoleMutation.mutate(values, {
              onSuccess: () => {
                createRoleForm.reset();
                setCreateRoleOpen(false);
              },
            }),
          )}
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

      {/* Confirm деструктивных действий: удаление роли / открепление права */}
      <ConfirmDialog
        open={confirm !== null}
        onClose={() => setConfirm(null)}
        onConfirm={() => {
          if (!confirm) return;
          if (confirm.kind === "delete-role") {
            const role = confirm.role;
            deleteRoleMutation.mutate(role, {
              onSuccess: () => {
                // При удалении выбранной роли возвращаемся к списку без выбора.
                if (selectedRole === role) navigate("/iam/roles");
              },
            });
          } else if (confirm.kind === "detach") {
            detachMutation.mutate({
              role: confirm.role,
              action: confirm.action,
              resource: confirm.resource,
            });
          }
          setConfirm(null);
        }}
        title={confirm?.kind === "delete-role" ? "Удалить роль?" : "Открепить право от роли?"}
        description={
          confirm?.kind === "delete-role"
            ? `Роль «${confirm.role}» будет удалена у всех носителей. Действие необратимо.`
            : confirm?.kind === "detach"
              ? `Право «${confirm.action} @ ${confirm.resource}» будет откреплено от роли «${confirm.role}».`
              : ""
        }
        confirmLabel="Подтвердить"
      />
    </section>
  );
}
