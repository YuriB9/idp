// Package identity — справочник субъектов из каталога идентичностей Keycloak
// (OIDC, ADR-0016). Содержит клиент Keycloak Admin REST (ЧТЕНИЕ: поиск/резолв),
// выдачу токена сервис-аккаунта по client_credentials, отдельный кэш
// идентичностей (TTL) и usecase-фасад. Источник правды — живой запрос в Keycloak
// + кэш; проекционной таблицы пользователей в IDM нет. Все исходящие вызовы
// проходят через SSRF-guard (см. pkg/ssrf), секрет сервис-аккаунта не логируется.
package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/YuriB9/idp/pkg/httpclient"
	"github.com/YuriB9/idp/pkg/ssrf"
)

// resolveConcurrency — предел параллелизма резолва набора субъектов в Keycloak
// (ограниченно-параллельный Resolve вместо N последовательных round-trips).
const resolveConcurrency = 8

// ErrUnavailable — каталог идентичностей недоступен (Keycloak недоступен/таймаут/
// 5xx или не удалось получить токен сервис-аккаунта). На периметре маппится в 503
// (деградация): справочник не критичен для CheckAccess. Сырые детали не
// раскрываются — попадают только в лог по ключу slog "err".
var ErrUnavailable = errors.New("identity: каталог недоступен")

// Identity — идентичность субъекта из каталога Keycloak. Found=false означает
// «осиротевший» субъект: роль есть, в каталоге его нет.
type Identity struct {
	Subject     string
	Username    string
	Email       string
	DisplayName string
	Enabled     bool
	Found       bool
}

// Config конфигурирует клиент Keycloak Admin REST.
type Config struct {
	// BaseURL — базовый адрес Keycloak (например, http://keycloak:8080).
	BaseURL string
	// Realm — realm каталога пользователей (например, "idp").
	Realm string
	// ClientID — confidential-клиент сервис-аккаунта (realm-management
	// view-users/query-users).
	ClientID string
	// ClientSecret — секрет сервис-аккаунта. Не логируется и наружу не отдаётся.
	ClientSecret string
	// Guarded включает SSRF-guard (ValidateURL + GuardedDialContext). В локалке с
	// приватным адресом keycloak выключается флагом (SSRF_DISABLED); в проде —
	// всегда true (https + SSRF-guard).
	Guarded bool
	// Timeout — таймаут запроса; 0 → дефолт httpclient.
	Timeout time.Duration
}

// KeycloakClient — клиент Keycloak Admin REST для чтения каталога пользователей.
type KeycloakClient struct {
	cfg  Config
	hc   *http.Client
	base string

	mu    sync.Mutex // защищает кэш токена сервис-аккаунта
	token string
	exp   time.Time
	// tokenSF дедуплицирует одновременные запросы токена: один in-flight запрос
	// на N конкурентных промахов (без удержания mu на время сетевого вызова).
	tokenSF singleflight.Group
}

// NewKeycloakClient собирает клиент. При Guarded=true базовый URL проверяется
// ssrf.ValidateURL (только https и публичный адрес), а транспорт использует
// ssrf.GuardedDialContext (защита от TOCTOU/DNS-rebinding на этапе соединения).
func NewKeycloakClient(cfg Config) (*KeycloakClient, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		return nil, fmt.Errorf("identity: пустой BaseURL Keycloak")
	}
	if cfg.Realm == "" || cfg.ClientID == "" {
		return nil, fmt.Errorf("identity: realm и client-id сервис-аккаунта обязательны")
	}
	if cfg.Guarded {
		if err := ssrf.ValidateURL(base); err != nil {
			return nil, fmt.Errorf("identity: BaseURL не прошёл SSRF-guard: %w", err)
		}
	}
	hcCfg := httpclient.Config{Timeout: cfg.Timeout}
	if cfg.Guarded {
		hcCfg.DialContext = ssrf.GuardedDialContext(10 * time.Second)
	}
	return &KeycloakClient{cfg: cfg, hc: httpclient.New(hcCfg), base: base}, nil
}

// keycloakUser — представление пользователя в ответах Admin REST (briefRepresentation).
type keycloakUser struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Enabled   bool   `json:"enabled"`
}

// toIdentity маппит пользователя Keycloak в доменную идентичность (Found=true).
func (u keycloakUser) toIdentity() Identity {
	return Identity{
		Subject:     u.ID,
		Username:    u.Username,
		Email:       u.Email,
		DisplayName: strings.TrimSpace(u.FirstName + " " + u.LastName),
		Enabled:     u.Enabled,
		Found:       true,
	}
}

// Search ищет пользователей каталога по строке (username/email/имя) c offset-
// пагинацией Keycloak (first/max). Недоступность Keycloak/токена → ErrUnavailable.
func (c *KeycloakClient) Search(ctx context.Context, query string, first, max int) ([]Identity, error) {
	q := url.Values{}
	q.Set("search", query)
	q.Set("first", strconv.Itoa(first))
	q.Set("max", strconv.Itoa(max))
	q.Set("briefRepresentation", "true")
	endpoint := fmt.Sprintf("%s/admin/realms/%s/users?%s", c.base, url.PathEscape(c.cfg.Realm), q.Encode())

	var users []keycloakUser
	if err := c.adminGet(ctx, endpoint, &users); err != nil {
		return nil, err
	}
	out := make([]Identity, 0, len(users))
	for _, u := range users {
		out = append(out, u.toIdentity())
	}
	return out, nil
}

