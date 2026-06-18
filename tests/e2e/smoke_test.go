//go:build integration

// Package e2e содержит сквозные тесты против docker-compose стенда.
// Запускаются только с build-тегом integration (отдельный CI-джоб), не в
// дефолтном прогоне (docs/IDP_MVP_plan.md, БЛОК 7).
package e2e

import (
	"net/http"
	"os"
	"testing"
	"time"
)

// TestPerimeterHealthz проверяет, что периметр (gateway через Oauth2-Proxy или
// напрямую) отвечает на /healthz. Адрес задаётся GATEWAY_BASE_URL.
func TestPerimeterHealthz(t *testing.T) {
	t.Parallel()
	base := os.Getenv("GATEWAY_BASE_URL")
	if base == "" {
		t.Skip("GATEWAY_BASE_URL not set; run against docker-compose stack")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("healthz request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}
