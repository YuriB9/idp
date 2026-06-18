# Одноразовый мигратор каталога проектов для локального стенда. Собирает goose
# из пинованного модуля ./tools (та же версия, что и в CI/Makefile, docs/adr/0007)
# и применяет миграции к postgres-projects. Контекст сборки — КОРЕНЬ репозитория.
#   docker build -f services/projects/migrate.Dockerfile -t idp-migrate-projects .
FROM golang:1.26.4 AS build
WORKDIR /src
# Модуль инструментов изолирован (GOWORK=off), чтобы его зависимости не текли
# в графы сервисов. Собираем только бинарь goose.
COPY tools/ ./tools/
WORKDIR /src/tools
ENV GOWORK=off CGO_ENABLED=0
RUN go build -trimpath -o /out/goose github.com/pressly/goose/v3/cmd/goose

FROM alpine:3.21
# Шелл нужен, чтобы подставить DSN из переменной окружения в команду goose.
COPY --from=build /out/goose /usr/local/bin/goose
COPY services/projects/migrations/ /migrations/
# PROJECTS_DSN задаётся в compose; up идемпотентен (повторный запуск без изменений).
ENTRYPOINT ["/bin/sh", "-c", "goose -dir /migrations postgres \"$PROJECTS_DSN\" up"]
