package usecase_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/services/projects/internal/repository"
)

// memStore — in-memory реализация usecase.Store для дефолтного прогона тестов
// (без Postgres). Моделирует контракт реального репозитория: уникальность
// (project, name), guarded-CAS-переходы (несовпадение статуса/отсутствие записи
// → ErrConflict) и keyset-пагинацию по (created_at, id).
type memStore struct {
	mu    sync.Mutex
	items map[uuid.UUID]repository.Service
	seq   int64 // монотонный счётчик для детерминированного created_at
}

func newMemStore() *memStore {
	return &memStore{items: make(map[uuid.UUID]repository.Service)}
}

func (m *memStore) Create(_ context.Context, s repository.Service) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ex := range m.items {
		if ex.Project == s.Project && ex.Name == s.Name {
			return fmt.Errorf("memstore: дубликат (project, name): %w", errs.ErrConflict)
		}
	}
	// Детерминированный возрастающий created_at для стабильной keyset-сортировки.
	m.seq++
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Unix(0, 0).Add(time.Duration(m.seq) * time.Second)
	}
	s.UpdatedAt = s.CreatedAt
	m.items[s.ID] = s
	return nil
}

func (m *memStore) GetByName(_ context.Context, project, name string) (repository.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.items {
		if s.Project == project && s.Name == name {
			return s, nil
		}
	}
	return repository.Service{}, fmt.Errorf("memstore: не найдено: %w", errs.ErrNotFound)
}

// TransitionStatus моделирует guarded-CAS: переход возможен только из ожидаемого
// статуса; иначе (или при отсутствии записи) — ErrConflict (аналог RowsAffected==0).
func (m *memStore) TransitionStatus(_ context.Context, id uuid.UUID, expected, next repository.Status) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.items[id]
	if !ok || s.Status != expected {
		return fmt.Errorf("memstore: guarded-CAS проигран: %w", errs.ErrConflict)
	}
	s.Status = next
	m.items[id] = s
	return nil
}

type memCursor struct {
	CreatedAt time.Time `json:"c"`
	ID        uuid.UUID `json:"i"`
}

func (m *memStore) List(_ context.Context, project string, pageSize int, pageToken string) ([]repository.Service, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	limit := pageSize
	switch {
	case limit <= 0:
		limit = 50
	case limit > 200:
		limit = 200
	}

	// Отбор проекта и сортировка по (created_at, id).
	var all []repository.Service
	for _, s := range m.items {
		if s.Project == project {
			all = append(all, s)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.Before(all[j].CreatedAt)
		}
		return all[i].ID.String() < all[j].ID.String()
	})

	// Применяем курсор продолжения.
	if pageToken != "" {
		raw, err := base64.RawURLEncoding.DecodeString(pageToken)
		if err != nil {
			return nil, "", fmt.Errorf("%w: курсор", errs.ErrValidation)
		}
		var cur memCursor
		if err := json.Unmarshal(raw, &cur); err != nil {
			return nil, "", fmt.Errorf("%w: курсор", errs.ErrValidation)
		}
		idx := 0
		for idx < len(all) {
			s := all[idx]
			if s.CreatedAt.After(cur.CreatedAt) || (s.CreatedAt.Equal(cur.CreatedAt) && s.ID.String() > cur.ID.String()) {
				break
			}
			idx++
		}
		all = all[idx:]
	}

	var next string
	if len(all) > limit {
		all = all[:limit]
		last := all[len(all)-1]
		raw, _ := json.Marshal(memCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return all, next, nil
}
