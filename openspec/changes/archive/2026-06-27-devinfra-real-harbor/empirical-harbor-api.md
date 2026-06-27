# Эмпирическая проверка Harbor v2.14.4 API (задача 1.3/1.4)

Стенд: официальный online-installer Harbor **v2.14.4**, HTTP на :8085, admin/Harbor12345.
Пробивался каждый метод, используемый клиентом. Ниже — ФАКТИЧЕСКИЕ коды/тела/расхождения
с мок-ориентированным `harborHTTP` (использовать при реализации блока 4).

## 1. Аутентификация
- HTTP **Basic** admin (`Authorization: Basic base64(admin:Harbor12345)`).
- Чтения (`GET /projects`, `/health`, `/ping`) — анонимно 200. **Запись требует auth**:
  no-auth `POST /projects` (валидное тело) → **401 UNAUTHORIZED**. Клиент шлёт Basic на КАЖДЫЙ запрос.

## 2. Health-gate
- `GET /api/v2.0/health` → 200 `{"status":"healthy","components":[...]}` (без auth).
- `GET /api/v2.0/ping` → 200 `Pong` (без auth). Берём `/api/v2.0/health`.

## 3. CreateProject
- `POST /api/v2.0/projects {"project_name":"<p>-<n>"}` → **201**.
- Повтор (тот же проект) → **409** CONFLICT `{"errors":[{"code":"CONFLICT",...}]}`. ✓ совпало с моком.

## 4. Проект по имени/id
- `GET /api/v2.0/projects/<name>` принимает ИМЯ прямо в path → 200 (заголовок
  `X-Is-Resource-Name` не нужен). Тело несёт `project_id`, `metadata`.
- `GET /api/v2.0/projects?name=<name>` → массив (резолвинг id при необходимости).
- `PUT /api/v2.0/projects/<name>` принимает имя в path → 200.

## 5. Robots — ГЛАВНЫЕ РАСХОЖДЕНИЯ
- Минимальное тело `{name,level}` → **400** «duration must be either -1(Never) or a positive integer».
- Полное тело v2.0 → **201**, возвращает `{id, name, secret}`:
  ```json
  {"name":"<n>","duration":-1,"level":"system",
   "permissions":[{"kind":"project","namespace":"<projName>",
     "access":[{"resource":"repository","action":"push"},{"resource":"repository","action":"pull"}]}]}
  ```
- **Формат имени**: запрошенное `name` префиксуется Harbor. Возвращается `name`=`robot$<n>`
  (system) либо `robot$<project>+<n>` (project). Инъектировать ФАКТИЧЕСКИ возвращённые `name`+`secret`.
- **Project-level robots НЕ перечисляются**: `GET /robots` отдаёт `X-Total-Count: 0` даже при
  существующем project-роботе (доступном по `GET /robots/<id>`); пути `/projects/<id>/robots` нет (404).
  → id project-робота НЕЛЬЗЯ резолвить по имени постфактум.
- **System-level robots ПЕРЕЧИСЛЯЮТСЯ и резолвятся по имени**:
  `GET /api/v2.0/robots?q=name=<запрошенное-имя>` → `[{id,name,...}]` (запрос по НЕпрефиксованному
  имени; по полному `robot$...` → []). 
  → **РЕШЕНИЕ**: создавать робота на уровне **`level:"system"`**, scope на проект через
  `permissions[]`, имя `<project>-<name>`. Тогда он резолвится для отзыва.
- Дубликат имени робота → **409** CONFLICT «robot account already exists».
- `DELETE /api/v2.0/robots/<id>` требует ЧИСЛОВОЙ id → **200**; отсутствующий → **404**;
  по ИМЕНИ → **422** UNPROCESSABLE_ENTITY «robot_id in path must be of type int64» (текущий код шлёт имя — баг).

## 6. DeleteProject
- `DELETE /api/v2.0/projects/<name|id>` → **200**; отсутствующий → **404**.
- Удаляется даже с привязанным роботом (блокируют только НЕпустые репозитории; у нас 0 репо).
- **Удаление проекта ОСТАВЛЯЕТ system-робота** (робот выживает) → `DeleteProject` (компенсация
  создания) должен ДОПОЛНИТЕЛЬНО резолвить и удалять робота.

## 7. read-only (decommission, ADR-0012)
- В Harbor НЕТ project-level `read_only` (поле в metadata игнорируется).
- `SetReadOnly` = резолв id робота по имени → `DELETE /robots/<id>`. Наблюдаемо:
  `GET /robots?q=name=<n>` → `[]`. Проект СОХРАНЯЕТСЯ. Необратимо (новый secret при воссоздании).
- `SetWritable` (компенсация) — воссоздать system-робота (новый secret).

## 8. UpdateMetadata (transfer, ADR-0013)
- `PUT /api/v2.0/projects/<name> {"metadata":{...}}` → **200**.
- **Произвольные ключи (`owner_project`) молча игнорируются** (не сохраняются в GET).
- Сохраняются и наблюдаемы только допустимые поля `ProjectMetadata` (`public`, `auto_scan`, ...).
  → маркер переноса = допустимое наблюдаемое поле (берём `auto_scan="true"`); Harbor не хранит имя
  целевого проекта (rename/transfer проекта в Harbor нет — перенос = метаданный маркер, ADR-0021/D5).

## Сводка кодов идемпотентности (реальный Harbor)
| операция | успех | идемпотентный | примечание |
|---|---|---|---|
| POST /projects | 201 | 409 (повтор) | |
| POST /robots | 201 | 409 (повтор имени) | тело v2.0 обязательно |
| DELETE /robots/{id} | 200 | 404 (нет) | только числовой id |
| DELETE /projects/{name} | 200 | 404 (нет) | пустой проект |
| PUT /projects/{name} | 200 | 200 (upsert) | произвольные metadata-ключи игнор |
