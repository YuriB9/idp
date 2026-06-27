//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// Интеграционные тесты воркфлоу против РЕАЛЬНОГО Harbor (ADR-0021). Активируются только
// при заданных HARBOR_ADDR/HARBOR_USERNAME/HARBOR_PASSWORD (профиль реального Harbor,
// `make harbor`); иначе пропускаются — прогон историй против моков (`make e2e`) и
// gitlab/vault-наборы не затрагиваются. Гоняют воркфлоу через периметр (переиспользуя
// харнесс) И ассертят фактическое состояние в Harbor через Harbor API.

// harborAPIURL — публикуемый адрес реального Harbor для ассертов через Harbor API.
func harborAPIURL() string { return env("HARBOR_ADDR", "") }

// harborUser/harborPass — admin-креденшелы стенда (передаются из Makefile).
func harborUser() string { return env("HARBOR_USERNAME", "") }
func harborPass() string { return env("HARBOR_PASSWORD", "") }

// harborConfigured сообщает, сконфигурирован ли профиль реального Harbor.
func harborConfigured() bool {
	return harborAPIURL() != "" && harborUser() != "" && harborPass() != ""
}

// harborStatusTimeout — бюджет ожиданий со стороны Harbor (связка контейнеров стартует
// не быстро — соразмерно GitLab).
func harborStatusTimeout() time.Duration {
	d, err := time.ParseDuration(env("HARBOR_STATUS_TIMEOUT", "600s"))
	if err != nil {
		return 600 * time.Second
	}
	return d
}

// requireHarbor пропускает тест без профиля реального Harbor и дожидается готовности
// Harbor API (без фиксированных sleep сверх интервала опроса).
func requireHarbor(t *testing.T) {
	t.Helper()
	requireStack(t)
	if !harborConfigured() {
		t.Skip("HARBOR_ADDR/HARBOR_USERNAME/HARBOR_PASSWORD не заданы; прогон только против реального Harbor (make harbor)")
	}
	deadline := time.Now().Add(harborStatusTimeout())
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, harborAPIURL()+"/api/v2.0/health", nil)
		resp, err := httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("Harbor API не готов за %s", harborStatusTimeout())
}

// harborClient — клиент для ассертов через Harbor API.
var harborClient = &http.Client{Timeout: 15 * time.Second}