// Resolve резолвит набор канонических ключей (sub = id пользователя) в
// идентичности. Отсутствующий в каталоге субъект возвращается с Found=false (не
// опускается). Недоступность Keycloak/токена → ErrUnavailable.
func (c *KeycloakClient) Resolve(ctx context.Context, subjects []string) ([]Identity, error) {
	// Ограниченно-параллельный резолв: запросы независимы, результат пишется в
	// предвыделенный срез по индексу субъекта (без гонок — каждая горутина пишет
	// свой элемент), поэтому порядок выхода совпадает с порядком входа.
	out := make([]Identity, len(subjects))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(resolveConcurrency)
	for i, sub := range subjects {
		g.Go(func() error {
			endpoint := fmt.Sprintf("%s/admin/realms/%s/users/%s", c.base, url.PathEscape(c.cfg.Realm), url.PathEscape(sub))
			var u keycloakUser
			err := c.adminGet(gctx, endpoint, &u)
			switch {
			case errors.Is(err, errNotFoundUser):
				// «Осиротевший» субъект: роль есть, в каталоге нет — не ошибка.
				out[i] = Identity{Subject: sub, Found: false}
			case err != nil:
				return err
			default:
				id := u.toIdentity()
				id.Subject = sub // канонический ключ — запрошенный sub
				out[i] = id
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// errNotFoundUser — внутренний маркер «пользователь не найден» (404 от Admin REST
// при резолве по id). Не выходит за пределы пакета.
var errNotFoundUser = errors.New("identity: пользователь не найден")

// adminGet выполняет авторизованный GET к Admin REST и декодирует JSON в out.
// 404 → errNotFoundUser; прочие не-2xx, транспортные ошибки и провал токена →
// ErrUnavailable (без сырых деталей; детали — у вызывающего в лог).
func (c *KeycloakClient) adminGet(ctx context.Context, endpoint string, out any) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: сборка запроса: %v", ErrUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: выполнение запроса: %v", ErrUnavailable, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return errNotFoundUser
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: admin rest статус %d", ErrUnavailable, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%w: разбор ответа: %v", ErrUnavailable, err)
	}
	return nil
}

// tokenResponse — ответ token-эндпоинта Keycloak.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// accessToken возвращает токен сервис-аккаунта, переиспользуя кэш в памяти до
// истечения с запасом. Мьютекс удерживается ТОЛЬКО на чтение/запись полей кэша
// (без сетевого I/O под локом); сетевой запрос дедуплицируется singleflight —
// один in-flight запрос на N конкурентных промахов. Секрет в логи/ошибки не
// попадает.
func (c *KeycloakClient) accessToken(ctx context.Context) (string, error) {
	// Быстрый путь: валидный кэшированный токен (под локом только чтение полей).
	if tok, ok := c.cachedToken(); ok {
		return tok, nil
	}

	// Промах: единственный in-flight запрос токена (singleflight). Сетевой вызов
	// выполняется без удержания мьютекса.
	v, err, _ := c.tokenSF.Do("token", func() (any, error) {
		// Повторная проверка: лидер мог обновить токен, пока мы ждали singleflight.
		if tok, ok := c.cachedToken(); ok {
			return tok, nil
		}
		// Запрос токена ведём в контексте, переживающем отмену контекста лидера:
		// ведомые singleflight не должны падать из-за отмены первого вызывающего
		// (таймаут всё равно ограничен httpclient). Это снимает caveat singleflight.
		tok, exp, ferr := c.fetchToken(context.WithoutCancel(ctx))
		if ferr != nil {
			return "", ferr
		}
		c.mu.Lock()
		c.token, c.exp = tok, exp
		c.mu.Unlock()
		return tok, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// cachedToken возвращает кэшированный токен, если он ещё валиден. Удерживает
// мьютекс только на время чтения полей (без I/O).
func (c *KeycloakClient) cachedToken() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.exp) {
		return c.token, true
	}
	return "", false
}

// fetchToken запрашивает новый токен сервис-аккаунта по client_credentials и
// возвращает токен с моментом истечения (с запасом). Без удержания мьютекса.
// Секрет/тело наружу не отдаются.
func (c *KeycloakClient) fetchToken(ctx context.Context) (string, time.Time, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	endpoint := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", c.base, url.PathEscape(c.cfg.Realm))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: сборка запроса токена: %v", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.hc.Do(req)
	if err != nil {
		// Намеренно НЕ включаем тело/секрет — только факт ошибки транспорта.
		return "", time.Time{}, fmt.Errorf("%w: запрос токена сервис-аккаунта", ErrUnavailable)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("%w: токен сервис-аккаунта статус %d", ErrUnavailable, resp.StatusCode)
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", time.Time{}, fmt.Errorf("%w: разбор токена", ErrUnavailable)
	}
	if tr.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("%w: пустой токен сервис-аккаунта", ErrUnavailable)
	}
	// Запас в 30с от заявленного срока, чтобы не использовать токен на грани истечения.
	ttl := time.Duration(tr.ExpiresIn) * time.Second
	if ttl > 30*time.Second {
		ttl -= 30 * time.Second
	}
	return tr.AccessToken, time.Now().Add(ttl), nil
}
