package repository

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// cursor — ключ keyset-пагинации: координаты последней отданной строки в
// порядке (created_at, id). Для клиента курсор непрозрачен (base64(JSON)).
type cursor struct {
	CreatedAt time.Time `json:"c"`
	ID        uuid.UUID `json:"i"`
}

// encodeCursor кодирует ключ в непрозрачную base64-строку.
func encodeCursor(c cursor) string {
	raw, _ := json.Marshal(c) // структура фиксирована — ошибка маршалинга невозможна
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor разбирает непрозрачный курсор. Повреждённый вход — ошибка
// валидации (вызывающий маппит в ErrValidation).
func decodeCursor(token string) (cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return cursor{}, fmt.Errorf("repository: не удалось декодировать курсор: %w", err)
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return cursor{}, fmt.Errorf("repository: некорректный курсор: %w", err)
	}
	return c, nil
}
