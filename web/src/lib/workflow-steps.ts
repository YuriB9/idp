// Клиентская модель шагов Temporal-воркфлоу (вариант B, ADR-0022). Источник «шагов
// от темпорала» — фиксированный порядок активностей воркфлоу сервиса projects
// (services/projects/{provisioning,changeowners,decommission,transfer}). Периметр
// отдаёт только ГРУБЫЙ статус сервиса (без per-activity прогресса), поэтому фаза
// операции выводится КЛИЕНТСКИ из статуса и доменных полей (owners_version,
// decommissioned_at), а шаги показываются ОДНОЙ фазой: нельзя достоверно указать,
// какой именно шаг идёт или упал. Это чистый модуль без побочных эффектов —
// тривиально тестируется (вход → ожидаемые состояния).

// Operation — асинхронная операция над сервисом, отражаемая ступенчатым прогрессом.
export type Operation = "create" | "change-owners" | "decommission" | "transfer";

// StepState — состояние отдельного шага в UI.
export type StepState = "pending" | "running" | "done" | "failed";

// WorkflowPhase — фаза операции в целом (грубая гранулярность варианта B).
export type WorkflowPhase = "pending" | "running" | "done" | "failed";

// Step — шаг прогресса: стабильный ключ, русская подпись и состояние.
export type Step = { key: string; label: string; state: StepState };

// OPERATION_STEPS — статические упорядоченные шаги по операции. Порядок и подписи
// соответствуют порядку активностей соответствующего воркфлоу projects.
export const OPERATION_STEPS: Record<Operation, { key: string; label: string }[]> = {
  // CreateServiceWorkflow: GitLab → Harbor → Vault → инъекция секретов → активация.
  create: [
    { key: "gitlab", label: "Создание репозитория GitLab" },
    { key: "harbor", label: "Создание проекта образов Harbor" },
    { key: "vault", label: "Настройка секретов Vault" },
    { key: "secrets", label: "Инъекция секретов в CI" },
    { key: "activate", label: "Активация сервиса" },
  ],
  // ChangeOwnersWorkflow — короткий воркфлоу (GitLab → Vault → каталог → IDM);
  // статус сервиса не меняется, поэтому показываем вырожденный одношаговый степпер.
  "change-owners": [{ key: "owners", label: "Применение состава владельцев" }],
  // DecommissionWorkflow: GitLab archive → Harbor read-only → Vault revoke → каталог.
  decommission: [
    { key: "gitlab", label: "Архивация репозитория GitLab" },
    { key: "harbor", label: "Перевод образов Harbor в read-only" },
    { key: "vault", label: "Отзыв секретов Vault" },
    { key: "catalog", label: "Фиксация вывода в каталоге" },
  ],
  // TransferServiceWorkflow: begin → GitLab transfer → Vault migrate → Harbor
  // metadata → commit → перенос ролей владельцев в IDM.
  transfer: [
    { key: "begin", label: "Начало переноса в каталоге" },
    { key: "gitlab", label: "Перенос репозитория GitLab" },
    { key: "vault", label: "Миграция секретов Vault" },
    { key: "harbor", label: "Обновление метаданных Harbor" },
    { key: "commit", label: "Фиксация переноса в каталоге" },
    { key: "roles", label: "Перенос ролей владельцев" },
  ],
};

// buildSteps применяет фазу операции единообразно ко всем её шагам. Честная
// гранулярность варианта B (ADR-0022): фаза операции напрямую = состояние каждого
// шага, без ложного утверждения о завершённости/падении конкретного шага.
export function buildSteps(operation: Operation, phase: WorkflowPhase): Step[] {
  return OPERATION_STEPS[operation].map((s) => ({ ...s, state: phase }));
}

// createPhase — фаза создания по статусу: active → done, failed → failed,
// иначе (creating) → running.
export function createPhase(status: string): WorkflowPhase {
  if (status === "active") return "done";
  if (status === "failed") return "failed";
  return "running";
}

// ownersPhase — фаза смены владельцев: завершено, когда owners_version вырос
// относительно базового значения на момент запуска (короткий воркфлоу не меняет
// статус сервиса; сигнал завершения — рост версии).
export function ownersPhase(baselineVersion: number, currentVersion: number): WorkflowPhase {
  return currentVersion > baselineVersion ? "done" : "running";
}

// decommissionPhase — фаза вывода из эксплуатации: decommissioned → done,
// failed → failed, иначе (статус ещё active) → running.
export function decommissionPhase(status: string): WorkflowPhase {
  if (status === "decommissioned") return "done";
  if (status === "failed") return "failed";
  return "running";
}

