## Context

IAM-админка портала (ADR-0014/0015/0016) сейчас целиком собрана на одной странице
`web/src/pages/IamPage.tsx` (маршрут `/iam`): панель ролей с чипами/выбором, модалка
создания роли, confirm удаления роли; права выбранной роли (attach-select + confirm
detach); каталог прав на DataTable + модалка создания + confirm удаления; форма
назначения роли с пикером из справочника Keycloak; список субъектов с ролями на
DataTable с курсорной пагинацией + confirm снятия роли. Деструктивные действия идут
через единый `ConfirmDialog` поверх discriminated-union `ConfirmState`. Маппинги кодов
периметра — `ASSIGN_OVERRIDES` (write) и `CATALOG_OVERRIDES` (manage). Тесты —
`IamPage.test.tsx` (15 it-блоков: happy-path, 403, назначение/снятие, создание/удаление
роли, attach/detach, read-only системных, пикер с debounce, обогащение/осиротевшие,
403/503 справочника).

Дизайн-система внедрена в ADR-0017 (change `portal-ui-design-system`): примитивы
`ui/data-table`, `ui/dialog`, `ui/confirm-dialog`, `ui/toast`, `PageHeader`,
`Sidebar` (плоское меню `SidebarGroup → SidebarItem`), `StatusBadge`/`CriticalityBadge`,
хелперы `lib/errors` и `lib/ui-state` (localStorage с белым списком: `idp-sidebar`,
`idp-density`, `idp-theme`). Стек зафиксирован: React 19 + Vite + TS + TanStack Query +
react-router v7, react-hook-form + zod, Tailwind + shadcn-подход, lucide-react.

Контракт периметра (OpenAPI), `web/src/api/*` (кодоген), `web/public/openapi.yaml` —
заморожены; `gen:check` обязан остаться зелёным. Это чистый рефактор фронтенда.

**Зависимость по порядку**: требование «Раздвигающееся левое меню» физически лежит в
архивируемом change `portal-ui-design-system` и попадёт в основной spec при
sync+archive. MODIFIED этого требования в данном change исходит из того, что архив
design-system уже в master к моменту apply (см. ограничение порядка PR).

## Goals / Non-Goals

**Goals:**
- Разнести IAM-функционал на три страницы (`/iam/roles`, `/iam/permissions`,
  `/iam/users`) с раскрывающимся подменю под «Роли и доступы».
- Полностью сохранить поведение, права и обработку ошибок (403/503/осиротевшие/пикер).
- Переиспользовать примитивы дизайн-системы и общие IAM-хуки/маппинги без дублей.
- Зелёные `tsc --noEmit` и `vitest` на каждом инкрементальном шаге.

**Non-Goals:**
- Любые изменения бэкенда: proto, `services/*`, `openapi/openapi.yaml`, коды ответов,
  RBAC, миграции. Сгенерированные `web/src/api/*` и `web/public/openapi.yaml` не трогаем.
- Новые бизнес-фичи/данные и новые IAM-операции вне контракта.
- Смена фреймворка/роутера/стейт-менеджера, SSR, i18n, редизайн входа/oauth2-proxy.

## Decisions

### Решение 1: Модель вложенного меню — `SidebarItem.children`, без нового типа группы

Расширяем существующий тип `SidebarItem` опциональным полем `children?: SidebarItem[]`
(а не вводим отдельный тип «группа-в-группе»). Причина: минимальная инвазивность —
`SidebarGroup` остаётся плоским контейнером с заголовком секции, а вложенность живёт
на уровне пункта. Пункт с `children` рендерится как раскрывающийся: кнопка-родитель с
`aria-expanded` + список под-пунктов (`NavLink` с `aria-current`). Родитель сам по
себе не навигирует (или навигирует на дефолтный под-пункт) — финально: родитель
переключает раскрытие, переход на под-разделы — по под-пунктам.

_Альтернатива_: новый тип `SidebarSubmenu` рядом с `SidebarItem`. Отклонено: дублирует
рендер-ветку и усложняет `groups`-конфиг в `GlobalLayout`.

