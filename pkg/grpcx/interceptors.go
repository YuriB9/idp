// Package grpcx предоставляет единый стек серверных gRPC-перехватчиков
// (recovery, request-id, otel, auth) для сервисов IDM и проектов (ADR-0002).
package grpcx

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/YuriB9/idp/pkg/auth"
	"github.com/YuriB9/idp/pkg/logger"
	"github.com/YuriB9/idp/pkg/reqid"
)

// requestIDMeta — ключ метаданных gRPC для сквозного request-id.
const requestIDMeta = "x-request-id"

// ServerOptions собирает grpc.ServerOption с otel stats-handler и цепочкой
// unary-перехватчиков: recovery → request-id → auth. otel-инструментация
// (трейсы/метрики) подключается через StatsHandler.
func ServerOptions(log *slog.Logger, v *auth.Verifier) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			RecoveryUnary(log),
			RequestIDUnary(),
			AuthUnary(v),
		),
	}
}

// RecoveryUnary перехватывает панику в хендлере, логирует под ключом "err"
// и возвращает gRPC-статус Internal, не роняя процесс.
func RecoveryUnary(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("grpc: recovered from panic",
					slog.Any(logger.ErrKey, fmt.Errorf("panic: %v", rec)),
					slog.String("method", info.FullMethod),
				)
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

// RequestIDUnary извлекает request-id из метаданных (или генерирует пустой
// проброс) и кладёт его в контекст.
func RequestIDUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get(requestIDMeta); len(vals) > 0 && vals[0] != "" {
				ctx = reqid.With(ctx, vals[0])
			}
		}
		return handler(ctx, req)
	}
}

// AuthUnary валидирует Bearer-токен из метаданни "authorization". При Disabled
// пропускает. Невалидный/отсутствующий токен → Unauthenticated.
func AuthUnary(v *auth.Verifier) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if v.Disabled() {
			// Локальный bypass: проверки нет, но кладём claims с DisabledSubject
			// (Verify("") в disabled-режиме), чтобы downstream RBAC видел субъекта.
			claims, _ := v.Verify("")
			return handler(auth.ContextWithClaims(ctx, claims), req)
		}
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}
		vals := md.Get("authorization")
		if len(vals) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization")
		}
		tok, ok := auth.BearerToken(vals[0])
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "malformed authorization")
		}
		claims, err := v.Verify(tok)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}
		return handler(auth.ContextWithClaims(ctx, claims), req)
	}
}
