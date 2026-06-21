// Юнит-тесты курсора keyset-пагинации субъектов (без БД): round-trip и отказ на
// повреждённом/небезопасном курсоре. Гарантируют, что подделанный page_token
// даёт ошибку валидации (→ 400), а не доходит до SQL как битый UTF-8/NUL (→ 500).
package repository

import (
	"encoding/base64"
	"testing"
)

func TestSubjectCursorRoundTrip(t *testing.T) {
	t.Parallel()
	for _, subject := range []string{"demo-user", "alice@example.com", "субъект-кириллица", ""} {
		token := encodeSubjectCursor(subject)
		got, err := decodeSubjectCursor(token)
		if err != nil {
			t.Fatalf("decodeSubjectCursor(%q): %v", token, err)
		}
		if got != subject {
			t.Fatalf("round-trip: получили %q, ожидали %q", got, subject)
		}
	}
}

func TestSubjectCursorRejectsInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		token string
	}{
		{name: "не base64", token: "!!!не-base64!!!"},
		// Валидный base64, декодируется в невалидный UTF-8 (0xFF) — Postgres
		// отверг бы такой текстовый параметр ошибкой уровня БД (→ 500 без проверки).
		{name: "валидный base64, битый UTF-8", token: base64.RawURLEncoding.EncodeToString([]byte{0xff, 0xfe})},
		// Валидный base64 с NUL — Postgres не принимает NUL в text.
		{name: "содержит NUL", token: base64.RawURLEncoding.EncodeToString([]byte("a\x00b"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeSubjectCursor(tt.token); err == nil {
				t.Fatalf("ожидали ошибку для повреждённого курсора %q, получили nil", tt.token)
			}
		})
	}
}
