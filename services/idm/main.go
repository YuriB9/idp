// Command idm — сервис прав/ролей: gRPC AccessService.CheckAccess поверх модели
// RBAC в Postgres с кэшем решений в DragonflyDB (ADR-0003, ADR-0010).
// Поведение fail-closed: недоступность БД → отказ; внутренние ошибки наружу не
// раскрываются.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	"github.com/YuriB9/idp/pkg/auth"
	"github.com/YuriB9/idp/pkg/config"
	"github.com/YuriB9/idp/pkg/db"
	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/pkg/grpcx"
	"github.com/YuriB9/idp/pkg/httpserver"
	"github.com/YuriB9/idp/pkg/logger"
	"github.com/YuriB9/idp/services/idm/internal/cache"
	"github.com/YuriB9/idp/services/idm/internal/repository"
	"github.com/YuriB9/idp/services/idm/internal/usecase"
)

// authorizer — зависимость транспорта от usecase-слоя.
type authorizer interface {
	CheckAccess(ctx context.Context, subject, resource, action string) (bool, error)
}

// roleManager — зависимость транспорта RoleAdminService от usecase-слоя.
type roleManager interface {
	AssignRole(ctx context.Context, subject, role string) error
	RevokeRole(ctx context.Context, subject, role string) error
}

// roleAdminServer реализует gRPC RoleAdminService поверх usecase-слоя. Путь не
// публичный (вызывается доменными сервисами/worker'ом). Внутренние ошибки
// клиенту не раскрываются.
type roleAdminServer struct {
	idmv1.UnimplementedRoleAdminServiceServer
	roles roleManager
	log   *slog.Logger
}

// AssignRole выдаёт субъекту роль (идемпотентно). Несуществующая роль → NotFound.
func (s *roleAdminServer) AssignRole(ctx context.Context, req *idmv1.AssignRoleRequest) (*idmv1.AssignRoleResponse, error) {
	if req.GetSubject() == "" || req.GetRole() == "" {
		return nil, status.Error(codes.InvalidArgument, "subject и role обязательны")
	}
	if err := s.roles.AssignRole(ctx, req.GetSubject(), req.GetRole()); err != nil {
		return nil, s.mapRoleError("AssignRole", err)
	}
	return &idmv1.AssignRoleResponse{}, nil
}

// RevokeRole отзывает у субъекта роль (идемпотентно).
func (s *roleAdminServer) RevokeRole(ctx context.Context, req *idmv1.RevokeRoleRequest) (*idmv1.RevokeRoleResponse, error) {
	if req.GetSubject() == "" || req.GetRole() == "" {
		return nil, status.Error(codes.InvalidArgument, "subject и role обязательны")
	}
	if err := s.roles.RevokeRole(ctx, req.GetSubject(), req.GetRole()); err != nil {
		return nil, s.mapRoleError("RevokeRole", err)
	}
	return &idmv1.RevokeRoleResponse{}, nil
}

// mapRoleError маппит доменную ошибку управления ролями в gRPC-код без утечки
// внутренних деталей (детали — в лог по ключу slog "err").
func (s *roleAdminServer) mapRoleError(method string, err error) error {
	if errors.Is(err, errs.ErrNotFound) {
		return status.Error(codes.NotFound, "роль не найдена")
	}
	s.log.Error("idm: ошибка управления ролями", slog.String("method", method), logger.Err(err))
	return status.Error(codes.Unavailable, "управление ролями временно недоступно")
}

// accessServer реализует gRPC AccessService поверх usecase-слоя. Транспортная
// ответственность: валидация запроса и маппинг решения в proto; внутренние
// ошибки клиенту не раскрываются.
type accessServer struct {
	idmv1.UnimplementedAccessServiceServer
	auth authorizer
	log  *slog.Logger
}

// CheckAccess проверяет право субъекта на действие над ресурсом.
func (s *accessServer) CheckAccess(ctx context.Context, req *idmv1.CheckAccessRequest) (*idmv1.CheckAccessResponse, error) {
	if req.GetSubject() == "" || req.GetResource() == "" || req.GetAction() == "" {
		return nil, status.Error(codes.InvalidArgument, "subject, resource и action обязательны")
	}
	allowed, err := s.auth.CheckAccess(ctx, req.GetSubject(), req.GetResource(), req.GetAction())
	if err != nil {
		// Fail-closed: деталь — только в лог (ключ slog "err"), наружу — общий статус.
		s.log.Error("idm: ошибка проверки доступа", logger.Err(err))
		return nil, status.Error(codes.Unavailable, "проверка доступа временно недоступна")
	}
	if !allowed {
		return &idmv1.CheckAccessResponse{Allowed: false, Reason: "no_matching_permission"}, nil
	}
	return &idmv1.CheckAccessResponse{Allowed: true}, nil
}

func main() {
	if err := run(); err != nil {
		slog.Default().Error("idm: fatal", logger.Err(err))
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log := logger.New(logger.Options{Level: config.String("LOG_LEVEL", "info"), JSON: true})
	verifier := auth.MustVerifierFromEnv(ctx, log)

	// Пул Postgres: источник модели RBAC.
	pool, err := db.NewPool(ctx, db.PoolConfig{
		DSN: config.String("PG_DSN", "postgres://idm:idm@postgres-idm:5432/idm?sslmode=disable"),
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	// Клиент DragonflyDB (протокол Redis): кэш решений.
	rdb := redis.NewClient(&redis.Options{Addr: config.String("REDIS_ADDR", "dragonfly:6379")})
	defer func() { _ = rdb.Close() }()

	ttlAllow, err := config.Duration("IDM_CACHE_TTL", 30*time.Second)
	if err != nil {
		return err
	}
	ttlDeny, err := config.Duration("IDM_CACHE_TTL_DENY", 10*time.Second)
	if err != nil {
		return err
	}

	// Сборка доменных слоёв: repository + cache → usecase → транспорт.
	repo := repository.New(pool)
	decisionCache := cache.New(rdb, ttlAllow, ttlDeny)
	authz := usecase.New(repo, decisionCache)
	roles := usecase.NewRoleManager(repo, decisionCache)

	grpcSrv := grpc.NewServer(grpcx.ServerOptions(log, verifier)...)
	idmv1.RegisterAccessServiceServer(grpcSrv, &accessServer{auth: authz, log: log})
	idmv1.RegisterRoleAdminServiceServer(grpcSrv, &roleAdminServer{roles: roles, log: log})

	lis, err := net.Listen("tcp", config.String("GRPC_ADDR", ":9090"))
	if err != nil {
		return err
	}

	httpSrv := httpserver.New(httpserver.Config{
		Addr:   config.String("HTTP_ADDR", ":8081"),
		Logger: log,
		ReadinessChecks: []httpserver.ReadinessCheck{
			// Content-aware /readyz: трафик не идёт при недоступных зависимостях.
			{Name: "postgres", Check: pool.Ping},
			{Name: "dragonfly", Check: decisionCache.Ping},
		},
	})

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		log.Info("idm: grpc listening", slog.String("addr", lis.Addr().String()))
		return grpcSrv.Serve(lis)
	})
	g.Go(func() error {
		<-gctx.Done()
		grpcSrv.GracefulStop()
		return nil
	})
	g.Go(func() error { return httpSrv.Run(gctx) })

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}