### Решение 2: Хранение состояния раскрытия подменю в localStorage

Добавляем в `lib/ui-state.ts` отдельный безопасный ключ `idp-sidebar-submenus` —
список раскрытых ключей подменю (по `label`/`to` родителя), читаемый/пишемый через те
же защищённые от исключений хелперы (как `readSidebarState`). Валидация — это массив
строк; при недоступности/мусоре → дефолт (пусто). Авто-раскрытие при активном дочернем
маршруте вычисляется из текущего пути и ОБЪЕДИНЯЕТСЯ с сохранённым состоянием (открыто
= сохранено-открыто ∪ содержит-активный-маршрут), чтобы не было рассинхронизации:
пользовательское сворачивание сохраняется, но активный маршрут всегда виден.

_Альтернатива_: расширить `SidebarState` enum. Отклонено: подменю — независимое
многозначное состояние, не вписывается в `collapsed|expanded`.

### Решение 3: Карта маршрутов и редирект

```
/iam                  → <Navigate to="/iam/roles" replace />   (редирект, ссылки не ломаются)
/iam/roles            → RolesPage              (каталог ролей, выбор не задан)
/iam/roles/:role      → RolesPage              (deep-link выбранной роли)
/iam/permissions      → PermissionsPage
/iam/users            → UsersPage
```

`/iam/roles/:role` читает `role` из `useParams`; отсутствие параметра = роль не
выбрана. Это заменяет локальный `selectedRole`-стейт, делая выбор шеринг-абельным и
переживающим перезагрузку (ADR-0009 deep-link стиль). При удалении выбранной роли —
`navigate("/iam/roles")`.

_Альтернатива_: query-параметр `?role=`. Отклонено: путь-сегмент чище для «выбранной
сущности» и согласован с остальными маршрутами портала (`/services/:name`).

### Решение 4: Распределение функционала и общие хуки

| Страница | Примитивы | Эндпоинты (через `apiClient`) |
|---|---|---|
| Роли (`/iam/roles[/:role]`) | PageHeader, DataTable/список-выбор, Dialog, ConfirmDialog, StatusBadge | `listRoles`, `getRolePermissions`, `listPermissions` (для attach), `createRole`, `deleteRole`, `attachPermission`, `detachPermission` |
| Права (`/iam/permissions`) | PageHeader, DataTable, Dialog, ConfirmDialog | `listPermissions`, `createPermission`, `deletePermission` |
| Пользователи (`/iam/users`) | PageHeader, DataTable (курсор), пикер-форма, ConfirmDialog | `listSubjects` (infinite), `listRoles` (для select), `searchDirectorySubjects`, `assignRole`, `revokeRole` |

Общий модуль `web/src/pages/iam/hooks.ts` инкапсулирует react-query хуки
(`useRolesQuery`, `usePermissionsQuery`, `useRolePermissionsQuery`,
`useSubjectsInfiniteQuery`, `useDirectorySearch`) и мутации
(`useAssignRole`/`useRevokeRole`/`useCreateRole`/`useDeleteRole`/`useCreatePermission`/
`useDeletePermission`/`useAttachPermission`/`useDetachPermission`), а также экспортирует
`ASSIGN_OVERRIDES`/`CATALOG_OVERRIDES` и `useDebounced`. Благодаря общим `queryKey`
(`["iam","roles"]`, `["iam","permissions"]`, …) кэш react-query разделяется между
страницами: `listRoles`/`listPermissions` не дублируются по сети.

Guard `web/src/pages/iam/IamGuard.tsx` (или хук `useIamForbidden`) принимает ошибку
ключевого запроса страницы и при `403` (read iam:global) рендерит единый fail-closed
блок (как текущий `ShieldX`-Card), иначе — `children`. Используется всеми тремя
страницами.

### Решение 5: Подменю в свёрнутом icon-only режиме (a11y)

