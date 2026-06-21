package identity

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sync/singleflight"

	"github.com/YuriB9/idp/pkg/errs"
)

// Лимиты поиска: дефолтный и максимальный размер страницы. Сервер клампит
// page_size к [1, maxPageSize]; пустой/слишком короткий query → ErrValidation.
const (
	defaultPageSize = 20
	maxPageSize     = 50
	maxResolve      = 200
)

// catalog — зависимость фасада от клиента каталога (для подмены стабом в тестах).
type catalog interface {
	Search(ctx context.Context, query string, first, max int) ([]Identity, error)
	Resolve(ctx context.Context, subjects []string) ([]Identity, error)
}

// identityCache — зависимость фасада от кэша идентичностей (для подмены в тестах).
type identityCache interface {
	GetResolve(ctx context.Context, subject string) (Identity, bool, error)
	SetResolve(ctx context.Context, id Identity) error
	GetSearch(ctx context.Context, query string, first, max int) ([]Identity, bool, error)
	SetSearch(ctx context.Context, query string, first, max int, page []Identity) error
}

// Directory — usecase-фасад справочника субъектов: валидация ввода, курсор поверх
// offset Keycloak, кэш идентичностей с TTL и объединение одинаковых запросов
// (singleflight) против стампеда к Keycloak. Не затрагивает decision-cache RBAC.
type Directory struct {
	cat   catalog
	cache identityCache
	sf    singleflight.Group
}

// NewDirectory собирает фасад над клиентом каталога и кэшем идентичностей.
func NewDirectory(cat catalog, cache identityCache) *Directory {
	return &Directory{cat: cat, cache: cache}
}

// clampPageSize приводит запрошенный размер страницы к допустимому диапазону.
func clampPageSize(n int) int {
	switch {
	case n <= 0:
		return defaultPageSize
	case n > maxPageSize:
		return maxPageSize
	default:
		return n
	}
}

// decodeCursor разбирает непрозрачный курсор страницы в offset Keycloak (first).
// Пустой курсор → первая страница (0). Битый курсор → ErrValidation.
func decodeCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("%w: битый курсор", errs.ErrValidation)
	}
	first, err := strconv.Atoi(string(raw))
	if err != nil || first < 0 {
		return 0, fmt.Errorf("%w: битый курсор", errs.ErrValidation)
	}
	return first, nil
}

// encodeCursor кодирует offset следующей страницы в непрозрачный курсор.
func encodeCursor(first int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(first)))
}

// Search ищет пользователей каталога. Возвращает страницу идентичностей и
// непрозрачный курсор следующей страницы (пусто — больше нет). Пустой/слишком
// короткий query или битый курсор → errs.ErrValidation; недоступность Keycloak →
// ErrUnavailable.
func (d *Directory) Search(ctx context.Context, query, cursor string, pageSize int) ([]Identity, string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, "", fmt.Errorf("%w: пустой поисковый запрос", errs.ErrValidation)
	}
	first, err := decodeCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	size := clampPageSize(pageSize)

	if page, ok, cerr := d.cache.GetSearch(ctx, query, first, size); cerr == nil && ok {
		return page, nextCursor(first, size, len(page)), nil
	}

	res, err, _ := d.sf.Do("search:"+searchKey(query, first, size), func() (any, error) {
		return d.cat.Search(ctx, query, first, size)
	})
	if err != nil {
		return nil, "", err
	}
	page, _ := res.([]Identity)
	// Промах кэша не критичен: записываем «best-effort», ошибку записи игнорируем.
	_ = d.cache.SetSearch(ctx, query, first, size, page)
	return page, nextCursor(first, size, len(page)), nil
}

// nextCursor возвращает курсор следующей страницы: непустой только если страница
// заполнена целиком (есть шанс следующей), иначе пусто.
func nextCursor(first, size, got int) string {
	if got < size {
		return ""
	}
	return encodeCursor(first + size)
}

// Resolve резолвит набор канонических ключей (sub) в идентичности. Отсутствующие
// в каталоге → Found=false. Пустой/слишком большой список → errs.ErrValidation;
// недоступность Keycloak → ErrUnavailable.
func (d *Directory) Resolve(ctx context.Context, subjects []string) ([]Identity, error) {
	if len(subjects) == 0 {
		return nil, fmt.Errorf("%w: пустой список субъектов", errs.ErrValidation)
	}
	if len(subjects) > maxResolve {
		return nil, fmt.Errorf("%w: слишком много субъектов в одном запросе", errs.ErrValidation)
	}

	// Дедуп с сохранением порядка первого появления.
	order := make([]string, 0, len(subjects))
	seen := make(map[string]struct{}, len(subjects))
	for _, s := range subjects {
		if s == "" {
			return nil, fmt.Errorf("%w: пустой sub в списке", errs.ErrValidation)
		}
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			order = append(order, s)
		}
	}

	resolved := make(map[string]Identity, len(order))
	var miss []string
	for _, s := range order {
		if id, ok, cerr := d.cache.GetResolve(ctx, s); cerr == nil && ok {
			resolved[s] = id
			continue
		}
		miss = append(miss, s)
	}

	if len(miss) > 0 {
		ids, err := d.resolveMiss(ctx, miss)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			resolved[id.Subject] = id
			_ = d.cache.SetResolve(ctx, id)
		}
	}

	out := make([]Identity, 0, len(order))
	for _, s := range order {
		if id, ok := resolved[s]; ok {
			out = append(out, id)
		} else {
			// Подстраховка: если каталог не вернул запись по ключу — «осиротевший».
			out = append(out, Identity{Subject: s, Found: false})
		}
	}
	return out, nil
}

// resolveMiss резолвит непопавшие в кэш ключи через каталог (с объединением
// одинаковых пакетов запросов singleflight).
func (d *Directory) resolveMiss(ctx context.Context, miss []string) ([]Identity, error) {
	res, err, _ := d.sf.Do("resolve:"+strings.Join(miss, ","), func() (any, error) {
		return d.cat.Resolve(ctx, miss)
	})
	if err != nil {
		return nil, err
	}
	ids, _ := res.([]Identity)
	return ids, nil
}

// IsValidation сообщает, является ли ошибка валидационной (для маппинга в 400).
func IsValidation(err error) bool { return errors.Is(err, errs.ErrValidation) }

// IsUnavailable сообщает, является ли ошибка недоступностью каталога (для 503).
func IsUnavailable(err error) bool { return errors.Is(err, ErrUnavailable) }
