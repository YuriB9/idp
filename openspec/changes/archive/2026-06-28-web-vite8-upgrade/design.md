## Context

Сборка портала `./web` стоит на Vite 6.0.7 — на два мажора ниже актуального
(Vite 8.1.0). Текущая поверхность конфигурации минимальна и удобна для апгрейда:
один `web/vite.config.ts` со встроенным блоком `test` (vitest), без отдельного
`vitest.config.*`, без project references (один `tsconfig.json`, `include:
["src"]`, т.е. сам конфиг не типизируется через `tsc --noEmit`). Зафиксированный
стек (ADR-0017, docs/IDP_MVP_План.md, раздел «Стек») — «React 19 + Vite»; смены
бандлера/раннера не предполагается, поэтому изменение инфраструктурное.

Фактическая инвентаризация (сверено с реестром npm на момент проектирования):
- `vite` latest = `8.1.0` (требует Node 20.19+/22.12+).
- `vitest@4.1.9` peer: `vite: "^6.0.0 || ^7.0.0 || ^8.0.0"` — **уже совместим с
  Vite 8**, бамп раннера НЕ нужен.
- `@vitejs/plugin-react@4.3.4` (текущий) поддерживает только Vite 6; `latest`
  `6.0.3` имеет peer `vite: "^8.0.0"` — требуется мажорный бамп плагина.
- `@tailwindcss/vite@4.3.1` peer: `vite: "^5.2.0 || ^6 || ^7 || ^8"` — совместим,
  бамп НЕ нужен.
- `jsdom@29.1.1` — актуальная версия, бамп НЕ нужен.
- CI и локально — Node 24 (`actions/setup-node@v6`, `node-version: "24"`),
  baseline Vite 8 удовлетворён.

Текущих vitest-тестов — 154, все зелёные; их API (jsdom, `globals`, `setupFiles`)
не должно меняться.

## Goals / Non-Goals

**Goals:**
- Поднять `vite` 6→8 и `@vitejs/plugin-react` 4→6 минимальными совместимыми
  версиями; `package-lock.json` пересобран воспроизводимо (`npm ci` зелёный).
- Привести `vite.config.ts` к нативному ESM-загрузчику (заменить `__dirname`),
  сохранив alias `@`, dev-прокси `/api`, Tailwind-плагин и встроенный vitest-блок.
- Зелёные `web-test` (`tsc --noEmit` + 154 vitest-теста), `build`
  (`tsc -b && vite build`), `preview`, `gen:check`/`codegen-check`,
  `openapi-lint` — локально и в CI на Node 24.

**Non-Goals:**
- Смена бандлера/раннера (Rolldown как отдельный продукт, rsbuild и т.п.) сверх
  того, что приносит сам Vite 8 по умолчанию.
- Бамп vitest, tailwindcss, jsdom сверх минимально необходимого (не требуется).
- Любые UI-/поведенческие изменения, новые фичи, редизайн.
- Изменения контракта (proto/OpenAPI), бэкенда, Go-модулей, `tests/e2e`.
- Апгрейды не связанных с Vite зависимостей (React/router/zod и пр.).

## Decisions

**Решение 1: vitest НЕ бампать (оставить 4.1.9).**
Peer-диапазон `vitest@4.1.9` уже включает `vite ^8`. Бамп ради бампа добавил бы
риск регрессии в 154 тестах без выгоды. Альтернатива (поднять до 4.x latest)
отклонена: API не меняется, совместимость уже есть.

**Решение 2: `@vitejs/plugin-react` бампнуть до `^6` (минимальная мажорная,
совместимая с Vite 8).** Линейка 4.x не поддерживает Vite 7/8. v6 — текущий
мажор с peer `vite: ^8`. Дополнительные peer'ы плагина
(`@rolldown/plugin-babel`, `babel-plugin-react-compiler`) опциональны и не
требуются для текущего использования (`react()` без babel-react-compiler).
Альтернатива (плагин SWC) отклонена — лишняя смена тулинга вне границ.

**Решение 3: `@tailwindcss/vite`, `tailwindcss`, `jsdom` — без бампа.** Peer
Tailwind-плагина уже покрывает Vite 8; стили генерируются тем же плагином, риск
визуальной регрессии минимален. jsdom актуален.

