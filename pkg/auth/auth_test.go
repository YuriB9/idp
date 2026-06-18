package auth_test

import (
	"context"
	"testing"

	"github.com/YuriB9/idp/pkg/auth"
)

func TestNewVerifier_FailClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     auth.Config
		wantErr bool
	}{
		{
			name:    "empty JWKS_URL with auth enabled is rejected",
			cfg:     auth.Config{Disabled: false, JWKSURL: ""},
			wantErr: true,
		},
		{
			name:    "non-https JWKS_URL is rejected",
			cfg:     auth.Config{Disabled: false, JWKSURL: "http://issuer/jwks"},
			wantErr: true,
		},
		{
			name:    "disabled bypasses JWKS requirement",
			cfg:     auth.Config{Disabled: true},
			wantErr: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := auth.NewVerifier(context.Background(), tc.cfg)
			if tc.wantErr && err == nil {
				t.Fatal("expected error (fail-closed), got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestVerify_DisabledReturnsEmptyClaims(t *testing.T) {
	t.Parallel()
	v, err := auth.NewVerifier(context.Background(), auth.Config{Disabled: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	claims, err := v.Verify("anything")
	if err != nil {
		t.Fatalf("disabled verify must not error: %v", err)
	}
	if claims == nil {
		t.Fatal("expected non-nil claims when disabled")
	}
}

func TestCheckAdminKey_ConstantTime(t *testing.T) {
	t.Parallel()
	v, err := auth.NewVerifier(context.Background(), auth.Config{Disabled: true, AdminKey: "s3cret"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v.CheckAdminKey("s3cret") {
		t.Fatal("expected matching admin key to pass")
	}
	if v.CheckAdminKey("wrong") {
		t.Fatal("expected mismatching admin key to fail")
	}

	noKey, _ := auth.NewVerifier(context.Background(), auth.Config{Disabled: true})
	if noKey.CheckAdminKey("anything") {
		t.Fatal("expected unset admin key to always fail")
	}
}

func TestBearerToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		header string
		want   string
		ok     bool
	}{
		{header: "Bearer abc.def.ghi", want: "abc.def.ghi", ok: true},
		{header: "bearer abc", want: "abc", ok: true},
		{header: "Basic xxx", want: "", ok: false},
		{header: "", want: "", ok: false},
		{header: "Bearer ", want: "", ok: false},
	}
	for _, tc := range tests {
		got, ok := auth.BearerToken(tc.header)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("BearerToken(%q) = (%q,%v), want (%q,%v)", tc.header, got, ok, tc.want, tc.ok)
		}
	}
}
