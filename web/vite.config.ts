/// <reference types="vitest/config" />
// Конфигурация Vite для портала IDP. Dev-сервер на :3000 проксирует запросы
// периметра (/api) на gateway (:8081), чтобы в браузере не было CORS (запросы
// идут same-origin на dev-сервер). См. design.md и ADR-0009.
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    // Алиас @/* → src/* (как в остальных фронтендах платформы).
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  server: {
    port: 3000,
    proxy: {
      // Прокси периметра: /api/* → gateway. Адрес переопределяется через
      // переменную окружения GATEWAY_URL (по умолчанию локальный gateway).
      "/api": {
        target: process.env.GATEWAY_URL ?? "http://localhost:8081",
        changeOrigin: true,
      },
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
  },
});
