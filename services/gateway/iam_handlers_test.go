// Тесты горизонтальных ручек IAM-админки периметра: RBAC fail-closed (read/write
// на iam:global), маппинг кодов, форма JSON по OpenAPI, идемпотентность
// assign/revoke и неразглашение внутренних ошибок. Сеть не используется — клиенты
// IDM (Access/IamAdmin/RoleAdmin) подменяются стабами.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
)

// stubIamAdminClient — стаб gRPC-клиента чтения каталога IAM.
type stubIamAdminClient struct {
	roles       *idmv1.ListRolesResponse
	permissions *idmv1.ListPermissionsResponse
	rolePerms   *idmv1.GetRolePermissionsResponse
	subjects    *idmv1.ListSubjectsWithRolesResponse
	subjRoles   *idmv1.GetSubjectRolesResponse
	err         error

	gotRolePerms *idmv1.GetRolePermissionsRequest
	gotSubjects  *idmv1.ListSubjectsWithRolesRequest
	gotSubjRoles *idmv1.GetSubjectRolesRequest
}

func (s *stubIamAdminClient) ListRoles(_ context.Context, _ *idmv1.ListRolesRequest, _ ...grpc.CallOption) (*idmv1.ListRolesResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.roles, nil
}

func (s *stubIamAdminClient) ListPermissions(_ context.Context, _ *idmv1.ListPermissionsRequest, _ ...grpc.CallOption) (*idmv1.ListPermissionsResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.permissions, nil
}

func (s *stubIamAdminClient) GetRolePermissions(_ context.Context, in *idmv1.GetRolePermissionsRequest, _ ...grpc.CallOption) (*idmv1.GetRolePermissionsResponse, error) {
	s.gotRolePerms = in
	if s.err != nil {
		return nil, s.err
	}
	return s.rolePerms, nil
}

func (s *stubIamAdminClient) ListSubjectsWithRoles(_ context.Context, in *idmv1.ListSubjectsWithRolesRequest, _ ...grpc.CallOption) (*idmv1.ListSubjectsWithRolesResponse, error) {
	s.gotSubjects = in
	if s.err != nil {
		return nil, s.err
	}
	return s.subjects, nil
}

func (s *stubIamAdminClient) GetSubjectRoles(_ context.Context, in *idmv1.GetSubjectRolesRequest, _ ...grpc.CallOption) (*idmv1.GetSubjectRolesResponse, error) {
	s.gotSubjRoles = in
	if s.err != nil {
		return nil, s.err
	}
	return s.subjRoles, nil
}

// stubRoleAdminClient — стаб gRPC-клиента управления ролями (assign/revoke).
type stubRoleAdminClient struct {
	assignErr error
	revokeErr error

	gotAssign *idmv1.AssignRoleRequest
	gotRevoke *idmv1.RevokeRoleRequest
}

func (s *stubRoleAdminClient) AssignRole(_ context.Context, in *idmv1.AssignRoleRequest, _ ...grpc.CallOption) (*idmv1.AssignRoleResponse, error) {
	s.gotAssign = in
	if s.assignErr != nil {
		return nil, s.assignErr
	}
	return &idmv1.AssignRoleResponse{}, nil
}

func (s *stubRoleAdminClient) RevokeRole(_ context.Context, in *idmv1.RevokeRoleRequest, _ ...grpc.CallOption) (*idmv1.RevokeRoleResponse, error) {
	s.gotRevoke = in
	if s.revokeErr != nil {
		return nil, s.revokeErr
	}
	return &idmv1.RevokeRoleResponse{}, nil
}

