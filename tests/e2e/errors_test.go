//go:build integration

package e2e

import (
	"net/http"
	"os"
	"testing"
)

// TestErrorCreateConflict — идемпотентность/конфликт создания: повторный
// createService для того же (project, name) при ещё живом воркфлоу отклоняется
// 409 (конфликт по детерминированному WorkflowID/занятому имени), исходный
// воркфлоу не дублируется (ADR-0004).
func TestErrorCreateConflict(t *testing.T) {
	requireStack(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("e2e-conflict")
	path := "/projects/" + projectDemo + "/services"

	res := callAPI(t, token, http.MethodPost, path, map[string]string{"name": name})
	if res.status != http.StatusCreated {
		t.Fatalf("первый createService: ожидался 201, получен %d (%s)", res.status, string(res.body))
	}
	// Повтор до завершения первого — конфликт.
	res = callAPI(t, token, http.MethodPost, path, map[string]string{"name": name})
	if res.status != http.StatusConflict {
		t.Fatalf("повторный createService: ожидался 409, получен %d (%s)", res.status, string(res.body))
	}
}

// TestErrorOwnersVersionConflict — optimistic-concurrency владельцев: setService
// Owners с устаревшей owners_version отклоняется 409, набор владельцев не меняется.
func TestErrorOwnersVersionConflict(t *testing.T) {
	requireStack(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("e2e-ownver")

	mustCreateActive(t, token, projectDemo, name)

	// Успешно поднимаем версию 0→1 и ждём отражения в каталоге (асинхронно).
	res := callAPI(t, token, http.MethodPut, "/projects/"+projectDemo+"/services/"+name+"/owners",
		map[string]any{"owners": []string{subjAlice}, "owners_version": 0})
	if res.status != http.StatusOK {
		t.Fatalf("setServiceOwners: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
	waitForOwnersVersion(t, token, projectDemo, name, 1)
	// Повтор с устаревшей версией 0 → конфликт.
	res = callAPI(t, token, http.MethodPut, "/projects/"+projectDemo+"/services/"+name+"/owners",
		map[string]any{"owners": []string{subjBob}, "owners_version": 0})
	if res.status != http.StatusConflict {
		t.Fatalf("setServiceOwners(stale): ожидался 409, получен %d (%s)", res.status, string(res.body))
	}
	// Набор владельцев не изменён конфликтной попыткой.
	s := getServiceOK(t, token, projectDemo, name)
	if !sameSet(s.Owners, []string{subjAlice}) {
		t.Fatalf("конфликтная попытка изменила владельцев: got=%v", s.Owners)
	}
}

// TestErrorDecommissionPrecondition — предусловие снятой нагрузки (ADR-0012):
// decommissionService с load_drained=false отклоняется 422 до побочных эффектов,
// статус не меняется (остаётся active).
func TestErrorDecommissionPrecondition(t *testing.T) {
	requireStack(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("e2e-precond")

	mustCreateActive(t, token, projectDemo, name)

	res := callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services/"+name+"/decommission",
		map[string]any{"load_drained": false})
	if res.status != http.StatusUnprocessableEntity {
		t.Fatalf("decommission(load_drained=false): ожидался 422, получен %d (%s)", res.status, string(res.body))
	}
	// Статус не изменился.
	s := getServiceOK(t, token, projectDemo, name)
	if s.Status != "active" {
		t.Fatalf("после отказанного decommission ожидался active, получен %q", s.Status)
	}
}

// TestErrorIdempotentDecommission — идемпотентный повтор decommission на уже
// выведенном сервисе → 200 без повторных побочных эффектов.
func TestErrorIdempotentDecommission(t *testing.T) {
	requireStack(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("e2e-idemdec")

	mustCreateActive(t, token, projectDemo, name)
	res := callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services/"+name+"/decommission",
		map[string]any{"load_drained": true})
	if res.status != http.StatusOK {
		t.Fatalf("первый decommission: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
	waitForStatus(t, token, projectDemo, name, "decommissioned")

	res = callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services/"+name+"/decommission",
		map[string]any{"load_drained": true})
	if res.status != http.StatusOK {
		t.Fatalf("повторный decommission: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
}

// TestErrorIdempotentTransfer — идемпотентный повтор transfer на уже перенесённом
// сервисе → 200 без повторного запуска побочных эффектов.
func TestErrorIdempotentTransfer(t *testing.T) {
	requireStack(t)
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	name := uniqueName("e2e-idemxfer")

	mustCreateActive(t, token, projectDemo, name)
	res := callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services/"+name+"/transfer",
		map[string]any{"target_project": projectDemo2})
	if res.status != http.StatusOK {
		t.Fatalf("первый transfer: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
	waitForStatus(t, token, projectDemo2, name, "active")

	// Повтор того же переноса → идемпотентный 200.
	res = callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services/"+name+"/transfer",
		map[string]any{"target_project": projectDemo2})
	if res.status != http.StatusOK {
		t.Fatalf("повторный transfer: ожидался 200, получен %d (%s)", res.status, string(res.body))
	}
}

// TestSagaRollbackOnVaultFailure — наблюдение Saga-отката создания (ADR-0005):
// для маркированного имени (sagafail*) мок Vault возвращает неустранимую ошибку
// на шаге политики; воркфлоу выполняет компенсации и переводит запись в failed.
// Опционально (env-gated E2E_RUN_SAGA_FAILURE=1): зависит от мок-маппинга
// saga-failure.json; при риске флака в основном наборе отключаемо.
func TestSagaRollbackOnVaultFailure(t *testing.T) {
	requireStack(t)
	if os.Getenv("E2E_RUN_SAGA_FAILURE") != "1" {
		t.Skip("E2E_RUN_SAGA_FAILURE!=1; сценарий Saga-отказа опционален (см. saga-failure.json)")
	}
	t.Parallel()
	token := fetchIDToken(t, userDev, userDev)
	// Имя с маркером sagafail → мок Vault вернёт 400 на политике.
	name := uniqueName("sagafail")

	res := callAPI(t, token, http.MethodPost, "/projects/"+projectDemo+"/services", map[string]string{"name": name})
	if res.status != http.StatusCreated {
		t.Fatalf("createService(sagafail): ожидался 201, получен %d (%s)", res.status, string(res.body))
	}
	// Компенсации + перевод в failed; молчаливого active быть не должно.
	s := waitForStatus(t, token, projectDemo, name, "failed")
	if s.Status != "failed" {
		t.Fatalf("ожидался failed после Saga-отката, получен %q", s.Status)
	}
}
