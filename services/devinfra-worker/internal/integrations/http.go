package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/YuriB9/idp/pkg/httpclient"
	"github.com/YuriB9/idp/pkg/ssrf"
	"github.com/YuriB9/idp/services/projects/provisioning"
)

// gitLabTokenHeader — заголовок аутентификации реального GitLab (PAT/PRIVATE-TOKEN).
const gitLabTokenHeader = "PRIVATE-TOKEN"

// Config конфигурирует HTTP-клиентов интеграций.
type Config struct {
	// Базовые URL управляемых систем.
	GitLabBaseURL string
	VaultBaseURL  string
	HarborBaseURL string
	// GitLabToken — токен аутентификации к реальному GitLab (PRIVATE-TOKEN). Пусто
	// для прогона против WireMock-мока (мок не требует токена). Значение не
	// логируется. Наличие токена включает резолвинг идентификаторов под реальный
	// GitLab API (namespace→group_id, владелец→user_id), см. ADR-0019.
	GitLabToken string
	// GitLabOwnerLogins — маппинг субъекта-владельца (UUID Keycloak) на GitLab-логин
	// (фикстура стенда). Пустое отображение для владельца → синхронизация участника
	// безопасно пропускается (членство в репозитории не обязательно для happy-path).
	GitLabOwnerLogins map[string]string
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

	sharedDoer := &doer{hc: hc}
	// GitLab-запросы к реальному GitLab несут PRIVATE-TOKEN. Отдельный doer, чтобы
	// заголовок аутентификации не протекал в запросы Vault/Harbor. Без токена
	// (прогон против мока) — общий doer без заголовка.
	gitlabDoer := sharedDoer
	if cfg.GitLabToken != "" {
		gitlabDoer = &doer{hc: hc, headers: map[string]string{gitLabTokenHeader: cfg.GitLabToken}}
	}
	return &Clients{
		GitLab: &gitLabHTTP{
			doer:        gitlabDoer,
			base:        strings.TrimRight(cfg.GitLabBaseURL, "/"),
			real:        cfg.GitLabToken != "",
			ownerLogins: cfg.GitLabOwnerLogins,
			groupIDs:    map[string]int{},
		},
		Harbor: &harborHTTP{doer: sharedDoer, base: strings.TrimRight(cfg.HarborBaseURL, "/")},
		Vault:  &vaultHTTP{doer: sharedDoer, base: strings.TrimRight(cfg.VaultBaseURL, "/")},
	}, nil
}

