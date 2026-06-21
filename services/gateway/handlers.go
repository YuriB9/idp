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

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	projectsv1 "github.com/YuriB9/idp/pkg/api/projects/v1"
	"github.com/YuriB9/idp/pkg/auth"
	"github.com/YuriB9/idp/pkg/logger"
)

// servicesAPI — обработчики ресурсов периметра, держащие gRPC-клиенты каталога
// проектов и IDM. Помимо project-scoped ручек сервисов хостит горизонтальные
// ручки IAM-админки (см. iam_handlers.go): для них держит клиенты чтения каталога
// (IamAdmin) и управления ролями (RoleAdmin). Создаётся один раз в main и
// навешивается на роутер.
type servicesAPI struct {
	client    projectsv1.ProjectsServiceClient
	idm       idmv1.AccessServiceClient
	iamAdmin  idmv1.IamAdminServiceClient
	roleAdmin idmv1.RoleAdminServiceClient
	log       *slog.Logger
}

// authorize выполняет RBAC-проверку IDM перед project-scoped операцией периметра:
// формирует ресурс "project:<project>" и делегирует в authorizeResource. Тонкая
// обёртка ради совместимости существующих вызовов (ADR-0014).
func (a *servicesAPI) authorize(w http.ResponseWriter, r *http.Request, project, action string) bool {
	return a.authorizeResource(w, r, "project:"+project, action)
}

// authorizeResource выполняет RBAC-проверку IDM по ПРОИЗВОЛЬНОМУ ресурсу
// (project-scoped "project:<p>" или горизонтальный "iam:global"). Возвращает true
// только при явном разрешении. При отказе ИЛИ недоступности/ошибке IDM пишет
// HTTP 403 (fail-closed) и возвращает false; внутренние детали наружу не
// раскрываются (только лог). subject берётся из claims (auth.ClaimsFromContext);
// пустой subject → IDM ответит отказом.
func (a *servicesAPI) authorizeResource(w http.ResponseWriter, r *http.Request, resource, action string) bool {
	var subject string
	if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
		subject = claims.Subject
	}
	resp, err := a.idm.CheckAccess(r.Context(), &idmv1.CheckAccessRequest{
		Subject:  subject,
		Resource: resource,
		Action:   action,
	})
	if err != nil || !resp.GetAllowed() {
		if err != nil {
			// Недоступность/ошибка IDM — деталь в лог, наружу стабильный 403.
			a.log.Warn("gateway: RBAC недоступен/ошибка (fail-closed)",
				logger.Err(err), slog.String("path", routePattern(r)))
		}
		a.writeError(w, r, http.StatusForbidden, "доступ запрещён")
		return false
	}
	return true
}

// errorBody — стабильное тело ошибки периметра (схема Error в OpenAPI).
// Внутренние детали (текст gRPC-ошибки) сюда не попадают — только в лог.
type errorBody struct {
	Error string `json:"error"`
}

// serviceSummary — представление сервиса для клиента (схема ServiceSummary).
type serviceSummary struct {
	Project          string   `json:"project"`
	Name             string   `json:"name"`
	Status           string   `json:"status"`
	Owners           []string `json:"owners"`
	OwnersVersion    int64    `json:"owners_version"`
	DecommissionedAt string   `json:"decommissioned_at,omitempty"`
}

// decommissionBody — тело запроса вывода из эксплуатации (схема
// DecommissionServiceRequest).
type decommissionBody struct {
	LoadDrained bool `json:"load_drained"`
}

// transferBody — тело запроса переноса сервиса (схема TransferServiceRequest).
type transferBody struct {
	TargetProject string `json:"target_project"`
}

// setOwnersBody — тело запроса смены владельцев (схема SetServiceOwnersRequest).
type setOwnersBody struct {
	Owners        []string `json:"owners"`
	OwnersVersion int64    `json:"owners_version"`
}

// setOwnersResult — тело ответа смены владельцев (схема SetServiceOwnersResult).
type setOwnersResult struct {
	Owners        []string `json:"owners"`
	OwnersVersion int64    `json:"owners_version"`
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
// периметра, см. main.go). Перед проксированием каждая ручка вызывает RBAC IDM
// CheckAccess (см. authorize), доступ fail-closed.
func (a *servicesAPI) register(r chi.Router) {
	r.Post("/projects/{project}/services", a.create)
	r.Get("/projects/{project}/services", a.list)
	r.Get("/projects/{project}/services/{name}", a.get)
	r.Put("/projects/{project}/services/{name}/owners", a.setOwners)
	r.Post("/projects/{project}/services/{name}/decommission", a.decommission)
	r.Post("/projects/{project}/services/{name}/transfer", a.transfer)
	// Горизонтальные (не project-scoped) ручки IAM-админки (см. iam_handlers.go).
	a.registerIAM(r)
}

// create — POST /projects/{project}/services: запускает создание сервиса.
func (a *servicesAPI) create(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	// RBAC: проверка права create до любой доменной обработки (fail-closed).
	if !a.authorize(w, r, project, "create") {
		return
	}

	var body createServiceBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "некорректное тело запроса")
		return
	}
	if body.Name == "" {
		a.writeError(w, r, http.StatusBadRequest, "имя сервиса обязательно")
		return
	}

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

	// RBAC: проверка права read до чтения (fail-closed).
	if !a.authorize(w, r, project, "read") {
		return
	}

	resp, err := a.client.GetService(r.Context(), &projectsv1.GetServiceRequest{
		Project: project,
		Name:    name,
	})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}

	a.writeJSON(w, http.StatusOK, serviceSummary{
		Project:          resp.GetProject(),
		Name:             resp.GetName(),
		Status:           statusString(resp.GetStatus()),
		Owners:           ownersOrEmpty(resp.GetOwners()),
		OwnersVersion:    resp.GetOwnersVersion(),
		DecommissionedAt: resp.GetDecommissionedAt(),
	})
}

