// Файл iam_handlers.go — горизонтальные (не project-scoped) REST-ручки IAM-админки
// периметра (ADR-0009, ADR-0014): просмотр каталога ролей/прав/субъектов и
// назначение/снятие ролей субъектам. Привилегированный периметр: КАЖДАЯ ручка
// под CheckAccess (fail-closed) — read для чтения, write для мутаций на ресурсе
// "iam:global". Шлюз не содержит доменной логики: маппит REST↔gRPC и НИКОГДА не
// раскрывает внутренние ошибки клиенту.
package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
)

// iamResource — горизонтальный ресурс полномочий IAM-админки (ADR-0014).
const iamResource = "iam:global"

// roleView — представление роли для клиента (схема Role): стабильный идентификатор
// — имя; внутренний id наружу не отдаётся.
type roleView struct {
	Name string `json:"name"`
}

// permissionView — представление права (схема Permission).
type permissionView struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
}

// roleListView — список ролей (схема RoleList).
type roleListView struct {
	Roles []roleView `json:"roles"`
}

// permissionListView — список прав (схема PermissionList).
type permissionListView struct {
	Permissions []permissionView `json:"permissions"`
}

// subjectRolesView — субъект и набор имён его ролей (схема SubjectRoles).
type subjectRolesView struct {
	Subject string   `json:"subject"`
	Roles   []string `json:"roles"`
}

// subjectListView — страница субъектов с ролями и курсором (схема SubjectList).
type subjectListView struct {
	Subjects      []subjectRolesView `json:"subjects"`
	NextPageToken string             `json:"next_page_token"`
}

// registerIAM навешивает горизонтальные маршруты IAM-админки. Чтение — право
// (read, iam:global), мутации — (write, iam:global); проверка до проксирования.
func (a *servicesAPI) registerIAM(r chi.Router) {
	r.Get("/iam/roles", a.iamListRoles)
	r.Get("/iam/permissions", a.iamListPermissions)
	r.Get("/iam/roles/{role}/permissions", a.iamRolePermissions)
	r.Get("/iam/subjects", a.iamListSubjects)
	r.Get("/iam/subjects/{subject}/roles", a.iamSubjectRoles)
	r.Post("/iam/subjects/{subject}/roles/{role}", a.iamAssignRole)
	r.Delete("/iam/subjects/{subject}/roles/{role}", a.iamRevokeRole)
}

// iamListRoles — GET /iam/roles: список ролей каталога (read-only).
func (a *servicesAPI) iamListRoles(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamResource, "read") {
		return
	}
	resp, err := a.iamAdmin.ListRoles(r.Context(), &idmv1.ListRolesRequest{})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}
	out := roleListView{Roles: make([]roleView, 0, len(resp.GetRoles()))}
	for _, role := range resp.GetRoles() {
		out.Roles = append(out.Roles, roleView{Name: role.GetName()})
	}
	a.writeJSON(w, http.StatusOK, out)
}

// iamListPermissions — GET /iam/permissions: все права каталога (read-only).
func (a *servicesAPI) iamListPermissions(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamResource, "read") {
		return
	}
	resp, err := a.iamAdmin.ListPermissions(r.Context(), &idmv1.ListPermissionsRequest{})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, permissionListFromProto(resp.GetPermissions()))
}

// iamRolePermissions — GET /iam/roles/{role}/permissions: права роли. Несуществующая
// роль → 404.
func (a *servicesAPI) iamRolePermissions(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamResource, "read") {
		return
	}
	role := chi.URLParam(r, "role")
	resp, err := a.iamAdmin.GetRolePermissions(r.Context(), &idmv1.GetRolePermissionsRequest{Role: role})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, permissionListFromProto(resp.GetPermissions()))
}

// iamListSubjects — GET /iam/subjects: субъекты с их ролями (keyset-пагинация).
func (a *servicesAPI) iamListSubjects(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamResource, "read") {
		return
	}
	pageSize, err := parsePageSize(r.URL.Query().Get("page_size"))
	if err != nil {
		a.writeError(w, r, http.StatusBadRequest, "некорректный page_size")
		return
	}
	resp, err := a.iamAdmin.ListSubjectsWithRoles(r.Context(), &idmv1.ListSubjectsWithRolesRequest{
		PageSize:  pageSize,
		PageToken: r.URL.Query().Get("page_token"),
	})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}
	out := subjectListView{
		Subjects:      make([]subjectRolesView, 0, len(resp.GetSubjects())),
		NextPageToken: resp.GetNextPageToken(),
	}
	for _, s := range resp.GetSubjects() {
		out.Subjects = append(out.Subjects, subjectRolesView{
			Subject: s.GetSubject(),
			Roles:   rolesOrEmpty(s.GetRoles()),
		})
	}
	a.writeJSON(w, http.StatusOK, out)
}

// iamSubjectRoles — GET /iam/subjects/{subject}/roles: роли конкретного субъекта.
func (a *servicesAPI) iamSubjectRoles(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamResource, "read") {
		return
	}
	subject := chi.URLParam(r, "subject")
	a.writeSubjectRoles(w, r, subject)
}

// iamAssignRole — POST /iam/subjects/{subject}/roles/{role}: назначить роль
// (идемпотентно). Ответ — актуальный набор ролей субъекта. Несуществующая роль →
// 404; пустые поля → 400 (из gRPC InvalidArgument).
func (a *servicesAPI) iamAssignRole(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamResource, "write") {
		return
	}
	subject := chi.URLParam(r, "subject")
	role := chi.URLParam(r, "role")
	if _, err := a.roleAdmin.AssignRole(r.Context(), &idmv1.AssignRoleRequest{Subject: subject, Role: role}); err != nil {
		a.writeGRPCError(w, r, err)
		return
	}
	a.writeSubjectRoles(w, r, subject)
}

// iamRevokeRole — DELETE /iam/subjects/{subject}/roles/{role}: снять роль
// (идемпотентно: снятие отсутствующей связки → успех). Ответ — актуальный набор
// ролей субъекта.
func (a *servicesAPI) iamRevokeRole(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamResource, "write") {
		return
	}
	subject := chi.URLParam(r, "subject")
	role := chi.URLParam(r, "role")
	if _, err := a.roleAdmin.RevokeRole(r.Context(), &idmv1.RevokeRoleRequest{Subject: subject, Role: role}); err != nil {
		a.writeGRPCError(w, r, err)
		return
	}
	a.writeSubjectRoles(w, r, subject)
}

// writeSubjectRoles читает актуальный набор ролей субъекта и отдаёт его клиенту
// (общий ответ чтения ролей и мутаций assign/revoke). Используется после мутации,
// чтобы клиент получил свежее состояние для рантайм-валидации и обновления UI.
func (a *servicesAPI) writeSubjectRoles(w http.ResponseWriter, r *http.Request, subject string) {
	resp, err := a.iamAdmin.GetSubjectRoles(r.Context(), &idmv1.GetSubjectRolesRequest{Subject: subject})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, subjectRolesView{
		Subject: subject,
		Roles:   rolesOrEmpty(resp.GetRoles()),
	})
}

// permissionListFromProto маппит proto-права в клиентское представление.
func permissionListFromProto(in []*idmv1.Permission) permissionListView {
	out := permissionListView{Permissions: make([]permissionView, 0, len(in))}
	for _, p := range in {
		out.Permissions = append(out.Permissions, permissionView{
			Action:   p.GetAction(),
			Resource: p.GetResource(),
		})
	}
	return out
}

// rolesOrEmpty гарантирует ненулевой slice (в JSON — [], а не null), чтобы
// клиентский zod .parse получал ожидаемый массив.
func rolesOrEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