// doer — общий HTTP-исполнитель с разбором JSON-ответа.
type doer struct {
	hc *http.Client
	// headers — дополнительные заголовки на каждый запрос (напр. PRIVATE-TOKEN для
	// GitLab). Значения не логируются.
	headers map[string]string
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
	for k, v := range d.headers {
		req.Header.Set(k, v) // напр. PRIVATE-TOKEN для GitLab; значение не логируем
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

// getFound выполняет GET и сообщает, существует ли ресурс: 2xx → true (с разбором
// тела в out, если не nil), 404 → false, прочие коды → каноническая ошибка. Это
// примитив идемпотентности GET-then-act для реального GitLab (коды надёжнее текста
// сообщений об ошибке, который не контракт и зависит от версии).
func (d *doer) getFound(ctx context.Context, url string, out any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("integrations: сборка запроса: %w", err)
	}
	for k, v := range d.headers {
		req.Header.Set(k, v)
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		return false, fmt.Errorf("integrations: выполнение запроса: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if err := httpclient.MapStatus(resp.StatusCode); err != nil {
		return false, fmt.Errorf("integrations: ответ GET %s: %w", url, err)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return false, fmt.Errorf("integrations: разбор ответа: %w", err)
		}
	}
	return true, nil
}

// --- GitLab ---

type gitLabHTTP struct {
	doer *doer
	base string
	// real включает семантику РЕАЛЬНОГО GitLab API (резолвинг namespace→group_id и
	// владелец→user_id, идемпотентность через GET-then-act, upsert переменных).
	// false → прогон против WireMock-мока со старой формой запросов (БЛОК 7).
	real bool
	// ownerLogins — маппинг субъекта-владельца (UUID) на GitLab-логин (фикстура
	// стенда); пусто для прогона против мока. См. Config.GitLabOwnerLogins.
	ownerLogins map[string]string
	// mu защищает кэш groupIDs (резолвинг пути группы в namespace_id выполняется
	// activities параллельно).
	mu sync.Mutex
	// groupIDs — кэш «путь группы → namespace_id» в пределах процесса (группы
	// стабильны на прогоне). Доступ под mu.
	groupIDs map[string]int
}

// encodeProjectPath кодирует полный путь репозитория group/name для URL GitLab
// (слэш → %2F). Имена проекта/сервиса ограничены [a-z0-9-], поэтому экранирование
// сегментов не требуется сверх слэша.
func encodeProjectPath(ref provisioning.ResourceRef) string {
	return ref.Project + "%2F" + ref.Name
}

// resolveGroupID резолвит путь группы (проект каталога) в числовой namespace_id
// через GET /api/v4/groups/:path с кэшированием. Группа ДОЛЖНА быть предсоздана
// сидом стенда; её отсутствие — ошибка конфигурации стенда.
func (c *gitLabHTTP) resolveGroupID(ctx context.Context, group string) (int, error) {
	c.mu.Lock()
	if id, ok := c.groupIDs[group]; ok {
		c.mu.Unlock()
		return id, nil
	}
	c.mu.Unlock()

	var resp struct {
		ID int `json:"id"`
	}
	url := fmt.Sprintf("%s/api/v4/groups/%s", c.base, group)
	found, err := c.doer.getFound(ctx, url, &resp)
	if err != nil {
		return 0, err
	}
	if !found || resp.ID == 0 {
		return 0, fmt.Errorf("integrations: группа GitLab %q не найдена (не засеяна?)", group)
	}
	c.mu.Lock()
	c.groupIDs[group] = resp.ID
	c.mu.Unlock()
	return resp.ID, nil
}

// resolveUserID резолвит GitLab-логин в числовой user_id через
// GET /api/v4/users?username=. Возвращает (0, false, nil), если пользователь не
// найден (вызывающий безопасно пропускает синхронизацию этого участника).
func (c *gitLabHTTP) resolveUserID(ctx context.Context, login string) (int, bool, error) {
	var users []struct {
		ID int `json:"id"`
	}
	url := fmt.Sprintf("%s/api/v4/users?username=%s", c.base, login)
	if _, err := c.doer.getFound(ctx, url, &users); err != nil {
		return 0, false, err
	}
	if len(users) == 0 || users[0].ID == 0 {
		return 0, false, nil
	}
	return users[0].ID, true, nil
}

// ownerUserID отображает субъект-владельца (UUID) на user_id GitLab через фикстуру
// ownerLogins и резолвинг логина. Возвращает found=false, если маппинг не задан или
// пользователь не найден — синхронизация участника тогда безопасно пропускается.
func (c *gitLabHTTP) ownerUserID(ctx context.Context, subject string) (int, bool, error) {
	login := c.ownerLogins[subject]
	if login == "" {
		return 0, false, nil // маппинг владельца не задан — пропуск (не ошибка)
	}
	return c.resolveUserID(ctx, login)
}

func (c *gitLabHTTP) CreateRepo(ctx context.Context, ref provisioning.ResourceRef) (provisioning.GitLabRepo, error) {
	path := ref.Project + "/" + ref.Name
	repo := provisioning.GitLabRepo{ProjectID: path, Path: path}
	if !c.real {
		// Прогон против WireMock-мока: мок принимает строковый namespace и отвечает
		// 409 на повторное создание (идемпотентность).
		body := map[string]string{"name": ref.Name, "namespace": ref.Project}
		if err := c.doer.call(ctx, http.MethodPost, c.base+"/api/v4/projects", body, nil, http.StatusConflict); err != nil {
			return provisioning.GitLabRepo{}, err
		}
		return repo, nil
	}
	// Реальный GitLab: GET-then-create. Повторный POST уже существующего проекта
	// отвечает 400 с текстом «has already been taken» (текст не контракт), поэтому
	// идемпотентность строим на проверке существования, а не на коде/тексте ошибки.
	exists, err := c.doer.getFound(ctx, fmt.Sprintf("%s/api/v4/projects/%s", c.base, encodeProjectPath(ref)), nil)
	if err != nil {
		return provisioning.GitLabRepo{}, err
	}
	if exists {
		return repo, nil // уже создан — no-op-успех
	}
	gid, err := c.resolveGroupID(ctx, ref.Project)
	if err != nil {
		return provisioning.GitLabRepo{}, err
	}
	// Реальный POST /api/v4/projects ожидает namespace_id (число), не строку.
	body := map[string]any{"name": ref.Name, "path": ref.Name, "namespace_id": gid}
	if err := c.doer.call(ctx, http.MethodPost, c.base+"/api/v4/projects", body, nil); err != nil {
		return provisioning.GitLabRepo{}, err
	}
	return repo, nil
}

func (c *gitLabHTTP) DeleteRepo(ctx context.Context, ref provisioning.ResourceRef) error {
	// Идемпотентность: 404 (уже удалён) — успех; реальный GitLab на удаление отвечает
	// 202 Accepted (принято к удалению) — тоже успех.
	url := fmt.Sprintf("%s/api/v4/projects/%s", c.base, encodeProjectPath(ref))
	return c.doer.call(ctx, http.MethodDelete, url, nil, nil, http.StatusNotFound, http.StatusAccepted)
}

func (c *gitLabHTTP) InjectVariables(ctx context.Context, in provisioning.InjectSecretsInput) error {
	// Записываем секреты по одному. Значения не логируем.
	vars := map[string]string{
		"VAULT_ROLE_ID":      in.Vault.RoleID,
		"VAULT_SECRET_ID":    in.Vault.SecretID,
		"HARBOR_ROBOT_NAME":  in.Harbor.RobotName,
		"HARBOR_ROBOT_TOKEN": in.Harbor.RobotToken,
	}
	if !c.real {
		// Мок: POST по одной; 409 (переменная существует) трактуем как успех.
		url := fmt.Sprintf("%s/api/v4/projects/%s/variables", c.base, in.GitLab.Path)
		for key, val := range vars {
			body := map[string]string{"key": key, "value": val}
			if err := c.doer.call(ctx, http.MethodPost, url, body, nil, http.StatusConflict); err != nil {
				return err
			}
		}
		return nil
	}
	// Реальный GitLab: upsert через GET-then-act (нативного upsert нет, а 400 «has
	// already been taken» — текст, не контракт). Существует → PUT перезапись, иначе
	// → POST создание.
	projEnc := encodeProjectPath(in.Ref)
	listURL := fmt.Sprintf("%s/api/v4/projects/%s/variables", c.base, projEnc)
	for key, val := range vars {
		varURL := fmt.Sprintf("%s/%s", listURL, key)
		found, err := c.doer.getFound(ctx, varURL, nil)
		if err != nil {
			return err
		}
		if found {
			if err := c.doer.call(ctx, http.MethodPut, varURL, map[string]string{"value": val}, nil); err != nil {
				return err
			}
			continue
		}
		if err := c.doer.call(ctx, http.MethodPost, listURL, map[string]string{"key": key, "value": val}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *gitLabHTTP) SyncMembers(ctx context.Context, ref provisioning.ResourceRef, add, remove []string) error {
	proj := encodeProjectPath(ref)
	if !c.real {
		// Мок: достаточно имени пользователя. Идемпотентность: добавление 409 —
		// успех; удаление 404 — успех.
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
	// Реальный GitLab: владелец (UUID) → user_id; access_level — число. Незаданный
	// маппинг владельца безопасно пропускаем (членство не обязательно для happy-path).
	membersURL := fmt.Sprintf("%s/api/v4/projects/%s/members", c.base, proj)
	for _, subj := range add {
		uid, ok, err := c.ownerUserID(ctx, subj)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		body := map[string]any{"user_id": uid, "access_level": 40}
		if err := c.doer.call(ctx, http.MethodPost, membersURL, body, nil, http.StatusConflict); err != nil {
			return err
		}
	}
	for _, subj := range remove {
		uid, ok, err := c.ownerUserID(ctx, subj)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		url := fmt.Sprintf("%s/%d", membersURL, uid)
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
	// Архивация репозитория (идемпотентно: реальный GitLab на повторную архивацию
	// отвечает 201 с текущим состоянием — успех; 404 на стенде — успех).
	url := fmt.Sprintf("%s/api/v4/projects/%s/archive", c.base, encodeProjectPath(ref))
	return c.doer.call(ctx, http.MethodPost, url, nil, nil, http.StatusConflict, http.StatusNotFound)
}

func (c *gitLabHTTP) Unarchive(ctx context.Context, ref provisioning.ResourceRef) error {
	// Компенсация: разархивировать репозиторий (идемпотентно).
	url := fmt.Sprintf("%s/api/v4/projects/%s/unarchive", c.base, encodeProjectPath(ref))
	return c.doer.call(ctx, http.MethodPost, url, nil, nil, http.StatusConflict, http.StatusNotFound)
}

func (c *gitLabHTTP) TransferRepo(ctx context.Context, ref provisioning.ResourceRef, target string) error {
	proj := encodeProjectPath(ref)
	url := fmt.Sprintf("%s/api/v4/projects/%s/transfer", c.base, proj)
	if !c.real {
		// Мок: 409 — репозиторий уже в целевой группе; 404 — успех на стенде.
		return c.doer.call(ctx, http.MethodPost, url, map[string]string{"namespace": target}, nil,
			http.StatusConflict, http.StatusNotFound)
	}
	// Реальный GitLab: идемпотентность по текущему namespace (повторный transfer в ту
	// же группу отвечает 400). Если репозиторий уже в целевой группе — no-op.
	var info struct {
		Namespace struct {
			FullPath string `json:"full_path"`
		} `json:"namespace"`
	}
	found, err := c.doer.getFound(ctx, fmt.Sprintf("%s/api/v4/projects/%s", c.base, proj), &info)
	if err != nil {
		return err
	}
	if !found {
		return nil // нечего переносить — на стенде трактуем как успех (no-op)
	}
	if info.Namespace.FullPath == target {
		return nil // уже в целевой группе — no-op
	}
	gid, err := c.resolveGroupID(ctx, target)
	if err != nil {
		return err
	}
	// Реальный GitLab переносит проект через PUT /projects/:id/transfer (POST на этот
	// путь — 404). Перенос необратим (точка невозврата, ADR-0013); namespace
	// принимает ID группы.
	return c.doer.call(ctx, http.MethodPut, url, map[string]any{"namespace": gid}, nil)
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
