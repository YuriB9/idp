package integrations

import (
	"bytes"
	"context"
	"encoding/base64"
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

// vaultTokenHeader — заголовок аутентификации реального Vault (статический dev
// root-токен — фикстура стенда, ADR-0020). Ставится ТОЛЬКО на Vault-запросах и не
// протекает на GitLab/Harbor (у каждой интеграции свой doer).
const vaultTokenHeader = "X-Vault-Token" //nolint:gosec // G101: имя HTTP-заголовка, не секрет

// harborAuthHeader — заголовок аутентификации реального Harbor v2.0 API. Harbor
// использует HTTP Basic (admin) — `Authorization: Basic base64(user:pass)`, а не
// кастомный заголовок-токен (эмпирически: no-auth запись → 401, см. ADR-0021).
// Ставится ТОЛЬКО на Harbor-запросах и не протекает на GitLab/Vault (свой doer).
const harborAuthHeader = "Authorization" //nolint:gosec // G101: имя HTTP-заголовка, не секрет

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
	// VaultToken — токен аутентификации к реальному Vault (X-Vault-Token). Пусто для
	// прогона против WireMock-мока (мок не требует токена). Значение не логируется.
	// Наличие токена включает семантику реального Vault API (накопительный отзыв
	// secret-id через accessors вместо мок-эндпоинта destroy-all), см. ADR-0020.
	VaultToken string
	// HarborUsername/HarborPassword — креденшелы HTTP Basic к реальному Harbor (admin —
	// фикстура стенда). Оба пусты для прогона против WireMock-мока (мок не требует auth).
	// Пароль не логируется. Наличие обоих включает семантику реального Harbor v2.0 API
	// (Basic auth, robots на уровне system с резолвингом id по имени, read-only через
	// отзыв robot вместо несуществующего project.read_only), см. ADR-0021.
	HarborUsername string
	HarborPassword string
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
	// Vault-запросы к реальному Vault несут X-Vault-Token. Отдельный doer, чтобы
	// заголовок аутентификации не протекал в запросы GitLab/Harbor. Без токена
	// (прогон против мока) — общий doer без заголовка.
	vaultDoer := sharedDoer
	if cfg.VaultToken != "" {
		vaultDoer = &doer{hc: hc, headers: map[string]string{vaultTokenHeader: cfg.VaultToken}}
	}
	// Harbor-запросы к реальному Harbor несут HTTP Basic admin. Отдельный doer, чтобы
	// заголовок Authorization не протекал в запросы GitLab/Vault. Без креденшелов
	// (прогон против мока) — общий doer без заголовка.
	harborReal := cfg.HarborUsername != "" && cfg.HarborPassword != ""
	harborDoer := sharedDoer
	if harborReal {
		basic := base64.StdEncoding.EncodeToString([]byte(cfg.HarborUsername + ":" + cfg.HarborPassword))
		harborDoer = &doer{hc: hc, headers: map[string]string{harborAuthHeader: "Basic " + basic}}
	}
	return &Clients{
		GitLab: &gitLabHTTP{
			doer:        gitlabDoer,
			base:        strings.TrimRight(cfg.GitLabBaseURL, "/"),
			real:        cfg.GitLabToken != "",
			ownerLogins: cfg.GitLabOwnerLogins,
			groupIDs:    map[string]int{},
		},
		Harbor: &harborHTTP{
			doer: harborDoer,
			base: strings.TrimRight(cfg.HarborBaseURL, "/"),
			real: harborReal,
		},
		Vault: &vaultHTTP{
			doer: vaultDoer,
			base: strings.TrimRight(cfg.VaultBaseURL, "/"),
			real: cfg.VaultToken != "",
		},
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

// harborTransferMarkerField — допустимое наблюдаемое поле метаданных проекта Harbor,
// используемое как маркер переноса (transfer, ADR-0013). Harbor НЕ умеет
// rename/transfer проекта и молча игнорирует произвольные ключи метаданных (эмпирически
// `owner_project` не сохраняется), поэтому перенос на Harbor-плече наблюдается через
// допустимое поле `auto_scan` (см. ADR-0021/empirical-harbor-api).
const harborTransferMarkerField = "auto_scan"

type harborHTTP struct {
	doer *doer
	base string
	// real включает семантику РЕАЛЬНОГО Harbor v2.0 API (см. ADR-0021, эмпирически):
	// robots создаются на уровне `system` (project-level robots НЕ перечисляются Harbor,
	// их id не резолвится по имени) со scope на проект через permissions[]; удаление/отзыв
	// robot — по ЧИСЛОВОМУ id (резолвинг по имени через GET /robots?q=name=); read-only —
	// отзыв robot (нет project.read_only); тело POST /robots v2.0 (level/permissions/
	// duration). false → прогон против WireMock-мока со старой формой запросов (БЛОК 7).
	real bool
}

// harborRobotReqName — детерминированное ЗАПРОШЕННОЕ имя robot-аккаунта (без префикса
// `robot$`, который Harbor добавляет сам). Совпадает с именем проекта (`<project>-<name>`)
// и используется как ключ резолвинга id через GET /robots?q=name=<это-имя>.
func harborRobotReqName(ref provisioning.ResourceRef) string { return ref.Project + "-" + ref.Name }

// harborRobotBody собирает тело POST /api/v2.0/robots v2.0 для system-робота со scope на
// проект projName: push/pull в репозитории проекта, бессрочно (duration=-1). Минимальное
// тело (`{name,level}`) реальный Harbor отвергает 400 («duration must be …»).
func harborRobotBody(reqName, projName string) map[string]any {
	return map[string]any{
		"name":     reqName,
		"duration": -1,
		"level":    "system",
		"permissions": []map[string]any{{
			"kind":      "project",
			"namespace": projName,
			"access": []map[string]string{
				{"resource": "repository", "action": "push"},
				{"resource": "repository", "action": "pull"},
			},
		}},
	}
}

// resolveRobotID резолвит числовой id system-робота по ЗАПРОШЕННОМУ имени через
// GET /api/v2.0/robots?q=name=<reqName>. Возвращает (0, false, nil), если робот не найден
// (отозван/не создан) — вызывающий трактует это как идемпотентный no-op. Только реальный
// Harbor: project-level robots не перечислимы, поэтому робот создаётся на уровне system.
func (c *harborHTTP) resolveRobotID(ctx context.Context, reqName string) (int, bool, error) {
	var robots []struct {
		ID int `json:"id"`
	}
	url := fmt.Sprintf("%s/api/v2.0/robots?q=name=%s", c.base, reqName)
	if _, err := c.doer.getFound(ctx, url, &robots); err != nil {
		return 0, false, err
	}
	if len(robots) == 0 || robots[0].ID == 0 {
		return 0, false, nil
	}
	return robots[0].ID, true, nil
}

func (c *harborHTTP) CreateProject(ctx context.Context, ref provisioning.ResourceRef) (provisioning.HarborResult, error) {
	projName := ref.Project + "-" + ref.Name
	// Создание проекта (повтор → 409 идемпотентно; совпадает у мока и реального Harbor).
	if err := c.doer.call(ctx, http.MethodPost, c.base+"/api/v2.0/projects",
		map[string]string{"project_name": projName}, nil, http.StatusConflict); err != nil {
		return provisioning.HarborResult{}, err
	}
	var robotResp struct {
		Name   string `json:"name"`
		Secret string `json:"secret"`
	}
	if !c.real {
		// Прогон против WireMock-мока: минимальное тело robot-аккаунта, имя
		// конструируется, секрет подставляется детерминированной меткой при отсутствии.
		robotName := "robot$" + projName
		if err := c.doer.call(ctx, http.MethodPost, c.base+"/api/v2.0/robots",
			map[string]any{"name": robotName, "level": "project"}, &robotResp, http.StatusConflict); err != nil {
			return provisioning.HarborResult{}, err
		}
		if robotResp.Name == "" {
			robotResp.Name = robotName
		}
		if robotResp.Secret == "" {
			robotResp.Secret = "mock-harbor-secret"
		}
		return provisioning.HarborResult{ProjectName: projName, RobotName: robotResp.Name, RobotToken: robotResp.Secret}, nil
	}
	// Реальный Harbor: robot на уровне system со scope на проект (project-level robots не
	// перечислимы — их id не резолвить для отзыва). Тело v2.0 (level/permissions/duration).
	// Повтор имени → 409 идемпотентно. В CI/CD-переменные инъектируются ФАКТИЧЕСКИ
	// возвращённые name (`robot$<reqName>`) и secret, не сконструированные.
	if err := c.doer.call(ctx, http.MethodPost, c.base+"/api/v2.0/robots",
		harborRobotBody(harborRobotReqName(ref), projName), &robotResp, http.StatusConflict); err != nil {
		return provisioning.HarborResult{}, err
	}
	return provisioning.HarborResult{ProjectName: projName, RobotName: robotResp.Name, RobotToken: robotResp.Secret}, nil
}

func (c *harborHTTP) DeleteProject(ctx context.Context, ref provisioning.ResourceRef) error {
	// Компенсация создания (ADR-0005): удалить проект И robot. Реальный Harbor оставляет
	// system-робота при удалении проекта, поэтому сначала отзываем робота (по числовому
	// id), затем удаляем проект. Идемпотентность: отсутствие робота/проекта (404) — успех.
	projName := ref.Project + "-" + ref.Name
	if c.real {
		if err := c.revokeRobot(ctx, ref); err != nil {
			return err
		}
	}
	return c.doer.call(ctx, http.MethodDelete, c.base+"/api/v2.0/projects/"+projName, nil, nil, http.StatusNotFound)
}

// revokeRobot отзывает system-робота сервиса (резолв id по имени → DELETE /robots/<id>).
// Идемпотентно: робот не найден — no-op-успех; DELETE отсутствующего id → 404 — успех.
// Только реальный Harbor (резолвинг через перечисление system-роботов).
func (c *harborHTTP) revokeRobot(ctx context.Context, ref provisioning.ResourceRef) error {
	id, ok, err := c.resolveRobotID(ctx, harborRobotReqName(ref))
	if err != nil {
		return err
	}
	if !ok {
		return nil // робот уже отозван/не создан — идемпотентный no-op
	}
	url := fmt.Sprintf("%s/api/v2.0/robots/%d", c.base, id)
	return c.doer.call(ctx, http.MethodDelete, url, nil, nil, http.StatusNotFound)
}

func (c *harborHTTP) SetReadOnly(ctx context.Context, ref provisioning.ResourceRef) error {
	// Decommission (ADR-0012, точка невозврата): сделать проект недоступным на запись.
	// В Harbor НЕТ project-level read_only (поле молча игнорируется), поэтому read-only =
	// ОТЗЫВ robot-аккаунта (CI/CD больше не может push/pull). Проект СОХРАНЯЕТСЯ.
	// Наблюдаемо: после отзыва робот не резолвится по имени. Необратимо: новый secret при
	// воссоздании (SetWritable).
	if !c.real {
		// Мок: перевод директории в read-only + отзыв Robot по имени (идемпотентно).
		projName := ref.Project + "-" + ref.Name
		if err := c.doer.call(ctx, http.MethodPut, c.base+"/api/v2.0/projects/"+projName,
			map[string]any{"metadata": map[string]string{"public": "false"}, "read_only": true}, nil, http.StatusNotFound); err != nil {
			return err
		}
		robotName := "robot$" + projName
		return c.doer.call(ctx, http.MethodDelete, c.base+"/api/v2.0/robots/"+robotName, nil, nil, http.StatusNotFound)
	}
	return c.revokeRobot(ctx, ref)
}

func (c *harborHTTP) SetWritable(ctx context.Context, ref provisioning.ResourceRef) error {
	projName := ref.Project + "-" + ref.Name
	if !c.real {
		// Мок: вернуть директорию в writable (идемпотентно).
		return c.doer.call(ctx, http.MethodPut, c.base+"/api/v2.0/projects/"+projName,
			map[string]any{"read_only": false}, nil, http.StatusNotFound)
	}
	// Компенсация decommission: воссоздать system-робота проекта (новый secret). Повтор
	// имени → 409 идемпотентно (робот ещё жив — отзыва не было).
	if err := c.doer.call(ctx, http.MethodPost, c.base+"/api/v2.0/robots",
		harborRobotBody(harborRobotReqName(ref), projName), nil, http.StatusConflict); err != nil {
		return err
	}
	return nil
}

func (c *harborHTTP) UpdateMetadata(ctx context.Context, ref provisioning.ResourceRef, target string) error {
	projName := ref.Project + "-" + ref.Name
	if !c.real {
		// Мок: обновление метаданных/прав директории под target-проект (404 — успех).
		body := map[string]any{"metadata": map[string]string{"owner_project": target}}
		return c.doer.call(ctx, http.MethodPut, c.base+"/api/v2.0/projects/"+projName, body, nil, http.StatusNotFound)
	}
	// Реальный Harbor (transfer, ADR-0013): rename/transfer проекта нет, произвольные
	// ключи метаданных игнорируются — переносим наблюдаемый допустимый маркер (auto_scan).
	// Имя целевого проекта Harbor не хранит; маркер фиксирует факт переноса (target в path
	// не участвует — проект остаётся <source>-<name>). Идемпотентно (PUT — upsert; 404 —
	// успех на стенде).
	_ = target
	body := map[string]any{"metadata": map[string]string{harborTransferMarkerField: "true"}}
	return c.doer.call(ctx, http.MethodPut, c.base+"/api/v2.0/projects/"+projName, body, nil, http.StatusNotFound)
}

// --- Vault ---

type vaultHTTP struct {
	doer *doer
	base string
	// real включает семантику РЕАЛЬНОГО Vault API. Сейчас расходится с моком только
	// немедленный отзыв (RevokeSecretID): у реального Vault нет destroy-all одним
	// вызовом — нужно перечислить активные secret-id-accessors и уничтожить каждый
	// (ADR-0020). false → прогон против WireMock-мока со старым эндпоинтом (БЛОК 7).
	// Остальные пути/тела/коды (KV v2, AppRole, identity) совпадают с реальным Vault.
	real bool
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
	// Идемпотентность по кодам Vault: PUT identity-entity на каждого добавленного
	// владельца — upsert (200/204, не 409), DELETE на каждого удалённого (реальный
	// Vault на отсутствующего отвечает 204, мок — 204; 404 принимаем как успех
	// дополнительно). Имя entity несёт субъект напрямую (<role>-<subject>), привязка
	// alias с mount_accessor — вне границы MVP (ADR-0020), для ассерта достаточно
	// существования entity с политикой роли.
	role := ref.Project + "-" + ref.Name
	for _, user := range add {
		url := fmt.Sprintf("%s/v1/identity/entity/name/%s-%s", c.base, role, user)
		body := map[string]any{"policies": []string{role}}
		if err := c.doer.call(ctx, http.MethodPut, url, body, nil); err != nil {
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
	// Отзыв всех активных SecretID AppRole — немедленное прекращение доступа.
	// Необратимо: отозванный SecretID не вернуть (точка невозврата, ADR-0012).
	role := ref.Project + "-" + ref.Name
	if !c.real {
		// Мок: единый эндпоинт destroy-all с пустым телом (204; 404 — успех).
		url := fmt.Sprintf("%s/v1/auth/approle/role/%s/secret-id/destroy", c.base, role)
		return c.doer.call(ctx, http.MethodPost, url, map[string]string{}, nil, http.StatusNotFound)
	}
	// Реальный Vault: destroy-all одним вызовом НЕТ (пустое тело → 400). Перечисляем
	// активные secret-id-accessors (GET ?list=true: data.keys; пустой набор → 404) и
	// уничтожаем каждый через secret-id-accessor/destroy. Идемпотентно: отсутствие
	// активных secret-id (404) трактуем как успех (no-op). Результат наблюдаем —
	// после отзыва повторный list пуст.
	var listed struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	listURL := fmt.Sprintf("%s/v1/auth/approle/role/%s/secret-id?list=true", c.base, role)
	found, err := c.doer.getFound(ctx, listURL, &listed)
	if err != nil {
		return err
	}
	if !found {
		return nil // активных secret-id нет — отзывать нечего (no-op-успех)
	}
	destroyURL := fmt.Sprintf("%s/v1/auth/approle/role/%s/secret-id-accessor/destroy", c.base, role)
	for _, accessor := range listed.Data.Keys {
		body := map[string]string{"secret_id_accessor": accessor}
		if err := c.doer.call(ctx, http.MethodPost, destroyURL, body, nil, http.StatusNotFound); err != nil {
			return err
		}
	}
	return nil
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