**Решение 4: `__dirname` → `import.meta.dirname` в `vite.config.ts`.** Конфиг
уже ESM (`"type":"module"`), но опирался на CJS-переменную `__dirname`, что под
нативным ESM-загрузчиком конфигов Vite ненадёжно. `import.meta.dirname` (Node
20.11+/22+, baseline Vite 8 удовлетворяет) даёт абсолютный путь без
`fileURLToPath(import.meta.url)`-boilerplate. Импорт `node:path` сохраняется для
`path.resolve`. Alias `@`→`./src`, прокси и `test`-блок не меняются.

**Решение 5: Node baseline.** CI-матрица Node не меняется (Node 24 покрывает
Vite 8). `engines.node` в `package.json` НЕ добавляем без необходимости, чтобы не
плодить расхождений с CI; если apply покажет полезность — зафиксировать
`">=20.19"` отдельной строкой. `.github/workflows/ci.yml` не трогаем.

**Решение 6: ADR не заводим.** Решение инструментальное в рамках выбранного
стека (ADR-0017). Если в ходе apply всплывёт архитектурно значимое (смена
раннера/бандлера) — оформить ADR отдельно.

**Решение 7: воспроизводимость lock-файла.** Версии в `package.json`
пинуются точными значениями (как сейчас); `package-lock.json` пересобирается
`npm install` и проверяется `npm ci` (как в CI). Без caret-диапазонов в
прод-зависимостях сверх существующего стиля.

## Risks / Trade-offs

- **Breaking-дефолты Vite 7/8 (build `target` → `baseline-widely-available`,
  изменения Environment API/Rolldown-дефолтов)** → проверить `vite build` и
  `vite preview` на валидный бандл; визуальная проверка не требуется (поведение
  то же), но сборка должна пройти без ошибок типизации (`tsc -b`).
- **Несовместимость `@vitejs/plugin-react` v6 с текущим JSX-runtime/Fast
  Refresh** → smoke-проверка `vite build` + 154 vitest-теста (jsdom-рендер RTL)
  как косвенный сигнал работоспособности плагина.
- **`import.meta.dirname` undefined на старом Node** → baseline Node 24 (CI и
  локально) гарантирует наличие; риск только при понижении Node, что вне границ.
- **vitest 4.1.9 формально совместим, но всплывут рантайм-различия под Vite 8**
  → mitigation: прогон всех 154 тестов локально до PR; при реальной регрессии —
  минимальный бамп vitest до совместимого минора (запасной план, не базовый).
- **`package-lock.json` дрейфует невоспроизводимо** → пересобрать начисто, затем
  отдельным шагом убедиться `npm ci` зелёный (как джоба CI).
- **codegen-check ложно покраснеет** → кодоген (`openapi-typescript`/
  `openapi-zod-client`) от Vite не зависит; `npm run gen` не должен дать diff.
  Mitigation: прогнать `gen:check` локально после обновления deps.

## Migration Plan

1. Ветка `change/web-vite8-upgrade` от master (прямые коммиты в master запрещены).
2. В `web/package.json`: `vite` → `8.1.0`, `@vitejs/plugin-react` → `6.x`;
   остальные Vite-зависимые пакеты без изменений.
3. `npm install` для пересборки `package-lock.json`; затем `npm ci` (зелёный).
4. Правка `web/vite.config.ts`: `__dirname` → `import.meta.dirname` (комментарии
   на русском).
5. Локально зелёные: `tsc --noEmit`, `npm test` (154), `npm run build`,
   `npm run preview` (smoke), `npm run gen:check`, `npm run lint:openapi`.
6. PR с зелёным CI (`web-test`, `openapi-lint`, `codegen-check` + Go-джобы).
7. **Откат:** ревертом PR — восстановление прежних версий и `package-lock.json`;
   провизию ресурсов изменение не затрагивает, компенсаций не требуется.

## Open Questions

- Нужно ли фиксировать `engines.node` в `package.json`? Базовый план — нет (CI
  уже на Node 24). Решение финализируется на шаге 2 apply, если выявится польза.
