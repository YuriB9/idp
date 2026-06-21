package identity_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/goleak"

	"github.com/YuriB9/idp/services/idm/internal/identity"
)

// TestMain ловит утечки горутин (singleflight в фасаде использует горутины).
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// kcStub — httptest-стаб Keycloak Admin REST: token-эндпоинт + users.
type kcStub struct {
	srv         *httptest.Server
	tokenCalls  int
	failToken   bool   // token-эндпоинт отвечает 503
	failUsers   bool   // users-эндпоинт отвечает 503
	lastFirst   string // последний first из запроса поиска
	lastMax     string // последний max из запроса поиска
	lastSecret  string // последний переданный client_secret (проверка, что секрет не пуст)
	usersByID   map[string]string
	searchUsers string // JSON-массив для ответа поиска
}

func newKCStub(t *testing.T) *kcStub {
	t.Helper()
	s := &kcStub{usersByID: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/realms/idp/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		s.tokenCalls++
		_ = r.ParseForm()
		s.lastSecret = r.Form.Get("client_secret")
		if s.failToken {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-123","expires_in":300}`))
	})
	mux.HandleFunc("/admin/realms/idp/users/", func(w http.ResponseWriter, r *http.Request) {
		if s.failUsers {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/admin/realms/idp/users/")
		body, ok := s.usersByID[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/admin/realms/idp/users", func(w http.ResponseWriter, r *http.Request) {
		if s.failUsers {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		s.lastFirst = r.URL.Query().Get("first")
		s.lastMax = r.URL.Query().Get("max")
		w.Header().Set("Content-Type", "application/json")
		if s.searchUsers == "" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(s.searchUsers))
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func newClient(t *testing.T, s *kcStub) *identity.KeycloakClient {
	t.Helper()
	// Guarded=false: httptest на 127.0.0.1 + http — как локалка (SSRF_DISABLED).
	c, err := identity.NewKeycloakClient(identity.Config{
		BaseURL:      s.srv.URL,
		Realm:        "idp",
		ClientID:     "idm-sa",
		ClientSecret: "shh-secret",
		Guarded:      false,
	})
	if err != nil {
		t.Fatalf("NewKeycloakClient: %v", err)
	}
	return c
}

func TestSearch_HappyAndPagination(t *testing.T) {
	t.Parallel()
	s := newKCStub(t)
	s.searchUsers = `[{"id":"u-1","username":"ivan","email":"ivan@example.com","firstName":"Иван","lastName":"Петров","enabled":true}]`
	c := newClient(t, s)

	ids, err := c.Search(context.Background(), "iv", 20, 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(ids) != 1 || ids[0].Subject != "u-1" || ids[0].Username != "ivan" {
		t.Fatalf("неожиданный результат поиска: %+v", ids)
	}
	if ids[0].DisplayName != "Иван Петров" || !ids[0].Found || !ids[0].Enabled {
		t.Fatalf("неверный маппинг идентичности: %+v", ids[0])
	}
	if s.lastFirst != "20" || s.lastMax != "20" {
		t.Fatalf("offset не проброшен в Keycloak: first=%s max=%s", s.lastFirst, s.lastMax)
	}
	if s.lastSecret != "shh-secret" {
		t.Fatalf("секрет сервис-аккаунта не передан в token-эндпоинт")
	}
}

func TestResolve_FoundAndOrphan(t *testing.T) {
	t.Parallel()
	s := newKCStub(t)
	s.usersByID["u-1"] = `{"id":"u-1","username":"ivan","email":"ivan@example.com","enabled":true}`
	c := newClient(t, s)

	ids, err := c.Resolve(context.Background(), []string{"u-1", "u-missing"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ожидали 2 записи, got %d", len(ids))
	}
	if !ids[0].Found || ids[0].Subject != "u-1" {
		t.Fatalf("первый субъект должен быть найден: %+v", ids[0])
	}
	if ids[1].Found || ids[1].Subject != "u-missing" {
		t.Fatalf("второй субъект должен быть осиротевшим: %+v", ids[1])
	}
}

func TestSearch_KeycloakDown_Unavailable(t *testing.T) {
	t.Parallel()
	s := newKCStub(t)
	s.failUsers = true
	c := newClient(t, s)

	_, err := c.Search(context.Background(), "iv", 0, 20)
	if !identity.IsUnavailable(err) {
		t.Fatalf("ожидали ErrUnavailable, got %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "shh-secret") {
		t.Fatalf("секрет не должен попадать в ошибку: %v", err)
	}
}

func TestResolve_TokenFails_Unavailable(t *testing.T) {
	t.Parallel()
	s := newKCStub(t)
	s.failToken = true
	c := newClient(t, s)

	_, err := c.Resolve(context.Background(), []string{"u-1"})
	if !identity.IsUnavailable(err) {
		t.Fatalf("ожидали ErrUnavailable при провале токена, got %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "shh-secret") {
		t.Fatalf("секрет не должен попадать в ошибку: %v", err)
	}
}

func TestToken_Reused(t *testing.T) {
	t.Parallel()
	s := newKCStub(t)
	s.usersByID["u-1"] = `{"id":"u-1","username":"ivan","enabled":true}`
	c := newClient(t, s)

	for i := 0; i < 3; i++ {
		if _, err := c.Resolve(context.Background(), []string{"u-1"}); err != nil {
			t.Fatalf("Resolve #%d: %v", i, err)
		}
	}
	if s.tokenCalls != 1 {
		t.Fatalf("токен должен переиспользоваться: вызовов token=%d", s.tokenCalls)
	}
}
