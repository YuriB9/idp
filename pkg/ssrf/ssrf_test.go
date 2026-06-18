package ssrf_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/YuriB9/idp/pkg/ssrf"
)

func TestValidateURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		url     string
		blocked bool
	}{
		{name: "public https host", url: "https://gitlab.example.com/api", blocked: false},
		{name: "http rejected", url: "http://gitlab.example.com", blocked: true},
		{name: "loopback ip", url: "https://127.0.0.1/x", blocked: true},
		{name: "private 10/8", url: "https://10.0.0.5", blocked: true},
		{name: "private 192.168", url: "https://192.168.1.1", blocked: true},
		{name: "link-local", url: "https://169.254.169.254/latest/meta-data", blocked: true},
		{name: "ipv6 loopback", url: "https://[::1]/x", blocked: true},
		{name: "ipv6 ULA", url: "https://[fc00::1]/x", blocked: true},
		{name: "empty host", url: "https://", blocked: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ssrf.ValidateURL(tc.url)
			if tc.blocked && err == nil {
				t.Fatalf("expected %q to be blocked", tc.url)
			}
			if !tc.blocked && err != nil {
				t.Fatalf("expected %q to pass, got %v", tc.url, err)
			}
		})
	}
}

func TestGuardedDialContext_BlocksLoopback(t *testing.T) {
	t.Parallel()
	dial := ssrf.GuardedDialContext(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := dial(ctx, "tcp", "127.0.0.1:443")
	if err == nil {
		t.Fatal("expected loopback dial to be blocked")
	}
	if !errors.Is(err, ssrf.ErrBlocked) && !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}
}
