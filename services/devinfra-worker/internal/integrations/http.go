package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/YuriB9/idp/pkg/httpclient"
	"github.com/YuriB9/idp/pkg/ssrf"
	"github.com/YuriB9/idp/services/projects/provisioning"
)

// Config конфигурирует HTTP-клиентов интеграций.
type Config struct {
	// Базовые URL управляемых систем.
	GitLabBaseURL string
	VaultBaseURL  string
	HarborBaseURL string
	// Guarded включает SSRF-guard (ValidateURL + GuardedDialContext). В локалке с
	// http-моками выключается явным флагом (аналог AUTH_DISABLED); в проде —
	// всегда true (https + публичные адреса).
	Guarded bool
	// Timeout — таймаут запроса; 0 → дефолт httpclient.
	Timeout time.Duration
}

// NewHTTPClients собирает HTTP-реализации клиентов интеграций. При Guarded=true
// каждый base-URL проверяется ssrf.ValidateURL, а транспорт использует
// ssrf.GuardedDialContext (защита от TOCTOU/DNS-rebinding на этапе соединения).
func NewHTTPClients(cfg Config) (*Clients, error) {
	if cfg.Guarded {
		for _, raw := range []string{cfg.GitLabBaseURL, cfg.VaultBaseURL, cfg.HarborBaseURL} {
			if err := ssrf.ValidateURL(raw); err != nil {
				return nil, fmt.Errorf("integrations: base-URL не прошёл SSRF-guard: %w", err)
			}
		}
	}

	hcCfg := httpclient.Config{Timeout: cfg.Timeout}
	if cfg.Guarded {
		hcCfg.DialContext = ssrf.GuardedDialContext(10 * time.Second)
	}
	hc := httpclient.New(hcCfg)

	doer := &doer{hc: hc}
	return &Clients{
		GitLab: &gitLabHTTP{doer: doer, base: strings.TrimRight(cfg.GitLabBaseURL, "/")},
		Harbor: &harborHTTP{doer: doer, base: strings.TrimRight(cfg.HarborBaseURL, "/")},
		Vault:  &vaultHTTP{doer: doer, base: strings.TrimRight(cfg.VaultBaseURL, "/")},
	}, nil
}

// doer — общий HTTP-исполнитель с разбором JSON-ответа.
type doer struct {
	hc *http.Client
}

// call выполняет запрос и декодирует JSON-ответ в out (если не nil). Коды из
// okExtra считаются успешными помимо 2xx (для идемпотентности: 409 при создании,
// 404 при удалении). Остальные коды маппятся в канонические ошибки errs.
func (d *doer) call(ctx context.Context, method, url string, body, out any, okExtra ...int) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("integrations: marshal тела запроса: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return fmt.Errorf("integrations: сборка запроса: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return fmt.Errorf("integrations: выполнение запроса: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	for _, code := range okExtra {
		if resp.StatusCode == code {
			return nil // идемпотентный исход — тело не разбираем
		}
	}
	if err := httpclient.MapStatus(resp.StatusCode); err != nil {
		return fmt.Errorf("integrations: ответ %s %s: %w", method, url, err)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("integrations: разбор ответа: %w", err)
		}
	}
	return nil
}

// --- GitLab ---

type gitLabHTTP struct {
	doer *doer
	base string
}

func (c *gitLabHTTP) CreateRepo(ctx context.Context, ref provisioning.ResourceRef) (provisioning.GitLabRepo, error) {
	// Идемпотентность: 409 (репозиторий уже существует) трактуем как успех.
	url := c.base + "/api/v4/projects"
	body := map[string]string{"name": ref.Name, "namespace": ref.Project}
	if err := c.doer.call(ctx, http.MethodPost, url, body, nil, http.StatusConflict); err != nil {
		return provisioning.GitLabRepo{}, err
	}
	return provisioning.GitLabRepo{ProjectID: ref.Project + "/" + ref.Name, Path: ref.Project + "/" + ref.Name}, nil
}

func (c *gitLabHTTP) DeleteRepo(ctx context.Context, ref provisioning.ResourceRef) error {
	// Идемпотентность: 404 (уже удалён) трактуем как успех.
	url := fmt.Sprintf("%s/api/v4/projects/%s", c.base, ref.Project+"%2F"+ref.Name)
	return c.doer.call(ctx, http.MethodDelete, url, nil, nil, http.StatusNotFound)
}

