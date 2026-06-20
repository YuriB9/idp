// Package integrations объявляет узкие интерфейсы клиентов управляемых систем
// (GitLab/Vault/Harbor) и их реализации. Каждый интерфейс предоставляет операцию
// провизии и парную компенсацию; операции идемпотентны (повторный вызов при уже
// существующем/удалённом ресурсе — не ошибка). Все исходящие HTTP-запросы
// проходят SSRF-guard (pkg/ssrf): ValidateURL на конфигурации base-URL и
// GuardedDialContext на соединении (docs/IDP_MVP_plan.md, БЛОК 2).
package integrations

import (
	"context"

	"github.com/YuriB9/idp/services/projects/provisioning"
)

// GitLab — клиент GitLab: репозиторий проекта и инъекция CI/CD-переменных.
type GitLab interface {
	// CreateRepo создаёт репозиторий в группе проекта (идемпотентно).
	CreateRepo(ctx context.Context, ref provisioning.ResourceRef) (provisioning.GitLabRepo, error)
	// DeleteRepo — компенсация: удаляет репозиторий (идемпотентно, no-op если нет).
	DeleteRepo(ctx context.Context, ref provisioning.ResourceRef) error
	// InjectVariables записывает секреты Vault/Harbor в CI/CD-переменные репозитория
	// (идемпотентно: перезапись без дублей). Секреты не логируются в открытом виде.
	InjectVariables(ctx context.Context, in provisioning.InjectSecretsInput) error
	// SyncMembers синхронизирует участников репозитория по diff: добавляет add,
	// удаляет remove (идемпотентно). Используется в сценарии «Изменение владельцев».
	SyncMembers(ctx context.Context, ref provisioning.ResourceRef, add, remove []string) error
	// RestoreMembers — компенсация: восстанавливает прежний состав участников
	// (идемпотентно).
	RestoreMembers(ctx context.Context, ref provisioning.ResourceRef, previous []string) error
	// Archive архивирует репозиторий и отзывает доступы участников при выводе из
	// эксплуатации (идемпотентно). Сценарий «Вывод из эксплуатации».
	Archive(ctx context.Context, ref provisioning.ResourceRef) error
	// Unarchive — компенсация: разархивирует репозиторий и восстанавливает доступы
	// (идемпотентно).
	Unarchive(ctx context.Context, ref provisioning.ResourceRef) error
	// TransferRepo переносит репозиторий в группу target-проекта при переносе
	// сервиса (идемпотентно: если репозиторий уже в target — no-op). НЕОБРАТИМО в
	// MVP (чистая компенсация transfer-back не моделируется — точка невозврата,
	// ADR-0013). Сценарий «Перенос сервиса».
	TransferRepo(ctx context.Context, ref provisioning.ResourceRef, target string) error
}

// Harbor — клиент Harbor: директория образов и Robot Account.
type Harbor interface {
	// CreateProject создаёт директорию образов и Robot Account (идемпотентно).
	CreateProject(ctx context.Context, ref provisioning.ResourceRef) (provisioning.HarborResult, error)
	// DeleteProject — компенсация: удаляет директорию образов (идемпотентно).
	DeleteProject(ctx context.Context, ref provisioning.ResourceRef) error
	// SetReadOnly переводит директорию образов в read-only и отзывает Robot при
	// выводе из эксплуатации (идемпотентно). Сценарий «Вывод из эксплуатации».
	SetReadOnly(ctx context.Context, ref provisioning.ResourceRef) error
	// SetWritable — компенсация: возвращает директорию в writable (идемпотентно).
	SetWritable(ctx context.Context, ref provisioning.ResourceRef) error
	// UpdateMetadata обновляет метаданные/права директории образов под target-проект
	// при переносе сервиса (идемпотентно). Сценарий «Перенос сервиса».
	UpdateMetadata(ctx context.Context, ref provisioning.ResourceRef, target string) error
}

// Vault — клиент Vault: политики и AppRole.
type Vault interface {
	// SetupAppRole создаёт политики и AppRole, возвращает RoleID/SecretID (идемпотентно).
	SetupAppRole(ctx context.Context, ref provisioning.ResourceRef) (provisioning.VaultResult, error)
	// TeardownAppRole — компенсация: удаляет политики и AppRole (идемпотентно).
	TeardownAppRole(ctx context.Context, ref provisioning.ResourceRef) error
	// SyncOwners синхронизирует политики доступа владельцев по diff: add получают
	// доступ, remove теряют (идемпотентно). Сценарий «Изменение владельцев».
	SyncOwners(ctx context.Context, ref provisioning.ResourceRef, add, remove []string) error
	// RestoreOwners — компенсация: восстанавливает прежние политики доступа
	// владельцев (идемпотентно).
	RestoreOwners(ctx context.Context, ref provisioning.ResourceRef, previous []string) error
	// RevokeSecretID отзывает активные SecretID/токены сервиса при выводе из
	// эксплуатации — немедленное прекращение доступа (идемпотентно). НЕОБРАТИМО:
	// компенсации нет (точка невозврата, ADR-0012).
	RevokeSecretID(ctx context.Context, ref provisioning.ResourceRef) error
	// MigratePaths мигрирует пути сервиса при переносе: копирует секреты source→target,
	// записывает новые политики и очищает старые пути/политики (идемпотентно).
	// Секреты не логируются. Сценарий «Перенос сервиса» (ADR-0013).
	MigratePaths(ctx context.Context, ref provisioning.ResourceRef, target string) error
}

// Clients — собранный набор клиентов интеграций для регистрации в activities.
type Clients struct {
	GitLab GitLab
	Harbor Harbor
	Vault  Vault
}
