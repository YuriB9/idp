// Package httpclient предоставляет межсервисный (S2S) HTTP-клиент с
// тюнингованным Transport и маппингом кодов ответа в канонические ошибки
// пакета errs (404 → ErrNotFound, 409 → ErrConflict).
package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/YuriB9/idp/pkg/errs"
)

// Config конфигурирует S2S-клиент.
type Config struct {
	// Timeout — общий таймаут запроса.
	Timeout time.Duration
	// MaxIdleConnsPerHost — лимит keep-alive соединений на хост.
	MaxIdleConnsPerHost int
	// DialContext позволяет подменить dialer (например,
	// ssrf.GuardedDialContext для исходящих к tenant-задаваемым URL).
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
}

// New создаёт *http.Client с тюнингованным Transport. Если cfg.DialContext не
// задан, используется стандартный dialer.
func New(cfg Config) *http.Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxIdleConnsPerHost == 0 {
		cfg.MaxIdleConnsPerHost = 100
	}
	dial := cfg.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dial,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{Timeout: cfg.Timeout, Transport: tr}
}

// MapStatus переводит HTTP-статус-код в каноническую ошибку errs.
// Для 2xx возвращается nil. Текст внутренней ошибки клиенту не отдаётся.
func MapStatus(code int) error {
	switch {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusNotFound:
		return errs.ErrNotFound
	case code == http.StatusConflict:
		return errs.ErrConflict
	case code == http.StatusUnauthorized:
		return errs.ErrUnauthorized
	case code == http.StatusForbidden:
		return errs.ErrForbidden
	case code == http.StatusBadRequest:
		return errs.ErrValidation
	default:
		return fmt.Errorf("httpclient: unexpected status %d", code)
	}
}
