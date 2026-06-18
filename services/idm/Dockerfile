# Многоэтапная сборка сервиса idm. Контекст сборки — КОРЕНЬ репозитория
# (нужен модуль ./pkg, на который ссылается replace в go.mod).
#   docker build -f services/idm/Dockerfile -t idp-idm .
FROM golang:1.26.4 AS build
WORKDIR /src
# Сначала модули pkg и сервиса — для кэширования слоёв.
COPY pkg/ ./pkg/
COPY services/idm/ ./services/idm/
WORKDIR /src/services/idm
ENV GOWORK=off CGO_ENABLED=0
RUN go mod download && go build -trimpath -o /out/idm .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/idm /usr/local/bin/idm
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/idm"]