// decommission — POST /projects/{project}/services/{name}/decommission: вывод
// сервиса из эксплуатации (soft-delete). RBAC decommission (fail-closed) до
// проксирования. Тело несёт явное предусловие load_drained (ADR-0012).
func (a *servicesAPI) decommission(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	name := chi.URLParam(r, "name")

	// RBAC: проверка права decommission до любой обработки (fail-closed).
	if !a.authorize(w, r, project, "decommission") {
		return
	}

	var body decommissionBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "некорректное тело запроса")
		return
	}

	resp, err := a.client.DecommissionService(r.Context(), &projectsv1.DecommissionServiceRequest{
		Project:     project,
		Name:        name,
		LoadDrained: body.LoadDrained,
	})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}

	svc := resp.GetService()
	a.writeJSON(w, http.StatusOK, serviceSummary{
		Project:          svc.GetProject(),
		Name:             svc.GetName(),
		Status:           statusString(svc.GetStatus()),
		Owners:           ownersOrEmpty(svc.GetOwners()),
		OwnersVersion:    svc.GetOwnersVersion(),
		DecommissionedAt: svc.GetDecommissionedAt(),
	})
}

// transfer — POST /projects/{project}/services/{name}/transfer: перенос сервиса в
// другой проект. RBAC двусторонний (fail-closed): transfer на исходном проекте И
// transfer_in на целевом — без права на target нельзя «вынести» сервис в чужой
// проект (ADR-0013). Тело несёт target_project.
func (a *servicesAPI) transfer(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	name := chi.URLParam(r, "name")

	var body transferBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "некорректное тело запроса")
		return
	}
	if body.TargetProject == "" {
		a.writeError(w, r, http.StatusBadRequest, "target_project обязателен")
		return
	}

	// RBAC: право transfer на исходном проекте И transfer_in на целевом
	// (defense-in-depth, fail-closed). Отказ по любому → 403, без проксирования.
	if !a.authorize(w, r, project, "transfer") {
		return
	}
	if !a.authorize(w, r, body.TargetProject, "transfer_in") {
		return
	}

	resp, err := a.client.TransferService(r.Context(), &projectsv1.TransferServiceRequest{
		Project:       project,
		Name:          name,
		TargetProject: body.TargetProject,
	})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}

	svc := resp.GetService()
	a.writeJSON(w, http.StatusOK, serviceSummary{
		Project:          svc.GetProject(),
		Name:             svc.GetName(),
		Status:           statusString(svc.GetStatus()),
		Owners:           ownersOrEmpty(svc.GetOwners()),
		OwnersVersion:    svc.GetOwnersVersion(),
		DecommissionedAt: svc.GetDecommissionedAt(),
	})
}

// setOwners — PUT /projects/{project}/services/{name}/owners: декларативная замена
// набора владельцев. RBAC change_owners (fail-closed) до проксирования.
func (a *servicesAPI) setOwners(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	name := chi.URLParam(r, "name")

	// RBAC: проверка права change_owners до любой обработки (fail-closed).
	if !a.authorize(w, r, project, "change_owners") {
		return
	}

	var body setOwnersBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "некорректное тело запроса")
		return
	}
	// Валидация: владельцы непустые и без дублей.
	seen := map[string]bool{}
	for _, o := range body.Owners {
		if o == "" {
			a.writeError(w, r, http.StatusBadRequest, "владелец не может быть пустым")
			return
		}
		if seen[o] {
			a.writeError(w, r, http.StatusBadRequest, "владельцы не должны дублироваться")
			return
		}
		seen[o] = true
	}

	resp, err := a.client.SetServiceOwners(r.Context(), &projectsv1.SetServiceOwnersRequest{
		Project:         project,
		Name:            name,
		Owners:          body.Owners,
		ExpectedVersion: body.OwnersVersion,
	})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}

	a.writeJSON(w, http.StatusOK, setOwnersResult{
		Owners:        ownersOrEmpty(resp.GetOwners()),
		OwnersVersion: resp.GetOwnersVersion(),
	})
}

// ownersOrEmpty гарантирует ненулевой slice (в JSON — [], а не null), чтобы
// клиентский zod .parse получал ожидаемый массив.
func ownersOrEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// list — GET /projects/{project}/services: keyset-листинг сервисов проекта.
func (a *servicesAPI) list(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")

	// RBAC: проверка права list до листинга (fail-closed).
	if !a.authorize(w, r, project, "list") {
		return
	}

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
			Project:          s.GetProject(),
			Name:             s.GetName(),
			Status:           statusString(s.GetStatus()),
			Owners:           ownersOrEmpty(s.GetOwners()),
			OwnersVersion:    s.GetOwnersVersion(),
			DecommissionedAt: s.GetDecommissionedAt(),
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
	case projectsv1.ServiceStatus_SERVICE_STATUS_TRANSFERRING:
		return "transferring"
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
	case codes.Aborted, codes.AlreadyExists:
		// Конкурентный конфликт guarded-CAS (Aborted) и занятое имя (AlreadyExists)
		// → 409 (ADR-0012).
		return http.StatusConflict, "конфликт состояния"
	case codes.FailedPrecondition:
		// Семантическое предусловие не выполнено (нагрузка не снята / недопустимый
		// исходный статус) → 422 (ADR-0012).
		return http.StatusUnprocessableEntity, "предусловие не выполнено"
	case codes.InvalidArgument:
		return http.StatusBadRequest, "некорректный запрос"
	case codes.PermissionDenied:
		return http.StatusForbidden, "доступ запрещён"
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
