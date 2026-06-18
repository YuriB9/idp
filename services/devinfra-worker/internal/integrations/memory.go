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
}

// NewMemory создаёт пустой in-memory набор клиентов.
func NewMemory() *Memory {
	return &Memory{
		repos:     map[string]bool{},
		harbor:    map[string]bool{},
		vault:     map[string]bool{},
		variables: map[string]map[string]string{},
	}
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
