package identity_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/YuriB9/idp/services/idm/internal/identity"
)

// stubCatalog — стаб клиента каталога с подсчётом обращений к Keycloak.
type stubCatalog struct {
	searchCalls  atomic.Int64
	resolveCalls atomic.Int64
	search       []identity.Identity
	resolve      []identity.Identity
	err          error
}

func (s *stubCatalog) Search(_ context.Context, _ string, _, _ int) ([]identity.Identity, error) {
	s.searchCalls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	return s.search, nil
}

func (s *stubCatalog) Resolve(_ context.Context, subjects []string) ([]identity.Identity, error) {
	s.resolveCalls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	if s.resolve != nil {
		return s.resolve, nil
	}
	out := make([]identity.Identity, 0, len(subjects))
	for _, sub := range subjects {
		out = append(out, identity.Identity{Subject: sub, Username: "u-" + sub, Found: true})
	}
	return out, nil
}

func setup(t *testing.T, cat *stubCatalog) (*identity.Directory, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return identity.NewDirectory(cat, identity.NewCache(rdb, time.Minute)), mr
}

func TestSearch_EmptyQuery_Validation(t *testing.T) {
	t.Parallel()
	dir, _ := setup(t, &stubCatalog{})
	_, _, err := dir.Search(context.Background(), "   ", "", 20)
	if !identity.IsValidation(err) {
		t.Fatalf("ожидали ErrValidation на пустой запрос, got %v", err)
	}
}

func TestSearch_BadCursor_Validation(t *testing.T) {
	t.Parallel()
	dir, _ := setup(t, &stubCatalog{})
	_, _, err := dir.Search(context.Background(), "iv", "!!!не-base64!!!", 20)
	if !identity.IsValidation(err) {
		t.Fatalf("ожидали ErrValidation на битый курсор, got %v", err)
	}
}

func TestSearch_NextCursorWhenFull(t *testing.T) {
	t.Parallel()
	cat := &stubCatalog{}
	// Полная страница (== page_size) → должен быть непустой курсор.
	for i := 0; i < 20; i++ {
		cat.search = append(cat.search, identity.Identity{Subject: "s", Found: true})
	}
	dir, _ := setup(t, cat)
	_, next, err := dir.Search(context.Background(), "iv", "", 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if next == "" {
		t.Fatalf("ожидали непустой курсор для полной страницы")
	}
	// Неполная страница → курсор пуст.
	cat.search = cat.search[:5]
	dir2, _ := setup(t, cat)
	_, next2, _ := dir2.Search(context.Background(), "iv", "", 20)
	if next2 != "" {
		t.Fatalf("ожидали пустой курсор для неполной страницы, got %q", next2)
	}
}

func TestSearch_CacheHit(t *testing.T) {
	t.Parallel()
	cat := &stubCatalog{search: []identity.Identity{{Subject: "u-1", Found: true}}}
	dir, _ := setup(t, cat)
	ctx := context.Background()

	if _, _, err := dir.Search(ctx, "iv", "", 20); err != nil {
		t.Fatalf("Search #1: %v", err)
	}
	if _, _, err := dir.Search(ctx, "iv", "", 20); err != nil {
		t.Fatalf("Search #2: %v", err)
	}
	if got := cat.searchCalls.Load(); got != 1 {
		t.Fatalf("ожидали 1 обращение к Keycloak (второе из кэша), got %d", got)
	}
}

func TestResolve_CacheHitAndOrder(t *testing.T) {
	t.Parallel()
	cat := &stubCatalog{}
	dir, _ := setup(t, cat)
	ctx := context.Background()

	ids, err := dir.Resolve(ctx, []string{"a", "b"})
	if err != nil {
		t.Fatalf("Resolve #1: %v", err)
	}
	if len(ids) != 2 || ids[0].Subject != "a" || ids[1].Subject != "b" {
		t.Fatalf("порядок/состав резолва нарушен: %+v", ids)
	}
	// Повтор — из кэша, без обращения к каталогу.
	if _, err := dir.Resolve(ctx, []string{"a", "b"}); err != nil {
		t.Fatalf("Resolve #2: %v", err)
	}
	if got := cat.resolveCalls.Load(); got != 1 {
		t.Fatalf("ожидали 1 обращение к каталогу (второе из кэша), got %d", got)
	}
}

func TestResolve_Empty_Validation(t *testing.T) {
	t.Parallel()
	dir, _ := setup(t, &stubCatalog{})
	if _, err := dir.Resolve(context.Background(), nil); !identity.IsValidation(err) {
		t.Fatalf("ожидали ErrValidation на пустой список, got %v", err)
	}
}

func TestResolve_Unavailable(t *testing.T) {
	t.Parallel()
	cat := &stubCatalog{err: identity.ErrUnavailable}
	dir, _ := setup(t, cat)
	if _, err := dir.Resolve(context.Background(), []string{"a"}); !identity.IsUnavailable(err) {
		t.Fatalf("ожидали ErrUnavailable, got %v", err)
	}
}

// TestDecisionCacheUntouched проверяет инвариант ADR-0016: операции справочника
// пишут только в idm:identity:* и не трогают decision-cache RBAC.
func TestDecisionCacheUntouched(t *testing.T) {
	t.Parallel()
	cat := &stubCatalog{search: []identity.Identity{{Subject: "u-1", Found: true}}}
	dir, mr := setup(t, cat)
	ctx := context.Background()

	// Эмулируем существующее состояние decision-cache.
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()
	if err := rdb.Set(ctx, "idm:cache:gen", "7", 0).Err(); err != nil {
		t.Fatalf("seed gen: %v", err)
	}
	if err := rdb.Set(ctx, "idm:decision:7:abc:def", "1", 0).Err(); err != nil {
		t.Fatalf("seed decision: %v", err)
	}

	if _, _, err := dir.Search(ctx, "iv", "", 20); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, err := dir.Resolve(ctx, []string{"u-2"}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if v, _ := rdb.Get(ctx, "idm:cache:gen").Result(); v != "7" {
		t.Fatalf("поколение decision-cache изменилось: %q", v)
	}
	if v, _ := rdb.Get(ctx, "idm:decision:7:abc:def").Result(); v != "1" {
		t.Fatalf("решение decision-cache затронуто: %q", v)
	}
	// А кэш идентичностей должен наполниться.
	keys, _ := rdb.Keys(ctx, "idm:identity:*").Result()
	if len(keys) == 0 {
		t.Fatalf("ожидали ключи кэша идентичностей")
	}
}

func TestErrorsHelpers(t *testing.T) {
	t.Parallel()
	if identity.IsValidation(errors.New("x")) || identity.IsUnavailable(errors.New("x")) {
		t.Fatalf("неклассифицированная ошибка не должна матчиться")
	}
}
