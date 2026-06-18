# ADR-0004: Переходы статусов сервиса через guarded-CAS

**Status:** Accepted
**Date:** 2026-06-18

## Context

Статус сервиса (`creating → active → decommissioned`, плюс `failed`) меняют конкурентно: Temporal workflow по ходу провизии и API по запросу пользователя. Урок прошлого проекта: check-then-act на переходах состояний даёт гонки и неконсистентность.

## Decision

Все переходы статусов — **guarded compare-and-set**:
`UPDATE services SET status=$new WHERE id=$id AND status=$expected`, затем проверка `RowsAffected==0 → ErrConflict (409)`. Никакого «прочитали статус → проверили в коде → записали». Многошаговые записи — в транзакции (`withTx`); публикация статуса/события — после commit.

## Consequences

**Положительные:** атомарность переходов; конкурентные попытки получают явный 409, а не молча перетирают друг друга; согласуется с durable-ретраями Temporal (повторный запуск activity не ломает статус).

**Отрицательные:** требует дисциплины — каждый переход через guarded-CAS, никаких «быстрых» прямых UPDATE без условия по `$expected`.
