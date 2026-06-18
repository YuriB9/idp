// Package errs определяет канонические sentinel-ошибки платформы.
//
// Сервисы ДОЛЖНЫ использовать эти значения вместо локальных дублей, чтобы
// маппинг ошибок (например, gRPC-статусы и HTTP-коды) был единообразным во
// всём монорепо. Проверять через errors.Is.
package errs

import "errors"

var (
	// ErrNotFound — запрашиваемый ресурс не найден (HTTP 404, gRPC NotFound).
	ErrNotFound = errors.New("not found")
	// ErrConflict — конфликт состояния, в т.ч. проигранный guarded-CAS
	// перехода статуса (HTTP 409, gRPC Aborted/AlreadyExists).
	ErrConflict = errors.New("conflict")
	// ErrUnauthorized — отсутствует или невалиден токен (HTTP 401).
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden — доступ запрещён политикой (HTTP 403).
	ErrForbidden = errors.New("forbidden")
	// ErrValidation — невалидный ввод (HTTP 400).
	ErrValidation = errors.New("validation failed")
)
