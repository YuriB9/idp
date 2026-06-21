// Юнит-тесты gRPC-сервера IamAdminService на стаб-каталоге (без БД): маппинг
// доменных типов в proto, маппинг ошибок в коды, валидация аргументов.
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

// stubCatalog — управляемый стаб catalogReader для транспортных тестов.
type stubCatalog struct {
	roles    []repository.Role
	perms    []repository.Permission
	rolePerm []repository.Permission
	subjects []repository.SubjectRoles
	next     string
	subjRole []string
	err      error
}

func (s *stubCatalog) ListRoles(context.Context) ([]repository.Role, error) {
	return s.roles, s.err
}

func (s *stubCatalog) ListPermissions(context.Context) ([]repository.Permission, error) {
	return s.perms, s.err
}

func (s *stubCatalog) GetRolePermissions(context.Context, string) ([]repository.Permission, error) {
	return s.rolePerm, s.err
}

func (s *stubCatalog) ListSubjectsWithRoles(context.Context, int, string) ([]repository.SubjectRoles, string, error) {
	return s.subjects, s.next, s.err
}

func (s *stubCatalog) GetSubjectRoles(context.Context, string) ([]string, error) {
	return s.subjRole, s.err
}

func newTestServer(c catalogReader) *iamAdminServer {
	return &iamAdminServer{catalog: c, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestIamAdminServer_ListRolesAndPermissions(t *testing.T) {
	t.Parallel()
	srv := newTestServer(&stubCatalog{
		roles: []repository.Role{{Name: "iam-admin"}, {Name: "project-creator"}},
		perms: []repository.Permission{{Action: "read", Resource: "iam:global"}},
	})
	ctx := context.Background()

	rresp, err := srv.ListRoles(ctx, &idmv1.ListRolesRequest{})
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if len(rresp.GetRoles()) != 2 || rresp.GetRoles()[0].GetName() != "iam-admin" {
		t.Fatalf("неожиданные роли: %+v", rresp.GetRoles())
	}

	presp, err := srv.ListPermissions(ctx, &idmv1.ListPermissionsRequest{})
	if err != nil {
		t.Fatalf("ListPermissions: %v", err)
	}
	if len(presp.GetPermissions()) != 1 || presp.GetPermissions()[0].GetResource() != "iam:global" {
		t.Fatalf("неожиданные права: %+v", presp.GetPermissions())
	}
}

func TestIamAdminServer_GetRolePermissions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		role     string
		stub     *stubCatalog
		wantCode codes.Code
	}{
		{
			name:     "пустая роль → InvalidArgument",
			role:     "",
			stub:     &stubCatalog{},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "несуществующая роль → NotFound",
			role:     "no-such-role",
			stub:     &stubCatalog{err: errs.ErrNotFound},
			wantCode: codes.NotFound,
		},
		{
			name:     "ошибка БД → Unavailable (fail-closed)",
			role:     "iam-admin",
			stub:     &stubCatalog{err: errors.New("boom")},
			wantCode: codes.Unavailable,
		},
		{
			name:     "успех",
			role:     "iam-admin",
			stub:     &stubCatalog{rolePerm: []repository.Permission{{Action: "write", Resource: "iam:global"}}},
			wantCode: codes.OK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newTestServer(tt.stub)
			_, err := srv.GetRolePermissions(context.Background(),
				&idmv1.GetRolePermissionsRequest{Role: tt.role})
			if status.Code(err) != tt.wantCode {
				t.Fatalf("код: получили %v, ожидали %v (err=%v)", status.Code(err), tt.wantCode, err)
			}
		})
	}
}

func TestIamAdminServer_ListSubjectsWithRoles(t *testing.T) {
	t.Parallel()
	srv := newTestServer(&stubCatalog{
		subjects: []repository.SubjectRoles{{Subject: "demo-user", Roles: []string{"iam-admin"}}},
		next:     "tok",
	})
	resp, err := srv.ListSubjectsWithRoles(context.Background(), &idmv1.ListSubjectsWithRolesRequest{PageSize: 10})
	if err != nil {
		t.Fatalf("ListSubjectsWithRoles: %v", err)
	}
	if resp.GetNextPageToken() != "tok" {
		t.Fatalf("ожидали курсор tok, получили %q", resp.GetNextPageToken())
	}
	if len(resp.GetSubjects()) != 1 || resp.GetSubjects()[0].GetSubject() != "demo-user" {
		t.Fatalf("неожиданные субъекты: %+v", resp.GetSubjects())
	}
	if resp.GetSubjects()[0].GetRoles()[0] != "iam-admin" {
		t.Fatalf("ожидали роль iam-admin, получили %v", resp.GetSubjects()[0].GetRoles())
	}
}

func TestIamAdminServer_BadCursor(t *testing.T) {
	t.Parallel()
	srv := newTestServer(&stubCatalog{err: errs.ErrValidation})
	_, err := srv.ListSubjectsWithRoles(context.Background(),
		&idmv1.ListSubjectsWithRolesRequest{PageToken: "bad"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ожидали InvalidArgument для повреждённого курсора, получили %v", status.Code(err))
	}
}

func TestIamAdminServer_GetSubjectRoles(t *testing.T) {
	t.Parallel()
	srv := newTestServer(&stubCatalog{subjRole: []string{"iam-admin", "project-creator"}})

	// Пустой subject → InvalidArgument.
	if _, err := srv.GetSubjectRoles(context.Background(), &idmv1.GetSubjectRolesRequest{Subject: ""}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ожидали InvalidArgument для пустого subject, получили %v", status.Code(err))
	}

	resp, err := srv.GetSubjectRoles(context.Background(), &idmv1.GetSubjectRolesRequest{Subject: "demo-user"})
	if err != nil {
		t.Fatalf("GetSubjectRoles: %v", err)
	}
	if len(resp.GetRoles()) != 2 {
		t.Fatalf("ожидали 2 роли, получили %v", resp.GetRoles())
	}
}
