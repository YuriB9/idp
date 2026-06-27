#!/bin/sh
# Поднятие РЕАЛЬНОГО Harbor для интеграционного стенда (ADR-0021). Harbor — НЕ один образ,
# а связка контейнеров (core+db+redis+registry+registryctl+jobservice+portal+nginx),
# официально разворачиваемая из installer-bundle через prepare. Скрипт инкапсулирует
# проверенный поток (см. openspec empirical-harbor-api):
#   1) скачать пиннутый online-installer Harbor (детерминированная версия);
#   2) сгенерировать harbor.yml из закоммиченного конфига (подставить data_volume);
#   3) prepare → docker-compose.yml + конфиги;
#   4) переключить docker-логи компонентов на json-file (убрать хрупкий syslog-контейнер
#      harbor-log, который падает на новых rsyslog);
#   5) сделать конфиги world-readable (контейнеры Harbor бегут под разными uid 10000/999;
#      world-read снимает конфликт владельцев без chown под каждый uid);
#   6) поднять стек ОТДЕЛЬНЫМ compose-проектом (harbor-real) на опубликованном порту.
# Запускается из Makefile (цель harbor-up). Требует сети (installer + образы goharbor/*).
set -eu

HARBOR_VERSION="${HARBOR_VERSION:-v2.14.4}"
HTTP_PORT="${HARBOR_PORT:-8085}"
PROJECT="${HARBOR_COMPOSE_PROJECT:-harbor-real}"

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
WORK="$SCRIPT_DIR/.work"
INSTALLER="$WORK/harbor"

echo ">> Harbor $HARBOR_VERSION: подготовка рабочего каталога $WORK"
mkdir -p "$WORK"
# Конфиги от prepare прошлого прогона принадлежат root — чистим распакованный installer
# через root-контейнер (иначе rm под текущим uid падает). Тарбол-кэш и данные не трогаем.
if [ -d "$INSTALLER" ]; then
  docker run --rm -v "$WORK:/w" alpine rm -rf /w/harbor
fi
mkdir -p "$WORK/data"

# 1) Скачать и распаковать пиннутый online-installer (идемпотентно — кэшируем тарбол).
TARBALL="$WORK/harbor-online-$HARBOR_VERSION.tgz"
if [ ! -f "$TARBALL" ]; then
  echo ">> скачиваем online-installer Harbor $HARBOR_VERSION"
  curl -fsSL -o "$TARBALL" \
    "https://github.com/goharbor/harbor/releases/download/$HARBOR_VERSION/harbor-online-installer-$HARBOR_VERSION.tgz"
fi
tar xzf "$TARBALL" -C "$WORK"

# 2) Сгенерировать harbor.yml из закоммиченного конфига: подставить абсолютный data_volume
#    и HTTP-порт.
sed -e "s#__DATA_VOLUME__#$WORK/data#" -e "s/^  port: .*/  port: $HTTP_PORT/" \
  "$SCRIPT_DIR/harbor.yml" > "$INSTALLER/harbor.yml"

# 3) prepare генерирует docker-compose.yml и конфиги (под root внутри контейнера).
echo ">> prepare (генерация compose + конфигов)"
( cd "$INSTALLER" && ./prepare >/dev/null )

# docker-compose.yml генерируется под root — возвращаем владение текущему пользователю,
# чтобы патч-шаг и `docker compose` (клиент читает env_file) работали. Файл в контейнеры
# не монтируется, поэтому владелец на рантайм не влияет.
docker run --rm -v "$INSTALLER:/w" alpine chown "$(id -u):$(id -g)" /w/docker-compose.yml

# 4) Переключить docker-логи всех сервисов на json-file и убрать сервис log (syslog).
python3 - "$INSTALLER/docker-compose.yml" <<'PY'
import re, sys
f = sys.argv[1]
s = open(f).read()
s = re.sub(r'    logging:\n      driver: "syslog"\n      options:\n        syslog-address: "tcp://localhost:1514"\n        tag: "[^"]*"\n',
           '    logging:\n      driver: "json-file"\n      options:\n        max-size: "10m"\n        max-file: "3"\n', s)
s = s.replace('    depends_on:\n      - log\n', '    depends_on:\n')
s = s.replace('      - log\n', '')
s = re.sub(r'  log:\n.*?\n(  registry:\n)', r'\1', s, flags=re.S)
s = re.sub(r'    depends_on:\n(    [a-z])', r'\1', s)
open(f, 'w').write(s)
PY

# 5) Конфиги/compose сгенерированы под root — делаем world-readable (компоненты Harbor
#    под uid 10000/999 читают их как world; каталог data НЕ трогаем — prepare выставил
#    владельцев по компонентам, postgres требует строгие права).
docker run --rm -v "$INSTALLER:/w" alpine sh -c 'chmod -R a+rX /w/common /w/docker-compose.yml'

# 6) Поднять стек отдельным проектом.
echo ">> поднимаем стек Harbor (проект $PROJECT)"
docker compose -p "$PROJECT" -f "$INSTALLER/docker-compose.yml" up -d

echo ">> Harbor поднимается на http://localhost:$HTTP_PORT (готовность ждёт harbor-seed)"