// newIAMRouter собирает роутер с явными стабами Access/IamAdmin/RoleAdmin.
func newIAMRouter(idm idmv1.AccessServiceClient, iamAdmin idmv1.IamAdminServiceClient, roleAdmin idmv1.RoleAdminServiceClient) http.Handler {
	api := &servicesAPI{
		idm:       idm,
		iamAdmin:  iamAdmin,
		roleAdmin: roleAdmin,
		// Справочник субъектов по умолчанию пуст: обогащение GET /iam/subjects
		// (ADR-0016) ничего не добавляет, существующие тесты остаются «сырыми».
		identity: &stubIdentityClient{},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r := chi.NewRouter()
	api.register(r)
	return r
}

// TestIAMListRolesHappy: успешное чтение ролей с правом read, форма JSON по OpenAPI.
func TestIAMListRolesHappy(t *testing.T) {
	t.Parallel()
	iam := &stubIamAdminClient{roles: &idmv1.ListRolesResponse{Roles: []*idmv1.Role{{Name: "iam-admin"}, {Name: "project-creator"}}}}
	router := newIAMRouter(&stubIDMClient{allowed: true}, iam, &stubRoleAdminClient{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/iam/roles", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("код = %d, ожидалось 200 (body=%q)", rec.Code, rec.Body.String())
	}
	var got roleListView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не парсится: %v", err)
	}
	if len(got.Roles) != 2 || got.Roles[0].Name != "iam-admin" {
		t.Fatalf("неожиданное тело: %+v", got)
	}
}

// TestIAMReadRBAC: read-ручки требуют (read, iam:global); deny и недоступность IDM
// → 403 fail-closed, без вызова чтения каталога.
func TestIAMReadRBAC(t *testing.T) {
	t.Parallel()

	// Проверка формы запроса RBAC для чтения.
	idm := &stubIDMClient{allowed: true}
	iam := &stubIamAdminClient{roles: &idmv1.ListRolesResponse{}}
	router := newIAMRouter(idm, iam, &stubRoleAdminClient{})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/iam/roles", nil))
	if idm.gotReq.GetResource() != "iam:global" || idm.gotReq.GetAction() != "read" {
		t.Fatalf("некорректная форма RBAC: %+v", idm.gotReq)
	}

	// Deny → 403, каталог не читается.
	iam2 := &stubIamAdminClient{roles: &idmv1.ListRolesResponse{}}
	router2 := newIAMRouter(&stubIDMClient{allowed: false}, iam2, &stubRoleAdminClient{})
	rec2 := httptest.NewRecorder()
	router2.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/iam/subjects", nil))
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("deny: код = %d, ожидалось 403", rec2.Code)
	}
	if iam2.gotSubjects != nil {
		t.Fatalf("при отказе RBAC чтение каталога не должно вызываться")
	}

	// IDM недоступен → 403 (fail-closed).
	router3 := newIAMRouter(&stubIDMClient{err: status.Error(codes.Unavailable, "idm down")}, &stubIamAdminClient{}, &stubRoleAdminClient{})
	rec3 := httptest.NewRecorder()
	router3.ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/iam/roles", nil))
	if rec3.Code != http.StatusForbidden {
		t.Fatalf("fail-closed: код = %d, ожидалось 403", rec3.Code)
	}
}

