## 1. Подготовка и сверка

- [x] 1.1 Сверить план с docs/IDP_MVP_План.md (раздел «Стек») и ADR-0017
  (React 19 + Vite) и ADR-0009 (dev-прокси периметра); подтвердить, что новый
  ADR не нужен (изменение инструментальное)
- [x] 1.2 Создать ветку `change/web-vite8-upgrade` от master (прямые коммиты в
  master запрещены)

## 2. Обновление зависимостей

- [x] 2.1 В `web/package.json` поднять `vite` → `8.1.0`
- [x] 2.2 В `web/package.json` поднять `@vitejs/plugin-react` → `6.x`
  (точная актуальная версия линейки 6)
- [x] 2.3 Подтвердить, что `vitest` (4.1.9), `@tailwindcss/vite` (4.3.1),
  `tailwindcss` (4.3.1), `jsdom` (29.1.1) остаются без изменений (peer уже
  совместим с Vite 8)
- [x] 2.4 Пересобрать `web/package-lock.json` (`npm install`) и проверить
  воспроизводимость через `npm ci` (как в CI)

## 3. Правка конфигурации сборки

- [x] 3.1 В `web/vite.config.ts` заменить `__dirname` на `import.meta.dirname`
  для alias `@`→`./src`; сохранить `node:path` (`path.resolve`), плагины
  `react()` + `tailwindcss()`, `server.proxy` `/api`→gateway (env `GATEWAY_URL`,
  порт 3000), встроенный блок `test` (vitest: jsdom, `globals`, `setupFiles`) и
  директиву `/// <reference types="vitest/config" />`; все комментарии — на русском
- [x] 3.2 Принять решение по `engines.node` в `package.json` (базовый план —
  не добавлять; добавить `">=20.19"` только при выявленной пользе); CI-матрицу
  Node и `.github/workflows/ci.yml` не менять (Node 24 покрывает Vite 8)
- [x] 3.3 Проверить `web/tsconfig.json` и `web/src/test/setup.ts`: правки только
  при реальной необходимости совместимости (ожидаемо — без изменений)
- [x] 3.4 В `.github/dependabot.yml` сгруппировать vite-экосистему (`vite`,
  `@vitejs/*`, `@tailwindcss/vite`, `vitest`) в npm-экосистеме `/web`, чтобы
  связанные peer-зависимости предлагались одним PR (устраняет ERESOLVE из
  закрытых PR #64/#26)

## 4. Локальная проверка (зелёные гейты)

- [x] 4.1 `cd web && npx tsc --noEmit` — без ошибок типизации
- [x] 4.2 `cd web && npm test` — все 154 vitest-теста зелёные (jsdom, globals,
  setup сохранены)
- [x] 4.3 `cd web && npm run build` (`tsc -b && vite build`) — сборка без ошибок,
  бандл валиден
- [x] 4.4 `cd web && npm run preview` — dev-preview поднимается (smoke)
- [x] 4.5 `cd web && npm run gen:check` — кодоген OpenAPI без diff
- [x] 4.6 `cd web && npm run lint:openapi` — Spectral без ошибок

## 5. PR и завершение

- [ ] 5.1 Открыть PR из `change/web-vite8-upgrade` в master; дождаться зелёного
  CI (`web-test` [vitest + typecheck], `openapi-lint`, `codegen-check`, плюс
  Go-джобы остаются зелёными)
- [ ] 5.2 После merge — отдельным PR `/opsx:archive` (sync+archive, по образцу
  PR #71/#73)
