package integrations_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/YuriB9/idp/services/devinfra-worker/internal/integrations"
	"github.com/YuriB9/idp/services/projects/provisioning"
)

// recordedReq — зафиксированный фейковым GitLab запрос (метод, экранированный путь,
// разобранное JSON-тело, наличие токена аутентификации).
type recordedReq struct {
	method   string
	path     string
	body     map[string]any
	hasToken bool
}

// fakeGitLab — минимальный фейковый GitLab для проверки формы исходящих запросов
// реального пути клиента. Маршрутизирует по методу+экранированному пути и отдаёт
// заранее заданные ответы; фиксирует все запросы под мьютексом.
type fakeGitLab struct {
	t        *testing.T
	mu       sync.Mutex
	reqs     []recordedReq
	handlers map[string]func(w http.ResponseWriter, body map[string]any)
}

func newFakeGitLab(t *testing.T) *fakeGitLab {
	return &fakeGitLab{t: t, handlers: map[string]func(http.ResponseWriter, map[string]any){}}
}

// on регистрирует ответ на «METHOD ESCAPED_PATH».
func (f *fakeGitLab) on(method, path string, h func(w http.ResponseWriter, body map[string]any)) {
	f.handlers[method+" "+path] = h
}

func (f *fakeGitLab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Connection", "close") // без keep-alive: важно для goleak
	var body map[string]any
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
	}
	path := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, recordedReq{
		method:   r.Method,
		path:     path,
		body:     body,
		hasToken: r.Header.Get("PRIVATE-TOKEN") != "",
	})
	f.mu.Unlock()

	if h, ok := f.handlers[r.Method+" "+path]; ok {
		h(w, body)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (f *fakeGitLab) recorded() []recordedReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedReq, len(f.reqs))
	copy(out, f.reqs)
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// newRealGitLab собирает клиента интеграций в режиме реального GitLab (токен задан)
// против фейкового сервера; guard выключен (http-тест-сервер).
func newRealGitLab(t *testing.T, srvURL string, owners map[string]string) *integrations.Clients {
	t.Helper()
	clients, err := integrations.NewHTTPClients(integrations.Config{
		GitLabBaseURL:     srvURL,
		VaultBaseURL:      srvURL,
		HarborBaseURL:     srvURL,
		GitLabToken:       "test-pat",
		GitLabOwnerLogins: owners,
		Guarded:           false,
	})
	if err != nil {
		t.Fatalf("NewHTTPClients: %v", err)
	}
	return clients
}

// TestGitLabReal_CreateRepoResolvesNamespace проверяет, что реальный путь CreateRepo
// аутентифицируется PRIVATE-TOKEN, резолвит группу в namespace_id и шлёт его числом.
func TestGitLabReal_CreateRepoResolvesNamespace(t *testing.T) {
	t.Parallel()
	f := newFakeGitLab(t)
	f.on(http.MethodGet, "/api/v4/projects/demo%2Fsvc", func(w http.ResponseWriter, _ map[string]any) {
		w.WriteHeader(http.StatusNotFound) // ещё не существует
	})
	f.on(http.MethodGet, "/api/v4/groups/demo", func(w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, http.StatusOK, map[string]any{"id": 42})
	})
	f.on(http.MethodPost, "/api/v4/projects", func(w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, http.StatusCreated, map[string]any{"id": 100})
	})
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	clients := newRealGitLab(t, srv.URL, nil)
	if _, err := clients.GitLab.CreateRepo(context.Background(), provisioning.ResourceRef{Project: "demo", Name: "svc"}); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}

	var post *recordedReq
	for i := range f.recorded() {
		r := f.recorded()[i]
		if !r.hasToken {
			t.Fatalf("запрос %s %s без PRIVATE-TOKEN", r.method, r.path)
		}
		if r.method == http.MethodPost && r.path == "/api/v4/projects" {
			rr := r
			post = &rr
		}
	}
	if post == nil {
		t.Fatal("ожидался POST /api/v4/projects")
	}
	if got, ok := post.body["namespace_id"]; !ok || got.(float64) != 42 {
		t.Fatalf("ожидался namespace_id=42 (число), тело=%v", post.body)
	}
	if _, hasStr := post.body["namespace"]; hasStr {
		t.Fatalf("реальный путь не должен слать строковый namespace, тело=%v", post.body)
	}
}

