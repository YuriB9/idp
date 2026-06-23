//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// Интеграционные тесты воркфлоу против РЕАЛЬНОГО GitLab (ADR-0019). Активируются
// только при заданных GITLAB_API_URL и GITLAB_TOKEN (профиль реального GitLab,
// `make gitlab`); иначе пропускаются — прогон историй против моков (`make e2e`) не
// затрагивается. Гоняют воркфлоу через периметр (переиспользуя харнесс) И ассертят
// фактическое состояние в GitLab через GitLab API.

// gitlabAPIURL — публикуемый адрес реального GitLab для ассертов через GitLab API.
func gitlabAPIURL() string { return env("GITLAB_API_URL", "") }

// gitlabToken — root-PAT, выданный сидом стенда (передаётся из Makefile).
func gitlabToken() string { return env("GITLAB_TOKEN", "") }

// gitlabConfigured сообщает, сконфигурирован ли профиль реального GitLab.
func gitlabConfigured() bool { return gitlabAPIURL() != "" && gitlabToken() != "" }

// gitlabStatusTimeout — бюджет ожиданий со стороны GitLab (старт медленный).
func gitlabStatusTimeout() time.Duration {
	d, err := time.ParseDuration(env("GITLAB_STATUS_TIMEOUT", "600s"))
	if err != nil {
		return 600 * time.Second
	}
	return d
}

// requireGitLab пропускает тест без профиля реального GitLab и дожидается готовности
// GitLab API (без фиксированных sleep сверх интервала опроса).
func requireGitLab(t *testing.T) {
	t.Helper()
	requireStack(t)
	if !gitlabConfigured() {
		t.Skip("GITLAB_API_URL/GITLAB_TOKEN не заданы; прогон только против реального GitLab (make gitlab)")
	}
	deadline := time.Now().Add(gitlabStatusTimeout())
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, gitlabAPIURL()+"/-/health", nil)
		resp, err := httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("GitLab API не готов за %s", gitlabStatusTimeout())
}

// gitlabProject — подмножество ответа GET /api/v4/projects/:id реального GitLab.
type gitlabProject struct {
	ID        int  `json:"id"`
	Archived  bool `json:"archived"`
	Namespace struct {
		FullPath string `json:"full_path"`
	} `json:"namespace"`
}

// gitlabClient — клиент для ассертов через GitLab API, НЕ следующий за редиректами:
// после переноса GitLab отдаёт на старый путь проекта 301 на канонический URL с
// внутренним хостом (external_url), который с хоста не резолвится. Нам редирект сам
// по себе и есть сигнал «по этому пути проекта больше нет».
var gitlabClient = &http.Client{
	Timeout:       15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// glGet выполняет GET к GitLab API с PRIVATE-TOKEN; возвращает код и тело.
func glGet(t *testing.T, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, gitlabAPIURL()+path, nil)
	if err != nil {
		t.Fatalf("сборка запроса GitLab %s: %v", path, err)
	}
	req.Header.Set("PRIVATE-TOKEN", gitlabToken())
	resp, err := gitlabClient.Do(req)
	if err != nil {
		t.Fatalf("запрос GitLab %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение ответа GitLab %s: %v", path, err)
	}
	return resp.StatusCode, body
}

// glProject читает проект GitLab по пути group/name. found=true только при 200; 404
// (нет проекта) и 3xx (путь-редирект после переноса — проекта по этому пути уже нет)
// дают found=false.
func glProject(t *testing.T, group, name string) (gitlabProject, bool) {
	t.Helper()
	code, body := glGet(t, fmt.Sprintf("/api/v4/projects/%s%%2F%s", group, name))
	if code == http.StatusNotFound || (code >= 300 && code < 400) {
		return gitlabProject{}, false
	}
	if code != http.StatusOK {
		t.Fatalf("GitLab GET project %s/%s: код %d (%s)", group, name, code, string(body))
	}
	var p gitlabProject
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("разбор проекта GitLab: %v", err)
	}
	return p, true
}

// glVariableExists сообщает, задана ли CI/CD-переменная key репозитория group/name.
func glVariableExists(t *testing.T, group, name, key string) bool {
	t.Helper()
	code, _ := glGet(t, fmt.Sprintf("/api/v4/projects/%s%%2F%s/variables/%s", group, name, key))
	return code == http.StatusOK
}

// TestGitLabIntegrationCreate проверяет, что воркфлоу создания РЕАЛЬНО создаёт
// репозиторий в группе demo и задаёт CI/CD-переменные (ассерт через GitLab API).
func TestGitLabIntegrationCreate(t *testing.T) {
	requireGitLab(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("gl-create")

	mustCreateActive(t, token, projectDemo, name)

	p, ok := glProject(t, projectDemo, name)
	if !ok {
		t.Fatalf("репозиторий %s/%s не найден в GitLab после создания", projectDemo, name)
	}
	if p.Namespace.FullPath != projectDemo {
		t.Fatalf("репозиторий не в группе %q: namespace=%q", projectDemo, p.Namespace.FullPath)
	}
	if !glVariableExists(t, projectDemo, name, "VAULT_ROLE_ID") {
		t.Fatalf("CI/CD-переменная VAULT_ROLE_ID не задана в репозитории %s/%s", projectDemo, name)
	}
}

// TestGitLabIntegrationDecommission проверяет, что воркфлоу вывода из эксплуатации
// РЕАЛЬНО архивирует репозиторий (archived=true через GitLab API).
func TestGitLabIntegrationDecommission(t *testing.T) {
	requireGitLab(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("gl-decom")

	mustCreateActive(t, token, projectDemo, name)

	res := callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services/"+name+"/decommission",
		map[string]any{"load_drained": true})
	if res.status != http.StatusOK {
		t.Fatalf("decommissionService: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
	waitForStatus(t, token, projectDemo, name, "decommissioned")

	p, ok := glProject(t, projectDemo, name)
	if !ok {
		t.Fatalf("репозиторий %s/%s исчез (ожидалась архивация, не удаление)", projectDemo, name)
	}
	if !p.Archived {
		t.Fatalf("репозиторий %s/%s не архивирован после decommission", projectDemo, name)
	}
}

// TestGitLabIntegrationTransfer проверяет, что воркфлоу переноса РЕАЛЬНО переносит
// репозиторий в группу demo2 и убирает из demo (через GitLab API).
func TestGitLabIntegrationTransfer(t *testing.T) {
	requireGitLab(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("gl-xfer")

	mustCreateActive(t, token, projectDemo, name)

	res := callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services/"+name+"/transfer",
		map[string]any{"target_project": projectDemo2})
	if res.status != http.StatusOK {
		t.Fatalf("transferService: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
	waitForStatus(t, token, projectDemo2, name, "active")

	p, ok := glProject(t, projectDemo2, name)
	if !ok {
		t.Fatalf("репозиторий %s/%s не найден в целевой группе после переноса", projectDemo2, name)
	}
	if p.Namespace.FullPath != projectDemo2 {
		t.Fatalf("репозиторий не в группе %q после переноса: namespace=%q", projectDemo2, p.Namespace.FullPath)
	}
	if _, stillInOld := glProject(t, projectDemo, name); stillInOld {
		t.Fatalf("репозиторий %s/%s остался в исходной группе после переноса", projectDemo, name)
	}
}
