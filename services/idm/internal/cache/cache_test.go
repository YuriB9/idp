package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/goleak"

	"github.com/YuriB9/idp/services/idm/internal/cache"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// newCache поднимает in-process miniredis и кэш поверх него.
func newCache(t *testing.T) (*cache.Cache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return cache.New(rdb, time.Minute, 30*time.Second), mr
}

func TestSetGet_HitAndMiss(t *testing.T) {
	t.Parallel()
	c, _ := newCache(t)
	ctx := context.Background()

	// Промах до записи.
	if _, found, err := c.Get(ctx, "u1", "project:demo", "create"); err != nil || found {
		t.Fatalf("ожидали промах, got found=%v err=%v", found, err)
	}

	// Запись allow → попадание.
	if err := c.Set(ctx, "u1", "project:demo", "create", true); err != nil {
		t.Fatalf("Set: %v", err)
	}
	allowed, found, err := c.Get(ctx, "u1", "project:demo", "create")
	if err != nil || !found || !allowed {
		t.Fatalf("ожидали попадание allow, got allowed=%v found=%v err=%v", allowed, found, err)
	}
}

func TestSetGet_NegativeCaching(t *testing.T) {
	t.Parallel()
	c, _ := newCache(t)
	ctx := context.Background()

	if err := c.Set(ctx, "u2", "project:demo", "create", false); err != nil {
		t.Fatalf("Set: %v", err)
	}
	allowed, found, err := c.Get(ctx, "u2", "project:demo", "create")
	if err != nil || !found || allowed {
		t.Fatalf("ожидали закэшированный deny, got allowed=%v found=%v err=%v", allowed, found, err)
	}
}

func TestInvalidateAll_MakesDecisionsUnreachable(t *testing.T) {
	t.Parallel()
	c, _ := newCache(t)
	ctx := context.Background()

	_ = c.Set(ctx, "u1", "project:demo", "create", true)
	if err := c.InvalidateAll(ctx); err != nil {
		t.Fatalf("InvalidateAll: %v", err)
	}
	// После смены поколения старое решение недостижимо (промах).
	if _, found, err := c.Get(ctx, "u1", "project:demo", "create"); err != nil || found {
		t.Fatalf("после инвалидации ожидали промах, got found=%v err=%v", found, err)
	}
}

func TestInvalidateSubject_RemovesOnlyThatSubject(t *testing.T) {
	t.Parallel()
	c, _ := newCache(t)
	ctx := context.Background()

	_ = c.Set(ctx, "u1", "project:demo", "create", true)
	_ = c.Set(ctx, "u2", "project:demo", "create", true)

	if err := c.InvalidateSubject(ctx, "u1"); err != nil {
		t.Fatalf("InvalidateSubject: %v", err)
	}
	if _, found, _ := c.Get(ctx, "u1", "project:demo", "create"); found {
		t.Fatal("решение u1 должно быть удалено")
	}
	if _, found, _ := c.Get(ctx, "u2", "project:demo", "create"); !found {
		t.Fatal("решение u2 не должно затрагиваться")
	}
}

func TestGet_TTLExpiry(t *testing.T) {
	t.Parallel()
	c, mr := newCache(t)
	ctx := context.Background()

	_ = c.Set(ctx, "u1", "project:demo", "create", true)
	mr.FastForward(2 * time.Minute) // за пределами ttlAllow
	if _, found, err := c.Get(ctx, "u1", "project:demo", "create"); err != nil || found {
		t.Fatalf("после TTL ожидали промах, got found=%v err=%v", found, err)
	}
}
