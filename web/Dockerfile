# Образ портала IDP для локального стенда: Vite dev-сервер с прокси /api на
# gateway. Для MVP-демо достаточно dev-режима (HMR + прокси периметра);
# production-сборка (vite build + статика) — отдельной задачей при выходе в прод.
FROM node:22-alpine
WORKDIR /app

# Сначала манифесты — для кэширования слоя зависимостей.
COPY package.json package-lock.json ./
RUN npm ci

# Затем исходники портала (сгенерированный из OpenAPI клиент уже в src/api).
COPY . ./

EXPOSE 3000
# --host 0.0.0.0 — чтобы dev-сервер был доступен снаружи контейнера.
CMD ["npm", "run", "dev", "--", "--host", "0.0.0.0"]