// harborGet выполняет GET к Harbor API с HTTP Basic admin; возвращает код и тело.
func harborGet(t *testing.T, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, harborAPIURL()+path, nil)
	if err != nil {
		t.Fatalf("сборка запроса Harbor %s: %v", path, err)
	}
	req.SetBasicAuth(harborUser(), harborPass())
	resp, err := harborClient.Do(req)
	if err != nil {
		t.Fatalf("запрос Harbor %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := make([]byte, 0, 1024)
	buf := make([]byte, 1024)
	for {
		n, rerr := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if rerr != nil {
			break
		}
	}
	return resp.StatusCode, body
}

// harborProjectName собирает имя проекта Harbor так же, как клиент интеграции:
// <project>-<name> (директория образов сервиса).
func harborProjectName(project, name string) string { return project + "-" + name }

// harborProjectExists сообщает, существует ли проект Harbor (200 → true, 404 → false).
func harborProjectExists(t *testing.T, projName string) bool {
	t.Helper()
	code, body := harborGet(t, "/api/v2.0/projects/"+projName)
	switch code {
	case http.StatusOK:
		return true
	case http.StatusNotFound:
		return false
	default:
		t.Fatalf("Harbor GET project %s: неожиданный код %d (%s)", projName, code, string(body))
		return false
	}
}

// harborProjectMetadata возвращает карту metadata проекта Harbor (nil, если проекта нет).
func harborProjectMetadata(t *testing.T, projName string) map[string]string {
	t.Helper()
	code, body := harborGet(t, "/api/v2.0/projects/"+projName)
	if code != http.StatusOK {
		return nil
	}
	var p struct {
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("разбор metadata проекта Harbor: %v", err)
	}
	return p.Metadata
}

// harborRobotExists сообщает, существует ли (резолвится по имени) system-robot сервиса.
// Клиент создаёт робота с запрошенным именем <project>-<name> и резолвит его через
// GET /robots?q=name=<это-имя> (project-level robots в Harbor не перечислимы, ADR-0021).
func harborRobotExists(t *testing.T, reqName string) bool {
	t.Helper()
	code, body := harborGet(t, "/api/v2.0/robots?q=name="+reqName)
	if code != http.StatusOK {
		t.Fatalf("Harbor GET robots?q=name=%s: код %d (%s)", reqName, code, string(body))
	}
	var robots []struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(body, &robots); err != nil {
		t.Fatalf("разбор списка robots Harbor: %v", err)
	}
	return len(robots) > 0 && robots[0].ID != 0
}

// TestHarborIntegrationCreate проверяет, что воркфлоу создания РЕАЛЬНО создаёт проект и
// robot-аккаунт в Harbor (ассерт через Harbor API: проект существует, robot резолвится).
func TestHarborIntegrationCreate(t *testing.T) {
	requireHarbor(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("harbor-create")

	mustCreateActive(t, token, projectDemo, name)

	projName := harborProjectName(projectDemo, name)
	if !harborProjectExists(t, projName) {
		t.Fatalf("проект Harbor %s не найден после создания", projName)
	}
	if !harborRobotExists(t, projName) {
		t.Fatalf("robot-аккаунт %s не найден в Harbor после создания (секрет не выдан?)", projName)
	}
}

// TestHarborIntegrationDecommission проверяет, что воркфлоу вывода из эксплуатации
// РЕАЛЬНО делает проект недоступным на запись — отзывает robot (в Harbor нет
// project-level read_only); проект сохраняется (ассерт через Harbor API).
func TestHarborIntegrationDecommission(t *testing.T) {
	requireHarbor(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("harbor-decom")

	mustCreateActive(t, token, projectDemo, name)
	projName := harborProjectName(projectDemo, name)
	// После создания robot выдан.
	if !harborRobotExists(t, projName) {
		t.Fatalf("ожидался созданный robot %s после создания", projName)
	}

	res := callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services/"+name+"/decommission",
		map[string]any{"load_drained": true})
	if res.status != http.StatusOK {
		t.Fatalf("decommissionService: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
	waitForStatus(t, token, projectDemo, name, "decommissioned")

	// Проект сохраняется (decommission не сносит проект), но robot отозван (недоступен на запись).
	if !harborProjectExists(t, projName) {
		t.Fatalf("проект %s исчез (ожидался лишь отзыв robot, не снос проекта)", projName)
	}
	if harborRobotExists(t, projName) {
		t.Fatalf("после decommission robot %s всё ещё резолвится (ожидался отзыв)", projName)
	}
}

// TestHarborIntegrationTransfer проверяет, что воркфлоу переноса РЕАЛЬНО обновляет
// наблюдаемое поле метаданных проекта Harbor (rename/transfer проекта в Harbor нет —
// перенос фиксируется допустимым маркером метаданных, ADR-0021). Проект остаётся
// <source>-<name> (Harbor не умеет rename).
func TestHarborIntegrationTransfer(t *testing.T) {
	requireHarbor(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("harbor-xfer")

	mustCreateActive(t, token, projectDemo, name)
	projName := harborProjectName(projectDemo, name)

	res := callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services/"+name+"/transfer",
		map[string]any{"target_project": projectDemo2})
	if res.status != http.StatusOK {
		t.Fatalf("transferService: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
	waitForStatus(t, token, projectDemo2, name, "active")

	// Маркер переноса (auto_scan) проставлен и наблюдаем на проекте-источнике.
	meta := harborProjectMetadata(t, projName)
	if meta["auto_scan"] != "true" {
		t.Fatalf("маркер переноса auto_scan не проставлен на проекте %s (metadata=%v)", projName, meta)
	}
}
