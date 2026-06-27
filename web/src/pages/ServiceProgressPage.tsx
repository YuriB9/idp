// Экран сервиса с ЕДИНЫМ ступенчатым прогрессом асинхронных операций (ADR-0022).
// Поллит статус через периметр (хук useServiceStatus) до терминальной фазы и
// показывает ОДИН компонент Stepper для текущей операции (создание / смена
// владельцев / decommission / transfer). Активную операцию задают карточки через
// onStarted; если её нет — операция и фаза выводятся из грубого статуса. Тосты в
// карточках остаются только для синхронных ошибок запуска (ADR-0017).
import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { AlertTriangle, ArrowLeft, Loader2 } from "lucide-react";

import { Card, CardContent } from "@/components/ui/card";
import { Stepper } from "@/components/ui/stepper";
import { PageHeader } from "@/components/PageHeader";
import { StatusBadge } from "@/components/StatusBadge";
import { OwnersCard } from "@/components/OwnersCard";
import { DecommissionCard } from "@/components/DecommissionCard";
import { TransferCard } from "@/components/TransferCard";
import { useServiceStatus } from "@/hooks/useServiceStatus";
import {
  buildSteps,
  isProgressActive,
  resolveProgress,
  type ActiveOp,
  type Operation,
} from "@/lib/workflow-steps";

// OPERATION_LABEL — заголовок прогресса по операции (для aria-label степпера).
const OPERATION_LABEL: Record<Operation, string> = {
  create: "Создание сервиса",
  "change-owners": "Смена владельцев",
  decommission: "Вывод из эксплуатации",
  transfer: "Перенос сервиса",
};

export function ServiceProgressPage() {
  const { project = "", name = "" } = useParams();

  // activeOp — операция, запущенная пользователем на этой странице (карточки
  // сообщают о запуске через onStarted). null — фаза выводится из статуса.
  const [activeOp, setActiveOp] = useState<ActiveOp>(null);
  // sawTransferring — наблюдался ли статус transferring (нужно, чтобы отличить
  // «перенос завершён» (active после transferring) от «active до старта»).
  const sawTransferring = useRef(false);

  const { data, status, isLoading, isError } = useServiceStatus(project, name, {
    // Поллим, пока фаза текущей операции не стала терминальной.
    keepPolling: (d) =>
      isProgressActive(resolveProgress(d, activeOp, sawTransferring.current)),
  });

  // Фиксируем факт наблюдавшегося transferring для разрешения фазы переноса.
  useEffect(() => {
    if (status === "transferring") sawTransferring.current = true;
  }, [status]);

  const progress = resolveProgress(data, activeOp, sawTransferring.current);

  return (
    <section className="flex flex-col gap-5">
      <Link
        to={`/projects/${project}/services`}
        className="inline-flex w-fit items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
      >
        <ArrowLeft className="size-4" />
        К списку сервисов
      </Link>

      <PageHeader
        title={name}
        description={`Проект «${project}»`}
        actions={status ? <StatusBadge status={status} /> : undefined}
      />

      <Card>
        <CardContent className="flex flex-col gap-5 p-6">
          <div className="rounded-lg border border-border bg-muted/30 p-4 text-sm">
            {isLoading && (
              <span className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="size-4 motion-safe:animate-spin" />
                Запрашиваем статус…
              </span>
            )}
            {isError && (
              <span className="flex items-center gap-2 text-destructive">
                <AlertTriangle className="size-4" />
                Не удалось получить статус сервиса.
              </span>
            )}
            {progress && (
              <Stepper
                steps={buildSteps(progress.operation, progress.phase)}
                irreversible={progress.irreversible}
                note={progress.note}
                label={OPERATION_LABEL[progress.operation]}
              />
            )}
          </div>
        </CardContent>
      </Card>

      {/* Владельцы сервиса: отображение и форма изменения. Запуск смены владельцев
          поднимает единый степпер (короткий воркфлоу) — базовая версия владельцев
          фиксируется на момент запуска. */}
      {data && (
        <OwnersCard
          project={project}
          name={name}
          owners={data.owners}
          ownersVersion={data.owners_version}
          onStarted={() =>
            setActiveOp({
              operation: "change-owners",
              ownersBaseline: data.owners_version,
            })
          }
        />
      )}

      {/* Вывод сервиса из эксплуатации (soft delete): запуск поднимает единый
          степпер с пометкой точки невозврата. */}
      {data && (
        <DecommissionCard
          project={project}
          name={name}
          status={data.status}
          onStarted={() => {
            sawTransferring.current = false;
            setActiveOp({ operation: "decommission" });
          }}
        />
      )}

      {/* Перенос сервиса в другой проект: запуск поднимает единый степпер с
          пометкой точки невозврата. */}
      {data && (
        <TransferCard
          project={project}
          name={name}
          status={data.status}
          onStarted={() => {
            sawTransferring.current = false;
            setActiveOp({ operation: "transfer" });
          }}
        />
      )}
    </section>
  );
}
