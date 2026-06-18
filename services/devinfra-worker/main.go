// Command devinfra-worker — единственный worker MVP: исполняет Temporal
// activities против GitLab/Vault/Harbor (ADR-0001). В этом изменении — скелет:
// процесс worker'а, регистрация task-queue и health с сигналом живости;
// сами activities/workflow — отдельные changes.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"golang.org/x/sync/errgroup"

	"github.com/YuriB9/idp/pkg/config"
	"github.com/YuriB9/idp/pkg/httpserver"
	"github.com/YuriB9/idp/pkg/logger"
	"github.com/YuriB9/idp/pkg/temporallog"
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

	temporalClient, err := client.NewLazyClient(client.Options{
		HostPort:  config.String("TEMPORAL_HOSTPORT", "temporal:7233"),
		Namespace: config.String("TEMPORAL_NAMESPACE", "default"),
		Logger:    temporallog.New(log),
	})
	if err != nil {
		return err
	}
	defer temporalClient.Close()

	taskQueue := config.String("TEMPORAL_TASK_QUEUE", "devinfra")
	w := worker.New(temporalClient, taskQueue, worker.Options{})
	// Регистрация workflow/activities — в доменных changes.

	// alive отражает, что worker запущен (k8s не должен слать трафик в мёртвый под).
	var alive atomic.Bool

	httpSrv := httpserver.New(httpserver.Config{
		Addr:   config.String("HTTP_ADDR", ":8083"),
		Logger: log,
		ReadinessChecks: []httpserver.ReadinessCheck{
			{
				Name: "worker",
				Check: func(_ context.Context) error {
					if !alive.Load() {
						return errors.New("worker not started")
					}
					return nil
				},
			},
		},
	})

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		log.Info("devinfra-worker: starting", slog.String("task_queue", taskQueue))
		alive.Store(true)
		defer alive.Store(false)
		// worker.Run завершается при закрытии переданного канала.
		return w.Run(worker.InterruptCh())
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
