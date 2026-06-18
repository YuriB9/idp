// Command devinfra-worker — единственный worker MVP: исполняет Temporal
// workflow «Создание сервиса» и activities провизии против GitLab/Vault/Harbor
// (ADR-0001). Регистрирует workflow и activities на task-queue, реально поллит
// очередь и отражает живость в /readyz. Клиенты интеграций — за интерфейсами
// (реализация против моков локально); финальные переходы статуса каталога —
// guarded-CAS через общий пакет projects/catalog (ADR-0004, ADR-0008).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"golang.org/x/sync/errgroup"

	"github.com/YuriB9/idp/pkg/config"
	"github.com/YuriB9/idp/pkg/db"
	"github.com/YuriB9/idp/pkg/httpserver"
	"github.com/YuriB9/idp/pkg/logger"
	"github.com/YuriB9/idp/pkg/temporallog"
	"github.com/YuriB9/idp/services/devinfra-worker/internal/activities"
	"github.com/YuriB9/idp/services/devinfra-worker/internal/integrations"
	"github.com/YuriB9/idp/services/projects/catalog"
	"github.com/YuriB9/idp/services/projects/provisioning"
)

func main() {
	if err := run(); err != nil {
		slog.Default().Error("devinfra-worker: fatal", logger.Err(err))
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log := logger.New(logger.Options{Level: config.String("LOG_LEVEL", "info"), JSON: true})

	// Пул Postgres каталога проектов — нужен финальным activities перехода
	// статуса (CREATING→ACTIVE/FAILED) через guarded-CAS (ADR-0008).
	maxConns, err := config.Int("PG_MAX_CONNS", 5)
	if err != nil {
		return err
	}
	if maxConns <= 0 || maxConns > math.MaxInt32 {
		return fmt.Errorf("devinfra-worker: PG_MAX_CONNS вне допустимого диапазона: %d", maxConns)
	}
	pool, err := db.NewPool(ctx, db.PoolConfig{
		DSN:      config.String("PG_DSN", "postgres://projects:projects@postgres-projects:5432/projects?sslmode=disable"),
		MaxConns: int32(maxConns),
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	// Клиенты интеграций. SSRF_DISABLED=true (локалка с http-моками) выключает
	// SSRF-guard — единственный разрешённый способ; в проде guard включён всегда.
	ssrfDisabled, err := config.Bool("SSRF_DISABLED", false)
	if err != nil {
		return err
	}
	guarded := !ssrfDisabled
	clients, err := integrations.NewHTTPClients(integrations.Config{
		GitLabBaseURL: config.String("GITLAB_BASE_URL", "http://mock-gitlab:8080"),
		VaultBaseURL:  config.String("VAULT_BASE_URL", "http://mock-vault:8080"),
		HarborBaseURL: config.String("HARBOR_BASE_URL", "http://mock-harbor:8080"),
		Guarded:       guarded,
	})
	if err != nil {
		return err
	}
	acts := activities.New(clients, catalog.NewStatusStore(pool))

	temporalClient, err := client.NewLazyClient(client.Options{
		HostPort:  config.String("TEMPORAL_HOSTPORT", "temporal:7233"),
		Namespace: config.String("TEMPORAL_NAMESPACE", "default"),
		Logger:    temporallog.New(log),
	})
	if err != nil {
		return err
	}
	defer temporalClient.Close()

	taskQueue := config.String("TEMPORAL_TASK_QUEUE", provisioning.DefaultTaskQueue)
	w := worker.New(temporalClient, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(provisioning.CreateServiceWorkflow,
		workflow.RegisterOptions{Name: provisioning.WorkflowName})
	acts.Register(w)

	// alive отражает, что worker запущен и поллит task-queue (k8s не должен слать
	// трафик в мёртвый под). Готовность снимается при завершении worker.Run.
	var alive atomic.Bool

	httpSrv := httpserver.New(httpserver.Config{
		Addr:   config.String("HTTP_ADDR", ":8083"),
		Logger: log,
		ReadinessChecks: []httpserver.ReadinessCheck{
			{
				Name: "worker",
				Check: func(_ context.Context) error {
					if !alive.Load() {
						return errors.New("worker не поллит task-queue")
					}
					return nil
				},
			},
		},
	})

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		log.Info("devinfra-worker: starting", slog.String("task_queue", taskQueue))
		if err := w.Start(); err != nil {
			return err
		}
		alive.Store(true)
		defer alive.Store(false)
		<-gctx.Done()
		return nil
	})
	g.Go(func() error {
		<-gctx.Done()
		w.Stop()
		return nil
	})
	g.Go(func() error { return httpSrv.Run(gctx) })

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
