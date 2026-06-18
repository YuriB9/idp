// Package temporallog адаптирует slog.Logger к интерфейсу логгера Temporal SDK
// (go.temporal.io/sdk/log.Logger), сохраняя единый ключ ошибки "err".
package temporallog

import (
	"log/slog"

	"go.temporal.io/sdk/log"
)

// Adapter реализует log.Logger поверх *slog.Logger.
type Adapter struct {
	l *slog.Logger
}

// New создаёт адаптер логгера Temporal.
func New(l *slog.Logger) log.Logger { return &Adapter{l: l} }

func (a *Adapter) Debug(msg string, keyvals ...any) { a.l.Debug(msg, keyvals...) }
func (a *Adapter) Info(msg string, keyvals ...any)  { a.l.Info(msg, keyvals...) }
func (a *Adapter) Warn(msg string, keyvals ...any)  { a.l.Warn(msg, keyvals...) }
func (a *Adapter) Error(msg string, keyvals ...any) { a.l.Error(msg, keyvals...) }
