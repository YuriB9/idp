#!/bin/sh
# Сидирование реального Harbor для интеграционного стенда (ADR-0021). По образцу
# vault-seed: дождаться готовности API и убедиться, что admin-доступ (фикстура стенда)
# работает по HTTP Basic. Per-service ПРОЕКТЫ и ROBOT-аккаунты создаёт САМ клиент в
# воркфлоу провизии (а не сид) — Harbor в дефолте уже имеет admin и пустой каталог
# проектов, дополнительных сущностей сид не заводит. Запускается с хоста из Makefile
# (цель harbor-seed) после поднятия стека.
set -eu

HA="${HARBOR_ADDR:-http://localhost:8085}"
USER="${HARBOR_USERNAME:-admin}"
PASS="${HARBOR_PASSWORD:?HARBOR_PASSWORD обязателен}"
# Бюджет ожидания в СЕКУНДАХ (число; отдельно от duration-строки HARBOR_STATUS_TIMEOUT
# набора go test).
TIMEOUT="${HARBOR_SEED_TIMEOUT:-600}"

echo ">> ждём готовности Harbor ($HA); связка контейнеров стартует не быстро ..."
i=0
while [ "$i" -lt "$TIMEOUT" ]; do
  # /api/v2.0/health отдаёт 200 со {status:healthy}, когда все компоненты подняты.
  if curl -fsS "$HA/api/v2.0/health" >/dev/null 2>&1; then
    echo ">> Harbor готов"
    break
  fi
  i=$((i + 3))
  sleep 3
done
if [ "$i" -ge "$TIMEOUT" ]; then
  echo "!! Harbor не готов за ${TIMEOUT}s" >&2
  exit 1
fi

echo ">> проверяем admin-доступ по HTTP Basic (фикстура стенда)"
# Запись требует auth: успешный GET с admin-кредами подтверждает рабочую фикстуру.
code=$(curl -s -o /dev/null -w '%{http_code}' -u "$USER:$PASS" "$HA/api/v2.0/projects?page_size=1")
if [ "$code" != "200" ]; then
  echo "!! admin-доступ к Harbor не подтверждён (код $code)" >&2
  exit 1
fi

echo ">> сид Harbor завершён (admin-доступ подтверждён, каталог проектов создаёт воркфлоу)"
