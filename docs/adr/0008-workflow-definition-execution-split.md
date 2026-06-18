# ADR-0008: Разделение определения и исполнения workflow «Создание сервиса»

**Status:** Accepted
**Date:** 2026-06-18
**Change:** create-service-workflow

## Context

Workflow «Создание сервиса» запускается из API-процесса `services/projects` (gRPC `CreateService`), но исполняется отдельным процессом `services/devinfra-worker` на task-queue `devinfra` (ADR-0001, раздельные процессы). Temporal требует, чтобы запускающая сторона знала имя workflow и сигнатуру input/output, а исполняющая сторона регистрировала реализацию workflow и activities. Нужно решить, где физически лежит код, чтобы:
- не дублировать строковые имена workflow/activities и тип input в двух модулях (рассинхрон не ловится компилятором);
- не тянуть интеграционные зависимости (клиенты GitLab/Vault/Harbor) в API-процесс;
- довести финальный статус (`CREATING→ACTIVE`/`FAILED`) надёжно, независимо от живости API-процесса.

## Considered Options

1. **Весь код workflow и activities в worker'е, API дублирует имена строками** — просто, но имена/типы рассинхронизируются молча.
2. **Общий пакет с определением workflow (имя, типы input/output, конструктор детерминированного WorkflowID, имена activities), импортируемый обоими модулями; реализация activities — в worker'е; финальный guarded-CAS-переход — activity на стороне worker'а.**
3. **Запуск и исполнение в одном процессе** — нарушает ADR-0001 (раздельность API и worker).

## Decision

Вариант 2. Определение workflow и общие константы выносятся в общий пакет, импортируемый и `services/projects` (для `ExecuteWorkflow`), и `services/devinfra-worker` (для регистрации workflow + реализаций activities). Реализация activities с клиентами интеграций живёт только в worker'е. Финальный переход статуса `CREATING→ACTIVE`/`CREATING→FAILED` выполняется activity на стороне worker'а (имеет доступ к Postgres-пулу), чтобы durable-исполнение само довело запись до конечного статуса вне зависимости от живости API-процесса. `WorkflowID` детерминирован (`create-service:<project>:<name>`) — идемпотентность повторного запуска.

## Consequences

**Положительные:** имена/типы workflow и activities — единый источник правды, рассинхрон ловится компилятором; API не зависит от интеграционных клиентов; статус доводится до конца durable-исполнением; повторный запуск идемпотентен.

**Отрицательные:** появляется общий пакет-контракт между двумя модулями go.work (нужно держать его узким); финальная transition-activity связывает worker с Postgres-пулом каталога (worker получает доступ к БД проектов).

## Related

ADR-0001 (Temporal, раздельность API/worker), ADR-0004 (guarded-CAS статусов), ADR-0005 (полный Saga-откат при недоступности Vault).
