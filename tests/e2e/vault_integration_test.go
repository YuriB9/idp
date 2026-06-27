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

// Интеграционные тесты воркфлоу против РЕАЛЬНОГО Vault (ADR-0020). Активируются
// только при заданных VAULT_ADDR и VAULT_TOKEN (профиль реального Vault,
// `make vault`); иначе пропускаются — прогон историй против моков (`make e2e`) и
// gitlab-набор не затрагиваются. Гоняют воркфлоу через периметр (переиспользуя
// харнесс) И ассертят фактическое состояние в Vault через Vault API.

// vaultAPIURL — публикуемый адрес реального Vault для ассертов через Vault API.
func vaultAPIURL() string { return env("VAULT_ADDR", "") }

// vaultAPIToken — dev root-токен стенда (передаётся из Makefile).
func vaultAPIToken() string { return env("VAULT_TOKEN", "") }

// vaultConfigured сообщает, сконфигурирован ли профиль реального Vault.
func vaultConfigured() bool { return vaultAPIURL() != "" && vaultAPIToken() != "" }

// vaultStatusTimeout — бюджет ожиданий со стороны Vault (dev-старт — секунды).
func vaultStatusTimeout() time.Duration {
	d, err := time.ParseDuration(env("VAULT_STATUS_TIMEOUT", "120s"))
	if err != nil {
		return 120 * time.Second
	}
	return d
}

// requireVault пропускает тест без профиля реального Vault и дожидается готовности
// Vault API (без фиксированных sleep сверх интервала опроса).
func requireVault(t *testing.T) {
	t.Helper()
	requireStack(t)
	if !vaultConfigured() {
		t.Skip("VAULT_ADDR/VAULT_TOKEN не заданы; прогон только против реального Vault (make vault)")
	}
	deadline := time.Now().Add(vaultStatusTimeout())
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, vaultAPIURL()+"/v1/sys/health", nil)
		resp, err := httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("Vault API не готов за %s", vaultStatusTimeout())
}

// vaultClient — клиент для ассертов через Vault API.
var vaultClient = &http.Client{Timeout: 15 * time.Second}

// vaultGet выполняет GET к Vault API с X-Vault-Token; возвращает код и тело.
func vaultGet(t *testing.T, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, vaultAPIURL()+path, nil)
	if err != nil {
		t.Fatalf("сборка запроса Vault %s: %v", path, err)
	}
	req.Header.Set("X-Vault-Token", vaultAPIToken())
	resp, err := vaultClient.Do(req)
	if err != nil {
		t.Fatalf("запрос Vault %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение ответа Vault %s: %v", path, err)
	}
	return resp.StatusCode, body
}

// vaultExists сообщает, существует ли ресурс по пути (200 → true, 404 → false).
func vaultExists(t *testing.T, path string) bool {
	t.Helper()
	code, body := vaultGet(t, path)
	switch code {
	case http.StatusOK:
		return true
	case http.StatusNotFound:
		return false
	default:
		t.Fatalf("Vault GET %s: неожиданный код %d (%s)", path, code, string(body))
		return false
	}
}

