// Command projects — сервис управления проектами: gRPC ProjectsService и
// Temporal Client (ADR-0001, ADR-0002). API и Temporal worker — раздельные
// процессы (worker — в services/devinfra-worker). В этом изменении — скелет:
// gRPC-сервер, Temporal-клиент и health, без доменной логики и workflow.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"go.temporal.io/sdk/client"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	projectsv1 "github.com/YuriB9/idp/pkg/api/projects/v1"
	"github.com/YuriB9/idp/pkg/auth"
	"github.com/YuriB9/idp/pkg/config"
	"github.com/YuriB9/idp/pkg/grpcx"
	"github.com/YuriB9/idp/pkg/httpserver"
	"github.com/YuriB9/idp/pkg/logger"
	"github.com/YuriB9/idp/pkg/temporallog"
)

// projectsServer — скелет реализации ProjectsService.
type projectsServer struct {
	projectsv1.UnimplementedProjectsServiceServer
}

func (s *projectsServer) GetService(_ context.Context, _ *projectsv1.GetServiceRequest) (*projectsv1.GetServiceResponse, error) {
	// Каркас: доменная логика каталога — отдельный change.
	return nil, status.Error(codes.Unimplemented, "GetService not implemented yet")
}

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
	projectsv1.RegisterProjectsServiceServer(grpcSrv, &projectsServer{})

	lis, err := net.Listen("tcp", config.String("GRPC_ADDR", ":9090"))
	if err != nil {
		return err
	}

	httpSrv := httpserver.New(httpserver.Config{
		Addr:   config.String("HTTP_ADDR", ":8082"),
		Logger: log,
		ReadinessChecks: []httpserver.ReadinessCheck{
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
