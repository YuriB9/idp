// Файл iam_handlers.go — горизонтальные (не project-scoped) REST-ручки IAM-админки
// периметра (ADR-0009, ADR-0014): просмотр каталога ролей/прав/субъектов и
// назначение/снятие ролей субъектам. Привилегированный периметр: КАЖДАЯ ручка
// под CheckAccess (fail-closed) — read для чтения, write для мутаций на ресурсе
// "iam:global". Шлюз не содержит доменной логики: маппит REST↔gRPC и НИКОГДА не
// раскрывает внутренние ошибки клиенту.
package main

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	"github.com/YuriB9/idp/pkg/logger"
)

// iamResource — горизонтальный ресурс полномочий IAM-админки (ADR-0014).
const iamResource = "iam:global"

// roleView — представление роли для клиента (схема Role): стабильный идентификатор
// — имя; внутренний id наружу не отдаётся. System — признак системной
// (сидированной) роли: для таких UI скрывает удаление/правку прав (ADR-0015).
type roleView struct {
	Name   string `json:"name"`
	System bool   `json:"system"`
}

// permissionView — представление права (схема Permission). System — признак
// системного (сидированного) права (защищено от удаления).
type permissionView struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	System   bool   `json:"system"`
}

// rolePermissionsView — роль и актуальный набор её прав (схема RolePermissions);
// ответ attach/detach.
type rolePermissionsView struct {
	Role        string           `json:"role"`
	Permissions []permissionView `json:"permissions"`
}

// roleNameBody — тело запроса создания роли (схема CreateRoleRequest).
type roleNameBody struct {
	Name string `json:"name"`
}

// permissionBody — тело запроса создания права / прикрепления права к роли
// (схемы CreatePermissionRequest / AttachPermissionRequest).
type permissionBody struct {
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
// Identity — опциональная идентичность из каталога (ADR-0016): присутствует
// только при обогащении (вызывающий держит (read, iam:directory) и Keycloak
// доступен); иначе опускается (без утечки PII / деградация). Для «осиротевшего»
// субъекта Identity присутствует с found=false.
type subjectRolesView struct {
	Subject  string                `json:"subject"`
	Roles    []string              `json:"roles"`
	Identity *directorySubjectView `json:"identity,omitempty"`
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
	// Структурные мутации каталога (ADR-0015) — под (manage, iam:global).
	r.Post("/iam/roles", a.iamCreateRole)
	r.Delete("/iam/roles/{role}", a.iamDeleteRole)
	r.Post("/iam/roles/{role}/permissions", a.iamAttachPermission)
	r.Delete("/iam/roles/{role}/permissions", a.iamDetachPermission)
	r.Post("/iam/permissions", a.iamCreatePermission)
	r.Delete("/iam/permissions", a.iamDeletePermission)
	// Справочник субъектов из каталога Keycloak (ADR-0016) — под (read, iam:directory).
	a.registerIAMDirectory(r)
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
		out.Roles = append(out.Roles, roleView{Name: role.GetName(), System: role.GetSystem()})
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
	// Обогащение идентичностями (ADR-0016) — ТОЛЬКО при наличии (read,
	// iam:directory): без этого права ответ остаётся «сырым» (PII не раскрывается).
	// При недоступном Keycloak обогащение деградирует (ответ без идентичностей),
	// управление ролями по сырому subject не ломается.
	if len(out.Subjects) > 0 && a.hasAccess(r, iamDirectoryResource, "read") {
		a.enrichSubjects(r, out.Subjects)
	}
	a.writeJSON(w, http.StatusOK, out)
}

// enrichSubjects дополняет субъектов идентичностями из каталога (резолв батчем).
// Недоступность Keycloak → деградация (идентичности не добавляются). Осиротевшие
// субъекты получают Identity с found=false.
func (a *servicesAPI) enrichSubjects(r *http.Request, subjects []subjectRolesView) {
	subs := make([]string, 0, len(subjects))
	for _, s := range subjects {
		subs = append(subs, s.Subject)
	}
	resp, err := a.identity.ResolveSubjects(r.Context(), &idmv1.ResolveSubjectsRequest{Subjects: subs})
	if err != nil {
		// Деградация: каталог недоступен — отдаём роли без идентичностей.
		a.log.Warn("gateway: обогащение субъектов недоступно (деградация)", logger.Err(err))
		return
	}
	byID := make(map[string]*idmv1.SubjectIdentity, len(resp.GetSubjects()))
	for _, id := range resp.GetSubjects() {
		byID[id.GetSubject()] = id
	}
	for i := range subjects {
		if id, ok := byID[subjects[i].Subject]; ok {
			view := directorySubjectView{
				Subject:     id.GetSubject(),
				Username:    id.GetUsername(),
				Email:       id.GetEmail(),
				DisplayName: id.GetDisplayName(),
				Enabled:     id.GetEnabled(),
				Found:       id.GetFound(),
			}
			subjects[i].Identity = &view
		}
	}
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

// iamCreateRole — POST /iam/roles: создать пользовательскую роль. Под (manage,
// iam:global). Дубль имени → 409; пустое имя → 400.
func (a *servicesAPI) iamCreateRole(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamResource, "manage") {
		return
	}
	var body roleNameBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "некорректное тело запроса")
		return
	}
	if body.Name == "" {
		a.writeError(w, r, http.StatusBadRequest, "имя роли обязательно")
		return
	}
	resp, err := a.iamCatalog.CreateRole(r.Context(), &idmv1.CreateRoleRequest{Name: body.Name})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}
	role := resp.GetRole()
	a.writeJSON(w, http.StatusCreated, roleView{Name: role.GetName(), System: role.GetSystem()})
}