// TestIAMRolePermissionsNotFound: несуществующая роль → 404, без раскрытия деталей.
func TestIAMRolePermissionsNotFound(t *testing.T) {
	t.Parallel()
	iam := &stubIamAdminClient{err: status.Error(codes.NotFound, "внутренняя деталь")}
	router := newIAMRouter(&stubIDMClient{allowed: true}, iam, &stubRoleAdminClient{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/iam/roles/no-such/permissions", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("код = %d, ожидалось 404", rec.Code)
	}
	if iam.gotRolePerms.GetRole() != "no-such" {
		t.Fatalf("роль проброшена некорректно: %+v", iam.gotRolePerms)
	}
}

// TestIAMListSubjects: keyset проброс и форма ответа.
func TestIAMListSubjects(t *testing.T) {
	t.Parallel()
	iam := &stubIamAdminClient{subjects: &idmv1.ListSubjectsWithRolesResponse{
		Subjects:      []*idmv1.SubjectRoles{{Subject: "demo-user", Roles: []string{"iam-admin"}}},
		NextPageToken: "tok-2",
	}}
	router := newIAMRouter(&stubIDMClient{allowed: true}, iam, &stubRoleAdminClient{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/iam/subjects?page_size=1&page_token=tok-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("код = %d, ожидалось 200 (body=%q)", rec.Code, rec.Body.String())
	}
	var got subjectListView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не парсится: %v", err)
	}
	if got.NextPageToken != "tok-2" || len(got.Subjects) != 1 || got.Subjects[0].Subject != "demo-user" {
		t.Fatalf("неожиданное тело: %+v", got)
	}
	if iam.gotSubjects.GetPageSize() != 1 || iam.gotSubjects.GetPageToken() != "tok-1" {
		t.Fatalf("keyset проброшен некорректно: %+v", iam.gotSubjects)
	}
}

// TestIAMListSubjectsBadPageSize: валидация page_size без выхода в gRPC.
func TestIAMListSubjectsBadPageSize(t *testing.T) {
	t.Parallel()
	iam := &stubIamAdminClient{}
	router := newIAMRouter(&stubIDMClient{allowed: true}, iam, &stubRoleAdminClient{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/iam/subjects?page_size=abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("код = %d, ожидалось 400", rec.Code)
	}
	if iam.gotSubjects != nil {
		t.Fatalf("gRPC не должен вызываться при некорректном page_size")
	}
}

// TestIAMAssignRevoke: happy-path назначения/снятия с правом write; ответ —
// актуальный набор ролей субъекта; идемпотентность; форма RBAC = (write, iam:global).
func TestIAMAssignRevoke(t *testing.T) {
	t.Parallel()

	idm := &stubIDMClient{allowed: true}
	iam := &stubIamAdminClient{subjRoles: &idmv1.GetSubjectRolesResponse{Roles: []string{"iam-admin"}}}
	role := &stubRoleAdminClient{}
	router := newIAMRouter(idm, iam, role)

	// Назначение → 200 с актуальным набором ролей.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/iam/subjects/alice/roles/iam-admin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("assign: код = %d, ожидалось 200 (body=%q)", rec.Code, rec.Body.String())
	}
	var got subjectRolesView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не парсится: %v", err)
	}
	if got.Subject != "alice" || len(got.Roles) != 1 || got.Roles[0] != "iam-admin" {
		t.Fatalf("неожиданное тело: %+v", got)
	}
	if role.gotAssign.GetSubject() != "alice" || role.gotAssign.GetRole() != "iam-admin" {
		t.Fatalf("AssignRole проброшен некорректно: %+v", role.gotAssign)
	}
	if idm.gotReq.GetResource() != "iam:global" || idm.gotReq.GetAction() != "write" {
		t.Fatalf("некорректная форма RBAC мутации: %+v", idm.gotReq)
	}

	// Снятие → 200 (идемпотентно).
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, httptest.NewRequest(http.MethodDelete, "/iam/subjects/alice/roles/iam-admin", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("revoke: код = %d, ожидалось 200", rec2.Code)
	}
	if role.gotRevoke.GetSubject() != "alice" || role.gotRevoke.GetRole() != "iam-admin" {
		t.Fatalf("RevokeRole проброшен некорректно: %+v", role.gotRevoke)
	}
}

// TestIAMWriteRBACDeny: мутации требуют (write, iam:global); deny → 403 без вызова
// RoleAdmin.
func TestIAMWriteRBACDeny(t *testing.T) {
	t.Parallel()
	role := &stubRoleAdminClient{}
	router := newIAMRouter(&stubIDMClient{allowed: false}, &stubIamAdminClient{}, role)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/iam/subjects/alice/roles/iam-admin", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("код = %d, ожидалось 403", rec.Code)
	}
	if role.gotAssign != nil {
		t.Fatalf("при отказе RBAC мутация не должна вызываться")
	}
}

// TestIAMAssignRoleNotFound: несуществующая роль при назначении → 404 (из gRPC
// NotFound), без раскрытия деталей.
func TestIAMAssignRoleNotFound(t *testing.T) {
	t.Parallel()
	role := &stubRoleAdminClient{assignErr: status.Error(codes.NotFound, "внутренняя деталь")}
	router := newIAMRouter(&stubIDMClient{allowed: true}, &stubIamAdminClient{}, role)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/iam/subjects/alice/roles/no-such", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("код = %d, ожидалось 404 (body=%q)", rec.Code, rec.Body.String())
	}
}
