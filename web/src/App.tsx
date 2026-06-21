// Корневой компонент: маршруты сценария «Создание сервиса» внутри глобального
// каркаса. Пути согласованы с периметром (ADR-0009):
//   /projects/:project/services            — список
//   /projects/:project/services/new        — форма создания
//   /projects/:project/services/:name      — экран прогресса/статуса
import { Navigate, Route, Routes } from "react-router-dom";

import { DEFAULT_PROJECT, GlobalLayout } from "@/layouts/GlobalLayout";
import { ServiceListPage } from "@/pages/ServiceListPage";
import { CreateServicePage } from "@/pages/CreateServicePage";
import { ServiceProgressPage } from "@/pages/ServiceProgressPage";
import { IamPage } from "@/pages/IamPage";

export function App() {
  return (
    <Routes>
      <Route element={<GlobalLayout />}>
        <Route path="/" element={<Navigate to={`/projects/${DEFAULT_PROJECT}/services`} replace />} />
        <Route path="/projects/:project/services" element={<ServiceListPage />} />
        <Route path="/projects/:project/services/new" element={<CreateServicePage />} />
        <Route path="/projects/:project/services/:name" element={<ServiceProgressPage />} />
        {/* Горизонтальный (не project-scoped) раздел IAM-админки */}
        <Route path="/iam" element={<IamPage />} />
        <Route path="*" element={<p className="text-muted-foreground">Страница не найдена</p>} />
      </Route>
    </Routes>
  );
}
