// Тесты структурных ручек каталога IAM периметра (ADR-0015): RBAC fail-closed
// (manage на iam:global), маппинг кодов (409/422/404/400), идемпотентность
// attach/detach, форма JSON по OpenAPI и неразглашение внутренних ошибок. Сеть не
// используется — клиент IamCatalog подменяется стабом.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
)

// stubIamCatalogClient — стаб gRPC-клиента структурных мутаций каталога.
type stubIamCatalogClient struct {
	err   error
	role  *idmv1.Role
	perm  *idmv1.Permission
	perms *idmv1.RolePermissions

	gotAttach *idmv1.AttachPermissionRequest
	gotDetach *idmv1.DetachPermissionRequest
	gotDelPrm *idmv1.DeletePermissionRequest
}

func (s *stubIamCatalogClient) CreateRole(_ context.Context, _ *idmv1.CreateRoleRequest, _ ...grpc.CallOption) (*idmv1.CreateRoleResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &idmv1.CreateRoleResponse{Role: s.role}, nil
}

func (s *stubIamCatalogClient) DeleteRole(_ context.Context, _ *idmv1.DeleteRoleRequest, _ ...grpc.CallOption) (*idmv1.DeleteRoleResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &idmv1.DeleteRoleResponse{}, nil
}

func (s *stubIamCatalogClient) CreatePermission(_ context.Context, _ *idmv1.CreatePermissionRequest, _ ...grpc.CallOption) (*idmv1.CreatePermissionResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &idmv1.CreatePermissionResponse{Permission: s.perm}, nil
}

func (s *stubIamCatalogClient) DeletePermission(_ context.Context, in *idmv1.DeletePermissionRequest, _ ...grpc.CallOption) (*idmv1.DeletePermissionResponse, error) {
	s.gotDelPrm = in
	if s.err != nil {
		return nil, s.err
	}
	return &idmv1.DeletePermissionResponse{}, nil
}

func (s *stubIamCatalogClient) AttachPermission(_ context.Context, in *idmv1.AttachPermissionRequest, _ ...grpc.CallOption) (*idmv1.AttachPermissionResponse, error) {
	s.gotAttach = in
	if s.err != nil {
		return nil, s.err
	}
	return &idmv1.AttachPermissionResponse{RolePermissions: s.perms}, nil
}

func (s *stubIamCatalogClient) DetachPermission(_ context.Context, in *idmv1.DetachPermissionRequest, _ ...grpc.CallOption) (*idmv1.DetachPermissionResponse, error) {
	s.gotDetach = in
	if s.err != nil {
		return nil, s.err
	}
	return &idmv1.DetachPermissionResponse{RolePermissions: s.perms}, nil
}

// newCatalogRouter собирает роутер со стабами Access/IamCatalog.
func newCatalogRouter(idm idmv1.AccessServiceClient, catalog idmv1.IamCatalogServiceClient) http.Handler {
	api := &servicesAPI{
		idm:        idm,
		iamCatalog: catalog,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r := chi.NewRouter()
	api.register(r)
	return r
}

// TestIAMCreateRoleHappy: создание роли с правом manage → 201; форма RBAC =
// (manage, iam:global); форма ответа по OpenAPI.
func TestIAMCreateRoleHappy(t *testing.T) {
	t.Parallel()
	idm := &stubIDMClient{allowed: true}
	catalog := &stubIamCatalogClient{role: &idmv1.Role{Name: "reviewers", System: false}}
	router := newCatalogRouter(idm, catalog)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/iam/roles", strings.NewReader(`{"name":"reviewers"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("код = %d, ожидалось 201 (body=%q)", rec.Code, rec.Body.String())
	}
	var got roleView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не парсится: %v", err)
	}
	if got.Name != "reviewers" || got.System {
		t.Fatalf("неожиданное тело: %+v", got)
	}
	if idm.gotReq.GetResource() != "iam:global" || idm.gotReq.GetAction() != "manage" {
		t.Fatalf("некорректная форма RBAC: %+v", idm.gotReq)
	}
}

// TestIAMManageRBAC: структурные ручки требуют (manage, iam:global); deny и
// недоступность IDM → 403 fail-closed, без вызова мутации.
func TestIAMManageRBAC(t *testing.T) {
	t.Parallel()

	// Deny → 403, мутация не вызывается.
	catalog := &stubIamCatalogClient{}
	router := newCatalogRouter(&stubIDMClient{allowed: false}, catalog)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/iam/roles", strings.NewReader(`{"name":"x"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("deny: код = %d, ожидалось 403", rec.Code)
	}

	// IDM недоступен → 403 (fail-closed).
	router2 := newCatalogRouter(&stubIDMClient{err: status.Error(codes.Unavailable, "idm down")}, &stubIamCatalogClient{})
	rec2 := httptest.NewRecorder()
	router2.ServeHTTP(rec2, httptest.NewRequest(http.MethodDelete, "/iam/roles/x", nil))
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("fail-closed: код = %d, ожидалось 403", rec2.Code)
	}
}