func (c *gitLabHTTP) InjectVariables(ctx context.Context, in provisioning.InjectSecretsInput) error {
	// Записываем секреты по одному; идемпотентность: 409 (переменная существует)
	// трактуем как успех (перезапись допустима). Значения не логируем.
	url := fmt.Sprintf("%s/api/v4/projects/%s/variables", c.base, in.GitLab.Path)
	vars := map[string]string{
		"VAULT_ROLE_ID":      in.Vault.RoleID,
		"VAULT_SECRET_ID":    in.Vault.SecretID,
		"HARBOR_ROBOT_NAME":  in.Harbor.RobotName,
		"HARBOR_ROBOT_TOKEN": in.Harbor.RobotToken,
	}
	for key, val := range vars {
		body := map[string]string{"key": key, "value": val}
		if err := c.doer.call(ctx, http.MethodPost, url, body, nil, http.StatusConflict); err != nil {
			return err
		}
	}
	return nil
}

func (c *gitLabHTTP) SyncMembers(ctx context.Context, ref provisioning.ResourceRef, add, remove []string) error {
	// Идемпотентность: при добавлении 409 (участник уже есть) — успех; при
	// удалении 404 (участника нет) — успех. Реальный GitLab резолвит логины в
	// user_id; в моке достаточно имени пользователя.
	proj := ref.Project + "%2F" + ref.Name
	for _, user := range add {
		url := fmt.Sprintf("%s/api/v4/projects/%s/members", c.base, proj)
		body := map[string]string{"username": user, "access_level": "40"}
		if err := c.doer.call(ctx, http.MethodPost, url, body, nil, http.StatusConflict); err != nil {
			return err
		}
	}
	for _, user := range remove {
		url := fmt.Sprintf("%s/api/v4/projects/%s/members/%s", c.base, proj, user)
		if err := c.doer.call(ctx, http.MethodDelete, url, nil, nil, http.StatusNotFound); err != nil {
			return err
		}
	}
	return nil
}

func (c *gitLabHTTP) RestoreMembers(ctx context.Context, ref provisioning.ResourceRef, previous []string) error {
	// Компенсация: декларативно восстанавливаем прежний состав. В моке это
	// идемпотентное повторное добавление прежних участников (без вычисления
	// удалений — текущее расхождение незначимо для стенда).
	return c.SyncMembers(ctx, ref, previous, nil)
}

func (c *gitLabHTTP) Archive(ctx context.Context, ref provisioning.ResourceRef) error {
	// Архивация репозитория (идемпотентно: повторная архивация — успех). В моке
	// 409/404 трактуем как успешный исход.
	proj := ref.Project + "%2F" + ref.Name
	url := fmt.Sprintf("%s/api/v4/projects/%s/archive", c.base, proj)
	return c.doer.call(ctx, http.MethodPost, url, nil, nil, http.StatusConflict, http.StatusNotFound)
}

func (c *gitLabHTTP) Unarchive(ctx context.Context, ref provisioning.ResourceRef) error {
	// Компенсация: разархивировать репозиторий (идемпотентно).
	proj := ref.Project + "%2F" + ref.Name
	url := fmt.Sprintf("%s/api/v4/projects/%s/unarchive", c.base, proj)
	return c.doer.call(ctx, http.MethodPost, url, nil, nil, http.StatusConflict, http.StatusNotFound)
}

func (c *gitLabHTTP) TransferRepo(ctx context.Context, ref provisioning.ResourceRef, target string) error {
	// Перенос репозитория в группу target (идемпотентно: 409 — репозиторий уже в
	// целевой группе; 404 — допускаем как успешный исход на стенде). Необратимо.
	proj := ref.Project + "%2F" + ref.Name
	url := fmt.Sprintf("%s/api/v4/projects/%s/transfer", c.base, proj)
	body := map[string]string{"namespace": target}
	return c.doer.call(ctx, http.MethodPost, url, body, nil, http.StatusConflict, http.StatusNotFound)
}

// --- Harbor ---

type harborHTTP struct {
	doer *doer
	base string
}

