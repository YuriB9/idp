// Файл iam_directory_handlers.go — REST-ручки справочника субъектов из каталога
// идентичностей Keycloak (ADR-0016). Привилегированный периметр: КАЖДАЯ ручка
// под CheckAccess(read, iam:directory) (fail-closed → 403) — отдельное право на
// PII, отличное от (read, iam:global). Недоступность Keycloak → 503 (деградация:
// справочник не критичен для CheckAccess). Шлюз маппит REST↔gRPC и НИКОГДА не
// раскрывает внутренние ошибки/секреты клиенту.
package main

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	"github.com/YuriB9/idp/pkg/auth"
	"github.com/YuriB9/idp/pkg/logger"
)

// iamDirectoryResource — горизонтальный ресурс просмотра каталога пользователей
// (PII): отдельное право наименьших привилегий (ADR-0016).
const iamDirectoryResource = "iam:directory"

// directorySubjectView — идентичность субъекта для клиента (схема SubjectIdentity).
// Found=false — «осиротевший» субъект (роль есть, в каталоге нет).
type directorySubjectView struct {
	Subject     string `json:"subject"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Enabled     bool   `json:"enabled"`
	Found       bool   `json:"found"`
}

// directoryListView — страница поиска справочника (схема DirectorySubjectList).
type directoryListView struct {
	Subjects   []directorySubjectView `json:"subjects"`
	NextCursor string                 `json:"next_cursor"`
}

// directoryResolveView — результат батч-резолва (схема DirectorySubjectResolveResult).
type directoryResolveView struct {
	Subjects []directorySubjectView `json:"subjects"`
}

// resolveBody — тело запроса батч-резолва (схема ResolveSubjectsRequest).
type resolveBody struct {
	Subjects []string `json:"subjects"`
}

// registerIAMDirectory навешивает маршруты справочника субъектов под
// (read, iam:directory).
func (a *servicesAPI) registerIAMDirectory(r chi.Router) {
	r.Get("/iam/directory/subjects", a.iamDirectorySearch)
	r.Post("/iam/directory/subjects/resolve", a.iamDirectoryResolve)
}

// iamDirectorySearch — GET /iam/directory/subjects?search=&cursor=&page_size=:
// поиск пользователей каталога. Пустой/битый ввод → 400; Keycloak недоступен → 503.
func (a *servicesAPI) iamDirectorySearch(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamDirectoryResource, "read") {
		return
	}
	search := r.URL.Query().Get("search")
	if search == "" {
		a.writeError(w, r, http.StatusBadRequest, "параметр search обязателен")
		return
	}
	pageSize, err := parsePageSize(r.URL.Query().Get("page_size"))
	if err != nil {
		a.writeError(w, r, http.StatusBadRequest, "некорректный page_size")
		return
	}
	resp, err := a.identity.SearchSubjects(r.Context(), &idmv1.SearchSubjectsRequest{
		Query:    search,
		Cursor:   r.URL.Query().Get("cursor"),
		PageSize: pageSize,
	})
	if err != nil {
		a.writeDirectoryError(w, r, err)
		return
	}
	out := directoryListView{
		Subjects:   identitiesToViews(resp.GetSubjects()),
		NextCursor: resp.GetNextCursor(),
	}
	a.writeJSON(w, http.StatusOK, out)
}

// iamDirectoryResolve — POST /iam/directory/subjects/resolve: батч-резолв sub →
// идентичность. Пустой список → 400; Keycloak недоступен → 503. Отсутствующие в
// каталоге субъекты — found=false (не опускаются).
func (a *servicesAPI) iamDirectoryResolve(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamDirectoryResource, "read") {
		return
	}
	var body resolveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "некорректное тело запроса")
		return
	}
	if len(body.Subjects) == 0 {
		a.writeError(w, r, http.StatusBadRequest, "список subjects обязателен")
		return
	}
	resp, err := a.identity.ResolveSubjects(r.Context(), &idmv1.ResolveSubjectsRequest{Subjects: body.Subjects})
	if err != nil {
		a.writeDirectoryError(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, directoryResolveView{Subjects: identitiesToViews(resp.GetSubjects())})
}

// identitiesToViews маппит proto-идентичности в клиентское представление.
func identitiesToViews(in []*idmv1.SubjectIdentity) []directorySubjectView {
	out := make([]directorySubjectView, 0, len(in))
	for _, id := range in {
		out = append(out, directorySubjectView{
			Subject:     id.GetSubject(),
			Username:    id.GetUsername(),
			Email:       id.GetEmail(),
			DisplayName: id.GetDisplayName(),
			Enabled:     id.GetEnabled(),
			Found:       id.GetFound(),
		})
	}
	return out
}

// hasAccess выполняет НЕписьменную RBAC-проверку (для условного обогащения PII):
// возвращает true только при явном разрешении; недоступность/ошибка IDM → false
// (fail-closed, без записи ответа). subject — из claims.
func (a *servicesAPI) hasAccess(r *http.Request, resource, action string) bool {
	var subject string
	if claims, ok := auth.ClaimsFromContext(r.Context()); ok {
		subject = claims.Subject
	}
	resp, err := a.idm.CheckAccess(r.Context(), &idmv1.CheckAccessRequest{
		Subject:  subject,
		Resource: resource,
		Action:   action,
	})
	return err == nil && resp.GetAllowed()
}

// writeDirectoryError маппит ошибку справочника: Unavailable → 503 (деградация,
// retryable), прочее — общий маппинг httpFromGRPC. Деталь — в лог по ключу "err".
func (a *servicesAPI) writeDirectoryError(w http.ResponseWriter, r *http.Request, err error) {
	if status.Code(err) == codes.Unavailable {
		a.log.Warn("gateway: каталог идентичностей недоступен (деградация)",
			logger.Err(err), slog.String("path", routePattern(r)))
		a.writeError(w, r, http.StatusServiceUnavailable, "каталог субъектов временно недоступен")
		return
	}
	a.writeGRPCError(w, r, err)
}
