// Package logger предоставляет общий slog-логгер платформы.
//
// Во всех сервисах ошибки логируются под ЕДИНЫМ ключом ErrKey ("err"), чтобы
// агрегация логов была единообразной. Используйте logger.Err(err) как атрибут.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// ErrKey — единый ключ для атрибута ошибки во всех логах платформы.
const ErrKey = "err"

// Err возвращает slog-атрибут ошибки с каноническим ключом ErrKey.
func Err(err error) slog.Attr {
	return slog.Any(ErrKey, err)
}

// Options конфигурирует логгер.
type Options struct {
	// Level — минимальный уровень ("debug", "info", "warn", "error").
	Level string
	// JSON включает JSON-формат (для прода); иначе текстовый (для локалки).
	JSON bool
}

// New создаёт slog.Logger по опциям, пишущий в stdout.
func New(opts Options) *slog.Logger {
	handlerOpts := &slog.HandlerOptions{Level: parseLevel(opts.Level)}
	var h slog.Handler
	if opts.JSON {
		h = slog.NewJSONHandler(os.Stdout, handlerOpts)
	} else {
		h = slog.NewTextHandler(os.Stdout, handlerOpts)
	}
	return slog.New(h)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
