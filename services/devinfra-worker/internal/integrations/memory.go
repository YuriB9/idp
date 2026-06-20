package integrations

import (
	"context"
	"sync"

	"github.com/YuriB9/idp/services/projects/provisioning"
)

// Memory — in-memory реализация всех клиентов интеграций для дефолтного прогона
// тестов (без сети). Идемпотентна и потокобезопасна; фиксирует созданные ресурсы,
// чтобы тесты могли проверять провизию и компенсации.
type Memory struct {
	mu        sync.Mutex
	repos     map[string]bool
	harbor    map[string]bool
	vault     map[string]bool
	variables map[string]map[string]string
	// members — участники репозитория GitLab по ключу сервиса (владельцы).
	members map[string]map[string]bool
	// vaultOwners — субъекты с доступом по политике Vault по ключу сервиса.
	vaultOwners map[string]map[string]bool
	// archived — флаг архивации репозитория GitLab (вывод из эксплуатации).
	archived map[string]bool
	// harborReadOnly — флаг режима read-only директории Harbor (вывод из эксплуатации).
	harborReadOnly map[string]bool
	// vaultRevoked — флаг отзыва активных SecretID/токенов Vault (вывод из эксплуатации).
	vaultRevoked map[string]bool
	// repoGroup — текущая группа (проект) репозитория GitLab по ключу сервиса
	// (перенос). Отсутствие ключа означает исходную группу.
	repoGroup map[string]string
	// vaultPath — текущий проект-путь секретов Vault по ключу сервиса (перенос).
	vaultPath map[string]string
	// harborProject — текущий проект-владелец метаданных Harbor по ключу сервиса (перенос).
	harborProject map[string]string
}

// NewMemory создаёт пустой in-memory набор клиентов.
func NewMemory() *Memory {
	return &Memory{
		repos:          map[string]bool{},
		harbor:         map[string]bool{},
		vault:          map[string]bool{},
		variables:      map[string]map[string]string{},
		members:        map[string]map[string]bool{},
		vaultOwners:    map[string]map[string]bool{},
		archived:       map[string]bool{},
		harborReadOnly: map[string]bool{},
		vaultRevoked:   map[string]bool{},
		repoGroup:      map[string]string{},
		vaultPath:      map[string]string{},
		harborProject:  map[string]string{},
	}
}

// applyDiff применяет add/remove к множеству set[k] (создаёт при необходимости).
func applyDiff(store map[string]map[string]bool, k string, add, remove []string) {
	set := store[k]
	if set == nil {
		set = map[string]bool{}
		store[k] = set
	}
	for _, a := range add {
		set[a] = true
	}
	for _, r := range remove {
		delete(set, r)
	}
}

// replaceSet заменяет множество store[k] на previous (идемпотентная компенсация).
func replaceSet(store map[string]map[string]bool, k string, previous []string) {
	set := map[string]bool{}
	for _, p := range previous {
		set[p] = true
	}
	store[k] = set
}

// Clients возвращает набор интерфейсов, обслуживаемых этим Memory.
func (m *Memory) Clients() *Clients {
	return &Clients{GitLab: m, Harbor: m, Vault: m}
}

func key(ref provisioning.ResourceRef) string { return ref.Project + "/" + ref.Name }

// --- GitLab ---

func (m *Memory) CreateRepo(_ context.Context, ref provisioning.ResourceRef) (provisioning.GitLabRepo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.repos[key(ref)] = true // идемпотентно: повторное создание — no-op
	return provisioning.GitLabRepo{ProjectID: key(ref), Path: key(ref)}, nil
}

func (m *Memory) DeleteRepo(_ context.Context, ref provisioning.ResourceRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.repos, key(ref)) // идемпотентно: delete отсутствующего — no-op
	return nil
}

func (m *Memory) SyncMembers(_ context.Context, ref provisioning.ResourceRef, add, remove []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	applyDiff(m.members, key(ref), add, remove)
	return nil
}

func (m *Memory) RestoreMembers(_ context.Context, ref provisioning.ResourceRef, previous []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	replaceSet(m.members, key(ref), previous)
	return nil
}

func (m *Memory) Archive(_ context.Context, ref provisioning.ResourceRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.archived[key(ref)] = true // идемпотентно: повторная архивация — no-op
	return nil
}

func (m *Memory) Unarchive(_ context.Context, ref provisioning.ResourceRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.archived, key(ref)) // идемпотентно: компенсация
	return nil
}

func (m *Memory) TransferRepo(_ context.Context, ref provisioning.ResourceRef, target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.repoGroup[key(ref)] = target // идемпотентно: повторная установка той же группы — no-op
	return nil
}

