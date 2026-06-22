// Страница «Права» раздела «Роли и доступы» (ADR-0015). Каталог прав на DataTable
// (сортировка, состояния loading/empty/error, переключатель плотности idp-density).
// Создание права — модалка (react-hook-form + zod), удаление пользовательского права —
// ConfirmDialog. Системные права read-only. 403 на админку — fail-closed (IamGuard);
// 403 на manage — структурные действия скрыты/заблокированы периметром, результат —
// тост. Ответы валидируются zod в общих хуках.
import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { KeyRound, Loader2, Lock, Plus, Rows2, Rows3, Trash2 } from "lucide-react";
import { z } from "zod";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { DataTable, type ColumnDef } from "@/components/ui/data-table";
import { Dialog } from "@/components/ui/dialog";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader } from "@/components/PageHeader";
import { readDensity, writeDensity, type Density } from "@/lib/ui-state";
import { IamGuard } from "./IamGuard";
import { useCreatePermission, useDeletePermission, usePermissionsQuery } from "./hooks";

// createPermissionSchema — валидация ввода формы создания права.
const createPermissionSchema = z.object({
  action: z.string().min(1, "Укажите действие"),
  resource: z.string().min(1, "Укажите ресурс"),
});
type CreatePermissionValues = z.infer<typeof createPermissionSchema>;

// ConfirmState — подтверждение удаления пользовательского права.
type ConfirmState = { action: string; resource: string } | null;

export function PermissionsPage() {
  const [createPermOpen, setCreatePermOpen] = useState(false);
  const [confirm, setConfirm] = useState<ConfirmState>(null);
  // Плотность таблицы (idp-density) — сохраняется между сессиями.
  const [density, setDensity] = useState<Density>(readDensity);

  const permissionsQuery = usePermissionsQuery();
  const createPermMutation = useCreatePermission();
  const deletePermMutation = useDeletePermission();

  const createPermForm = useForm<CreatePermissionValues>({
    resolver: zodResolver(createPermissionSchema),
    defaultValues: { action: "", resource: "" },
  });

  const permissions = permissionsQuery.data?.permissions ?? [];

  // toggleDensity переключает и сохраняет плотность таблицы.
  const toggleDensity = () => {
    const next: Density = density === "compact" ? "comfortable" : "compact";
    setDensity(next);
    writeDensity(next);
  };

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
            onClick={() => setConfirm({ action: p.action, resource: p.resource })}
          >
            <Trash2 className="size-3.5" />
          </button>
        ),
    },
  ];

  return (
    <section className="flex flex-col gap-5">
      <PageHeader
        title="Права"
        description="Роли и доступы / Права — каталог прав (пара действие/ресурс)"
        actions={
          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-label={density === "compact" ? "Комфортная плотность" : "Компактная плотность"}
              title={density === "compact" ? "Комфортная плотность" : "Компактная плотность"}
              onClick={toggleDensity}
            >
              {density === "compact" ? <Rows3 className="size-4" /> : <Rows2 className="size-4" />}
            </Button>
            <Button type="button" variant="outline" size="sm" onClick={() => setCreatePermOpen(true)}>
              <Plus className="size-4" /> Создать право
            </Button>
          </div>
        }
      />

      <IamGuard error={permissionsQuery.error}>
        <div className="flex flex-col gap-2">
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <KeyRound className="size-4" /> Системные права защищены от удаления.
          </p>
          <DataTable
            columns={permColumns}
            rows={permissions}
            rowKey={(p) => `${p.action}:${p.resource}`}
            caption="Каталог прав"
            density={density}
            isLoading={permissionsQuery.isLoading}
            isError={permissionsQuery.isError}
            errorMessage="Не удалось загрузить права."
            emptyMessage="Каталог прав пуст."
          />
        </div>
      </IamGuard>

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
          onSubmit={createPermForm.handleSubmit((values) =>
            createPermMutation.mutate(values, {
              onSuccess: () => {
                createPermForm.reset();
                setCreatePermOpen(false);
              },
            }),
          )}
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

      {/* Confirm удаления пользовательского права */}
      <ConfirmDialog
        open={confirm !== null}
        onClose={() => setConfirm(null)}
        onConfirm={() => {
          if (confirm) deletePermMutation.mutate(confirm);
          setConfirm(null);
        }}
        title="Удалить право?"
        description={
          confirm
            ? `Право «${confirm.action} @ ${confirm.resource}» будет удалено из каталога и откреплено от ролей.`
            : ""
        }
        confirmLabel="Подтвердить"
      />
    </section>
  );
}
