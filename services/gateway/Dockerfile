# Многоэтапная сборка сервиса gateway. Контекст сборки — КОРЕНЬ репозитория
# (нужен модуль ./pkg, на который ссылается replace в go.mod).
#   docker build -f services/gateway/Dockerfile -t idp-gateway .
FROM golang:1.26.4 AS build
WORKDIR /src
# Сначала модули pkg и сервиса — для кэширования слоёв.
COPY pkg/ ./pkg/
COPY services/gateway/ ./services/gateway/
WORKDIR /src/services/gateway
ENV GOWORK=off CGO_ENABLED=0
RUN go mod download && go build -trimpath -o /out/gateway .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gateway /usr/local/bin/gateway
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/gateway"]
