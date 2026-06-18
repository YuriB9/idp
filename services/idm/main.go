// Command idm — сервис прав/ролей (минимум): gRPC AccessService.CheckAccess,
// Postgres + кэш DragonflyDB (ADR-0003). В этом изменении — скелет: gRPC-сервер
// и health, без реального RBAC.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	"github.com/YuriB9/idp/pkg/auth"
	"github.com/YuriB9/idp/pkg/config"
	"github.com/YuriB9/idp/pkg/grpcx"
	"github.com/YuriB9/idp/pkg/httpserver"
	"github.com/YuriB9/idp/pkg/logger"
)

// accessServer — скелет реализации AccessService. Доменный RBAC — отдельный change.
type accessServer struct {
	idmv1.UnimplementedAccessServiceServer
}

func (s *accessServer) CheckAccess(_ context.Context, _ *idmv1.CheckAccessRequest) (*idmv1.CheckAccessResponse, error) {
	// Каркас: целевой интерфейс задан, доменная логика — позже.
	return nil, status.Error(codes.Unimplemented, "CheckAccess not implemented yet")
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

	grpcSrv := grpc.NewServer(grpcx.ServerOptions(log, verifier)...)
	idmv1.RegisterAccessServiceServer(grpcSrv, &accessServer{})

	lis, err := net.Listen("tcp", config.String("GRPC_ADDR", ":9090"))
	if err != nil {
		return err
	}

	httpSrv := httpserver.New(httpserver.Config{
		Addr:   config.String("HTTP_ADDR", ":8081"),
		Logger: log,
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
