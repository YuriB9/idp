// Карточка вывода сервиса из эксплуатации (soft delete / decommission). Действие
// необратимо для доступа (отзыв GitLab/Harbor/Vault), поэтому выполняется через
// ConfirmDialog (ADR-0017) с явным подтверждением: ввод имени сервиса и отметка
// снятой нагрузки из K8s (load_drained — предусловие, ADR-0012). Доступно только
// для активного сервиса; для прочих статусов действие заблокировано. Результат —
// через тосты (единый маппинг кодов 403/409/422), без раскрытия внутренних деталей.
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { PowerOff } from "lucide-react";

import { apiClient } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Input } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";

type Props = {
  project: string;
  name: string;
  status: string;
};

export function DecommissionCard({ project, name, status }: Props) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  // confirmName — введённое имя для подтверждения; loadDrained — отметка снятой
  // нагрузки (предусловие). Подтверждение доступно только при их выполнении.
  const [confirmName, setConfirmName] = useState("");
  const [loadDrained, setLoadDrained] = useState(false);

  const isActive = status === "active";
  const isDecommissioned = status === "decommissioned";

  const mutation = useMutation({
    mutationFn: () =>
      apiClient.decommissionService(
        { load_drained: loadDrained },
        { params: { project, name } },
      ),
    onSuccess: () => {
      toast.success("Сервис выведен из эксплуатации.");
      setOpen(false);
      setConfirmName("");
      setLoadDrained(false);
      void queryClient.invalidateQueries({ queryKey: ["service", project, name] });
    },
    onError: (err) =>
      toast.error(err, {
        action: "вывести сервис из эксплуатации",
        overrides: {
          403: "Недостаточно прав для вывода сервиса из эксплуатации.",
          409: "Статус сервиса изменился в другом месте. Обновите страницу и повторите.",
          422: "Предусловие не выполнено: убедитесь, что нагрузка снята из K8s.",
        },
      }),
  });

  // canConfirm — подтверждение корректно: точное совпадение имени и отметка нагрузки.
  const canConfirm = confirmName === name && loadDrained;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <PowerOff className="size-4" />
          Вывод из эксплуатации
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {isDecommissioned ? (
          <p className="text-sm text-muted-foreground">
            Сервис уже выведен из эксплуатации. Данные каталога сохранены.
          </p>
        ) : !isActive ? (
          <p className="text-sm text-muted-foreground">
            Вывод из эксплуатации доступен только для активного сервиса.
          </p>
        ) : (
          <>
            <p className="text-sm text-muted-foreground">
              Это <strong>вывод из эксплуатации</strong> (soft delete), а не
              удаление данных: запись каталога сохранится, но доступ во внешних
              системах (GitLab, Harbor, Vault) будет <strong>необратимо</strong>{" "}
              отозван.
            </p>
            <div className="flex justify-end">
              <Button type="button" variant="destructive" onClick={() => setOpen(true)}>
                Вывести из эксплуатации
              </Button>
            </div>

            <ConfirmDialog
              open={open}
              onClose={() => setOpen(false)}
              onConfirm={() => mutation.mutate()}
              title="Вывести сервис из эксплуатации?"
              description="Доступ во внешних системах будет необратимо отозван. Подтвердите снятие нагрузки и введите имя сервиса."
              confirmLabel="Вывести"
              confirmDisabled={!canConfirm}
              pending={mutation.isPending}
            >
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={loadDrained}
                  onChange={(e) => setLoadDrained(e.target.checked)}
                />
                Нагрузка сервиса снята из K8s
              </label>
              <label htmlFor="confirmName" className="text-sm font-medium">
                Имя сервиса для подтверждения
              </label>
              <Input
                id="confirmName"
                value={confirmName}
                placeholder={name}
                onChange={(e) => setConfirmName(e.target.value)}
              />
            </ConfirmDialog>
          </>
        )}
      </CardContent>
    </Card>
  );
}
