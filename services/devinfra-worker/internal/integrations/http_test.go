package integrations_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YuriB9/idp/services/devinfra-worker/internal/integrations"
	"github.com/YuriB9/idp/services/projects/provisioning"
)

// TestNewHTTPClients_SSRFBlocksPrivate проверяет, что при включённом guard
// base-URL на запрещённый (приватный/не-https) адрес отклоняется SSRF-guard —
// клиент не создаётся, запрос во внешнюю сеть не уходит.
func TestNewHTTPClients_SSRFBlocksPrivate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
	}{
		{name: "loopback http", url: "http://127.0.0.1:8080"},
		{name: "приватный 10/8 https", url: "https://10.0.0.5"},
		{name: "метаданные link-local", url: "https://169.254.169.254"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := integrations.NewHTTPClients(integrations.Config{
				GitLabBaseURL: tc.url,
				VaultBaseURL:  "https://vault.example.com",
				HarborBaseURL: "https://harbor.example.com",
				Guarded:       true,
			})
			if err == nil {
				t.Fatalf("ожидали отказ SSRF-guard для %q", tc.url)
			}
		})
	}
}

// TestGitLabHTTP_CreateRepoIdempotent проверяет, что 409 (репозиторий уже
// существует) трактуется как успех (идемпотентность), а guard выключен для
// http-сервера теста.
func TestGitLabHTTP_CreateRepoIdempotent(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Connection: close — без keep-alive, чтобы не оставлять фоновых горутин
		// транспорта (важно для goleak в этом пакете).
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusConflict) // уже существует
	}))
	t.Cleanup(srv.Close)

	clients, err := integrations.NewHTTPClients(integrations.Config{
		GitLabBaseURL: srv.URL,
		VaultBaseURL:  srv.URL,
		HarborBaseURL: srv.URL,
		Guarded:       false, // http-тест-сервер; guard выключен явно
	})
	if err != nil {
		t.Fatalf("NewHTTPClients: %v", err)
	}
	repo, err := clients.GitLab.CreateRepo(context.Background(), provisioning.ResourceRef{Project: "p1", Name: "svc"})
	if err != nil {
		t.Fatalf("CreateRepo при 409 должен быть успешным (идемпотентность): %v", err)
	}
	if repo.Path == "" {
		t.Fatal("ожидали непустой путь репозитория")
	}
}
