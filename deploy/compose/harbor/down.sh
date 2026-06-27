#!/bin/sh
# Гашение стенда РЕАЛЬНОГО Harbor (ADR-0021): остановить стек-проект и очистить данные.
# Каталог .work/data принадлежит контейнерным uid (10000/999) — чистим через root-контейнер.
set -eu

PROJECT="${HARBOR_COMPOSE_PROJECT:-harbor-real}"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
WORK="$SCRIPT_DIR/.work"
INSTALLER="$WORK/harbor"

if [ -f "$INSTALLER/docker-compose.yml" ]; then
  echo ">> гасим стек Harbor (проект $PROJECT)"
  docker compose -p "$PROJECT" -f "$INSTALLER/docker-compose.yml" down -v || true
fi

# Очистить данные (root-owned subdirs) через временный контейнер.
if [ -d "$WORK/data" ]; then
  echo ">> очищаем данные стенда"
  docker run --rm -v "$WORK:/w" alpine sh -c 'rm -rf /w/data /w/harbor' || true
fi

echo ">> стенд Harbor погашен"
