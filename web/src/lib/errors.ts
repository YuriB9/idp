// Единый UI-маппинг ошибок периметра (ADR-0017 поверх ADR-0009). Переводит
// HTTP-коды стабилизированного периметра (400/403/404/409/422/503) в понятные
// пользователю сообщения БЕЗ раскрытия сырых внутренних деталей сервера.
// Консолидирует разрозненные ранее httpStatusOf/mutationMessage/catalogMessage по
// страницам. Рантайм-валидация ответов zod (.parse) сохраняется отдельно — этот
// модуль отвечает только за сообщения об ошибках действий/загрузок.

// httpStatusOf аккуратно достаёт HTTP-статус из ошибки zodios/axios.
export function httpStatusOf(err: unknown): number | undefined {
  if (typeof err === "object" && err !== null && "response" in err) {
    return (err as { response?: { status?: number } }).response?.status;
  }
  return undefined;
}

// PerimeterErrorOptions — настройка сообщения под контекст вызывающего кода.
export type PerimeterErrorOptions = {
  // action — короткое описание действия для дефолтного сообщения («назначить роль»).
  // Подставляется в «Не удалось {action}. Повторите попытку.».
  action?: string;
  // overrides — переопределение текста для конкретных кодов (например, более точное
  // 409 «имя занято» для создания сервиса). Имеет приоритет над дефолтами.
  overrides?: Partial<Record<number, string>>;
};

// Дефолтные сообщения по кодам периметра (стабильные, без внутренних деталей).
const defaultByCode: Record<number, string> = {
  400: "Некорректные данные запроса.",
  403: "Недостаточно прав для выполнения операции.",
  404: "Ресурс не найден.",
  409: "Конфликт состояния: данные изменились или уже существуют. Обновите и повторите.",
  422: "Предусловие операции не выполнено.",
  503: "Сервис временно недоступен. Повторите попытку позже.",
};

// perimeterErrorMessage возвращает стабильное пользовательское сообщение об ошибке
// действия/загрузки по её HTTP-коду. Сырые внутренние ошибки наружу не идут.
export function perimeterErrorMessage(
  err: unknown,
  options: PerimeterErrorOptions = {},
): string {
  const status = httpStatusOf(err);
  if (status !== undefined && options.overrides && status in options.overrides) {
    return options.overrides[status] as string;
  }
  if (status !== undefined && status in defaultByCode) {
    return defaultByCode[status];
  }
  return options.action
    ? `Не удалось ${options.action}. Повторите попытку.`
    : "Не удалось выполнить операцию. Повторите попытку.";
}

// isRetryable сообщает, является ли ошибка временной деградацией (503, ADR-0009):
// UI может предложить повтор, не ломая остальной интерфейс.
export function isRetryable(err: unknown): boolean {
  return httpStatusOf(err) === 503;
}
