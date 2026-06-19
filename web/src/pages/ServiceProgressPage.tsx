// Экран прогресса создания сервиса. Поллит статус через периметр
// (GET /projects/{project}/services/{name}) с интервалом, пока статус не станет
// терминальным (active/failed); затем опрос останавливается и показывается
// исход. Каждый ответ валидируется zod в zodios-клиенте.
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { AlertTriangle, ArrowLeft, CheckCircle2, Loader2, XCircle } from "lucide-react";

import { apiClient } from "@/api";
import { Card, CardContent } from "@/components/ui/card";
import { StatusBadge } from "@/components/StatusBadge";
import { OwnersCard } from "@/components/OwnersCard";

// POLL_INTERVAL_MS — интервал поллинга статуса (см. design.md, стратегия).
const POLL_INTERVAL_MS = 1500;

// isTerminal — достигнут ли терминальный статус (опрос пора останавливать).
function isTerminal(status: string): boolean {
  return status === "active" || status === "failed";
}

// apiGetService — обёртка над клиентом периметра для чтения статуса.
function apiGetService(project: string, name: string) {
  return apiClient.getService({ params: { project, name } });
}

export function ServiceProgressPage() {
  const { project = "", name = "" } = useParams();

  const query = useQuery({
    queryKey: ["service", project, name],
    queryFn: () => apiGetService(project, name),
    // Поллим, пока статус не терминальный; на терминале refetchInterval=false.
    refetchInterval: (q) => {
      const status = q.state.data?.status;
      return status && isTerminal(status) ? false : POLL_INTERVAL_MS;
    },
  });

  const status = query.data?.status;

  return (
    <section className="flex flex-col gap-5">
      <Link
        to={`/projects/${project}/services`}
        className="inline-flex w-fit items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
      >
        <ArrowLeft className="size-4" />
        К списку сервисов
      </Link>

      <Card>
        <CardContent className="flex flex-col gap-5 p-6">
          <div className="flex items-center justify-between">
            <div>
              <h1 className="text-lg font-semibold">{name}</h1>
              <p className="text-sm text-muted-foreground">Проект «{project}»</p>
            </div>
            {status && <StatusBadge status={status} />}
          </div>

          <div className="rounded-lg border border-border bg-muted/30 p-4 text-sm">
            {query.isLoading && (
              <span className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                Запрашиваем статус…
              </span>
            )}
            {query.isError && (
              <span className="flex items-center gap-2 text-destructive">
                <AlertTriangle className="size-4" />
                Не удалось получить статус сервиса.
              </span>
            )}
            {status === "creating" && (
              <span className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                Идёт создание — провизия GitLab, Harbor и Vault. Статус
                обновляется автоматически.
              </span>
            )}
            {status === "active" && (
              <span className="flex items-center gap-2 text-success">
                <CheckCircle2 className="size-4" />
                Сервис создан и активен.
              </span>
            )}
            {status === "failed" && (
              <span className="flex items-center gap-2 text-destructive">
                <XCircle className="size-4" />
                Создание завершилось ошибкой — выполнен откат (Saga).
              </span>
            )}
            {status === "decommissioned" && (
              <span className="text-muted-foreground">Сервис выведен из эксплуатации.</span>
            )}
          </div>
        </CardContent>
      </Card>

      {/* Владельцы сервиса: отображение и форма изменения (доступны при наличии
          данных сервиса; смена владельцев требует права change_owners). */}
      {query.data && (
        <OwnersCard
          project={project}
          name={name}
          owners={query.data.owners}
          ownersVersion={query.data.owners_version}
        />
      )}
    </section>
  );
}
