package config_test

import (
	"testing"
	"time"

	"github.com/YuriB9/idp/pkg/config"
)

// Тесты с t.Setenv не могут быть параллельными (ограничение testing).
//
//nolint:paralleltest
func TestInt_AcceptsLegitimateZero(t *testing.T) {
	t.Setenv("CFG_INT", "0")
	got, err := config.Int("CFG_INT", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Fatalf("want 0 (legit zero must not become default), got %d", got)
	}
}

func TestInt_UnsetReturnsDefault(t *testing.T) {
	t.Parallel()
	got, err := config.Int("CFG_INT_UNSET", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42 {
		t.Fatalf("want default 42, got %d", got)
	}
}

//nolint:paralleltest // uses t.Setenv
func TestString(t *testing.T) {
	tests := []struct {
		name string
		set  bool
		val  string
		def  string
		want string
	}{
		{name: "set", set: true, val: "x", def: "d", want: "x"},
		{name: "empty_treated_as_unset", set: true, val: "", def: "d", want: "d"},
		{name: "unset", set: false, def: "d", want: "d"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key := "CFG_STR_" + tc.name
			if tc.set {
				t.Setenv(key, tc.val)
			}
			if got := config.String(key, tc.def); got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

//nolint:paralleltest // uses t.Setenv
func TestDuration(t *testing.T) {
	t.Setenv("CFG_DUR", "1500ms")
	got, err := config.Duration("CFG_DUR", time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 1500*time.Millisecond {
		t.Fatalf("want 1.5s, got %v", got)
	}
}
