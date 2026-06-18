// Файл handlers.go — доменные REST-ручки периметра (ADR-0002, ADR-0009) поверх
// gRPC-клиента projectsv1: создание сервиса, чтение статуса и листинг сервисов
// проекта. Шлюз НЕ содержит доменной логики — он лишь маппит REST↔gRPC, маппит
// gRPC-коды в HTTP и НИКОГДА не раскрывает внутренние ошибки клиенту (ADR-0003).
package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	projectsv1 "github.com/YuriB9/idp/pkg/api/projects/v1"
	"github.com/YuriB9/idp/pkg/logger"
)

// servicesAPI — обработчики ресурса сервисов периметра, держащие gRPC-клиент
// каталога проектов. Создаётся один раз в main и навешивается на роутер.
type servicesAPI struct {
	client projectsv1.ProjectsServiceClient
	log    *slog.Logger
}

// errorBody — стабильное тело ошибки периметра (схема Error в OpenAPI).
// Внутренние детали (текст gRPC-ошибки) сюда не попадают — только в лог.
type errorBody struct {
	Error string `json:"error"`
}

// serviceSummary — представление сервиса для клиента (схема ServiceSummary).
type serviceSummary struct {
	Project string `json:"project"`
	Name    string `json:"name"`
	Status  string `json:"status"`
}

// serviceList — страница сервисов с курсором (схема ServiceList).
type serviceList struct {
	Services      []serviceSummary `json:"services"`
	NextPageToken string           `json:"next_page_token"`
}

// createServiceBody — тело запроса создания (схема CreateServiceRequest).
type createServiceBody struct {
	Name string `json:"name"`
}

// createServiceResult — тело ответа создания (схема CreateServiceResult).
type createServiceResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// register навешивает доменные маршруты на переданный роутер (под middlewares
// периметра, см. main.go). RBAC IDM CheckAccess пока заглушка — точка вызова
// помечена ниже, реальная проверка добавляется отдельным change (idm-rbac-min).
func (a *servicesAPI) register(r chi.Router) {
	r.Post("/projects/{project}/services", a.create)
	r.Get("/projects/{project}/services", a.list)
	r.Get("/projects/{project}/services/{name}", a.get)
}

// create — POST /projects/{project}/services: запускает создание сервиса.
func (a *servicesAPI) create(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	var body createServiceBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "некорректное тело запроса")
		return
	}
	if body.Name == "" {
		a.writeError(w, r, http.StatusBadRequest, "имя сервиса обязательно")
		return
	}

	// TODO(idm-rbac-min): здесь будет вызов IDM CheckAccess (create service);
	// пока граница-заглушка, как в gRPC CreateService.
	resp, err := a.client.CreateService(r.Context(), &projectsv1.CreateServiceRequest{
		Project: project,
		Name:    body.Name,
	})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}

	a.writeJSON(w, http.StatusCreated, createServiceResult{
		ID:     resp.GetId(),
		Status: statusString(resp.GetStatus()),
	})
}

// get — GET /projects/{project}/services/{name}: чтение статуса одного сервиса.
func (a *servicesAPI) get(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	name := chi.URLParam(r, "name")

	resp, err := a.client.GetService(r.Context(), &projectsv1.GetServiceRequest{
		Project: project,
		Name:    name,
	})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}

	a.writeJSON(w, http.StatusOK, serviceSummary{
		Project: resp.GetProject(),
		Name:    resp.GetName(),
		Status:  statusString(resp.GetStatus()),
	})
}

// list — GET /projects/{project}/services: keyset-листинг сервисов проекта.
func (a *servicesAPI) list(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	pageSize, err := parsePageSize(r.URL.Query().Get("page_size"))
	if err != nil {
		a.writeError(w, r, http.StatusBadRequest, "некорректный page_size")
		return
	}

	resp, err := a.client.ListServices(r.Context(), &projectsv1.ListServicesRequest{
		Project: project,
		// Курсор непрозрачный — передаём gRPC без интерпретации.
		PageSize:  pageSize,
		PageToken: r.URL.Query().Get("page_token"),
	})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}

	out := serviceList{
		Services:      make([]serviceSummary, 0, len(resp.GetServices())),
		NextPageToken: resp.GetNextPageToken(),
	}
	for _, s := range resp.GetServices() {
		out.Services = append(out.Services, serviceSummary{
			Project: s.GetProject(),
			Name:    s.GetName(),
			Status:  statusString(s.GetStatus()),
		})
	}
	a.writeJSON(w, http.StatusOK, out)
}

// parsePageSize разбирает необязательный query-параметр page_size в int32.
// Пустая строка → 0 (сервер сам клампит к пределу).
func parsePageSize(raw string) (int32, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n < 0 {
		return 0, errInvalidPageSize
	}
	return int32(n), nil
}

// errInvalidPageSize — внутренняя ошибка разбора page_size (наружу не уходит).
var errInvalidPageSize = errors.New("invalid page_size")

// statusString маппит gRPC-статус сервиса в строковый статус периметра
// (схема ServiceStatus). Неизвестное значение не маппится молча в дефолт.
func statusString(s projectsv1.ServiceStatus) string {
	switch s {
	case projectsv1.ServiceStatus_SERVICE_STATUS_CREATING:
		return "creating"
	case projectsv1.ServiceStatus_SERVICE_STATUS_ACTIVE:
		return "active"
	case projectsv1.ServiceStatus_SERVICE_STATUS_DECOMMISSIONED:
		return "decommissioned"
	case projectsv1.ServiceStatus_SERVICE_STATUS_FAILED:
		return "failed"
	default:
		// UNSPECIFIED/неизвестное — пустая строка; клиентский zod .parse
		// упадёт явно, сигнализируя о дрейфе контракта.
		return ""
	}
}

// httpFromGRPC детерминированно маппит gRPC-код в HTTP-статус и стабильное
// клиентское сообщение (ADR-0003: внутренние детали наружу не отдаём).
func httpFromGRPC(err error) (int, string) {
	switch status.Code(err) {
	case codes.NotFound:
		return http.StatusNotFound, "сервис не найден"
	case codes.FailedPrecondition, codes.AlreadyExists:
		return http.StatusConflict, "конфликт состояния"
	case codes.InvalidArgument:
		return http.StatusBadRequest, "некорректный запрос"
	default:
		return http.StatusInternalServerError, "внутренняя ошибка"
	}
}

// writeGRPCError маппит gRPC-ошибку в HTTP-ответ: подробность пишет в лог
// (ключ slog "err"), клиенту отдаёт только стабильное сообщение.
func (a *servicesAPI) writeGRPCError(w http.ResponseWriter, r *http.Request, err error) {
	code, msg := httpFromGRPC(err)
	if code >= http.StatusInternalServerError {
		a.log.Error("gateway: upstream gRPC error",
			logger.Err(err), slog.String("path", routePattern(r)))
	}
	a.writeError(w, r, code, msg)
}

// writeError отдаёт стабильное JSON-тело ошибки с заданным HTTP-статусом.
func (a *servicesAPI) writeError(w http.ResponseWriter, _ *http.Request, code int, msg string) {
	a.writeJSON(w, code, errorBody{Error: msg})
}

// writeJSON сериализует значение в JSON-ответ с указанным статусом.
func (a *servicesAPI) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		a.log.Error("gateway: ошибка кодирования ответа", logger.Err(err))
	}
}

// routePattern возвращает шаблон маршрута chi для безопасных меток/логов
// (а не сырой URL.Path). Дублирует helper из pkg/httpserver на уровне сервиса.
func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	return "unmatched"
}
