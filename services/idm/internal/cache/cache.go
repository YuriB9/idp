// Package cache — кэш решений RBAC в DragonflyDB (протокол Redis).
// Стратегия — docs/adr/0010: ключ от (subject, resource, action) с конечным
// TTL (включая negative caching отказов), инвалидация версионным префиксом
// (служебный ключ generation) и точечным удалением по субъекту.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// genKey — служебный ключ поколения кэша. Его инкремент делает все ранее
// записанные решения недостижимыми (грубая, но корректная инвалидация).
const genKey = "idm:cache:gen"

// Cache хранит решения CheckAccess в DragonflyDB.
type Cache struct {
	rdb      *redis.Client
	ttlAllow time.Duration
	ttlDeny  time.Duration
}

// New создаёт кэш. ttlAllow/ttlDeny — TTL для разрешений и отказов
// соответственно (negative caching с обычно меньшим TTL).
func New(rdb *redis.Client, ttlAllow, ttlDeny time.Duration) *Cache {
	return &Cache{rdb: rdb, ttlAllow: ttlAllow, ttlDeny: ttlDeny}
}

// generation возвращает текущее поколение кэша ("0", если ключ ещё не задан).
func (c *Cache) generation(ctx context.Context) (string, error) {
	gen, err := c.rdb.Get(ctx, genKey).Result()
	if errors.Is(err, redis.Nil) {
		return "0", nil
	}
	if err != nil {
		return "", fmt.Errorf("cache: чтение поколения: %w", err)
	}
	return gen, nil
}

// subjectHash возвращает hex-хэш субъекта (для безопасной длины ключа и
// сохранения возможности pattern-match по субъекту при инвалидации).
func subjectHash(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(sum[:8])
}

// decisionKey строит ключ решения для поколения gen.
func decisionKey(gen, subject, resource, action string) string {
	sum := sha256.Sum256([]byte(resource + "\x00" + action))
	return fmt.Sprintf("idm:decision:%s:%s:%s", gen, subjectHash(subject), hex.EncodeToString(sum[:12]))
}

// Get возвращает закэшированное решение. found=false — промах кэша. Ошибка
// (например, недоступность DragonflyDB) возвращается вызывающему, который
// деградирует к чтению БД (а не разрешает молча).
func (c *Cache) Get(ctx context.Context, subject, resource, action string) (allowed, found bool, err error) {
	gen, err := c.generation(ctx)
	if err != nil {
		return false, false, err
	}
	v, err := c.rdb.Get(ctx, decisionKey(gen, subject, resource, action)).Result()
	if errors.Is(err, redis.Nil) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("cache: чтение решения: %w", err)
	}
	return v == "1", true, nil
}

// Set записывает решение с TTL по знаку (allow/deny).
func (c *Cache) Set(ctx context.Context, subject, resource, action string, allowed bool) error {
	gen, err := c.generation(ctx)
	if err != nil {
		return err
	}
	val, ttl := "0", c.ttlDeny
	if allowed {
		val, ttl = "1", c.ttlAllow
	}
	if err := c.rdb.Set(ctx, decisionKey(gen, subject, resource, action), val, ttl).Err(); err != nil {
		return fmt.Errorf("cache: запись решения: %w", err)
	}
	return nil
}

// InvalidateAll инкрементит поколение — все ранее закэшированные решения
// становятся недостижимыми. Применяется при изменении ролей/прав/привязок.
func (c *Cache) InvalidateAll(ctx context.Context) error {
	if err := c.rdb.Incr(ctx, genKey).Err(); err != nil {
		return fmt.Errorf("cache: инвалидация поколения: %w", err)
	}
	return nil
}

// InvalidateSubject удаляет закэшированные решения конкретного субъекта в
// текущем поколении (точечная инвалидация без сканирования всего пространства).
func (c *Cache) InvalidateSubject(ctx context.Context, subject string) error {
	gen, err := c.generation(ctx)
	if err != nil {
		return err
	}
	pattern := fmt.Sprintf("idm:decision:%s:%s:*", gen, subjectHash(subject))
	var cursor uint64
	for {
		keys, next, serr := c.rdb.Scan(ctx, cursor, pattern, 100).Result()
		if serr != nil {
			return fmt.Errorf("cache: scan по субъекту: %w", serr)
		}
		if len(keys) > 0 {
			if derr := c.rdb.Del(ctx, keys...).Err(); derr != nil {
				return fmt.Errorf("cache: удаление ключей субъекта: %w", derr)
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return nil
}

// Ping проверяет доступность DragonflyDB (для content-aware /readyz).
func (c *Cache) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}
