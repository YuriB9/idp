package httpserver

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/YuriB9/idp/pkg/auth"
	"github.com/YuriB9/idp/pkg/logger"
	"github.com/YuriB9/idp/pkg/reqid"
)

// requestIDHeader — заголовок сквозного идентификатора запроса.
const requestIDHeader = "X-Request-Id"

// RequestID проставляет request-id (из заголовка или новый) в контекст и ответ.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		ctx := reqid.With(r.Context(), id)
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Recoverer перехватывает панику в обработчике, логирует под ключом "err"
// и возвращает 500 без падения процесса.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("http: recovered from panic",
						slog.Any(logger.ErrKey, recoveredError(rec)),
						slog.String("path", routePattern(r)),
					)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit ограничивает частоту запросов глобально (rps с burst).
func RateLimit(rps float64, burst int) func(http.Handler) http.Handler {
	lim := rate.NewLimiter(rate.Limit(rps), burst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !lim.Allow() {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Auth проверяет Bearer-токен через verifier и кладёт Claims в контекст.
// При Disabled пропускает запросы (только локалка).
func Auth(v *auth.Verifier, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if v.Disabled() {
				next.ServeHTTP(w, r)
				return
			}
			tok, ok := auth.BearerToken(r.Header.Get("Authorization"))
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			claims, err := v.Verify(tok)
			if err != nil {
				// Внутренний текст не отдаём наружу.
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.ContextWithClaims(r.Context(), claims)))
		})
	}
}

// recoveredError приводит значение из recover() к error.
func recoveredError(rec any) error {
	if err, ok := rec.(error); ok {
		return err
	}
	return fmt.Errorf("panic: %v", rec)
}

// routePattern возвращает шаблон маршрута chi (а НЕ r.URL.Path) для
// безопасного использования в метках/логах. См. БЛОК 6.
func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	return "unmatched"
}
