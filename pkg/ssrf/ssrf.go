// Package ssrf предоставляет защиту от SSRF для исходящих вызовов к
// tenant-задаваемым URL (GitLab/Vault/Harbor).
//
// Защита двухуровневая (см. docs/IDP_MVP_plan.md, БЛОК 2):
//   - ValidateURL — проверка на этапе ЗАПИСИ/конфигурации: только https и не
//     приватный/loopback/link-local/ULA адрес.
//   - GuardedDialContext — проверка на этапе СОЕДИНЕНИЯ: даже если имя прошло
//     ValidateURL, на dial повторно проверяется фактический IP (против
//     TOCTOU / DNS-rebinding).
package ssrf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"
)

// ErrBlocked возвращается, когда адрес запрещён политикой SSRF-guard.
var ErrBlocked = errors.New("ssrf: address blocked")

// ValidateURL проверяет, что raw — корректный https-URL, ведущий на публичный
// адрес. Литеральные IP проверяются немедленно; для имён хостов фактический IP
// дополнительно проверяется в GuardedDialContext на этапе соединения.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("ssrf: parse url: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: scheme %q is not https", ErrBlocked, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: empty host", ErrBlocked)
	}
	if ip := net.ParseIP(host); ip != nil {
		if err := checkIP(ip); err != nil {
			return err
		}
	}
	return nil
}

// GuardedDialContext оборачивает dialer так, что каждый резолвнутый IP
// проверяется checkIP перед установкой TCP-соединения. Возвращается функция,
// пригодная для http.Transport.DialContext.
func GuardedDialContext(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("ssrf: split host port: %w", err)
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("ssrf: resolve %q: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("%w: no addresses for %q", ErrBlocked, host)
		}
		for _, ip := range ips {
			if err := checkIP(ip.IP); err != nil {
				return nil, err
			}
		}
		// Соединяемся по уже проверенному IP, чтобы исключить повторный
		// (потенциально перебинденный) резолв между проверкой и dial.
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

// checkIP отклоняет приватные, loopback, link-local, ULA и прочие
// непубличные адреса.
func checkIP(ip net.IP) error {
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("%w: loopback %s", ErrBlocked, ip)
	case ip.IsPrivate():
		return fmt.Errorf("%w: private %s", ErrBlocked, ip)
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return fmt.Errorf("%w: link-local %s", ErrBlocked, ip)
	case ip.IsUnspecified():
		return fmt.Errorf("%w: unspecified %s", ErrBlocked, ip)
	case ip.IsMulticast():
		return fmt.Errorf("%w: multicast %s", ErrBlocked, ip)
	case isULA(ip):
		return fmt.Errorf("%w: ULA %s", ErrBlocked, ip)
	default:
		return nil
	}
}

// isULA проверяет IPv6 Unique Local Address (fc00::/7).
func isULA(ip net.IP) bool {
	v6 := ip.To16()
	if v6 == nil || ip.To4() != nil {
		return false
	}
	return v6[0]&0xfe == 0xfc
}
