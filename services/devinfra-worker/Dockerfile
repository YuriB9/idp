# Многоэтапная сборка сервиса devinfra-worker. Контекст сборки — КОРЕНЬ репозитория
# (нужен модуль ./pkg, на который ссылается replace в go.mod).
#   docker build -f services/devinfra-worker/Dockerfile -t idp-devinfra-worker .
FROM golang:1.26.4 AS build
WORKDIR /src
# Сначала модули pkg и сервиса — для кэширования слоёв.
COPY pkg/ ./pkg/
COPY services/devinfra-worker/ ./services/devinfra-worker/
WORKDIR /src/services/devinfra-worker
ENV GOWORK=off CGO_ENABLED=0
RUN go mod download && go build -trimpath -o /out/devinfra-worker .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/devinfra-worker /usr/local/bin/devinfra-worker
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/devinfra-worker"]
