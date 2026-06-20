// Карточка вывода сервиса из эксплуатации (soft delete / decommission). Действие
// необратимо для доступа (отзыв GitLab/Harbor/Vault), поэтому требует явного
// подтверждения: ввод имени сервиса и отметку о снятой нагрузке из K8s
// (load_drained — предусловие, ADR-0012). Доступно только для активного сервиса;
// для прочих статусов действие заблокировано. Обрабатываем 403 (нет права), 409
// (конкурентный конфликт) и 422 (предусловие не выполнено) понятными сообщениями
// без раскрытия внутренних деталей сервера.
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Loader2, PowerOff } from "lucide-react";

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

export function DecommissionCard({ project, name, status }: Props) {
  const queryClient = useQueryClient();
  const [serverError, setServerError] = useState<string | null>(null);
  // confirmName — введённое имя для подтверждения; loadDrained — отметка снятой
  // нагрузки (предусловие). Кнопка активна только при совпадении имени и отметке.
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
      setServerError(null);
      void queryClient.invalidateQueries({ queryKey: ["service", project, name] });
    },
    onError: (err) => {
      const code = httpStatusOf(err);
      if (code === 403) {
        setServerError("Недостаточно прав для вывода сервиса из эксплуатации.");
      } else if (code === 409) {
        setServerError(
          "Статус сервиса изменился в другом месте. Обновите страницу и повторите.",
        );
      } else if (code === 422) {
        setServerError(
          "Предусловие не выполнено: убедитесь, что нагрузка снята из K8s.",
        );
      } else {
        setServerError("Не удалось вывести сервис из эксплуатации. Повторите попытку.");
      }
    },
  });

  // canSubmit — подтверждение корректно: точное совпадение имени и отметка нагрузки.
  const canSubmit = isActive && confirmName === name && loadDrained && !mutation.isPending;

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
              отозван. Чтобы подтвердить, введите имя сервиса.
            </p>

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
                {mutation.isPending ? "Выводим…" : "Вывести из эксплуатации"}
              </Button>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}
