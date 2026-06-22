// Точка входа SPA: монтирует приложение, провайдер TanStack Query и роутер.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router-dom";

import { App } from "./App";
import { ThemeProvider, applyTheme, readInitialTheme } from "./lib/theme";
import { ToastProvider } from "./components/ui/toast";
import "./index.css";

// Применяем сохранённую (или дефолтную тёмную) тему синхронно до первого рендера,
// чтобы не было мигания темы (FOUC).
applyTheme(readInitialTheme());

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
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <ToastProvider>
          <BrowserRouter>
            <App />
          </BrowserRouter>
        </ToastProvider>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
