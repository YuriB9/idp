package usecase_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/YuriB9/idp/services/idm/internal/usecase"
)

// TestMain ловит утечки горутин (singleflight использует горутины-вызовы).
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// fakeRepo — стаб репозитория с подсчётом обращений к БД.
type fakeRepo struct {
	allowed bool
	err     error
	calls   atomic.Int64
	block   chan struct{} // если не nil — Allowed блокируется до закрытия
}

func (r *fakeRepo) Allowed(_ context.Context, _, _, _ string) (bool, error) {
	r.calls.Add(1)
	if r.block != nil {
		<-r.block
	}
	if r.err != nil {
		return false, r.err
	}
	return r.allowed, nil
}

// fakeCache — управляемый стаб кэша.
type fakeCache struct {
	mu       sync.Mutex
	store    map[string]bool
	getErr   error // имитирует недоступность кэша на чтении
	setCalls atomic.Int64
}

func newFakeCache() *fakeCache { return &fakeCache{store: map[string]bool{}} }

func key(s, r, a string) string { return s + "|" + r + "|" + a }

func (c *fakeCache) Get(_ context.Context, s, r, a string) (bool, bool, error) {
	if c.getErr != nil {
		return false, false, c.getErr
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.store[key(s, r, a)]
	return v, ok, nil
}

func (c *fakeCache) Set(_ context.Context, s, r, a string, allowed bool) error {
	c.setCalls.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[key(s, r, a)] = allowed
	return nil
}

func TestCheckAccess_Decisions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		allowed bool
		repoErr error
		want    bool
		wantErr bool
	}{
		{name: "право выдано — allow", allowed: true, want: true},
		{name: "нет права — deny", allowed: false, want: false},
		{name: "deny-by-default при ошибке БД (fail-closed)", repoErr: errors.New("db down"), want: false, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := &fakeRepo{allowed: tt.allowed, err: tt.repoErr}
			a := usecase.New(repo, newFakeCache())
			got, err := a.CheckAccess(context.Background(), "u1", "project:demo", "create")
			if tt.wantErr != (err != nil) {
				t.Fatalf("ошибка: получили %v, ожидали wantErr=%v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("решение: получили %v, ожидали %v", got, tt.want)
			}
		})
	}
}

func TestCheckAccess_CacheHitSkipsDB(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{allowed: true}
	c := newFakeCache()
	c.store[key("u1", "project:demo", "create")] = true
	a := usecase.New(repo, c)

	got, err := a.CheckAccess(context.Background(), "u1", "project:demo", "create")
	if err != nil || !got {
		t.Fatalf("ожидали allow без ошибки, получили got=%v err=%v", got, err)
	}
	if repo.calls.Load() != 0 {
		t.Fatalf("при попадании в кэш БД не должна вызываться, calls=%d", repo.calls.Load())
	}
}

func TestCheckAccess_CacheDownDegradesToDB(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{allowed: true}
	c := newFakeCache()
	c.getErr = errors.New("cache down") // кэш недоступен на чтении
	a := usecase.New(repo, c)

	got, err := a.CheckAccess(context.Background(), "u1", "project:demo", "create")
	if err != nil || !got {
		t.Fatalf("при недоступном кэше ожидали чтение БД и allow, got=%v err=%v", got, err)
	}
	if repo.calls.Load() != 1 {
		t.Fatalf("ожидали ровно одно обращение к БД, calls=%d", repo.calls.Load())
	}
}

func TestCheckAccess_SingleflightCollapsesMisses(t *testing.T) {
	t.Parallel()

	const n = 50
	repo := &fakeRepo{allowed: true, block: make(chan struct{})}
	a := usecase.New(repo, newFakeCache())

	var started, wg sync.WaitGroup
	started.Add(n)
	wg.Add(n)
	results := make([]bool, n)
	for i := range n {
		go func() {
			started.Done()
			defer wg.Done()
			got, _ := a.CheckAccess(context.Background(), "u1", "project:demo", "create")
			results[i] = got
		}()
	}
	// Ждём старта всех горутин и даём им дойти до singleflight.Do (лидер
	// блокируется в repo.Allowed, остальные ждут на том же ключе), затем
	// разблокируем единственный запрос в БД.
	started.Wait()
	time.Sleep(100 * time.Millisecond)
	close(repo.block)
	wg.Wait()

	if repo.calls.Load() != 1 {
		t.Fatalf("singleflight должен дать один запрос в БД на N промахов, calls=%d", repo.calls.Load())
	}
	for i, r := range results {
		if !r {
			t.Fatalf("вызов %d получил deny, ожидали allow", i)
		}
	}
}