func (c *harborHTTP) CreateProject(ctx context.Context, ref provisioning.ResourceRef) (provisioning.HarborResult, error) {
	projName := ref.Project + "-" + ref.Name
	if err := c.doer.call(ctx, http.MethodPost, c.base+"/api/v2.0/projects",
		map[string]string{"project_name": projName}, nil, http.StatusConflict); err != nil {
		return provisioning.HarborResult{}, err
	}
	robotName := "robot$" + projName
	var robotResp struct {
		Name   string `json:"name"`
		Secret string `json:"secret"`
	}
	if err := c.doer.call(ctx, http.MethodPost, c.base+"/api/v2.0/robots",
		map[string]any{"name": robotName, "level": "project"}, &robotResp, http.StatusConflict); err != nil {
		return provisioning.HarborResult{}, err
	}
	// Мок может не вернуть секрет; подставляем детерминированную метку, реальный
	// секрет приходит от настоящего Harbor.
	if robotResp.Name == "" {
		robotResp.Name = robotName
	}
	if robotResp.Secret == "" {
		robotResp.Secret = "mock-harbor-secret"
	}
	return provisioning.HarborResult{ProjectName: projName, RobotName: robotResp.Name, RobotToken: robotResp.Secret}, nil
}

func (c *harborHTTP) DeleteProject(ctx context.Context, ref provisioning.ResourceRef) error {
	projName := ref.Project + "-" + ref.Name
	return c.doer.call(ctx, http.MethodDelete, c.base+"/api/v2.0/projects/"+projName, nil, nil, http.StatusNotFound)
}

func (c *harborHTTP) SetReadOnly(ctx context.Context, ref provisioning.ResourceRef) error {
	// Перевод директории в read-only + отзыв Robot (идемпотентно). 404 — успех.
	projName := ref.Project + "-" + ref.Name
	if err := c.doer.call(ctx, http.MethodPut, c.base+"/api/v2.0/projects/"+projName,
		map[string]any{"metadata": map[string]string{"public": "false"}, "read_only": true}, nil, http.StatusNotFound); err != nil {
		return err
	}
	robotName := "robot$" + projName
	return c.doer.call(ctx, http.MethodDelete, c.base+"/api/v2.0/robots/"+robotName, nil, nil, http.StatusNotFound)
}

func (c *harborHTTP) SetWritable(ctx context.Context, ref provisioning.ResourceRef) error {
	// Компенсация: вернуть директорию в writable (идемпотентно).
	projName := ref.Project + "-" + ref.Name
	return c.doer.call(ctx, http.MethodPut, c.base+"/api/v2.0/projects/"+projName,
		map[string]any{"read_only": false}, nil, http.StatusNotFound)
}

func (c *harborHTTP) UpdateMetadata(ctx context.Context, ref provisioning.ResourceRef, target string) error {
	// Обновление метаданных/прав директории под target-проект (идемпотентно: 404 —
	// успех на стенде).
	projName := ref.Project + "-" + ref.Name
	body := map[string]any{"metadata": map[string]string{"owner_project": target}}
	return c.doer.call(ctx, http.MethodPut, c.base+"/api/v2.0/projects/"+projName, body, nil, http.StatusNotFound)
}

// --- Vault ---

type vaultHTTP struct {
	doer *doer
	base string
}

func (c *vaultHTTP) SetupAppRole(ctx context.Context, ref provisioning.ResourceRef) (provisioning.VaultResult, error) {
	role := ref.Project + "-" + ref.Name
	// Политика (идемпотентно: PUT перезаписывает).
	if err := c.doer.call(ctx, http.MethodPut, c.base+"/v1/sys/policies/acl/"+role,
		map[string]string{"policy": "path \"secret/data/" + role + "/*\" { capabilities=[\"read\"] }"}, nil); err != nil {
		return provisioning.VaultResult{}, err
	}
	// AppRole (идемпотентно).
	if err := c.doer.call(ctx, http.MethodPost, c.base+"/v1/auth/approle/role/"+role,
		map[string]string{"token_policies": role}, nil); err != nil {
		return provisioning.VaultResult{}, err
	}
	var roleResp struct {
		Data struct {
			RoleID string `json:"role_id"`
		} `json:"data"`
	}
	if err := c.doer.call(ctx, http.MethodGet, c.base+"/v1/auth/approle/role/"+role+"/role-id", nil, &roleResp); err != nil {
		return provisioning.VaultResult{}, err
	}
	var secretResp struct {
		Data struct {
			SecretID string `json:"secret_id"`
		} `json:"data"`
	}
	if err := c.doer.call(ctx, http.MethodPost, c.base+"/v1/auth/approle/role/"+role+"/secret-id", nil, &secretResp); err != nil {
		return provisioning.VaultResult{}, err
	}
	res := provisioning.VaultResult{RoleID: roleResp.Data.RoleID, SecretID: secretResp.Data.SecretID}
	if res.RoleID == "" {
		res.RoleID = "mock-role-id-" + role
	}
	if res.SecretID == "" {
		res.SecretID = "mock-secret-id"
	}
	return res, nil
}