// vaultRoleID читает role_id AppRole-роли (пусто, если роли нет).
func vaultRoleID(t *testing.T, role string) string {
	t.Helper()
	code, body := vaultGet(t, "/v1/auth/approle/role/"+role+"/role-id")
	if code != http.StatusOK {
		return ""
	}
	var r struct {
		Data struct {
			RoleID string `json:"role_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("разбор role-id Vault: %v", err)
	}
	return r.Data.RoleID
}

// vaultSecretIDCount возвращает число активных secret-id-accessors роли. Пустой
// набор Vault отдаёт как 404 (трактуем как 0).
func vaultSecretIDCount(t *testing.T, role string) int {
	t.Helper()
	code, body := vaultGet(t, "/v1/auth/approle/role/"+role+"/secret-id?list=true")
	if code == http.StatusNotFound {
		return 0
	}
	if code != http.StatusOK {
		t.Fatalf("Vault LIST secret-id %s: код %d (%s)", role, code, string(body))
	}
	var r struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("разбор списка secret-id Vault: %v", err)
	}
	return len(r.Data.Keys)
}

// vaultEntityPolicies возвращает политики identity-entity по имени (nil, если нет).
func vaultEntityPolicies(t *testing.T, name string) []string {
	t.Helper()
	code, body := vaultGet(t, "/v1/identity/entity/name/"+name)
	if code != http.StatusOK {
		return nil
	}
	var r struct {
		Data struct {
			Policies []string `json:"policies"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("разбор identity-entity Vault: %v", err)
	}
	return r.Data.Policies
}

// vaultRole собирает имя AppRole-роли так же, как клиент интеграции: <project>-<name>.
func vaultRole(project, name string) string { return project + "-" + name }

// TestVaultIntegrationCreate проверяет, что воркфлоу создания РЕАЛЬНО создаёт
// AppRole и per-service политику (ассерт через Vault API: роль/политика существуют,
// role-id читается).
func TestVaultIntegrationCreate(t *testing.T) {
	requireVault(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("vault-create")

	mustCreateActive(t, token, projectDemo, name)

	role := vaultRole(projectDemo, name)
	if !vaultExists(t, "/v1/auth/approle/role/"+role) {
		t.Fatalf("AppRole-роль %s не найдена в Vault после создания", role)
	}
	if !vaultExists(t, "/v1/sys/policies/acl/"+role) {
		t.Fatalf("per-service политика %s не найдена в Vault после создания", role)
	}
	if vaultRoleID(t, role) == "" {
		t.Fatalf("role-id роли %s не читается после создания", role)
	}
}

// TestVaultIntegrationDecommission проверяет, что воркфлоу вывода из эксплуатации
// РЕАЛЬНО отзывает активные secret-id (набор accessors пуст через Vault API).
func TestVaultIntegrationDecommission(t *testing.T) {
	requireVault(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("vault-decom")

	mustCreateActive(t, token, projectDemo, name)
	role := vaultRole(projectDemo, name)
	// После создания выдан как минимум один secret-id.
	if vaultSecretIDCount(t, role) == 0 {
		t.Fatalf("ожидался хотя бы один активный secret-id роли %s после создания", role)
	}

	res := callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services/"+name+"/decommission",
		map[string]any{"load_drained": true})
	if res.status != http.StatusOK {
		t.Fatalf("decommissionService: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
	waitForStatus(t, token, projectDemo, name, "decommissioned")

	// Роль сохраняется (decommission не сносит роль), но активных secret-id нет.
	if !vaultExists(t, "/v1/auth/approle/role/"+role) {
		t.Fatalf("роль %s исчезла (ожидался лишь отзыв secret-id, не снос роли)", role)
	}
	if n := vaultSecretIDCount(t, role); n != 0 {
		t.Fatalf("после decommission остались активные secret-id роли %s: %d", role, n)
	}
}

// TestVaultIntegrationTransfer проверяет, что воркфлоу переноса РЕАЛЬНО мигрирует
// секреты source→target: целевая политика создана, исходная очищена (через Vault API).
func TestVaultIntegrationTransfer(t *testing.T) {
	requireVault(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("vault-xfer")

	mustCreateActive(t, token, projectDemo, name)
	srcRole := vaultRole(projectDemo, name)
	dstRole := vaultRole(projectDemo2, name)

	res := callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services/"+name+"/transfer",
		map[string]any{"target_project": projectDemo2})
	if res.status != http.StatusOK {
		t.Fatalf("transferService: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
	waitForStatus(t, token, projectDemo2, name, "active")

	if !vaultExists(t, "/v1/sys/policies/acl/"+dstRole) {
		t.Fatalf("целевая политика %s не создана после переноса", dstRole)
	}
	if vaultExists(t, "/v1/sys/policies/acl/"+srcRole) {
		t.Fatalf("исходная политика %s не очищена после переноса", srcRole)
	}
}

// TestVaultIntegrationChangeOwners проверяет, что смена владельцев РЕАЛЬНО отражается
// в identity Vault: создаётся entity владельца с политикой роли (через Vault API).
func TestVaultIntegrationChangeOwners(t *testing.T) {
	requireVault(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("vault-owners")

	mustCreateActive(t, token, projectDemo, name)
	role := vaultRole(projectDemo, name)

	// Детерминированный change-owners WorkflowID: одна успешная смена владельцев на
	// сервис (см. память сессии). Задаём владельца alice с версии 0.
	res := callAPI(t, token, http.MethodPut, "/projects/"+projectDemo+"/services/"+name+"/owners",
		map[string]any{"owners": []string{subjAlice}, "owners_version": 0})
	if res.status != http.StatusOK {
		t.Fatalf("setServiceOwners: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
	waitForOwnersVersion(t, token, projectDemo, name, 1)

	// Vault SyncOwners создаёт entity <role>-<subject> с политикой роли.
	entity := fmt.Sprintf("%s-%s", role, subjAlice)
	policies := vaultEntityPolicies(t, entity)
	found := false
	for _, p := range policies {
		if p == role {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("identity-entity %s не несёт политику роли %s (policies=%v)", entity, role, policies)
	}
}
