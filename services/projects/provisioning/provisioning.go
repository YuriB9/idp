// Package provisioning — общий контракт workflow «Создание сервиса» (ADR-0008).
// Пакет публичный и не зависит от слоя хранения/транспорта: его импортируют и
// сервис projects (для запуска workflow), и DevInfra worker (для регистрации
// workflow и реализации activities). Тело workflow детерминировано — весь
// ввод-вывод вынесен в activities, вызываемые по строковым именам, чтобы не
// тянуть реализации интеграций в граф API-процесса.
package provisioning

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// DefaultTaskQueue — очередь задач DevInfra worker'а по умолчанию (ADR-0001).
// API и worker должны использовать одну и ту же очередь; значение совпадает с
// дефолтом env TEMPORAL_TASK_QUEUE worker'а.
const DefaultTaskQueue = "devinfra"

// WorkflowName — зарегистрированное имя workflow «Создание сервиса».
const WorkflowName = "CreateServiceWorkflow"

// Имена activities. Workflow вызывает их по строковым именам (а не по ссылке на
// функцию), чтобы реализации (с клиентами GitLab/Vault/Harbor) оставались в
// worker'е и не протекали в граф зависимостей API.
const (
	// ActivityGitLabCreateRepo — создать репозиторий в группе проекта.
	ActivityGitLabCreateRepo = "GitLabCreateRepo"
	// ActivityGitLabDeleteRepo — компенсация: удалить репозиторий.
	ActivityGitLabDeleteRepo = "GitLabDeleteRepo"
	// ActivityHarborCreate — создать директорию образов + Robot Account.
	ActivityHarborCreate = "HarborCreateProject"
	// ActivityHarborDelete — компенсация: удалить директорию образов.
	ActivityHarborDelete = "HarborDeleteProject"
	// ActivityVaultSetup — создать политики + AppRole (RoleID/SecretID).
	ActivityVaultSetup = "VaultSetupAppRole"
	// ActivityVaultTeardown — компенсация: удалить политики + AppRole.
	ActivityVaultTeardown = "VaultTeardownAppRole"
	// ActivityInjectSecrets — записать секреты Vault/Harbor в CI/CD-переменные GitLab.
	// Это имя activity, а не учётные данные (подавляем ложное срабатывание gosec G101).
	ActivityInjectSecrets = "GitLabInjectSecrets" //nolint:gosec // G101: имя activity, не секрет
	// ActivityTransitionActive — guarded-CAS перевод записи CREATING→ACTIVE.
	ActivityTransitionActive = "CatalogTransitionActive"
	// ActivityTransitionFailed — guarded-CAS перевод записи CREATING→FAILED.
	ActivityTransitionFailed = "CatalogTransitionFailed"
	// Activities назначения владельцев при создании (вариант B, ADR-0011/0023).
	// Реализации живут в DevInfra worker'е и переиспользуются из сценария смены
	// владельцев. Пакет provisioning НЕ может импортировать changeowners (тот
	// зависит от provisioning), поэтому имена дублируются здесь; они ДОЛЖНЫ
	// совпадать с именами в changeowners.
	// ActivityGitLabSyncMembers — назначить владельцев участниками репозитория GitLab.
	ActivityGitLabSyncMembers = "GitLabSyncMembers"
	// ActivityVaultSyncOwners — выдать владельцам политики доступа Vault.
	ActivityVaultSyncOwners = "VaultSyncOwners"
	// ActivityIDMSyncOwnerRoles — выдать владельцам роли в IDM (после активации).
	ActivityIDMSyncOwnerRoles = "IDMSyncOwnerRoles"
)

// SyncOwnersInput — вход activities назначения владельцев по diff. JSON-форма
// ДОЛЖНА совпадать с changeowners.SyncMembersInput (Ref/Add/Remove), чтобы worker
// мог десериализовать аргумент в свой тип activity.
type SyncOwnersInput struct {
	Ref    ResourceRef
	Add    []string
	Remove []string
}

// IDMSyncOwnersInput — вход activity синхронизации ролей IDM по diff владельцев.
// JSON-форма ДОЛЖНА совпадать с changeowners.IDMSyncInput (Project/Add/Remove).
type IDMSyncOwnersInput struct {
	Project string
	Add     []string
	Remove  []string
}

// WorkflowID возвращает детерминированный идентификатор workflow для пары
// (project, name). Стабильность ID обеспечивает идемпотентность повторного
// запуска: при той же паре повторный старт не порождает второй конкурентный
// workflow (см. WorkflowIDReusePolicy на стороне запуска).
func WorkflowID(project, name string) string {
	return fmt.Sprintf("create-service:%s:%s", project, name)
}

