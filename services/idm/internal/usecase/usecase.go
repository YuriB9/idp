// Package usecase — логика принятия решения CheckAccess поверх кэша и
// репозитория. Порядок: кэш → при промахе чтение БД под singleflight →
// запись в кэш. Поведение fail-closed (docs/adr/0010, БЛОК 2/5).
package usecase

import (
	"context"
	"fmt"

	"golang.org/x/sync/singleflight"
)

// Repo читает решение из БД (deny-by-default на уровне модели).
type Repo interface {
	Allowed(ctx context.Context, subject, resource, action string) (bool, error)
}

// Cache кэширует решения. Ошибка Get/Set означает недоступность кэша —
// usecase деградирует к прямому чтению БД, но НИКОГДА не разрешает молча.
type Cache interface {
	Get(ctx context.Context, subject, resource, action string) (allowed, found bool, err error)
	Set(ctx context.Context, subject, resource, action string, allowed bool) error
}

// Authorizer вычисляет решения CheckAccess.
type Authorizer struct {
	repo  Repo
	cache Cache
	group singleflight.Group
}

// New создаёт usecase поверх репозитория и кэша.
func New(repo Repo, cache Cache) *Authorizer {
	return &Authorizer{repo: repo, cache: cache}
}

// CheckAccess возвращает решение для (subject, resource, action).
// Сначала кэш; при промахе — чтение БД под singleflight (один запрос на N
// конкурентных одинаковых промахов) и запись в кэш. Ошибка БД → ошибка
// (вызывающий трактует как отказ, fail-closed); ошибка кэша → деградация к БД.
func (a *Authorizer) CheckAccess(ctx context.Context, subject, resource, action string) (bool, error) {
	// 1. Попытка кэша. Ошибка кэша — не повод разрешать: деградируем к БД.
	if allowed, found, err := a.cache.Get(ctx, subject, resource, action); err == nil && found {
		return allowed, nil
	}

	// 2. Промах (или недоступный кэш): чтение БД под singleflight против stampede.
	sfKey := subject + "\x00" + resource + "\x00" + action
	v, err, _ := a.group.Do(sfKey, func() (any, error) {
		allowed, derr := a.repo.Allowed(ctx, subject, resource, action)
		if derr != nil {
			return false, derr
		}
		// 3. Запись в кэш — best-effort: ошибка кэша не влияет на решение.
		_ = a.cache.Set(ctx, subject, resource, action, allowed)
		return allowed, nil
	})
	if err != nil {
		return false, fmt.Errorf("usecase: проверка доступа: %w", err)
	}
	return v.(bool), nil
}
