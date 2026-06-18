// Экземпляр API-клиента периметра. Базовый URL — /api (тот же origin, что и
// dev-прокси Vite / oauth2-proxy локально). Zodios валидирует КАЖДЫЙ ответ по
// сгенерированной из OpenAPI zod-схеме (response validation), поэтому дрейф
// контракта падает явно прямо на границе клиента (см. ADR-0009, design.md).
import { createApiClient, schemas } from "./client";

// apiClient — типобезопасный клиент периметра с рантайм-валидацией ответов.
export const apiClient = createApiClient("/api");

// Реэкспорт zod-схем для переиспользования в формах (источник правды
// валидации ввода) и в тестах валидации ответов.
export { schemas };
