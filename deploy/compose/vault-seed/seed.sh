#!/bin/sh
# Сидирование реального Vault (dev-режим) по REST уже известным dev root-токеном
# (фикстура стенда VAULT_DEV_ROOT_TOKEN_ID). Создаёт идемпотентно ИНФРАСТРУКТУРУ
# движков (ADR-0020):
#   - включает AppRole auth-движок (без него SetupAppRole воркфлоу → 404);
#   - KV v2 на secret/ — дефолт dev-режима, монтировать не нужно;
#   - предзасевает базовый секрет для transfer-теста (MigratePaths source→target).
# Per-service ACL-политики и AppRole-роли создаёт САМ клиент в воркфлоу провизии,
# а НЕ сид. Запускается с хоста из Makefile (цель vault-seed) после готовности Vault.
set -eu

VA="${VAULT_ADDR:-http://localhost:8200}"
TOKEN="${VAULT_TOKEN:?VAULT_TOKEN обязателен}"

# auth выполняет запрос к Vault API с X-Vault-Token.
auth() { curl -fsS -H "X-Vault-Token: $TOKEN" "$@"; }

echo ">> включаем AppRole auth-движок (идемпотентно)"
# Повторное включение уже включённого движка → 400 «path is already in use»: успех.
auth -X POST "$VA/v1/sys/auth/approle" --data '{"type":"approle"}' >/dev/null 2>&1 || true

echo ">> предзасев секрета demo/<seed> для transfer-теста (KV v2, идемпотентно)"
# Базовый секрет по исходному пути, чтобы MigratePaths переносил непустые данные.
auth -X PUT "$VA/v1/secret/data/devinfra-seed" \
  --data '{"data":{"seeded":"true"}}' >/dev/null 2>&1 || true

echo ">> сид Vault завершён (approle включён, KV v2 готов)"