func (m *Memory) InjectVariables(_ context.Context, in provisioning.InjectSecretsInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.variables[in.GitLab.Path] = map[string]string{
		"VAULT_ROLE_ID":      in.Vault.RoleID,
		"VAULT_SECRET_ID":    in.Vault.SecretID,
		"HARBOR_ROBOT_TOKEN": in.Harbor.RobotToken,
	}
	return nil
}

// --- Harbor ---

func (m *Memory) CreateProject(_ context.Context, ref provisioning.ResourceRef) (provisioning.HarborResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.harbor[key(ref)] = true
	return provisioning.HarborResult{
		ProjectName: key(ref),
		RobotName:   "robot$" + ref.Project + "-" + ref.Name,
		RobotToken:  "mem-harbor-token",
	}, nil
}

func (m *Memory) DeleteProject(_ context.Context, ref provisioning.ResourceRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.harbor, key(ref))
	return nil
}

func (m *Memory) SetReadOnly(_ context.Context, ref provisioning.ResourceRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.harborReadOnly[key(ref)] = true // идемпотентно
	return nil
}

func (m *Memory) SetWritable(_ context.Context, ref provisioning.ResourceRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.harborReadOnly, key(ref)) // идемпотентно: компенсация
	return nil
}

func (m *Memory) UpdateMetadata(_ context.Context, ref provisioning.ResourceRef, target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.harborProject[key(ref)] = target // идемпотентно
	return nil
}

// --- Vault ---

func (m *Memory) SetupAppRole(_ context.Context, ref provisioning.ResourceRef) (provisioning.VaultResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vault[key(ref)] = true
	return provisioning.VaultResult{RoleID: "mem-role-" + key(ref), SecretID: "mem-secret"}, nil
}

func (m *Memory) TeardownAppRole(_ context.Context, ref provisioning.ResourceRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.vault, key(ref))
	return nil
}

func (m *Memory) SyncOwners(_ context.Context, ref provisioning.ResourceRef, add, remove []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	applyDiff(m.vaultOwners, key(ref), add, remove)
	return nil
}

func (m *Memory) RestoreOwners(_ context.Context, ref provisioning.ResourceRef, previous []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	replaceSet(m.vaultOwners, key(ref), previous)
	return nil
}

func (m *Memory) RevokeSecretID(_ context.Context, ref provisioning.ResourceRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vaultRevoked[key(ref)] = true // идемпотентно; необратимо (компенсации нет)
	return nil
}

func (m *Memory) MigratePaths(_ context.Context, ref provisioning.ResourceRef, target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vaultPath[key(ref)] = target // идемпотентно: копия секретов + новые политики + очистка старых
	return nil
}

// --- помощники для тестов ---

// HasRepo сообщает, существует ли репозиторий для ref.
func (m *Memory) HasRepo(ref provisioning.ResourceRef) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.repos[key(ref)]
}

// HasHarbor сообщает, существует ли Harbor-директория для ref.
func (m *Memory) HasHarbor(ref provisioning.ResourceRef) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.harbor[key(ref)]
}

// HasVault сообщает, существует ли Vault AppRole для ref.
func (m *Memory) HasVault(ref provisioning.ResourceRef) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.vault[key(ref)]
}

// HasMember сообщает, входит ли subject в участники репозитория для ref.
func (m *Memory) HasMember(ref provisioning.ResourceRef, subject string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.members[key(ref)][subject]
}

// HasVaultOwner сообщает, есть ли у subject доступ по политике Vault для ref.
func (m *Memory) HasVaultOwner(ref provisioning.ResourceRef, subject string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.vaultOwners[key(ref)][subject]
}

// IsArchived сообщает, архивирован ли репозиторий GitLab для ref.
func (m *Memory) IsArchived(ref provisioning.ResourceRef) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.archived[key(ref)]
}

// IsHarborReadOnly сообщает, переведена ли директория Harbor в read-only для ref.
func (m *Memory) IsHarborReadOnly(ref provisioning.ResourceRef) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.harborReadOnly[key(ref)]
}

// IsVaultRevoked сообщает, отозваны ли активные SecretID/токены Vault для ref.
func (m *Memory) IsVaultRevoked(ref provisioning.ResourceRef) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.vaultRevoked[key(ref)]
}

// RepoGroup возвращает целевую группу репозитория GitLab для ref после переноса
// (пусто, если перенос не выполнялся).
func (m *Memory) RepoGroup(ref provisioning.ResourceRef) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.repoGroup[key(ref)]
}

// VaultPath возвращает целевой проект-путь секретов Vault для ref после переноса.
func (m *Memory) VaultPath(ref provisioning.ResourceRef) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.vaultPath[key(ref)]
}

// HarborProject возвращает целевой проект-владелец метаданных Harbor для ref.
func (m *Memory) HarborProject(ref provisioning.ResourceRef) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.harborProject[key(ref)]
}
