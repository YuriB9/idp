// Command projects — сервис управления проектами: gRPC ProjectsService и
// Temporal Client (ADR-0001, ADR-0002). API и Temporal worker — раздельные
// процессы (worker — в services/devinfra-worker). В этом изменении подключён
// доменный слой каталога: Postgres-репозиторий с guarded-CAS-переходами,
// usecase и gRPC-реализация чтения; Temporal workflow — отдельный change.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"os/signal"
	"syscall"

	"go.temporal.io/sdk/client"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	projectsv1 "github.com/YuriB9/idp/pkg/api/projects/v1"
	"github.com/YuriB9/idp/pkg/auth"
	"github.com/YuriB9/idp/pkg/config"
	"github.com/YuriB9/idp/pkg/db"
	"github.com/YuriB9/idp/pkg/grpcx"
	"github.com/YuriB9/idp/pkg/httpserver"
	"github.com/YuriB9/idp/pkg/logger"
	"github.com/YuriB9/idp/pkg/temporallog"
	"github.com/YuriB9/idp/services/projects/internal/grpcapi"
	"github.com/YuriB9/idp/services/projects/internal/repository"
	"github.com/YuriB9/idp/services/projects/internal/usecase"
)

func main() {
	if err := run(); err != nil {
		slog.Default().Error("projects: fatal", logger.Err(err))
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log := logger.New(logger.Options{Level: config.String("LOG_LEVEL", "info"), JSON: true})
	verifier := auth.MustVerifierFromEnv(ctx, log)

	// Пул Postgres каталога проектов: конфигурация обязательна (docs БЛОК 4).
	maxConns, err := config.Int("PG_MAX_CONNS", 10)
	if err != nil {
		return err
	}
	if maxConns <= 0 || maxConns > math.MaxInt32 {
		return fmt.Errorf("projects: PG_MAX_CONNS вне допустимого диапазона: %d", maxConns)
	}
	pool, err := db.NewPool(ctx, db.PoolConfig{
		DSN:      config.String("PG_DSN", "postgres://projects:projects@postgres-projects:5432/projects?sslmode=disable"),
		MaxConns: int32(maxConns),
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	// Сборка доменных слоёв: repository → usecase → gRPC-транспорт.
	repo := repository.New(pool)
	catalog := usecase.New(repo)
	projectsAPI := grpcapi.New(catalog, log)

	// Lazy-клиент Temporal: не падаем на старте, если сервер недоступен;
	// готовность отражается в /readyz через CheckHealth.
	temporalClient, err := client.NewLazyClient(client.Options{
		HostPort:  config.String("TEMPORAL_HOSTPORT", "temporal:7233"),
		Namespace: config.String("TEMPORAL_NAMESPACE", "default"),
		Logger:    temporallog.New(log),
	})
	if err != nil {
		return err
	}
	defer temporalClient.Close()

	grpcSrv := grpc.NewServer(grpcx.ServerOptions(log, verifier)...)
	projectsv1.RegisterProjectsServiceServer(grpcSrv, projectsAPI)

	lis, err := net.Listen("tcp", config.String("GRPC_ADDR", ":9090"))
	if err != nil {
		return err
	}

	httpSrv := httpserver.New(httpserver.Config{
		Addr:   config.String("HTTP_ADDR", ":8082"),
		Logger: log,
		ReadinessChecks: []httpserver.ReadinessCheck{
			{
				// Реальный пинг Postgres: k8s не должен слать трафик при недоступной БД.
				Name:  "postgres",
				Check: pool.Ping,
			},
			{
				Name: "temporal",
				Check: func(ctx context.Context) error {
					_, herr := temporalClient.CheckHealth(ctx, &client.CheckHealthRequest{})
					return herr
				},
			},
		},
	})

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		log.Info("projects: grpc listening", slog.String("addr", lis.Addr().String()))
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
