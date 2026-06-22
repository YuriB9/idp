// Экран списка сервисов проекта. Данные — через периметр
// (GET /projects/{project}/services) с keyset-пагинацией (next_page_token,
// ADR-0009); ответ валидируется zod в zodios-клиенте, поэтому дрейф контракта
// падает явно. Подача — единый DataTable дизайн-системы (ADR-0017): плотные строки,
// бейдж статуса, состояния loading/empty/error, курсорная пагинация и переход к
// детали сервиса по строке.
import { useInfiniteQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Plus } from "lucide-react";

import { apiClient } from "@/api";
import { buttonVariants } from "@/components/ui/button";
import { DataTable, type ColumnDef } from "@/components/ui/data-table";
import { PageHeader } from "@/components/PageHeader";
import { StatusBadge } from "@/components/StatusBadge";
import { cn } from "@/lib/utils";

// ServiceRow — строка таблицы сервисов (подмножество ServiceSummary периметра).
type ServiceRow = {
  name: string;
  status: string;
  owners: string[];
};

export function ServiceListPage() {
  const { project = "" } = useParams();
  const navigate = useNavigate();

  // Keyset-пагинация по next_page_token (ADR-0009): курсор непрозрачен и
  // пробрасывается без интерпретации.
  const query = useInfiniteQuery({
    queryKey: ["services", project],
    initialPageParam: "",
    queryFn: ({ pageParam }) =>
      apiClient.listServices({
        params: { project },
        queries: { page_token: pageParam || undefined },
      }),
    getNextPageParam: (lastPage) => lastPage.next_page_token || undefined,
  });

  const services: ServiceRow[] =
    query.data?.pages.flatMap((p) => p.services) ?? [];

  const columns: ColumnDef<ServiceRow>[] = [
    {
      id: "name",
      header: "Сервис",
      sortValue: (s) => s.name,
      cell: (s) => <span className="font-medium">{s.name}</span>,
    },
    {
      id: "owners",
      header: "Владельцы",
      cell: (s) =>
        s.owners.length > 0 ? (
          <span className="text-muted-foreground">{s.owners.join(", ")}</span>
        ) : (
          <span className="text-muted-foreground/60">—</span>
        ),
    },
    {
      id: "status",
      header: "Статус",
      align: "right",
      cell: (s) => (
        <span className="inline-flex justify-end">
          <StatusBadge status={s.status} />
        </span>
      ),
    },
  ];

  return (
    <section className="flex flex-col gap-5">
      <PageHeader
        title="Сервисы"
        description={`Проект «${project}»`}
        actions={
          <Link
            to={`/projects/${project}/services/new`}
            className={cn(buttonVariants())}
          >
            <Plus className="size-4" />
            Создать сервис
          </Link>
        }
      />

      <DataTable
        columns={columns}
        rows={services}
        rowKey={(s) => s.name}
        caption="Список сервисов проекта"
        isLoading={query.isLoading}
        isError={query.isError}
        errorMessage="Не удалось загрузить список сервисов."
        emptyMessage="В проекте пока нет ни одного сервиса."
        onRowClick={(s) => navigate(`/projects/${project}/services/${s.name}`)}
        pagination={{
          hasNextPage: Boolean(query.hasNextPage),
          isFetchingNextPage: query.isFetchingNextPage,
          onLoadMore: () => void query.fetchNextPage(),
        }}
      />
    </section>
  );
}
