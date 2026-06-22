## 1. Подготовка ветки и состояния UI

- [x] 1.1 Создать ветку `change/portal-iam-split-pages` от `master` (прямые коммиты в master запрещены)
- [x] 1.2 В `web/src/lib/ui-state.ts` добавить безопасное хранение состояния раскрытых подменю: ключ `idp-sidebar-submenus`, `readSidebarSubmenus()/writeSidebarSubmenus()` (массив строк, валидация, деградация при недоступности localStorage); комментарии на русском
- [x] 1.3 Добавить unit-тесты на новые хелперы `ui-state` (чтение/запись/деградация). `tsc --noEmit` и `vitest` зелёные

## 2. Вложенное подменю в Sidebar

- [x] 2.1 Расширить тип `SidebarItem` полем `children?: SidebarItem[]` в `web/src/components/Sidebar.tsx`
- [x] 2.2 Рендер пункта с `children`: кнопка-родитель с `aria-expanded`, список под-пунктов (`NavLink` с `aria-current`); плоские пункты «Сервисы» и внешняя ссылка Swagger не ломаются
- [x] 2.3 Авто-раскрытие подменю при активном дочернем маршруте (объединение сохранённого состояния с активным маршрутом)
- [x] 2.4 Сохранение состояния раскрытия в localStorage через хелперы из 1.2
- [x] 2.5 Поведение подменю в свёрнутом icon-only режиме (поповер/раскрытие при hover/focus, клавиатура, `prefers-reduced-motion`)
- [x] 2.6 Расширить `web/src/components/Sidebar.test.tsx`: раскрыть/свернуть, сохранение/восстановление, авто-раскрытие на активном маршруте, активный под-пункт (`aria-current`), `aria-expanded`, клавиатурная навигация, icon-only. `tsc`/`vitest` зелёные

## 3. Общие IAM-хуки, маппинги и guard

- [x] 3.1 Создать `web/src/pages/iam/hooks.ts`: react-query хуки чтения (`useRolesQuery`, `usePermissionsQuery`, `useRolePermissionsQuery`, `useSubjectsInfiniteQuery`, `useDirectorySearch`) на тех же `queryKey`, что и сейчас (общий кэш)
- [x] 3.2 В тот же модуль — мутации (`useAssignRole`, `useRevokeRole`, `useCreateRole`, `useDeleteRole`, `useCreatePermission`, `useDeletePermission`, `useAttachPermission`, `useDetachPermission`) с инвалидацией соответствующих запросов и тостами
- [x] 3.3 Вынести `ASSIGN_OVERRIDES`, `CATALOG_OVERRIDES`, `useDebounced` в общий модуль (без дублей)
- [x] 3.4 Создать guard `web/src/pages/iam/IamGuard.tsx` (или хук `useIamForbidden`): при `403` на read `iam:global` — единый fail-closed блок, иначе `children`
- [x] 3.5 Тесты на guard (403 → отказ; иначе контент). `tsc`/`vitest` зелёные

## 4. Страница «Роли» (/iam/roles[/:role])

- [x] 4.1 Создать `web/src/pages/iam/RolesPage.tsx`: `PageHeader` + каталог ролей; выбранная роль из `useParams` (`/iam/roles/:role`), а не локального стейта
- [x] 4.2 Права выбранной роли: список + attach из каталога прав + detach через `ConfirmDialog`; системная роль read-only (бейдж, действия скрыты)
- [x] 4.3 Создание роли в `Dialog` (react-hook-form + zod), удаление пользовательской роли через `ConfirmDialog`; при удалении выбранной роли — `navigate("/iam/roles")`
- [x] 4.4 Тосты с `CATALOG_OVERRIDES`; `zod .parse` сохраняется; guard 403 fail-closed; 403 manage → структурные действия скрыты
- [x] 4.5 `web/src/pages/iam/RolesPage.test.tsx`: deep-link выбранной роли, создание/удаление роли, attach/detach, read-only системной, 403 admin/manage, базовые a11y (роли/aria/фокус)

## 5. Страница «Права» (/iam/permissions)

- [x] 5.1 Создать `web/src/pages/iam/PermissionsPage.tsx`: `PageHeader` + каталог прав на `DataTable` (сортировка, loading/empty/error); подключить плотность `idp-density`
- [x] 5.2 Создание права в `Dialog` (react-hook-form + zod: `action`/`resource`), удаление пользовательского права через `ConfirmDialog`; системное право read-only (бейдж, удаление скрыто)
- [x] 5.3 Тосты с `CATALOG_OVERRIDES`; `zod .parse`; guard 403 fail-closed; 403 manage → создание/удаление скрыты
- [x] 5.4 `web/src/pages/iam/PermissionsPage.test.tsx`: создание/удаление права, read-only системного, 403 admin/manage, состояния таблицы, базовые a11y

## 6. Страница «Пользователи» (/iam/users)

- [x] 6.1 Создать `web/src/pages/iam/UsersPage.tsx`: `PageHeader` + список субъектов с ролями на `DataTable` с курсорной пагинацией (`next_page_token`)
- [x] 6.2 Форма назначения роли с пикером из справочника Keycloak: debounce, `username`/`email`, канонический `subject` (`sub`), обработка 403/503
- [x] 6.3 Снятие роли через `ConfirmDialog`; пометка «осиротевших» (`found=false`) как raw `sub` + «нет в каталоге»
- [x] 6.4 Тосты с `ASSIGN_OVERRIDES`; `zod .parse`; guard 403 fail-closed; 403 `iam:directory` → пикер скрыт; 503 → индикация, поиск остаётся
- [x] 6.5 `web/src/pages/iam/UsersPage.test.tsx`: happy-path списка, обогащённый/осиротевший субъект, назначение через пикер с debounce, снятие роли, валидация формы, 403 admin, 403 directory, 503 directory, курсорная пагинация

## 7. Маршруты, навигация, очистка

- [x] 7.1 `web/src/App.tsx`: добавить маршруты `/iam/roles`, `/iam/roles/:role`, `/iam/permissions`, `/iam/users`; `/iam` → `<Navigate to="/iam/roles" replace />`
- [x] 7.2 `web/src/layouts/GlobalLayout.tsx`: «Роли и доступы» → пункт с `children` (Роли/Права/Пользователи); плоский «Сервисы» и Swagger сохранены
- [x] 7.3 Тест на роутинг/редирект `/iam → /iam/roles` (App или маршрут-уровень)
- [x] 7.4 Удалить `web/src/pages/IamPage.tsx` и `web/src/pages/IamPage.test.tsx` после переноса эквивалентного покрытия; проверить отсутствие висячих импортов

## 8. Проверки CI

- [x] 8.1 `npm run gen:check` зелёный (контракт и кодоген БЕЗ изменений: `openapi/openapi.yaml`, `web/src/api/*`, `web/public/openapi.yaml` не тронуты)
- [x] 8.2 `tsc --noEmit` и `vitest` зелёные; web-test проходит (19 файлов, 114 тестов)
- [x] 8.3 Тема (ThemeProvider) не тронута; новые цвета не вводились — только семантические токены дизайн-системы (AA по ADR-0017), статусы иконкой+текстом; маршруты сервисов и поведение прав (403/503/осиротевшие/пикер) сохранены и покрыты тестами
- [ ] 8.4 Открыть PR с зелёным CI; после merge — отдельный PR sync+archive через `/opsx:archive` (требует push — выполняется по запросу пользователя)
