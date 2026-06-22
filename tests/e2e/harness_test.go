//go:build integration

// Package e2e содержит сквозные тесты 4 user stories против docker-compose
// стенда с РЕАЛЬНЫМ OIDC (Keycloak + Oauth2-Proxy), ADR-0018. Запускаются только
// с build-тегом integration (локальный ручной прогон через `make e2e-*`), не в
// дефолтном прогоне (docs/IDP_MVP_plan.md, БЛОК 7).
//
// Этот файл — харнесс: получение токена (password-grant idp-portal), HTTP-клиент
// периметра через oauth2-proxy, ожидание готовности стенда и переходов статусов
// воркфлоу ретраи-поллингом (без sleep), генерация уникальных имён для изоляции.
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Демо-проекты и пользователь из realm-фикстуры (deploy/keycloak/idp-realm.json)
// и сидов RBAC (services/idm/migrations). Субъект dev имеет на project:demo права
// create/read/list/change_owners/decommission/transfer, а transfer_in — на
// project:demo2 (миграции 0003-0005).
const (
	realmClientID     = "idp-portal"
	realmClientSecret = "idp-portal-secret"
	userDev           = "dev"
	projectDemo       = "demo"
	projectDemo2      = "demo2"
)

// env читает переменную окружения с дефолтом.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// proxyBaseURL — база периметра через oauth2-proxy (:4180); перименетр живёт под
// префиксом /api (gateway монтирует доменные ручки там).
func proxyBaseURL() string { return strings.TrimRight(env("E2E_PROXY_URL", ""), "/") }

// keycloakBaseURL — база Keycloak для получения токена (token endpoint).
func keycloakBaseURL() string {
	return strings.TrimRight(env("E2E_KEYCLOAK_URL", "http://localhost:8088"), "/")
}

// statusTimeout — бюджет ожидания терминального статуса воркфлоу.
func statusTimeout() time.Duration {
	d, err := time.ParseDuration(env("E2E_STATUS_TIMEOUT", "120s"))
	if err != nil {
		return 120 * time.Second
	}
	return d
}

