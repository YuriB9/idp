// Package db предоставляет конструктор пула соединений к PostgreSQL поверх pgx.
//
// Конфигурация пула обязательна: размеры и таймауты задаются явно, чтобы не
// полагаться на неявные дефолты (см. docs/IDP_MVP_plan.md, БЛОК 4).
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig задаёт параметры пула соединений. Пустая структура невалидна —
// DSN обязателен.
type PoolConfig struct {
	// DSN — строка подключения PostgreSQL (обязательна).
	DSN string
	// MaxConns — максимум соединений в пуле. <=0 → дефолт pgx.
	MaxConns int32
	// MinConns — минимум поддерживаемых соединений.
	MinConns int32
	// MaxConnLifetime — максимальное время жизни соединения.
	MaxConnLifetime time.Duration
	// MaxConnIdleTime — максимальное время простоя соединения.
	MaxConnIdleTime time.Duration
	// ConnectTimeout — таймаут установки одного соединения.
	ConnectTimeout time.Duration
}

// NewPool создаёт и проверяет (Ping) пул соединений по конфигурации.
// Возвращает ошибку при пустом DSN или недоступной БД.
func NewPool(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
	if cfg.DSN == "" {
		return nil, errors.New("db: empty DSN in pool config")
	}
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db: parse dsn: %w", err)
	}
	if cfg.MaxConns > 0 {
		pcfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		pcfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		pcfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		pcfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.ConnectTimeout > 0 {
		pcfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	}

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}