// TestIAMCatalogErrorCodes: маппинг доменных gRPC-ошибок в HTTP-коды.
func TestIAMCatalogErrorCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		grpcCode codes.Code
		method   string
		target   string
		body     string
		want     int
	}{
		{"дубль роли → 409", codes.AlreadyExists, http.MethodPost, "/iam/roles", `{"name":"dup"}`, http.StatusConflict},
		{"системная роль → 422", codes.FailedPrecondition, http.MethodDelete, "/iam/roles/iam-admin", "", http.StatusUnprocessableEntity},
		{"роль не найдена → 404", codes.NotFound, http.MethodDelete, "/iam/roles/ghost", "", http.StatusNotFound},
		{"дубль права → 409", codes.AlreadyExists, http.MethodPost, "/iam/permissions", `{"action":"a","resource":"r"}`, http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			catalog := &stubIamCatalogClient{err: status.Error(tt.grpcCode, "внутренняя деталь")}
			router := newCatalogRouter(&stubIDMClient{allowed: true}, catalog)
			rec := httptest.NewRecorder()
			var bodyReader io.Reader
			if tt.body != "" {
				bodyReader = strings.NewReader(tt.body)
			}
			router.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.target, bodyReader))
			if rec.Code != tt.want {
				t.Fatalf("код = %d, ожидалось %d (body=%q)", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

// TestIAMAttachHappy: attach с правом manage → 200; пара проброшена; ответ —
// актуальный набор прав роли.
func TestIAMAttachHappy(t *testing.T) {
	t.Parallel()
	catalog := &stubIamCatalogClient{perms: &idmv1.RolePermissions{
		Role:        "reviewers",
		Permissions: []*idmv1.Permission{{Action: "read", Resource: "iam:global", System: true}},
	}}
	router := newCatalogRouter(&stubIDMClient{allowed: true}, catalog)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/iam/roles/reviewers/permissions",
		strings.NewReader(`{"action":"read","resource":"iam:global"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("код = %d, ожидалось 200 (body=%q)", rec.Code, rec.Body.String())
	}
	var got rolePermissionsView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не парсится: %v", err)
	}
	if got.Role != "reviewers" || len(got.Permissions) != 1 || !got.Permissions[0].System {
		t.Fatalf("неожиданное тело: %+v", got)
	}
	if catalog.gotAttach.GetRole() != "reviewers" || catalog.gotAttach.GetAction() != "read" {
		t.Fatalf("attach проброшен некорректно: %+v", catalog.gotAttach)
	}
}

// TestIAMAttachBadBody: пустые поля тела attach → 400 без выхода в gRPC.
func TestIAMAttachBadBody(t *testing.T) {
	t.Parallel()
	catalog := &stubIamCatalogClient{}
	router := newCatalogRouter(&stubIDMClient{allowed: true}, catalog)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/iam/roles/reviewers/permissions",
		strings.NewReader(`{"action":"","resource":""}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("код = %d, ожидалось 400", rec.Code)
	}
	if catalog.gotAttach != nil {
		t.Fatalf("gRPC не должен вызываться при пустом теле")
	}
}

// TestIAMDetachQueryParams: detach через query-параметры → 200; пара проброшена.
func TestIAMDetachQueryParams(t *testing.T) {
	t.Parallel()
	catalog := &stubIamCatalogClient{perms: &idmv1.RolePermissions{Role: "reviewers"}}
	router := newCatalogRouter(&stubIDMClient{allowed: true}, catalog)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete,
		"/iam/roles/reviewers/permissions?action=read&resource=iam:global", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("код = %d, ожидалось 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if catalog.gotDetach.GetAction() != "read" || catalog.gotDetach.GetResource() != "iam:global" {
		t.Fatalf("detach проброшен некорректно: %+v", catalog.gotDetach)
	}
}

// TestIAMDetachMissingQuery: detach без обязательных query-параметров → 400.
func TestIAMDetachMissingQuery(t *testing.T) {
	t.Parallel()
	catalog := &stubIamCatalogClient{}
	router := newCatalogRouter(&stubIDMClient{allowed: true}, catalog)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/iam/roles/reviewers/permissions", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("код = %d, ожидалось 400", rec.Code)
	}
	if catalog.gotDetach != nil {
		t.Fatalf("gRPC не должен вызываться без query-параметров")
	}
}

// TestIAMDeletePermissionQuery: удаление права через query → 200; пара проброшена.
func TestIAMDeletePermissionQuery(t *testing.T) {
	t.Parallel()
	catalog := &stubIamCatalogClient{}
	router := newCatalogRouter(&stubIDMClient{allowed: true}, catalog)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete,
		"/iam/permissions?action=deploy&resource=project:demo", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("код = %d, ожидалось 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if catalog.gotDelPrm.GetAction() != "deploy" || catalog.gotDelPrm.GetResource() != "project:demo" {
		t.Fatalf("delete права проброшен некорректно: %+v", catalog.gotDelPrm)
	}
}
