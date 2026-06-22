//go:build integration

package e2e

import (
	"net/http"
	"testing"
)

// realm-субъекты (deploy/keycloak/idp-realm.json) — используются как владельцы в
// сценарии смены владельцев и переноса (значение для diff add/remove).
const (
	subjAlice = "22222222-2222-2222-2222-222222222222"
	subjBob   = "33333333-3333-3333-3333-333333333333"
)

// TestStoryCreateService — user story «Создание сервиса»: createService →
// дождаться creating→active (getService), Saga-успех против моков GitLab/Vault/
// Harbor. Проверяет happy-path всего воркфлоу провизии (ADR-0005).
func TestStoryCreateService(t *testing.T) {
	requireStack(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("e2e-create")

	res := callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services", map[string]string{"name": name})
	if res.status != http.StatusCreated {
		t.Fatalf("createService: ожидался 201, получен %d (%s)", res.status, string(res.body))
	}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	res.decode(t, &created)
	if created.ID == "" {
		t.Fatalf("createService: пустой id в ответе (%s)", string(res.body))
	}
	if created.Status != "creating" {
		t.Fatalf("createService: ожидался статус creating, получен %q", created.Status)
	}

	// Дожидаемся перехода creating→active (ретраи-поллинг, не sleep).
	s := waitForStatus(t, token, projectDemo, name, "active")
	if s.Name != name || s.Project != projectDemo {
		t.Fatalf("getService вернул чужую запись: %+v", s)
	}
}

// TestStoryChangeOwners — user story «Изменение владельцев»: setServiceOwners
// (декларативный diff add/remove), отражение в каталоге и инкремент версии
// (optimistic-concurrency), синхронизация ролей в IDM воркфлоу (ADR-0011).
func TestStoryChangeOwners(t *testing.T) {
	requireStack(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("e2e-owners")

	mustCreateActive(t, token, projectDemo, name)

	// Декларативная замена набора владельцев целиком: из пустого набора → alice+bob
	// (diff add двух владельцев). Сервер вычисляет diff и синхронизирует роли в IDM
	// (ADR-0011). ПРИМЕЧАНИЕ: повторная успешная смена владельцев того же сервиса
	// невозможна — детерминированный change-owners WorkflowID с политикой
	// ALLOW_DUPLICATE_FAILED_ONLY переиспользуется только после неуспеха (свойство
	// уже реализованного воркфлоу, вне границ этого change), поэтому сценарий
	// делает ровно одну декларативную замену.
	owners := []string{subjAlice, subjBob}
	res := callAPI(t, token, http.MethodPut, "/projects/"+projectDemo+"/services/"+name+"/owners",
		map[string]any{"owners": owners, "owners_version": 0})
	if res.status != http.StatusOK {
		t.Fatalf("setServiceOwners: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
	var r1 struct {
		Owners        []string `json:"owners"`
		OwnersVersion int64    `json:"owners_version"`
	}
	res.decode(t, &r1)
	if r1.OwnersVersion != 1 {
		t.Fatalf("setServiceOwners: ожидалась версия 1, получена %d", r1.OwnersVersion)
	}
	// Отражение в каталоге (применяется асинхронно воркфлоу — ждём версию 1).
	s := waitForOwnersVersion(t, token, projectDemo, name, 1)
	if !sameSet(s.Owners, owners) {
		t.Fatalf("каталог не отражает владельцев: got=%v want=%v", s.Owners, owners)
	}
}

// TestStoryDecommission — user story «Удаление/decommission»: decommissionService
// с load_drained=true → статус decommissioned (soft delete, не purge): запись
// остаётся читаемой через getService (ADR-0012).
func TestStoryDecommission(t *testing.T) {
	requireStack(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("e2e-decom")

	mustCreateActive(t, token, projectDemo, name)

	res := callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services/"+name+"/decommission",
		map[string]any{"load_drained": true})
	if res.status != http.StatusOK {
		t.Fatalf("decommissionService: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}

	waitForStatus(t, token, projectDemo, name, "decommissioned")
	// Soft delete: запись по-прежнему читается (не purge).
	s := getServiceOK(t, token, projectDemo, name)
	if s.Status != "decommissioned" {
		t.Fatalf("ожидался decommissioned, получен %q", s.Status)
	}
}

// TestStoryTransfer — user story «Перенос сервиса»: transferService с двойной
// авторизацией (transfer на demo + transfer_in на demo2) → active→transferring→
// active, смена проекта-владельца, переезд владельцев вместе с записью; запись
// под исходным проектом больше не доступна (ADR-0013).
func TestStoryTransfer(t *testing.T) {
	requireStack(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("e2e-xfer")

	mustCreateActive(t, token, projectDemo, name)

	// Задаём владельца, чтобы проверить его переезд вместе с записью.
	res := callAPI(t, token, http.MethodPut, "/projects/"+projectDemo+"/services/"+name+"/owners",
		map[string]any{"owners": []string{subjAlice}, "owners_version": 0})
	if res.status != http.StatusOK {
		t.Fatalf("setServiceOwners перед переносом: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
	// Дожидаемся отражения владельца в каталоге (версия 1) перед переносом.
	waitForOwnersVersion(t, token, projectDemo, name, 1)

	res = callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services/"+name+"/transfer",
		map[string]any{"target_project": projectDemo2})
	if res.status != http.StatusOK {
		t.Fatalf("transferService: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}

	// Перенос завершён: запись active в целевом проекте, владелец переехал.
	s := waitForStatus(t, token, projectDemo2, name, "active")
	if s.Project != projectDemo2 {
		t.Fatalf("после переноса ожидался проект %q, получен %q", projectDemo2, s.Project)
	}
	if !sameSet(s.Owners, []string{subjAlice}) {
		t.Fatalf("владельцы не переехали вместе с записью: got=%v", s.Owners)
	}
	// Под исходным проектом записи больше нет.
	if _, code := getService(t, token, projectDemo, name); code != http.StatusNotFound {
		t.Fatalf("после переноса demo/%s ожидался 404, получен %d", name, code)
	}
}
