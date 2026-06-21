// Command gateway — тонкий API-шлюз периметра: HTTP-роутер (chi) для портала
// и gRPC-клиенты к IDM и сервису проектов (ADR-0002, ADR-0003, ADR-0009).
// Доменные REST-ручки сервисов (создание/чтение/листинг) реализованы в
// handlers.go поверх gRPC-клиента projectsv1.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	projectsv1 "github.com/YuriB9/idp/pkg/api/projects/v1"
	"github.com/YuriB9/idp/pkg/auth"
	"github.com/YuriB9/idp/pkg/config"
	"github.com/YuriB9/idp/pkg/httpserver"
	"github.com/YuriB9/idp/pkg/logger"
)

func main() {
	if err := run(); err != nil {
		slog.Default().Error("gateway: fatal", logger.Err(err))
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log := logger.New(logger.Options{Level: config.String("LOG_LEVEL", "info"), JSON: true})
	verifier := auth.MustVerifierFromEnv(ctx, log)

	idmConn, err := grpc.NewClient(config.String("IDM_GRPC_ADDR", "idm:9090"),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = idmConn.Close() }()
	projectsConn, err := grpc.NewClient(config.String("PROJECTS_GRPC_ADDR", "projects:9090"),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = projectsConn.Close() }()

	// IDM-клиенты — RBAC CheckAccess перед доменными ручками (fail-closed), чтение
	// каталога IAM-админки (IamAdmin) и управление ролями (RoleAdmin).
	// Клиент projects — основа REST-ручек периметра.
	services := &servicesAPI{
		client:     projectsv1.NewProjectsServiceClient(projectsConn),
		idm:        idmv1.NewAccessServiceClient(idmConn),
		iamAdmin:   idmv1.NewIamAdminServiceClient(idmConn),
		iamCatalog: idmv1.NewIamCatalogServiceClient(idmConn),
		roleAdmin:  idmv1.NewRoleAdminServiceClient(idmConn),
		identity:   idmv1.NewIdentityServiceClient(idmConn),
		log:        log,
	}

	router := chi.NewRouter()
	router.Use(httpserver.RequestID)
	router.Use(httpserver.Recoverer(log))
	router.Use(httpserver.RateLimit(100, 50))
	router.Route("/api", func(r chi.Router) {
		r.Use(httpserver.Auth(verifier, log))
		// Доменные ресурсы периметра (создание/чтение/листинг сервисов).
		services.register(r)
	})

	srv := httpserver.New(httpserver.Config{
		Addr:    config.String("HTTP_ADDR", ":8080"),
		Handler: router,
		Logger:  log,
		ReadinessChecks: []httpserver.ReadinessCheck{
			grpcReadiness("idm", idmConn),
			grpcReadiness("projects", projectsConn),
		},
	})

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return srv.Run(gctx) })
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// grpcReadiness строит проверку готовности по состоянию gRPC-соединения.
func grpcReadiness(name string, conn *grpc.ClientConn) httpserver.ReadinessCheck {
	return httpserver.ReadinessCheck{
		Name: name,
		Check: func(_ context.Context) error {
			conn.Connect()
			switch conn.GetState() {
			case connectivity.Ready, connectivity.Idle:
				return nil
			default:
				return errors.New("grpc not ready")
			}
		},
	}
}