// TestGitLabReal_CreateRepoIdempotent проверяет, что существующий проект не пересоздаётся
// (GET-then-create): POST /projects не выполняется.
func TestGitLabReal_CreateRepoIdempotent(t *testing.T) {
	t.Parallel()
	f := newFakeGitLab(t)
	f.on(http.MethodGet, "/api/v4/projects/demo%2Fsvc", func(w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, http.StatusOK, map[string]any{"id": 100}) // уже существует
	})
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	clients := newRealGitLab(t, srv.URL, nil)
	if _, err := clients.GitLab.CreateRepo(context.Background(), provisioning.ResourceRef{Project: "demo", Name: "svc"}); err != nil {
		t.Fatalf("CreateRepo (идемпотентный): %v", err)
	}
	for _, r := range f.recorded() {
		if r.method == http.MethodPost {
			t.Fatalf("повторное создание не должно слать POST, получили %s %s", r.method, r.path)
		}
	}
}

// TestGitLabReal_SyncMembersResolvesUserID проверяет резолвинг владельца (UUID)→логин→
// user_id и числовой access_level; незаданный маппинг владельца пропускается.
func TestGitLabReal_SyncMembersResolvesUserID(t *testing.T) {
	t.Parallel()
	f := newFakeGitLab(t)
	f.on(http.MethodGet, "/api/v4/users?username=alice", func(w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, http.StatusOK, []map[string]any{{"id": 7}})
	})
	f.on(http.MethodPost, "/api/v4/projects/demo%2Fsvc/members", func(w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, http.StatusCreated, map[string]any{"id": 7})
	})
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	owners := map[string]string{"uuid-alice": "alice"} // "uuid-bob" намеренно не задан
	clients := newRealGitLab(t, srv.URL, owners)
	err := clients.GitLab.SyncMembers(context.Background(),
		provisioning.ResourceRef{Project: "demo", Name: "svc"}, []string{"uuid-alice", "uuid-bob"}, nil)
	if err != nil {
		t.Fatalf("SyncMembers: %v", err)
	}

	var posts int
	for _, r := range f.recorded() {
		if r.method == http.MethodPost && strings.HasSuffix(r.path, "/members") {
			posts++
			if got, ok := r.body["user_id"]; !ok || got.(float64) != 7 {
				t.Fatalf("ожидался user_id=7 (число), тело=%v", r.body)
			}
			if got, ok := r.body["access_level"]; !ok || got.(float64) != 40 {
				t.Fatalf("ожидался числовой access_level=40, тело=%v", r.body)
			}
			if _, hasName := r.body["username"]; hasName {
				t.Fatalf("реальный путь не должен слать username, тело=%v", r.body)
			}
		}
	}
	if posts != 1 {
		t.Fatalf("ожидался ровно 1 POST members (uuid-bob пропущен), получили %d", posts)
	}
}

