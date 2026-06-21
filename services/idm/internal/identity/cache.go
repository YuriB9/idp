package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Кэш идентичностей живёт в ОТДЕЛЬНОМ пространстве ключей idm:identity:* и НЕ
// пересекается с decision-cache RBAC (idm:cache:gen / idm:decision:*). Операции
// справочника не читают и не меняют поколение решений и не вызывают точечную
// инвалидацию субъекта (ADR-0016): у решений и идентичностей разные жизненные
// циклы (решения инвалидируются мутациями RBAC, идентичности — по TTL).
const (
	resolvePrefix = "idm:identity:resolve:"
	searchPrefix  = "idm:identity:search:"
)

// Cache кэширует идентичности и страницы поиска в DragonflyDB с TTL.
type Cache struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewCache создаёт кэш идентичностей с заданным TTL.
func NewCache(rdb *redis.Client, ttl time.Duration) *Cache {
	return &Cache{rdb: rdb, ttl: ttl}
}

// searchKey строит ключ страницы поиска от нормализованных параметров.
func searchKey(query string, first, max int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d", query, first, max)))
	return searchPrefix + hex.EncodeToString(sum[:16])
}

// GetResolve возвращает закэшированную идентичность субъекта. found=false — промах.
func (c *Cache) GetResolve(ctx context.Context, subject string) (Identity, bool, error) {
	v, err := c.rdb.Get(ctx, resolvePrefix+subject).Result()
	if errors.Is(err, redis.Nil) {
		return Identity{}, false, nil
	}
	if err != nil {
		return Identity{}, false, fmt.Errorf("identity cache: чтение резолва: %w", err)
	}
	var id Identity
	if err := json.Unmarshal([]byte(v), &id); err != nil {
		return Identity{}, false, fmt.Errorf("identity cache: разбор резолва: %w", err)
	}
	return id, true, nil
}

// SetResolve кэширует идентичность субъекта (в т.ч. found=false — «осиротевший»,
// чтобы не дёргать Keycloak повторно в пределах TTL).
func (c *Cache) SetResolve(ctx context.Context, id Identity) error {
	b, err := json.Marshal(id)
	if err != nil {
		return fmt.Errorf("identity cache: сериализация резолва: %w", err)
	}
	if err := c.rdb.Set(ctx, resolvePrefix+id.Subject, b, c.ttl).Err(); err != nil {
		return fmt.Errorf("identity cache: запись резолва: %w", err)
	}
	return nil
}

// GetSearch возвращает закэшированную страницу поиска. found=false — промах.
func (c *Cache) GetSearch(ctx context.Context, query string, first, max int) ([]Identity, bool, error) {
	v, err := c.rdb.Get(ctx, searchKey(query, first, max)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("identity cache: чтение поиска: %w", err)
	}
	var page []Identity
	if err := json.Unmarshal([]byte(v), &page); err != nil {
		return nil, false, fmt.Errorf("identity cache: разбор поиска: %w", err)
	}
	return page, true, nil
}

// SetSearch кэширует страницу поиска.
func (c *Cache) SetSearch(ctx context.Context, query string, first, max int, page []Identity) error {
	b, err := json.Marshal(page)
	if err != nil {
		return fmt.Errorf("identity cache: сериализация поиска: %w", err)
	}
	if err := c.rdb.Set(ctx, searchKey(query, first, max), b, c.ttl).Err(); err != nil {
		return fmt.Errorf("identity cache: запись поиска: %w", err)
	}
	return nil
}
