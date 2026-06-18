package repository

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestCursorRoundtrip проверяет, что кодирование/декодирование курсора сохраняет ключ.
func TestCursorRoundtrip(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	ts := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		in   cursor
	}{
		{name: "обычный ключ", in: cursor{CreatedAt: ts, ID: id}},
		{name: "нулевой id", in: cursor{CreatedAt: ts, ID: uuid.Nil}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := decodeCursor(encodeCursor(tc.in))
			if err != nil {
				t.Fatalf("decodeCursor: неожиданная ошибка: %v", err)
			}
			if !got.CreatedAt.Equal(tc.in.CreatedAt) || got.ID != tc.in.ID {
				t.Fatalf("после roundtrip получили %+v, ожидали %+v", got, tc.in)
			}
		})
	}
}

// TestDecodeCursorInvalid проверяет, что повреждённый курсор отвергается.
func TestDecodeCursorInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
	}{
		{name: "не base64", token: "!!!не-base64!!!"},
		{name: "base64 но не JSON", token: "Zm9vYmFy"}, // "foobar"
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := decodeCursor(tc.token); err == nil {
				t.Fatalf("ожидали ошибку для %q, получили nil", tc.token)
			}
		})
	}
}

// TestClampPageSize проверяет приведение размера страницы к допустимому диапазону.
func TestClampPageSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "ноль → дефолт", in: 0, want: defaultPageSize},
		{name: "отрицательный → дефолт", in: -5, want: defaultPageSize},
		{name: "в пределах", in: 25, want: 25},
		{name: "сверх предела → максимум", in: 1000, want: maxPageSize},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := clampPageSize(tc.in); got != tc.want {
				t.Fatalf("clampPageSize(%d) = %d, ожидали %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseStatus проверяет строгий маппинг статусов из БД.
func TestParseStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    Status
		wantErr bool
	}{
		{name: "creating", raw: "creating", want: StatusCreating},
		{name: "active", raw: "active", want: StatusActive},
		{name: "decommissioned", raw: "decommissioned", want: StatusDecommissioned},
		{name: "failed", raw: "failed", want: StatusFailed},
		{name: "неизвестный → ошибка", raw: "bogus", wantErr: true},
		{name: "пустой → ошибка", raw: "", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseStatus(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ожидали ошибку для %q", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ParseStatus(%q) = %q, ожидали %q", tc.raw, got, tc.want)
			}
		})
	}
}
