// Package httpserver предоставляет HTTP-сервер с едиными таймаутами, graceful
// shutdown и middleware-стеком (Recoverer, RequestID, rate-limit, auth-toggle,
// content-aware /readyz). См. docs/IDP_MVP_plan.md, общие pkg/*.
package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/YuriB9/idp/pkg/logger"
)

// drainTimeout — предел дренажа in-flight запросов при остановке.
const drainTimeout = 30 * time.Second

// ReadinessCheck — именованная проверка готовности (пинг зависимости).
type ReadinessCheck struct {
	Name  string
	Check func(ctx context.Context) error
}

// Config конфигурирует сервер.
type Config struct {
	Addr              string
	Handler           http.Handler
	Logger            *slog.Logger
	ReadinessChecks   []ReadinessCheck
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

// Server оборачивает http.Server с graceful shutdown.
type Server struct {
	httpSrv *http.Server
	log     *slog.Logger
}

// New создаёт сервер. /healthz и /readyz регистрируются поверх переданного
// Handler через внутренний mux.
func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.ReadHeaderTimeout == 0 {
		cfg.ReadHeaderTimeout = 5 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 15 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 30 * time.Second
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 60 * time.Second
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", readinessHandler(cfg.ReadinessChecks, cfg.Logger))
	if cfg.Handler != nil {
		mux.Handle("/", cfg.Handler)
	}

	return &Server{
		httpSrv: &http.Server{
			Addr:              cfg.Addr,
			Handler:           mux,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			ReadTimeout:       cfg.ReadTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
		},
		log: cfg.Logger,
	}
}

// Handler возвращает внутренний http.Handler (с /healthz, /readyz и
// пользовательскими маршрутами). Полезно для тестов через httptest.
func (s *Server) Handler() http.Handler { return s.httpSrv.Handler }

// Run запускает сервер и блокируется до отмены ctx, после чего выполняет
// graceful shutdown с дренажом in-flight в пределах drainTimeout. Drain-контекст
// строится через WithoutCancel, чтобы переживать отмену исходного ctx.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http: listening", slog.String("addr", s.httpSrv.Addr))
		if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.log.Info("http: shutting down")
		drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), drainTimeout)
		defer cancel()
		if err := s.httpSrv.Shutdown(drainCtx); err != nil {
			s.log.Error("http: graceful shutdown failed", logger.Err(err))
			return err
		}
		return nil
	}
}

// readinessHandler возвращает content-aware обработчик /readyz: пингует все
// зарегистрированные зависимости; при любой ошибке — 503 с именами упавших.
func readinessHandler(checks []ReadinessCheck, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		for _, c := range checks {
			if err := c.Check(ctx); err != nil {
				log.Warn("readyz: check failed", slog.String("check", c.Name), logger.Err(err))
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("not ready: " + c.Name))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}
