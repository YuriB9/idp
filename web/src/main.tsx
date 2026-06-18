// Точка входа SPA: монтирует приложение, провайдер TanStack Query и роутер.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router-dom";

import { App } from "./App";
import "./index.css";

// По умолчанию — тёмная тема (единый визуальный язык платформы).
document.documentElement.classList.add("dark");

// queryClient — единый клиент кэша запросов портала.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Не ретраим бесконечно: ошибки контракта/4xx должны быть видны быстро.
      retry: 1,
    },
  },
});

const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("Не найден корневой элемент #root");
}

createRoot(rootElement).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
