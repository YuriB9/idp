// Карточка переноса сервиса в другой проект (transfer). Действие частично
// НЕОБРАТИМО (transfer репозитория GitLab и миграция путей Vault — точка
// невозврата, ADR-0013), поэтому выполняется через ConfirmDialog (ADR-0017) с
// явным подтверждением: ввод имени сервиса и указание целевого проекта. Доступно
// только для активного сервиса; для прочих статусов заблокировано. Результат — через
// тосты (единый маппинг 403/409/422), без раскрытия внутренних деталей сервера.
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeftRight } from "lucide-react";

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
  // onStarted — вызывается после принятого запуска переноса; страница поднимает
  // единый ступенчатый прогресс (вместо тоста об успехе).
  onStarted?: () => void;
};

export function TransferCard({ project, name, status, onStarted }: Props) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  // targetProject — целевой проект; confirmName — введённое имя для подтверждения.
  const [targetProject, setTargetProject] = useState("");
  const [confirmName, setConfirmName] = useState("");

  const isActive = status === "active";
  const isTransferring = status === "transferring";

  const mutation = useMutation({
    mutationFn: () =>
      apiClient.transferService(
        { target_project: targetProject },
        { params: { project, name } },
      ),
    onSuccess: () => {
      // Ход и исход показываем не тостом, а единым ступенчатым прогрессом.
      setOpen(false);
      setTargetProject("");
      setConfirmName("");
      onStarted?.();
      void queryClient.invalidateQueries({ queryKey: ["service", project, name] });
    },
    onError: (err) =>
      toast.error(err, {
        action: "перенести сервис",
        overrides: {
          403: "Недостаточно прав: нужно право переноса в исходном и целевом проектах.",
          409: "Имя сервиса уже занято в целевом проекте или статус изменился. Выберите другой проект или обновите страницу.",
          422: "Перенос доступен только для активного сервиса (недопустимый исходный статус).",
        },
      }),
  });

  // canConfirm — корректное подтверждение: совпадение имени, заданный целевой
  // проект, отличный от исходного.
  const canConfirm =
    confirmName === name &&
    targetProject.trim() !== "" &&
    targetProject.trim() !== project;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <ArrowLeftRight className="size-4" />
          Перенос в другой проект
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {isTransferring ? (
          <p className="text-sm text-muted-foreground">
            Сервис уже переносится. Дождитесь завершения переноса.
          </p>
        ) : !isActive ? (
          <p className="text-sm text-muted-foreground">
            Перенос доступен только для активного сервиса.
          </p>
        ) : (
          <>
            <p className="text-sm text-muted-foreground">
              Перенос меняет проект-владельца: репозиторий GitLab переедет в новую
              группу, пути секретов Vault будут мигрированы, роли владельцев —
              перенесены. Часть шагов <strong>необратима</strong> (точка невозврата).
            </p>
            <div className="flex justify-end">
              <Button type="button" variant="destructive" onClick={() => setOpen(true)}>
                Перенести сервис
              </Button>
            </div>

            <ConfirmDialog
              open={open}
              onClose={() => setOpen(false)}
              onConfirm={() => mutation.mutate()}
              title="Перенести сервис в другой проект?"
              description="Часть шагов необратима (точка невозврата). Укажите целевой проект и введите имя сервиса."
              confirmLabel="Перенести"
              confirmDisabled={!canConfirm}
              pending={mutation.isPending}
            >
              <label htmlFor="targetProject" className="text-sm font-medium">
                Целевой проект
              </label>
              <Input
                id="targetProject"
                value={targetProject}
                placeholder="например, demo2"
                onChange={(e) => setTargetProject(e.target.value)}
              />
              <label htmlFor="confirmTransferName" className="text-sm font-medium">
                Имя сервиса для подтверждения
              </label>
              <Input
                id="confirmTransferName"
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
