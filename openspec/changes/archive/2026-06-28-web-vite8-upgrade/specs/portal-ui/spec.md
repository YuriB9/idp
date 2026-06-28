## MODIFIED Requirements

### Requirement: SPA-портал поднимается как реальное приложение

Модуль `./web` ДОЛЖЕН (MUST) быть полноценным SPA на React 19 + Vite +
TypeScript с роутингом (react-router) и `QueryClient` (TanStack Query):
`index.html`, `main.tsx`, корневой `App`. ДОЛЖНЫ (MUST) присутствовать
npm-скрипты `dev`/`build`/`preview` и dev-прокси `/api` → gateway. OpenAPI
остаётся единственным источником правды для TS-клиента/zod (кодоген `gen` без
diff в CI).

Сборка и dev-сервер ДОЛЖНЫ (MUST) работать на актуальном мажоре Vite (≥8) при
Node baseline ≥20.19/22.12; конфигурация Vite ДОЛЖНА (MUST) загружаться нативным
ESM-загрузчиком (без CJS-переменных вроде `__dirname`), сохраняя alias `@`→`src`,
dev-прокси `/api`→gateway и встроенный блок тестов (vitest). Апгрейд тулчейна НЕ
ДОЛЖЕН (MUST NOT) менять пользовательское поведение портала: dev-прокси, алиасы,
Tailwind-стили и набор vitest-тестов сохраняются.

#### Scenario: Сборка и запуск

- **WHEN** выполняется `npm run build`
- **THEN** Vite собирает SPA без ошибок типизации

#### Scenario: Dev-прокси периметра

- **GIVEN** запущенный gateway
- **WHEN** в dev-режиме (`npm run dev`) портал шлёт запрос на `/api/...`
- **THEN** dev-сервер Vite проксирует его на gateway, без CORS-ошибок в браузере

#### Scenario: Конфиг грузится нативным ESM-загрузчиком

- **GIVEN** `vite.config.ts` как ESM-модуль (`"type":"module"`) без `__dirname`
- **WHEN** Vite загружает конфигурацию для `dev`/`build`/`preview`
- **THEN** alias `@`→`src` и dev-прокси `/api` разрешаются корректно, ошибок
  загрузки конфига нет
