//go:build integration

// Интеграционный тест IDM-уровня: собирает полный стек решения
// (repository + cache + usecase + gRPC accessServer) против реального
// PostgreSQL с применёнными миграциями и сидом демо-роли. Кэш — in-process
// miniredis. Проверяет сквозной сценарий «demo-user может create в project:demo».
//
//	go test -tags=integration ./...
//
// DSN — IDM_TEST_DSN (по умолчанию локальный postgres-idm на :5433). При
// отсутствии БД тест пропускается.
package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	"github.com/YuriB9/idp/services/idm/internal/cache"
	"github.com/YuriB9/idp/services/idm/internal/repository"
	"github.com/YuriB9/idp/services/idm/internal/usecase"
)

func newServer(t *testing.T) *accessServer {
	t.Helper()
	dsn := os.Getenv("IDM_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://idm:idm@localhost:5433/idm?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("нет доступа к БД (%v) — пропуск интеграционного теста", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("БД недоступна (%v) — пропуск интеграционного теста", err)
	}
	t.Cleanup(pool.Close)

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	authz := usecase.New(repository.New(pool), cache.New(rdb, time.Minute, 30*time.Second))
	return &accessServer{auth: authz, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// TestIntegrationCheckAccess_SeededDemo проверяет реальную модель + сид:
// demo-user разрешён create в project:demo, посторонний — нет, пустые поля —
// InvalidArgument.
func TestIntegrationCheckAccess_SeededDemo(t *testing.T) {
	srv := newServer(t)
	ctx := context.Background()

	tests := []struct {
		name        string
		req         *idmv1.CheckAccessRequest
		wantAllowed bool
		wantCode    codes.Code
	}{
		{
			name:        "demo-user create project:demo → allow",
			req:         &idmv1.CheckAccessRequest{Subject: "demo-user", Resource: "project:demo", Action: "create"},
			wantAllowed: true, wantCode: codes.OK,
		},
		{
			name:     "посторонний субъект → deny",
			req:      &idmv1.CheckAccessRequest{Subject: "stranger", Resource: "project:demo", Action: "create"},
			wantCode: codes.OK,
		},
		{
			name:     "несовпадение ресурса → deny",
			req:      &idmv1.CheckAccessRequest{Subject: "demo-user", Resource: "project:other", Action: "create"},
			wantCode: codes.OK,
		},
		{
			name:     "пустой subject → InvalidArgument",
			req:      &idmv1.CheckAccessRequest{Resource: "project:demo", Action: "create"},
			wantCode: codes.InvalidArgument,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := srv.CheckAccess(ctx, tt.req)
			if tt.wantCode != codes.OK {
				if status.Code(err) != tt.wantCode {
					t.Fatalf("код = %v, ожидали %v", status.Code(err), tt.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if resp.GetAllowed() != tt.wantAllowed {
				t.Fatalf("allowed = %v, ожидали %v", resp.GetAllowed(), tt.wantAllowed)
			}
		})
	}
}
