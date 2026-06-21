// Юнит-тесты gRPC-сервера IamCatalogService на стаб-каталоге (без БД): маппинг
// доменных ошибок в коды (AlreadyExists/FailedPrecondition/NotFound/InvalidArgument/
// Unavailable), валидация аргументов, проброс актуального набора прав роли.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/services/idm/internal/repository"
)

// stubCatalogMgr — управляемый стаб catalogManager для транспортных тестов.
type stubCatalogMgr struct {
	role  repository.Role
	perm  repository.Permission
	perms []repository.Permission
	err   error
}

func (s *stubCatalogMgr) CreateRole(context.Context, string) (repository.Role, error) {
	return s.role, s.err
}
func (s *stubCatalogMgr) DeleteRole(context.Context, string) error { return s.err }
func (s *stubCatalogMgr) CreatePermission(context.Context, string, string) (repository.Permission, error) {
	return s.perm, s.err
}
func (s *stubCatalogMgr) DeletePermission(context.Context, string, string) error { return s.err }
func (s *stubCatalogMgr) AttachPermission(context.Context, string, string, string) ([]repository.Permission, error) {
	return s.perms, s.err
}
func (s *stubCatalogMgr) DetachPermission(context.Context, string, string, string) ([]repository.Permission, error) {
	return s.perms, s.err
}

func newCatalogServer(m catalogManager) *iamCatalogServer {
	return &iamCatalogServer{catalog: m, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// TestIamCatalogServer_ErrorMapping проверяет маппинг доменных ошибок в gRPC-коды
// для всех мутаций (table-driven). Используется CreateRole/AttachPermission как
// репрезентативные пути create/attach.
func TestIamCatalogServer_ErrorMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
	}{
		{"дубль → AlreadyExists (409)", errs.ErrConflict, codes.AlreadyExists},
		{"системный ресурс → FailedPrecondition (422)", errs.ErrPrecondition, codes.FailedPrecondition},
		{"не найден → NotFound (404)", errs.ErrNotFound, codes.NotFound},
		{"невалидно → InvalidArgument (400)", errs.ErrValidation, codes.InvalidArgument},
		{"ошибка БД → Unavailable (fail-closed)", errors.New("boom"), codes.Unavailable},
		{"успех", nil, codes.OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newCatalogServer(&stubCatalogMgr{err: tt.err})
			_, err := srv.CreateRole(context.Background(), &idmv1.CreateRoleRequest{Name: "reviewers"})
			if status.Code(err) != tt.wantCode {
				t.Fatalf("CreateRole код: получили %v, ожидали %v (err=%v)", status.Code(err), tt.wantCode, err)
			}
		})
	}
}

// TestIamCatalogServer_Validation проверяет валидацию пустых/битых аргументов →
// InvalidArgument (до обращения к стору).
func TestIamCatalogServer_Validation(t *testing.T) {
	t.Parallel()
	srv := newCatalogServer(&stubCatalogMgr{})
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{"CreateRole пустое имя", func() error {
			_, err := srv.CreateRole(ctx, &idmv1.CreateRoleRequest{Name: ""})
			return err
		}},
		{"DeleteRole пустое имя", func() error {
			_, err := srv.DeleteRole(ctx, &idmv1.DeleteRoleRequest{Name: ""})
			return err
		}},
		{"CreatePermission пустой action", func() error {
			_, err := srv.CreatePermission(ctx, &idmv1.CreatePermissionRequest{Action: "", Resource: "r"})
			return err
		}},
		{"CreatePermission NUL в resource", func() error {
			_, err := srv.CreatePermission(ctx, &idmv1.CreatePermissionRequest{Action: "a", Resource: "x\x00y"})
			return err
		}},
		{"AttachPermission пустая роль", func() error {
			_, err := srv.AttachPermission(ctx, &idmv1.AttachPermissionRequest{Role: "", Action: "a", Resource: "r"})
			return err
		}},
		{"DetachPermission пустой resource", func() error {
			_, err := srv.DetachPermission(ctx, &idmv1.DetachPermissionRequest{Role: "x", Action: "a", Resource: ""})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if status.Code(tt.call()) != codes.InvalidArgument {
				t.Fatalf("%s: ожидали InvalidArgument", tt.name)
			}
		})
	}
}

// TestIamCatalogServer_AttachReturnsPermissions проверяет, что ответ attach несёт
// актуальный набор прав роли (для рантайм-валидации и обновления UI).
func TestIamCatalogServer_AttachReturnsPermissions(t *testing.T) {
	t.Parallel()
	srv := newCatalogServer(&stubCatalogMgr{
		perms: []repository.Permission{{Action: "read", Resource: "iam:global", System: true}},
	})
	resp, err := srv.AttachPermission(context.Background(),
		&idmv1.AttachPermissionRequest{Role: "reviewers", Action: "read", Resource: "iam:global"})
	if err != nil {
		t.Fatalf("AttachPermission: %v", err)
	}
	rp := resp.GetRolePermissions()
	if rp.GetRole() != "reviewers" || len(rp.GetPermissions()) != 1 {
		t.Fatalf("ожидали набор прав роли reviewers, получили %+v", rp)
	}
	if !rp.GetPermissions()[0].GetSystem() {
		t.Fatalf("ожидали признак system в проброшенном праве")
	}
}

// TestIamCatalogServer_CreateRoleSuccess проверяет успешное создание роли
// (system=false в ответе).
func TestIamCatalogServer_CreateRoleSuccess(t *testing.T) {
	t.Parallel()
	srv := newCatalogServer(&stubCatalogMgr{role: repository.Role{Name: "reviewers", System: false}})
	resp, err := srv.CreateRole(context.Background(), &idmv1.CreateRoleRequest{Name: "reviewers"})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if resp.GetRole().GetName() != "reviewers" || resp.GetRole().GetSystem() {
		t.Fatalf("ожидали пользовательскую роль reviewers, получили %+v", resp.GetRole())
	}
}
