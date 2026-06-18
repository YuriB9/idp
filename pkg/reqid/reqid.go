// Package reqid хранит сквозной идентификатор запроса в context.Context.
//
// Используется как HTTP-, так и gRPC-стеком, чтобы request-id единообразно
// пробрасывался по цепочке вызовов и попадал в логи.
package reqid

import "context"

type contextKey struct{}

// With возвращает контекст с проставленным request-id.
func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// From извлекает request-id из контекста.
func From(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(contextKey{}).(string)
	return id, ok
}