// CreateServiceInput — вход workflow «Создание сервиса».
type CreateServiceInput struct {
	// ServiceID — идентификатор записи каталога (UUID в строковом виде).
	ServiceID string
	// Project — идентификатор проекта-владельца.
	Project string
	// Name — имя создаваемого сервиса.
	Name string
	// Owners — нормализованный набор владельцев, устанавливаемых воркфлоу во
	// внешних системах (GitLab members → Vault policies → IDM owner roles,
	// вариант B ADR-0023). В каталог владельцы уже записаны атомарно при вставке.
	Owners []string
}

// ResourceRef — общий аргумент activities провизии и компенсаций.
type ResourceRef struct {
	ServiceID string
	Project   string
	Name      string
}

// GitLabRepo — результат создания репозитория GitLab.
type GitLabRepo struct {
	// ProjectID — числовой/строковый идентификатор проекта в GitLab.
	ProjectID string
	// Path — полный путь репозитория (группа/имя).
	Path string
}

// HarborResult — результат создания директории и Robot Account в Harbor.
type HarborResult struct {
	// ProjectName — имя директории образов в Harbor.
	ProjectName string
	// RobotName — имя Robot Account.
	RobotName string
	// RobotToken — секрет Robot Account (НЕ логировать в открытом виде).
	RobotToken string
}

// VaultResult — выданные Vault реквизиты AppRole.
type VaultResult struct {
	// RoleID — публичная часть AppRole.
	RoleID string
	// SecretID — секретная часть AppRole (НЕ логировать в открытом виде).
	SecretID string
}

// InjectSecretsInput — вход activity инъекции секретов в CI/CD-переменные GitLab.
type InjectSecretsInput struct {
	Ref    ResourceRef
	GitLab GitLabRepo
	Harbor HarborResult
	Vault  VaultResult
}

// activityOptions — единые таймауты/ретраи для activities провизии. Транзиентные
// ошибки повторяются с экспоненциальным backoff; окончательные ошибки activity
// возвращают как temporal.NewNonRetryableApplicationError → ветка компенсации.
func activityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		HeartbeatTimeout:    15 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    5,
		},
	}
}

