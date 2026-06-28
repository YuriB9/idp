# Production-образ портала IDP: многоэтапная сборка (vite build → раздача
# статики через nginx). Предназначен для Kubernetes-деплоя (ADR-0024), где /api
# маршрутизирует Istio VirtualService на gateway (а НЕ встроенный прокси образа).
# Для локального стенда (compose) используется web/Dockerfile.dev (Vite + HMR).
#
# Контекст сборки — каталог web/:
#   docker build -f web/Dockerfile -t idp-web web
FROM node:22-alpine AS build
WORKDIR /app

# Сначала манифесты — для кэширования слоя зависимостей.
COPY package.json package-lock.json ./
RUN npm ci

# Затем исходники портала (сгенерированный из OpenAPI клиент уже в src/api).
COPY . ./
# tsc -b + vite build → /app/dist (статические ассеты SPA).
RUN npm run build

# nginx-unprivileged: образ слушает :8080 и работает от non-root (uid 101) без
# правки конфигурации — соответствует securityContext деплоя (ADR-0024/0025).
FROM nginxinc/nginx-unprivileged:1.27-alpine
# SPA-конфиг с fallback на index.html; без проксирования /api (см. nginx.conf).
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 8080
