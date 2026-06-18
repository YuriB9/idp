package httpserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/goleak"

	"github.com/YuriB9/idp/pkg/httpserver"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestReadyz_AllChecksPass(t *testing.T) {
	t.Parallel()
	srv := httpserver.New(httpserver.Config{
		ReadinessChecks: []httpserver.ReadinessCheck{
			{Name: "ok", Check: func(context.Context) error { return nil }},
		},
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
}

func TestReadyz_FailingCheckReturns503(t *testing.T) {
	t.Parallel()
	srv := httpserver.New(httpserver.Config{
		ReadinessChecks: []httpserver.ReadinessCheck{
			{Name: "db", Check: func(context.Context) error { return errors.New("down") }},
		},
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rr.Code)
	}
}

func TestHealthz(t *testing.T) {
	t.Parallel()
	srv := httpserver.New(httpserver.Config{})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
}
