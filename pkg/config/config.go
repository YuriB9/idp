// Package config предоставляет хелперы чтения конфигурации из окружения.
//
// Ключевая особенность: легитимный "0" принимается как значение, а не молча
// подменяется дефолтом (урок прошлого проекта). Отсутствие переменной и пустая
// строка трактуются как «не задано» и приводят к значению по умолчанию.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// String возвращает значение переменной окружения или def, если она не задана
// (отсутствует или пустая).
func String(key, def string) string {
	if v, ok := lookup(key); ok {
		return v
	}
	return def
}

// MustString возвращает значение переменной окружения или ошибку, если она не
// задана. Используется для обязательных параметров.
func MustString(key string) (string, error) {
	if v, ok := lookup(key); ok {
		return v, nil
	}
	return "", fmt.Errorf("config: required env %q is not set", key)
}

// Int возвращает целочисленное значение переменной окружения. Заданный "0"
// корректно возвращается как 0, а не как def.
func Int(key string, def int) (int, error) {
	v, ok := lookup(key)
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config: env %q is not an int: %w", key, err)
	}
	return n, nil
}

// Bool возвращает булево значение переменной окружения.
func Bool(key string, def bool) (bool, error) {
	v, ok := lookup(key)
	if !ok {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("config: env %q is not a bool: %w", key, err)
	}
	return b, nil
}

// Duration возвращает длительность из переменной окружения (формат time.ParseDuration).
func Duration(key string, def time.Duration) (time.Duration, error) {
	v, ok := lookup(key)
	if !ok {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: env %q is not a duration: %w", key, err)
	}
	return d, nil
}

// lookup возвращает значение и признак того, что переменная задана непустой
// строкой. Пустая строка считается «не задано».
func lookup(key string) (string, bool) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}