В свёрнутом режиме родитель показывается иконкой; под-пункты раскрываются поповером
при `hover`/`focus` (CSS group-hover + focus-within, как тултипы существующего меню,
но с интерактивным списком ссылок). Управление с клавиатуры: фокус на родителе →
под-пункты в DOM достижимы табом (поповер виден через `focus-within`). Никаких тяжёлых
UI-китов — чистый Tailwind + ARIA. `prefers-reduced-motion` уважается (без анимаций
раскрытия при reduce).

### Решение 6: Включаемые улучшения (по обоснованию, без раздувания scope)

- **Хлебные крошки/подзаголовки** в `PageHeader` каждой страницы («Роли и доступы / …»)
  — дёшево, помогает ориентации. Включаем.
- **URL-driven выбранная роль** — уже в Решении 3. Включаем.
- **Плотность таблиц** (`idp-density` из `lib/ui-state`) — подключаем переключатель к
  DataTable на «Права»/«Пользователи» (ключ уже готов, UI не задействован). Включаем
  лёгкий тоггл.
- **404/forbidden маршрут** — оставляем существующий `*`-fallback; отдельную страницу
  forbidden покрывает guard. Не раздуваем.
- **Командная палитра (Cmd/Ctrl-K)** — отдельный последующий change. НЕ включаем.
- **Клиентский поиск/фильтр** по таблицам ролей/прав — опционально, если не удлиняет;
  по умолчанию НЕ обязательно для зелёного CI.

## Risks / Trade-offs

- **Рассинхронизация состояния подменю** (сохранённое vs активный маршрут) → объединение
  множеств (Решение 2): активный маршрут всегда раскрыт, ручное сворачивание не теряет
  активный под-пункт.
- **MODIFIED требования о меню до его попадания в основной spec** → зависит от порядка
  PR (design-system archive в master раньше apply). Митигируется ограничением порядка
  и тем, что текст требования скопирован дословно из delta design-system.
- **Эквивалентность покрытия тестов при разносе** `IamPage.test.tsx` → каждый исходный
  it-блок маппится на тест соответствующей страницы (матрица в tasks); ничего не теряем.
- **Дрейф контракта/кодогена** при правке фронтенда → `web/src/api/*`,
  `openapi/openapi.yaml`, `web/public/openapi.yaml` не трогаем; `gen:check` зелёный.
- **Регрессия deep-link при удалении выбранной роли** → явный `navigate` на дефолт +
  тест на этот сценарий.

## Migration Plan

Инкрементальный рефактор, зелёные `tsc --noEmit` + `vitest` на каждом шаге:

1. `lib/ui-state.ts`: добавить ключ/хелперы состояния подменю (+тесты).
2. `Sidebar.tsx`: поддержка `children`, `aria-expanded`, авто-раскрытие, icon-only
   поповер (+расширить `Sidebar.test.tsx`). Плоские пункты не ломаются.
3. `pages/iam/hooks.ts` + `IamGuard`: вынести запросы/мутации/маппинги из `IamPage`.
4. Создать `pages/iam/RolesPage.tsx`, `PermissionsPage.tsx`, `UsersPage.tsx` поверх
   общих хуков (+тесты каждой, перенос покрытия из `IamPage.test.tsx`).
5. `App.tsx`: новые маршруты + редирект `/iam → /iam/roles`; `GlobalLayout.tsx`: группа
   с подменю.
6. Удалить `IamPage.tsx` и `IamPage.test.tsx` после переноса покрытия.

Откат: рефактор фронтенда без миграций данных — откат = revert PR.

## Open Questions

- Нужен ли клиентский поиск/фильтр по таблицам ролей/прав в этом change или отдельно?
  (По умолчанию — опционально, не блокирует CI.)
- Должен ли клик по родителю «Роли и доступы» в развёрнутом меню только раскрывать
  подменю или ещё и навигировать на `/iam/roles`? (Предлагается: только раскрывать;
  навигация — по под-пунктам.)

## ADR

Опирается на **ADR-0017** (дизайн-система и UI-архитектура портала) и ADR-0009/0014/
0015/0016. Решение о вложенном подменю и карте маршрутов IA — расширение ADR-0017, не
тянет на отдельный значимый архитектурный ADR (новых сервисных границ/контрактов/
транспорта нет). Новый ADR не создаётся.