// transferPhase — фаза переноса: failed → failed; transferring → running; возврат
// в active ПОСЛЕ наблюдавшегося transferring → done; иначе (active до старта) →
// running (мост до перехода статуса в transferring).
export function transferPhase(status: string, sawTransferring: boolean): WorkflowPhase {
  if (status === "failed") return "failed";
  if (status === "transferring") return "running";
  if (sawTransferring && status === "active") return "done";
  return "running";
}

// noteFor — сопроводительное сообщение к терминальной фазе (для UI). На running
// сообщение не нужно. failed честно сообщает о факте отката (Saga, ADR-0005) без
// атрибуции конкретного упавшего шага.
export function noteFor(operation: Operation, phase: WorkflowPhase): string | undefined {
  if (phase === "failed") {
    switch (operation) {
      case "create":
        return "Создание завершилось ошибкой — выполнен откат (Saga).";
      case "decommission":
        return "Вывод из эксплуатации завершился ошибкой — выполнен откат (Saga).";
      case "transfer":
        return "Перенос завершился ошибкой — выполнен откат (Saga).";
      case "change-owners":
        return "Смена владельцев завершилась ошибкой — выполнен откат (Saga).";
    }
  }
  if (phase === "done") {
    switch (operation) {
      case "create":
        return "Сервис создан и активен.";
      case "decommission":
        return "Сервис выведен из эксплуатации. Данные каталога сохранены.";
      case "transfer":
        return "Перенос завершён.";
      case "change-owners":
        return "Состав владельцев обновлён.";
    }
  }
  return undefined;
}

// ActiveOp — операция, запущенная пользователем на текущей странице (или null —
// фаза выводится из одного лишь статуса). Для смены владельцев храним базовую
// версию владельцев для детекта завершения.
export type ActiveOp =
  | { operation: "change-owners"; ownersBaseline: number }
  | { operation: "decommission" }
  | { operation: "transfer" }
  | null;

// ResolvedProgress — итог разрешения прогресса: какая операция, её фаза, пометка
// необратимости (точка невозврата) и сопроводительное сообщение.
export type ResolvedProgress = {
  operation: Operation;
  phase: WorkflowPhase;
  irreversible: boolean;
  note?: string;
};

// ProgressData — минимум доменных полей из ответа периметра для разрешения фазы.
export type ProgressData = { status?: string; owners_version?: number };

// resolveProgress — центральная чистая функция: по доменным данным, активной
// операции и факту наблюдавшегося transferring определяет операцию и её фазу.
// Возвращает null, если статус ещё неизвестен (нет прогресса для показа).
export function resolveProgress(
  data: ProgressData | undefined,
  activeOp: ActiveOp,
  sawTransferring: boolean,
): ResolvedProgress | null {
  const status = data?.status;
  if (!status) return null;

  if (activeOp) {
    switch (activeOp.operation) {
      case "change-owners": {
        const phase = ownersPhase(
          activeOp.ownersBaseline,
          data?.owners_version ?? activeOp.ownersBaseline,
        );
        return {
          operation: "change-owners",
          phase,
          irreversible: false,
          note: noteFor("change-owners", phase),
        };
      }
      case "decommission": {
        const phase = decommissionPhase(status);
        return {
          operation: "decommission",
          phase,
          irreversible: true,
          note: noteFor("decommission", phase),
        };
      }
      case "transfer": {
        const phase = transferPhase(status, sawTransferring);
        return {
          operation: "transfer",
          phase,
          irreversible: true,
          note: noteFor("transfer", phase),
        };
      }
    }
  }

  // Активной операции нет — выводим операцию и фазу из одного грубого статуса.
  switch (status) {
    case "creating":
      return { operation: "create", phase: "running", irreversible: false };
    case "active":
      return { operation: "create", phase: "done", irreversible: false, note: noteFor("create", "done") };
    case "failed":
      return { operation: "create", phase: "failed", irreversible: false, note: noteFor("create", "failed") };
    case "transferring":
      return { operation: "transfer", phase: "running", irreversible: true };
    case "decommissioned":
      return {
        operation: "decommission",
        phase: "done",
        irreversible: true,
        note: noteFor("decommission", "done"),
      };
    default:
      return null;
  }
}

// isProgressActive — нужно ли продолжать поллинг: фаза ещё не терминальная.
// Используется хуком поллинга как предикат keepPolling.
export function isProgressActive(p: ResolvedProgress | null): boolean {
  return p !== null && (p.phase === "running" || p.phase === "pending");
}
