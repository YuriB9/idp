#!/bin/sh
# Сидирование реального GitLab по REST уже выпущенным фиксированным root-PAT
# (его минтит seed.rb через gitlab-rails runner — password-grant в GitLab отключён).
# Создаёт идемпотентно:
#   - группы под проекты каталога demo/demo2 (namespace репозиториев);
#   - тест-пользователей под владельцев alice/bob (для SyncMembers).
# CI-runner и пайплайны не нужны (репозитории создаются пустыми). Запускается с хоста
# из Makefile (цель gitlab-up) после минта токена.
set -eu

GL="${GITLAB_URL:-http://localhost:8929}"
TOKEN="${GITLAB_TOKEN:?GITLAB_TOKEN обязателен}"
USER_PASSWORD="${SEED_USER_PASSWORD:-Idp-9fK2-xQ7w-Vb3T}"

# auth выполняет запрос к GitLab API с PRIVATE-TOKEN.
auth() { curl -fsS -H "PRIVATE-TOKEN: $TOKEN" "$@"; }

echo ">> создаём группы demo/demo2 (идемпотентно)"
for g in demo demo2; do
  # Повторное создание существующей группы → 400 «has already been taken»: успех.
  auth -X POST "$GL/api/v4/groups" --data "name=$g&path=$g&visibility=private" >/dev/null 2>&1 || true
done

echo ">> создаём пользователей alice/bob (идемпотентно)"
for u in alice bob; do
  auth -X POST "$GL/api/v4/users" \
    --data "email=$u@example.com&username=$u&name=$u&password=$USER_PASSWORD&skip_confirmation=true" \
    >/dev/null 2>&1 || true
done

echo ">> сид REST завершён (группы demo/demo2, пользователи alice/bob)"