// httpClient — общий клиент с разумным таймаутом на запрос.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// fetchIDToken получает id_token пользователя через password-grant клиента
// idp-portal (directAccessGrantsEnabled=true). id_token используется как Bearer:
// его aud=idp-portal принимается и oauth2-proxy (skip_jwt_bearer_tokens), и
// gateway (AUTH_AUDIENCE=idp-portal). Креды — только из realm-фикстуры.
func fetchIDToken(t *testing.T, username, password string) string {
	t.Helper()
	form := url.Values{
		"grant_type":    {"password"},
		"client_id":     {realmClientID},
		"client_secret": {realmClientSecret},
		"username":      {username},
		"password":      {password},
		"scope":         {"openid"},
	}
	endpoint := keycloakBaseURL() + "/realms/idp/protocol/openid-connect/token"
	resp, err := httpClient.PostForm(endpoint, form)
	if err != nil {
		t.Fatalf("запрос токена Keycloak (%s): %v", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("токен Keycloak: код %d, тело %s", resp.StatusCode, string(body))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		t.Fatalf("разбор ответа токена: %v", err)
	}
	if tok.IDToken == "" {
		t.Fatalf("Keycloak не вернул id_token (нужен scope=openid)")
	}
	return tok.IDToken
}

// apiResult — разобранный ответ периметра.
type apiResult struct {
	status int
	body   []byte
}

// decode разбирает JSON-тело ответа в out (если задан).
func (r apiResult) decode(t *testing.T, out any) {
	t.Helper()
	if out == nil {
		return
	}
	if err := json.Unmarshal(r.body, out); err != nil {
		t.Fatalf("разбор тела ответа (%s): %v", string(r.body), err)
	}
}

// callAPI выполняет запрос к периметру через oauth2-proxy с Bearer-токеном.
// path — относительный путь под /api (например, "/projects/demo/services").
func callAPI(t *testing.T, token, method, path string, body any) apiResult {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal тела запроса: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	reqURL := proxyBaseURL() + "/api" + path
	req, err := http.NewRequest(method, reqURL, rdr)
	if err != nil {
		t.Fatalf("сборка запроса %s %s: %v", method, reqURL, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("запрос %s %s: %v", method, reqURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return apiResult{status: resp.StatusCode, body: b}
}

// serviceSummary — подмножество ServiceSummary периметра (openapi/openapi.yaml).
type serviceSummary struct {
	Project       string   `json:"project"`
	Name          string   `json:"name"`
	Status        string   `json:"status"`
	Owners        []string `json:"owners"`
	OwnersVersion int64    `json:"owners_version"`
}

// getService читает текущее состояние сервиса; возвращает summary и HTTP-код.
func getService(t *testing.T, token, project, name string) (serviceSummary, int) {
	t.Helper()
	res := callAPI(t, token, http.MethodGet, "/projects/"+project+"/services/"+name, nil)
	var s serviceSummary
	if res.status == http.StatusOK {
		res.decode(t, &s)
	}
	return s, res.status
}

// waitForStatus поллит getService, пока статус не станет want, либо пока не
// встретит терминальный failed (при ожидании не-failed → немедленный провал с
// диагностикой), либо пока не истечёт бюджет. Без sleep-констант: интервал ~500ms.
func waitForStatus(t *testing.T, token, project, name, want string) serviceSummary {
	t.Helper()
	deadline := time.Now().Add(statusTimeout())
	var last serviceSummary
	var lastCode int
	for time.Now().Before(deadline) {
		s, code := getService(t, token, project, name)
		last, lastCode = s, code
		if code == http.StatusOK {
			if s.Status == want {
				return s
			}
			// Терминальный failed при ожидании другого статуса — не ждём весь бюджет.
			if s.Status == "failed" && want != "failed" {
				t.Fatalf("сервис %s/%s перешёл в failed, ожидался %q", project, name, want)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("таймаут ожидания статуса %q для %s/%s за %s (последний код=%d, статус=%q)",
		want, project, name, statusTimeout(), lastCode, last.Status)
	return last
}

// waitForOwnersVersion поллит getService, пока версия владельцев не достигнет
// want. Изменение владельцев применяется асинхронно (воркфлоу смены владельцев,
// ADR-0011): периметр отвечает 200 «команда принята», а каталог обновляется
// позже — поэтому ждём отражения ретраи-поллингом, не sleep.
func waitForOwnersVersion(t *testing.T, token, project, name string, want int64) serviceSummary {
	t.Helper()
	deadline := time.Now().Add(statusTimeout())
	var last serviceSummary
	for time.Now().Before(deadline) {
		s := getServiceOK(t, token, project, name)
		last = s
		if s.OwnersVersion == want {
			return s
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("таймаут ожидания owners_version=%d для %s/%s за %s (последняя=%d)",
		want, project, name, statusTimeout(), last.OwnersVersion)
	return last
}

// uniqueName возвращает уникальное имя сервиса для изоляции параллельных историй
// (детерминированный, но различный WorkflowID). Только [a-z0-9-]: префикс +
// случайный hex.
func uniqueName(prefix string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b[:]))
}

// waitReady дожидается готовности всей цепочки периметра: Keycloak выдаёт токен,
// и периметр через oauth2-proxy+gateway отвечает 200 на listServices (это
// проверяет OIDC, проверку JWT в gateway и RBAC в IDM). Возвращает ошибку по
// истечении бюджета.
func waitReady(ctx context.Context) error {
	deadline := time.Now().Add(statusTimeout())
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		token, err := tryToken(userDev, userDev)
		if err != nil {
			lastErr = err
			time.Sleep(time.Second)
			continue
		}
		code, err := tryListServices(token, projectDemo)
		if err != nil {
			lastErr = err
			time.Sleep(time.Second)
			continue
		}
		if code == http.StatusOK {
			return nil
		}
		lastErr = fmt.Errorf("listServices вернул код %d", code)
		time.Sleep(time.Second)
	}
	return fmt.Errorf("стенд не готов за %s: %w", statusTimeout(), lastErr)
}

// tryToken — версия получения токена без *testing.T (для waitReady).
func tryToken(username, password string) (string, error) {
	form := url.Values{
		"grant_type":    {"password"},
		"client_id":     {realmClientID},
		"client_secret": {realmClientSecret},
		"username":      {username},
		"password":      {password},
		"scope":         {"openid"},
	}
	resp, err := httpClient.PostForm(keycloakBaseURL()+"/realms/idp/protocol/openid-connect/token", form)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("токен: код %d", resp.StatusCode)
	}
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", err
	}
	if tok.IDToken == "" {
		return "", fmt.Errorf("пустой id_token")
	}
	return tok.IDToken, nil
}

// tryListServices — версия listServices без *testing.T (для waitReady).
func tryListServices(token, project string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, proxyBaseURL()+"/api/projects/"+project+"/services", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// TestMain ждёт готовности стенда перед прогоном (если задан E2E_PROXY_URL). Без
// E2E_PROXY_URL набор пропускается целиком (каждый тест вызывает requireStack) —
// это позволяет компилировать пакет в CI (-tags=integration) без живого стенда.
func TestMain(m *testing.M) {
	if proxyBaseURL() != "" {
		ctx := context.Background()
		if err := waitReady(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "E2E: стенд не готов: %v\n", err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// requireStack пропускает тест, если E2E-стенд не сконфигурирован (нет
// E2E_PROXY_URL). Применяется в каждом сквозном тесте.
func requireStack(t *testing.T) {
	t.Helper()
	if proxyBaseURL() == "" {
		t.Skip("E2E_PROXY_URL не задан; прогон только против поднятого стенда (make e2e)")
	}
}

// mustCreateActive создаёт сервис и дожидается перехода в active. Используется
// сценариями, которым нужен заранее готовый активный сервис.
func mustCreateActive(t *testing.T, token, project, name string) {
	t.Helper()
	res := callAPI(t, token, http.MethodPost, "/projects/"+project+"/services", map[string]string{"name": name})
	if res.status != http.StatusCreated {
		t.Fatalf("createService(%s/%s): ожидался 201, получен %d (%s)", project, name, res.status, string(res.body))
	}
	waitForStatus(t, token, project, name, "active")
}

// getServiceOK читает сервис и проваливает тест, если код ответа не 200.
func getServiceOK(t *testing.T, token, project, name string) serviceSummary {
	t.Helper()
	s, code := getService(t, token, project, name)
	if code != http.StatusOK {
		t.Fatalf("getService(%s/%s): ожидался 200, получен %d", project, name, code)
	}
	return s
}

// sameSet сравнивает два набора строк без учёта порядка (владельцы возвращаются в
// детерминированном, но не обязательно совпадающем с запросом порядке).
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]int, len(a))
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}
