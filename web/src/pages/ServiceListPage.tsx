// Экран списка сервисов проекта. Данные — через периметр
// (GET /projects/{project}/services); ответ валидируется zod в zodios-клиенте,
// поэтому дрейф контракта падает явно.
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { ChevronRight, Inbox, Plus, AlertTriangle } from "lucide-react";

import { apiClient } from "@/api";
import { buttonVariants } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { StatusBadge } from "@/components/StatusBadge";
import { cn } from "@/lib/utils";

export function ServiceListPage() {
  const { project = "" } = useParams();

  const query = useQuery({
    queryKey: ["services", project],
    queryFn: () => apiClient.listServices({ params: { project } }),
  });

  return (
    <section className="flex flex-col gap-5">
      <div className="flex items-end justify-between">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Сервисы</h1>
          <p className="text-sm text-muted-foreground">Проект «{project}»</p>
        </div>
        <Link to={`/projects/${project}/services/new`} className={cn(buttonVariants())}>
          <Plus className="size-4" />
          Создать сервис
        </Link>
      </div>

      {query.isLoading && (
        <Card className="p-8 text-center text-sm text-muted-foreground">Загрузка…</Card>
      )}

      {query.isError && (
        <Card className="flex items-center gap-2 p-5 text-sm text-destructive">
          <AlertTriangle className="size-4" />
          Не удалось загрузить список сервисов.
        </Card>
      )}

      {query.data && query.data.services.length === 0 && (
        <Card className="flex flex-col items-center gap-2 p-10 text-center">
          <Inbox className="size-8 text-muted-foreground" />
          <p className="text-sm text-muted-foreground">В проекте пока нет ни одного сервиса.</p>
        </Card>
      )}

      {query.data && query.data.services.length > 0 && (
        <Card className="divide-y divide-border overflow-hidden p-0">
          {query.data.services.map((s) => (
            <Link
              key={s.name}
              to={`/projects/${project}/services/${s.name}`}
              className="flex items-center justify-between px-5 py-3.5 transition-colors hover:bg-muted/50"
            >
              <span className="font-medium">{s.name}</span>
              <span className="flex items-center gap-3">
                <StatusBadge status={s.status} />
                <ChevronRight className="size-4 text-muted-foreground" />
              </span>
            </Link>
          ))}
        </Card>
      )}
    </section>
  );
}