// iamDeleteRole — DELETE /iam/roles/{role}: удалить роль (каскад). Под (manage,
// iam:global). Системная → 422; несуществующая → 404.
func (a *servicesAPI) iamDeleteRole(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamResource, "manage") {
		return
	}
	role := chi.URLParam(r, "role")
	if _, err := a.iamCatalog.DeleteRole(r.Context(), &idmv1.DeleteRoleRequest{Name: role}); err != nil {
		a.writeGRPCError(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, roleView{Name: role})
}

// iamAttachPermission — POST /iam/roles/{role}/permissions: прикрепить право к роли
// (идемпотентно). Под (manage, iam:global). Тело {action,resource}. Роль/право нет
// → 404; системная роль → 422; пустые поля → 400. Ответ — актуальный набор прав роли.
func (a *servicesAPI) iamAttachPermission(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamResource, "manage") {
		return
	}
	role := chi.URLParam(r, "role")
	body, ok := a.decodePermissionBody(w, r)
	if !ok {
		return
	}
	resp, err := a.iamCatalog.AttachPermission(r.Context(), &idmv1.AttachPermissionRequest{
		Role: role, Action: body.Action, Resource: body.Resource,
	})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}
	a.writeRolePermissions(w, resp.GetRolePermissions())
}

// iamDetachPermission — DELETE /iam/roles/{role}/permissions?action=&resource=:
// открепить право от роли (идемпотентно). Под (manage, iam:global). Пара —
// в query-параметрах. Системная роль → 422; роль нет → 404; пустые поля → 400.
func (a *servicesAPI) iamDetachPermission(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamResource, "manage") {
		return
	}
	role := chi.URLParam(r, "role")
	action := r.URL.Query().Get("action")
	resource := r.URL.Query().Get("resource")
	if action == "" || resource == "" {
		a.writeError(w, r, http.StatusBadRequest, "action и resource обязательны")
		return
	}
	resp, err := a.iamCatalog.DetachPermission(r.Context(), &idmv1.DetachPermissionRequest{
		Role: role, Action: action, Resource: resource,
	})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}
	a.writeRolePermissions(w, resp.GetRolePermissions())
}

// iamCreatePermission — POST /iam/permissions: создать пользовательское право. Под
// (manage, iam:global). Тело {action,resource}. Дубль пары → 409; пустые поля → 400.
func (a *servicesAPI) iamCreatePermission(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamResource, "manage") {
		return
	}
	body, ok := a.decodePermissionBody(w, r)
	if !ok {
		return
	}
	resp, err := a.iamCatalog.CreatePermission(r.Context(), &idmv1.CreatePermissionRequest{
		Action: body.Action, Resource: body.Resource,
	})
	if err != nil {
		a.writeGRPCError(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusCreated, permissionViewFromProto(resp.GetPermission()))
}

// iamDeletePermission — DELETE /iam/permissions?action=&resource=: удалить право
// (каскад). Под (manage, iam:global). Системное → 422; несуществующее → 404;
// пустые поля → 400.
func (a *servicesAPI) iamDeletePermission(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeResource(w, r, iamResource, "manage") {
		return
	}
	action := r.URL.Query().Get("action")
	resource := r.URL.Query().Get("resource")
	if action == "" || resource == "" {
		a.writeError(w, r, http.StatusBadRequest, "action и resource обязательны")
		return
	}
	if _, err := a.iamCatalog.DeletePermission(r.Context(), &idmv1.DeletePermissionRequest{
		Action: action, Resource: resource,
	}); err != nil {
		a.writeGRPCError(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, permissionView{Action: action, Resource: resource})
}

// decodePermissionBody разбирает и валидирует тело {action,resource}. При ошибке
// сам пишет 400 и возвращает ok=false.
func (a *servicesAPI) decodePermissionBody(w http.ResponseWriter, r *http.Request) (permissionBody, bool) {
	var body permissionBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "некорректное тело запроса")
		return body, false
	}
	if body.Action == "" || body.Resource == "" {
		a.writeError(w, r, http.StatusBadRequest, "action и resource обязательны")
		return body, false
	}
	return body, true
}

// writeRolePermissions отдаёт клиенту актуальный набор прав роли (ответ attach/detach).
func (a *servicesAPI) writeRolePermissions(w http.ResponseWriter, rp *idmv1.RolePermissions) {
	out := rolePermissionsView{
		Role:        rp.GetRole(),
		Permissions: make([]permissionView, 0, len(rp.GetPermissions())),
	}
	for _, p := range rp.GetPermissions() {
		out.Permissions = append(out.Permissions, permissionViewFromProto(p))
	}
	a.writeJSON(w, http.StatusOK, out)
}

// permissionViewFromProto маппит одно proto-право в клиентское представление.
func permissionViewFromProto(p *idmv1.Permission) permissionView {
	return permissionView{Action: p.GetAction(), Resource: p.GetResource(), System: p.GetSystem()}
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
		out.Permissions = append(out.Permissions, permissionViewFromProto(p))
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
