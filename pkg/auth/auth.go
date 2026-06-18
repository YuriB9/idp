// Package auth реализует строгую, fail-closed проверку JWT (см. ADR-0003,
// docs/IDP_MVP_plan.md БЛОК 2).
//
// Правила:
//   - JWT валидируется строго: WithAudience / WithIssuer / WithValidMethods /
//     WithExpirationRequired, по ключам JWKS.
//   - Fail-closed: пустой JWKS_URL НЕ означает passthrough. Конструктор
//     возвращает ошибку; обёртка MustVerifierFromEnv завершает процесс
//     os.Exit(1). Отключить проверку можно ТОЛЬКО явным AUTH_DISABLED=true.
//   - JWKS_URL форсируется на https.
//   - Сравнение admin/god-key — через subtle.ConstantTimeCompare.
package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"

	"github.com/YuriB9/idp/pkg/config"
	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/pkg/logger"
)

// Config конфигурирует верификатор JWT.
type Config struct {
	// Disabled отключает проверку (только локалка, явный AUTH_DISABLED=true).
	Disabled bool
	// JWKSURL — endpoint JWKS (обязателен и https, если !Disabled).
	JWKSURL string
	// Issuer — ожидаемый iss.
	Issuer string
	// Audience — ожидаемый aud.
	Audience string
	// ValidMethods — допустимые алгоритмы подписи (например, ["RS256"]).
	ValidMethods []string
	// AdminKey — опциональный god-key для сервисного доступа.
	AdminKey string
}

// Verifier проверяет токены согласно Config.
type Verifier struct {
	cfg      Config
	parser   *jwt.Parser
	keyfunc  jwt.Keyfunc
	adminKey []byte
}

// claimsContextKey — приватный тип ключа контекста для проброса claims.
type claimsContextKey struct{}

// NewVerifier создаёт верификатор. Fail-closed: при !Disabled пустой или
// не-https JWKSURL → ошибка (НЕ passthrough). Сетевой вызов JWKS выполняется
// в фоне keyfunc и обновляется по TTL.
func NewVerifier(ctx context.Context, cfg Config) (*Verifier, error) {
	v := &Verifier{cfg: cfg, adminKey: []byte(cfg.AdminKey)}
	if cfg.Disabled {
		return v, nil
	}
	if cfg.JWKSURL == "" {
		return nil, errors.New("auth: empty JWKS_URL with auth enabled (fail-closed)")
	}
	u, err := url.Parse(cfg.JWKSURL)
	if err != nil {
		return nil, fmt.Errorf("auth: parse JWKS_URL: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("auth: JWKS_URL must be https, got %q", u.Scheme)
	}
	methods := cfg.ValidMethods
	if len(methods) == 0 {
		methods = []string{"RS256"}
	}
	opts := []jwt.ParserOption{
		jwt.WithValidMethods(methods),
		jwt.WithExpirationRequired(),
	}
	if cfg.Issuer != "" {
		opts = append(opts, jwt.WithIssuer(cfg.Issuer))
	}
	if cfg.Audience != "" {
		opts = append(opts, jwt.WithAudience(cfg.Audience))
	}
	v.parser = jwt.NewParser(opts...)

	k, err := keyfunc.NewDefaultCtx(ctx, []string{cfg.JWKSURL})
	if err != nil {
		return nil, fmt.Errorf("auth: init JWKS: %w", err)
	}
	v.keyfunc = k.Keyfunc
	return v, nil
}

// MustVerifierFromEnv читает конфигурацию из окружения и при ошибке/misconfig
// завершает процесс os.Exit(1) (fail-closed). Используется в main сервисов.
//
// Переменные: AUTH_DISABLED, JWKS_URL, AUTH_ISSUER, AUTH_AUDIENCE,
// AUTH_METHODS (csv), AUTH_ADMIN_KEY.
func MustVerifierFromEnv(ctx context.Context, log *slog.Logger) *Verifier {
	disabled, err := config.Bool("AUTH_DISABLED", false)
	if err != nil {
		log.Error("auth: bad AUTH_DISABLED", logger.Err(err))
		os.Exit(1)
	}
	cfg := Config{
		Disabled: disabled,
		JWKSURL:  config.String("JWKS_URL", ""),
		Issuer:   config.String("AUTH_ISSUER", ""),
		Audience: config.String("AUTH_AUDIENCE", ""),
		AdminKey: config.String("AUTH_ADMIN_KEY", ""),
	}
	if m := config.String("AUTH_METHODS", ""); m != "" {
		cfg.ValidMethods = strings.Split(m, ",")
	}
	v, err := NewVerifier(ctx, cfg)
	if err != nil {
		log.Error("auth: verifier init failed (fail-closed)", logger.Err(err))
		os.Exit(1)
	}
	if disabled {
		log.Warn("auth: DISABLED via AUTH_DISABLED=true (local only)")
	}
	return v
}

// Claims — извлечённые из токена утверждения.
type Claims struct {
	Subject string
	Issuer  string
	Raw     jwt.MapClaims
}

// Verify проверяет строковый токен и возвращает Claims или ошибку errs.ErrUnauthorized.
// При Disabled возвращает пустые Claims без проверки.
func (v *Verifier) Verify(tokenString string) (*Claims, error) {
	if v.cfg.Disabled {
		return &Claims{}, nil
	}
	mc := jwt.MapClaims{}
	tok, err := v.parser.ParseWithClaims(tokenString, mc, v.keyfunc)
	if err != nil || !tok.Valid {
		// Внутренний текст ошибки наружу не отдаём — только канонический sentinel.
		return nil, errs.ErrUnauthorized
	}
	sub, _ := mc["sub"].(string)
	iss, _ := mc["iss"].(string)
	return &Claims{Subject: sub, Issuer: iss, Raw: mc}, nil
}

// CheckAdminKey сравнивает предъявленный ключ с настроенным admin-key за
// постоянное время (subtle.ConstantTimeCompare). false, если admin-key не задан.
func (v *Verifier) CheckAdminKey(presented string) bool {
	if len(v.adminKey) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(v.adminKey, []byte(presented)) == 1
}

// Disabled сообщает, отключена ли проверка.
func (v *Verifier) Disabled() bool { return v.cfg.Disabled }

// ContextWithClaims кладёт Claims в контекст.
func ContextWithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, c)
}

// ClaimsFromContext извлекает Claims из контекста.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsContextKey{}).(*Claims)
	return c, ok
}

// BearerToken извлекает токен из заголовка Authorization вида "Bearer <token>".
func BearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(header[len(prefix):]), true
}
