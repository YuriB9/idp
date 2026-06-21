// Юнит-тесты gRPC-сервера IdentityService на стаб-фасаде (без сети/Keycloak):
// маппинг идентичностей в proto и маппинг ошибок в коды (InvalidArgument →
// валидация, Unavailable → недоступность каталога).
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
	"github.com/YuriB9/idp/services/idm/internal/identity"
)

// stubDirectory — управляемый стаб фасада справочника для транспортных тестов.
type stubDirectory struct {
	search  []identity.Identity
	next    string
	resolve []identity.Identity
	err     error
}

func (s *stubDirectory) Search(context.Context, string, string, int) ([]identity.Identity, string, error) {
	return s.search, s.next, s.err
}

func (s *stubDirectory) Resolve(context.Context, []string) ([]identity.Identity, error) {
	return s.resolve, s.err
}

func newIdentitySrv(d directory) *identityServer {
	return &identityServer{dir: d, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestIdentityServer_SearchHappy(t *testing.T) {
	t.Parallel()
	srv := newIdentitySrv(&stubDirectory{
		search: []identity.Identity{{Subject: "u-1", Username: "ivan", Email: "i@e", Found: true}},
		next:   "cur-2",
	})
	resp, err := srv.SearchSubjects(context.Background(), &idmv1.SearchSubjectsRequest{Query: "iv"})
	if err != nil {
		t.Fatalf("SearchSubjects: %v", err)
	}
	if len(resp.GetSubjects()) != 1 || resp.GetSubjects()[0].GetSubject() != "u-1" {
		t.Fatalf("неверный маппинг: %+v", resp.GetSubjects())
	}
	if resp.GetNextCursor() != "cur-2" {
		t.Fatalf("курсор не проброшен: %q", resp.GetNextCursor())
	}
}

func TestIdentityServer_ResolveOrphan(t *testing.T) {
	t.Parallel()
	srv := newIdentitySrv(&stubDirectory{
		resolve: []identity.Identity{
			{Subject: "u-1", Username: "ivan", Found: true},
			{Subject: "u-x", Found: false},
		},
	})
	resp, err := srv.ResolveSubjects(context.Background(), &idmv1.ResolveSubjectsRequest{Subjects: []string{"u-1", "u-x"}})
	if err != nil {
		t.Fatalf("ResolveSubjects: %v", err)
	}
	if len(resp.GetSubjects()) != 2 || resp.GetSubjects()[1].GetFound() {
		t.Fatalf("осиротевший должен иметь found=false: %+v", resp.GetSubjects())
	}
}

func TestIdentityServer_ErrorMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		code codes.Code
	}{
		{"валидация → InvalidArgument", errs.ErrValidation, codes.InvalidArgument},
		{"недоступность → Unavailable", identity.ErrUnavailable, codes.Unavailable},
		{"прочее → Unavailable", errors.New("boom"), codes.Unavailable},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := newIdentitySrv(&stubDirectory{err: tc.err})
			_, err := srv.SearchSubjects(context.Background(), &idmv1.SearchSubjectsRequest{Query: "iv"})
			if status.Code(err) != tc.code {
				t.Fatalf("ожидали %v, got %v (%v)", tc.code, status.Code(err), err)
			}
		})
	}
}