// TestGitLabReal_InjectVariablesUpsert проверяет upsert переменных: отсутствующая →
// POST, существующая → PUT (GET-then-act), без зависимости от текста ошибки.
func TestGitLabReal_InjectVariablesUpsert(t *testing.T) {
	t.Parallel()
	f := newFakeGitLab(t)
	// VAULT_ROLE_ID существует → PUT; остальные отсутствуют → POST.
	existing := "VAULT_ROLE_ID"
	for _, key := range []string{"VAULT_ROLE_ID", "VAULT_SECRET_ID", "HARBOR_ROBOT_NAME", "HARBOR_ROBOT_TOKEN"} {
		varPath := "/api/v4/projects/demo%2Fsvc/variables/" + key
		if key == existing {
			f.on(http.MethodGet, varPath, func(w http.ResponseWriter, _ map[string]any) {
				writeJSON(w, http.StatusOK, map[string]any{"key": existing})
			})
			f.on(http.MethodPut, varPath, func(w http.ResponseWriter, _ map[string]any) {
				writeJSON(w, http.StatusOK, nil)
			})
			continue
		}
		f.on(http.MethodGet, varPath, func(w http.ResponseWriter, _ map[string]any) {
			w.WriteHeader(http.StatusNotFound)
		})
	}
	f.on(http.MethodPost, "/api/v4/projects/demo%2Fsvc/variables", func(w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, http.StatusCreated, nil)
	})
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	clients := newRealGitLab(t, srv.URL, nil)
	in := provisioning.InjectSecretsInput{
		Ref:    provisioning.ResourceRef{Project: "demo", Name: "svc"},
		GitLab: provisioning.GitLabRepo{Path: "demo/svc"},
		Vault:  provisioning.VaultResult{RoleID: "r", SecretID: "s"},
		Harbor: provisioning.HarborResult{RobotName: "robot", RobotToken: "tok"},
	}
	if err := clients.GitLab.InjectVariables(context.Background(), in); err != nil {
		t.Fatalf("InjectVariables: %v", err)
	}

	var puts, posts int
	for _, r := range f.recorded() {
		switch {
		case r.method == http.MethodPut:
			puts++
		case r.method == http.MethodPost && strings.HasSuffix(r.path, "/variables"):
			posts++
		}
	}
	if puts != 1 {
		t.Fatalf("ожидался 1 PUT (перезапись существующей), получили %d", puts)
	}
	if posts != 3 {
		t.Fatalf("ожидалось 3 POST (создание отсутствующих), получили %d", posts)
	}
}

// TestGitLabReal_TransferIdempotent проверяет, что перенос в уже целевую группу —
// no-op (по текущему namespace), без POST /transfer.
func TestGitLabReal_TransferIdempotent(t *testing.T) {
	t.Parallel()
	f := newFakeGitLab(t)
	f.on(http.MethodGet, "/api/v4/projects/demo%2Fsvc", func(w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, http.StatusOK, map[string]any{"namespace": map[string]any{"full_path": "demo2"}})
	})
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	clients := newRealGitLab(t, srv.URL, nil)
	if err := clients.GitLab.TransferRepo(context.Background(),
		provisioning.ResourceRef{Project: "demo", Name: "svc"}, "demo2"); err != nil {
		t.Fatalf("TransferRepo (идемпотентный): %v", err)
	}
	for _, r := range f.recorded() {
		if r.method == http.MethodPost {
			t.Fatalf("перенос в уже целевую группу не должен слать POST, получили %s %s", r.method, r.path)
		}
	}
}

// TestGitLabReal_TokenNotLeakedToOthers проверяет, что PRIVATE-TOKEN ставится на
// GitLab-запросы, но не на запросы Vault/Harbor (отдельный doer).
func TestGitLabReal_TokenScopedToGitLab(t *testing.T) {
	t.Parallel()
	var gotHarborToken, gotVaultToken bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		has := r.Header.Get("PRIVATE-TOKEN") != ""
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v2.0/"): // Harbor
			gotHarborToken = gotHarborToken || has
		case strings.HasPrefix(r.URL.Path, "/v1/"): // Vault
			gotVaultToken = gotVaultToken || has
		}
		writeJSON(w, http.StatusOK, map[string]any{})
	}))
	t.Cleanup(srv.Close)

	clients := newRealGitLab(t, srv.URL, nil)
	_ = clients.Harbor.DeleteProject(context.Background(), provisioning.ResourceRef{Project: "demo", Name: "svc"})
	_ = clients.Vault.TeardownAppRole(context.Background(), provisioning.ResourceRef{Project: "demo", Name: "svc"})
	if gotHarborToken {
		t.Fatal("PRIVATE-TOKEN не должен попадать в запросы Harbor")
	}
	if gotVaultToken {
		t.Fatal("PRIVATE-TOKEN не должен попадать в запросы Vault")
	}
}