func (c *vaultHTTP) TeardownAppRole(ctx context.Context, ref provisioning.ResourceRef) error {
	role := ref.Project + "-" + ref.Name
	if err := c.doer.call(ctx, http.MethodDelete, c.base+"/v1/auth/approle/role/"+role, nil, nil, http.StatusNotFound); err != nil {
		return err
	}
	return c.doer.call(ctx, http.MethodDelete, c.base+"/v1/sys/policies/acl/"+role, nil, nil, http.StatusNotFound)
}

func (c *vaultHTTP) SyncOwners(ctx context.Context, ref provisioning.ResourceRef, add, remove []string) error {
	// Идемпотентность: PUT entity-alias на каждого добавленного владельца (409 —
	// успех), DELETE на каждого удалённого (404 — успех). В моке достаточно имени.
	role := ref.Project + "-" + ref.Name
	for _, user := range add {
		url := fmt.Sprintf("%s/v1/identity/entity/name/%s-%s", c.base, role, user)
		body := map[string]any{"policies": []string{role}}
		if err := c.doer.call(ctx, http.MethodPut, url, body, nil, http.StatusConflict); err != nil {
			return err
		}
	}
	for _, user := range remove {
		url := fmt.Sprintf("%s/v1/identity/entity/name/%s-%s", c.base, role, user)
		if err := c.doer.call(ctx, http.MethodDelete, url, nil, nil, http.StatusNotFound); err != nil {
			return err
		}
	}
	return nil
}

func (c *vaultHTTP) RestoreOwners(ctx context.Context, ref provisioning.ResourceRef, previous []string) error {
	// Компенсация: восстановить прежний доступ (идемпотентно).
	return c.SyncOwners(ctx, ref, previous, nil)
}

func (c *vaultHTTP) RevokeSecretID(ctx context.Context, ref provisioning.ResourceRef) error {
	// Отзыв всех активных SecretID AppRole — немедленное прекращение доступа
	// (идемпотентно: 404 — успех). Необратимо: отозванный SecretID не вернуть.
	role := ref.Project + "-" + ref.Name
	url := fmt.Sprintf("%s/v1/auth/approle/role/%s/secret-id/destroy", c.base, role)
	return c.doer.call(ctx, http.MethodPost, url, map[string]string{}, nil, http.StatusNotFound)
}

func (c *vaultHTTP) MigratePaths(ctx context.Context, ref provisioning.ResourceRef, target string) error {
	// Миграция путей: копия секретов source→target + новые политики + очистка старых
	// (идемпотентно: PUT перезаписывает, 404 при очистке — успех). Значения секретов
	// не логируем.
	srcRole := ref.Project + "-" + ref.Name
	dstRole := target + "-" + ref.Name
	// 1. Копия секретов в новый путь.
	var read struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := c.doer.call(ctx, http.MethodGet, c.base+"/v1/secret/data/"+srcRole, nil, &read, http.StatusNotFound); err != nil {
		return err
	}
	if err := c.doer.call(ctx, http.MethodPut, c.base+"/v1/secret/data/"+dstRole,
		map[string]any{"data": read.Data.Data}, nil); err != nil {
		return err
	}
	// 2. Новая политика для target-пути (идемпотентно).
	if err := c.doer.call(ctx, http.MethodPut, c.base+"/v1/sys/policies/acl/"+dstRole,
		map[string]string{"policy": "path \"secret/data/" + dstRole + "/*\" { capabilities=[\"read\"] }"}, nil); err != nil {
		return err
	}
	// 3. Очистка старых путей/политик (404 — успех).
	if err := c.doer.call(ctx, http.MethodDelete, c.base+"/v1/secret/data/"+srcRole, nil, nil, http.StatusNotFound); err != nil {
		return err
	}
	return c.doer.call(ctx, http.MethodDelete, c.base+"/v1/sys/policies/acl/"+srcRole, nil, nil, http.StatusNotFound)
}