// CreateServiceWorkflow оркеструет провизию сервиса (вариант B, ADR-0023):
// GitLab репозиторий → назначение владельцев в GitLab → Harbor → Vault AppRole →
// выдача доступа владельцам в Vault → инъекция секретов → перевод записи в ACTIVE
// → выдача ролей владельцев в IDM. Владельцы берутся из in.Owners (в каталог они
// уже записаны атомарно при вставке). При фатальном (non-retryable) сбое ДО
// активации выполняет Saga-компенсации в обратном порядке и переводит запись в
// FAILED (ADR-0005); компенсации членства GitLab и политик Vault поглощаются
// удалением соответствующего ресурса (delete repo / teardown Vault). Сбой выдачи
// ролей IDM ПОСЛЕ активации не откатывает созданное — алерт оператору (каталог —
// источник правды, ADR-0005/0008). Тело детерминировано: I/O только через
// activities.
func CreateServiceWorkflow(ctx workflow.Context, in CreateServiceInput) error {
	ctx = workflow.WithActivityOptions(ctx, activityOptions())
	log := workflow.GetLogger(ctx)
	ref := ResourceRef{ServiceID: in.ServiceID, Project: in.Project, Name: in.Name}

	// comps — компенсации успешно выполненных шагов; запускаются в обратном
	// порядке при фатальном сбое. Храним замыкания в памяти исполнения workflow.
	var comps []func(workflow.Context) error
	addComp := func(activityName string, arg any) {
		comps = append(comps, func(c workflow.Context) error {
			return workflow.ExecuteActivity(c, activityName, arg).Get(c, nil)
		})
	}

	// fail выполняет компенсации и переводит запись в FAILED. При неуспехе самой
	// компенсации — alert оператору (структурный лог error), не молчим (ADR-0005).
	fail := func(cause error) error {
		// Компенсации выполняем в «отвязанном» контексте, чтобы они отработали
		// даже если основной контекст workflow отменяют.
		dctx, cancel := workflow.NewDisconnectedContext(ctx)
		defer cancel()
		dctx = workflow.WithActivityOptions(dctx, activityOptions())
		compsOK := runCompensations(dctx, log, comps)
		if !compsOK {
			log.Error("create-service: ALERT оператору — компенсация не удалась полностью",
				"service_id", in.ServiceID, "project", in.Project, "name", in.Name, "err", cause)
		}
		if err := workflow.ExecuteActivity(dctx, ActivityTransitionFailed, ref).Get(dctx, nil); err != nil {
			log.Error("create-service: ALERT оператору — перевод записи в FAILED не удался",
				"service_id", in.ServiceID, "err", err)
		}
		return fmt.Errorf("create-service: провизия не удалась: %w", cause)
	}

	// owners — назначаемые владельцы. В каталоге они уже зафиксированы при вставке;
	// здесь устанавливаем их роли во внешних системах (add=owners, remove=[]).
	owners := in.Owners

	// 1. GitLab: репозиторий.
	var repo GitLabRepo
	if err := workflow.ExecuteActivity(ctx, ActivityGitLabCreateRepo, ref).Get(ctx, &repo); err != nil {
		// На первом шаге компенсировать нечего — сразу FAILED.
		return fail(err)
	}
	addComp(ActivityGitLabDeleteRepo, ref)

	// 2. GitLab: назначить владельцев участниками репозитория. Отдельная
	// компенсация не нужна — удаление репозитория (шаг 1) снимает и членство.
	gitlabOwners := SyncOwnersInput{Ref: ref, Add: owners, Remove: nil}
	if err := workflow.ExecuteActivity(ctx, ActivityGitLabSyncMembers, gitlabOwners).Get(ctx, nil); err != nil {
		return fail(err)
	}

	// 3. Harbor: директория образов + Robot Account.
	var harbor HarborResult
	if err := workflow.ExecuteActivity(ctx, ActivityHarborCreate, ref).Get(ctx, &harbor); err != nil {
		return fail(err)
	}
	addComp(ActivityHarborDelete, ref)

	// 4. Vault: политики + AppRole. При окончательной недоступности Vault —
	// полный откат (Harbor, затем GitLab) через fail (ADR-0005).
	var vault VaultResult
	if err := workflow.ExecuteActivity(ctx, ActivityVaultSetup, ref).Get(ctx, &vault); err != nil {
		return fail(err)
	}
	addComp(ActivityVaultTeardown, ref)

	// 5. Vault: выдать владельцам политики доступа. Отдельная компенсация не нужна —
	// teardown Vault (шаг 4) снимает и политики владельцев.
	vaultOwners := SyncOwnersInput{Ref: ref, Add: owners, Remove: nil}
	if err := workflow.ExecuteActivity(ctx, ActivityVaultSyncOwners, vaultOwners).Get(ctx, nil); err != nil {
		return fail(err)
	}

	// 6. Инъекция секретов Vault/Harbor в CI/CD-переменные GitLab.
	inject := InjectSecretsInput{Ref: ref, GitLab: repo, Harbor: harbor, Vault: vault}
	if err := workflow.ExecuteActivity(ctx, ActivityInjectSecrets, inject).Get(ctx, nil); err != nil {
		return fail(err)
	}

	// 7. Успех: guarded-CAS CREATING→ACTIVE.
	if err := workflow.ExecuteActivity(ctx, ActivityTransitionActive, ref).Get(ctx, nil); err != nil {
		// Ресурсы созданы, но финальный переход проигран (статус уже не CREATING).
		// Не откатываем созданное; фиксируем для оператора.
		log.Error("create-service: ALERT оператору — перевод записи в ACTIVE не удался",
			"service_id", in.ServiceID, "err", err)
		return fmt.Errorf("create-service: перевод в ACTIVE не удался: %w", err)
	}

	// 8. IDM: выдать роли владельцев. ПОСЛЕ активации — без молчаливого отката:
	// ретраи идемпотентны (RetryPolicy), при исчерпании — алерт оператору, каталог
	// остаётся источником правды (ADR-0005/0008).
	idmOwners := IDMSyncOwnersInput{Project: in.Project, Add: owners, Remove: nil}
	if err := workflow.ExecuteActivity(ctx, ActivityIDMSyncOwnerRoles, idmOwners).Get(ctx, nil); err != nil {
		log.Error("create-service: ALERT оператору — выдача ролей владельцев в IDM не удалась после активации",
			"service_id", in.ServiceID, "project", in.Project, "name", in.Name, "err", err)
		return fmt.Errorf("create-service: выдача ролей владельцев в IDM не удалась: %w", err)
	}
	return nil
}

// runCompensations выполняет компенсации в обратном порядке. Возвращает true,
// если все компенсации успешны. Компенсации идемпотентны, поэтому повторный
// прогон безопасен.
func runCompensations(ctx workflow.Context, log log.Logger, comps []func(workflow.Context) error) bool {
	ok := true
	for i := len(comps) - 1; i >= 0; i-- {
		if err := comps[i](ctx); err != nil {
			ok = false
			log.Error("create-service: компенсация шага не удалась", "step", i, "err", err)
		}
	}
	return ok
}
