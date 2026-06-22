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
import { RolesPage } from "@/pages/iam/RolesPage";
import { PermissionsPage } from "@/pages/iam/PermissionsPage";
import { UsersPage } from "@/pages/iam/UsersPage";

export function App() {
  return (
    <Routes>
      <Route element={<GlobalLayout />}>
        <Route path="/" element={<Navigate to={`/projects/${DEFAULT_PROJECT}/services`} replace />} />
        <Route path="/projects/:project/services" element={<ServiceListPage />} />
        <Route path="/projects/:project/services/new" element={<CreateServicePage />} />
        <Route path="/projects/:project/services/:name" element={<ServiceProgressPage />} />
        {/* Горизонтальный (не project-scoped) раздел IAM-админки, разделённый на три
            страницы (ADR-0017). Старый /iam редиректит на дефолтный под-раздел —
            существующие ссылки не ломаются. Выбранная роль — в сегменте пути. */}
        <Route path="/iam" element={<Navigate to="/iam/roles" replace />} />
        <Route path="/iam/roles" element={<RolesPage />} />
        <Route path="/iam/roles/:role" element={<RolesPage />} />
        <Route path="/iam/permissions" element={<PermissionsPage />} />
        <Route path="/iam/users" element={<UsersPage />} />
        <Route path="*" element={<p className="text-muted-foreground">Страница не найдена</p>} />
      </Route>
    </Routes>
  );
}
