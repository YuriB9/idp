# Многоэтапная сборка сервиса projects. Контекст сборки — КОРЕНЬ репозитория
# (нужен модуль ./pkg, на который ссылается replace в go.mod).
#   docker build -f services/projects/Dockerfile -t idp-projects .
FROM golang:1.26.4 AS build
WORKDIR /src
# Сначала модули pkg и сервиса — для кэширования слоёв.
COPY pkg/ ./pkg/
COPY services/projects/ ./services/projects/
WORKDIR /src/services/projects
ENV GOWORK=off CGO_ENABLED=0
RUN go mod download && go build -trimpath -o /out/projects .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/projects /usr/local/bin/projects
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/projects"]
