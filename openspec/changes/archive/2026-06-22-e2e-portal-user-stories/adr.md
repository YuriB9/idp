# ADR (изменение: e2e-portal-user-stories)

Этот файл — индекс архитектурных решений данного изменения. Канонические ADR
лежат в `docs/adr/NNNN-kebab-title.md` (вне `openspec/`).

## Новые ADR

- **ADR-0018 — Путь аутентификации сквозных E2E и модель детерминизма воркфлоу**
  (`docs/adr/0018-e2e-authentication-path-and-workflow-determinism.md`): E2E
  аутентифицируется реальным OIDC (password-grant Keycloak → Bearer через
  oauth2-proxy на gateway с включённым JWT), а не обходом `AUTH_DISABLED`; асинхронные
  воркфлоу доводятся до терминального статуса ретраи-поллингом `getService` с
  таймаут-бюджетом (не sleep); совместимость с локалкой — через отдельный
  compose-override.

## Реализуемые существующие ADR

- **ADR-0001 — Temporal как оркестратор**: E2E наблюдает терминальные статусы
  воркфлоу create/change-owners/decommission/transfer через периметр.
- **ADR-0004 — guarded-CAS переходов статусов**: ожидаемые исходные статусы
  ассертятся как guarded-CAS (повтор/конфликт → 409).
- **ADR-0005 — Saga-откат при создании**: сценарий отказа Vault через мок → `failed`
  + alert, без молчаливого отката.
- **ADR-0009 — форма REST периметра**: все вызовы идут через operationId периметра
  (createService/getService/listServices/setServiceOwners/decommissionService/
  transferService) без изменения контракта.
- **ADR-0011 — владельцы и синхронизация ролей**: проверка отражения diff владельцев
  в каталоге и ролях IDM.
- **ADR-0012 — semantics decommission и K8s load-check**: предусловие снятой
  нагрузки моделируется чек-флагом `load_drained` (K8s worker вне MVP).
- **ADR-0013 — transfer PONR и двусторонняя авторизация**: наблюдение точки
  невозврата и требования прав `transfer`+`transfer_in`.
