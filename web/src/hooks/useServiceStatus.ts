// Хук поллинга статуса сервиса. Инкапсулирует логику опроса периметра
// (GET /projects/{project}/services/{name}) через TanStack Query: поллит с
// интервалом и ОСТАНАВЛИВАЕТСЯ, когда поллинг больше не нужен (предикат
// keepPolling). Без sleep — остановка выражается возвратом false из
// refetchInterval. Ответы валидируются zod (.parse) в zodios-клиенте. Хук
// переиспользуется экраном прогресса и карточками операций (общий queryKey →
// дедупликация и единый кэш TanStack Query).
import { useQuery } from "@tanstack/react-query";

import { apiClient } from "@/api";

// SERVICE_POLL_INTERVAL_MS — интервал поллинга статуса (мс).
export const SERVICE_POLL_INTERVAL_MS = 1500;

// ServiceData — тип ответа периметра по сервису (вывод из клиента).
export type ServiceData = Awaited<ReturnType<typeof apiClient.getService>>;

// isTerminalStatus — терминальный ли статус создания (поведение по умолчанию):
// active/failed/decommissioned — дальнейший поллинг не нужен.
export function isTerminalStatus(status?: string): boolean {
  return status === "active" || status === "failed" || status === "decommissioned";
}

type Options = {
  // enabled — выполнять ли запрос (по умолчанию true).
  enabled?: boolean;
  // keepPolling — продолжать ли поллинг по текущим данным. По умолчанию —
  // семантика создания (поллим, пока статус не терминальный). Карточки операций
  // передают свой предикат (decommission/transfer/owners завершаются иначе).
  keepPolling?: (data: ServiceData | undefined) => boolean;
};

export function useServiceStatus(project: string, name: string, options: Options = {}) {
  const keepPolling =
    options.keepPolling ?? ((data) => !isTerminalStatus(data?.status));

  const query = useQuery({
    queryKey: ["service", project, name],
    queryFn: () => apiClient.getService({ params: { project, name } }),
    enabled: options.enabled ?? true,
    // Поллим, пока keepPolling истинно; иначе refetchInterval=false (стоп без sleep).
    refetchInterval: (q) => (keepPolling(q.state.data) ? SERVICE_POLL_INTERVAL_MS : false),
  });

  return {
    query,
    data: query.data,
    status: query.data?.status,
    isLoading: query.isLoading,
    isError: query.isError,
  };
}
