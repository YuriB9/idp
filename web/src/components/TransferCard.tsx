// Карточка переноса сервиса в другой проект (transfer). Действие частично
// НЕОБРАТИМО (transfer репозитория GitLab и миграция путей Vault — точка
// невозврата, ADR-0013), поэтому требует явного подтверждения: ввод имени сервиса
// и указание целевого проекта. Доступно только для активного сервиса; для прочих
// статусов действие заблокировано. Обрабатываем 403 (нет права на исходный или
// целевой проект), 409 (занятое имя в target или конкурентный конфликт) и 422
// (недопустимый исходный статус) понятными сообщениями без раскрытия внутренних
// деталей сервера.
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowLeftRight, Loader2 } from "lucide-react";

import { apiClient } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

// httpStatusOf аккуратно достаёт HTTP-статус из ошибки zodios/axios.
function httpStatusOf(err: unknown): number | undefined {
  if (typeof err === "object" && err !== null && "response" in err) {
    return (err as { response?: { status?: number } }).response?.status;
  }
  return undefined;
}

type Props = {
  project: string;
  name: string;
  status: string;
};

export function TransferCard({ project, name, status }: Props) {
  const queryClient = useQueryClient();
  const [serverError, setServerError] = useState<string | null>(null);
  // targetProject — целевой проект; confirmName — введённое имя для подтверждения.
  // Кнопка активна только при совпадении имени, заданном target (!= source) и
  // активном статусе.
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
      setServerError(null);
      void queryClient.invalidateQueries({ queryKey: ["service", project, name] });
    },
    onError: (err) => {
      const code = httpStatusOf(err);
      if (code === 403) {
        setServerError(
          "Недостаточно прав: нужно право переноса в исходном и целевом проектах.",
        );
      } else if (code === 409) {
        setServerError(
          "Имя сервиса уже занято в целевом проекте или статус изменился. Выберите другой проект или обновите страницу.",
        );
      } else if (code === 422) {
        setServerError(
          "Перенос доступен только для активного сервиса (недопустимый исходный статус).",
        );
      } else {
        setServerError("Не удалось перенести сервис. Повторите попытку.");
      }
    },
  });

  // canSubmit — подтверждение корректно: активный статус, точное совпадение имени,
  // заданный целевой проект, отличный от исходного.
  const canSubmit =
    isActive &&
    confirmName === name &&
    targetProject.trim() !== "" &&
    targetProject.trim() !== project &&
    !mutation.isPending;

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
              перенесены. Часть шагов <strong>необратима</strong> (точка
              невозврата). Укажите целевой проект и введите имя сервиса для
              подтверждения.
            </p>

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

            {serverError && (
              <p className="flex items-center gap-2 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">
                <AlertTriangle className="size-4 shrink-0" />
                {serverError}
              </p>
            )}

            <div className="flex justify-end">
              <Button
                type="button"
                variant="destructive"
                disabled={!canSubmit}
                onClick={() => {
                  setServerError(null);
                  mutation.mutate();
                }}
              >
                {mutation.isPending && <Loader2 className="size-4 animate-spin" />}
                {mutation.isPending ? "Переносим…" : "Перенести сервис"}
              </Button>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}
