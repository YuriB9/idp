// Package changeowners — общий контракт workflow «Изменение владельцев»
// (ADR-0008, ADR-0011). Пакет публичный и не зависит от слоя хранения/
// транспорта: его импортируют и сервис projects (для запуска workflow), и
// DevInfra worker (для регистрации workflow и реализации activities). Тело
// workflow детерминировано — весь ввод-вывод вынесен в activities, вызываемые по
// строковым именам.
//
// Семантика — декларативная замена набора владельцев: на вход подаётся полный
// желаемый набор (Desired) и прежний (Previous); diff (add/remove) вычисляется
// детерминированно внутри workflow. Saga с точкой невозврата на записи владельцев
// в каталог: сбой ДО — идемпотентные компенсации; сбой ПОСЛЕ — алерт оператору
// без молчаливого отката (ADR-0005).
package changeowners

import (
	"fmt"
	"slices"
	"time"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/YuriB9/idp/services/projects/provisioning"
)

// WorkflowName — зарегистрированное имя workflow «Изменение владельцев».
const WorkflowName = "ChangeOwnersWorkflow"

// Имена activities. Workflow вызывает их по строковым именам (реализации с
// клиентами GitLab/Vault/IDM живут в worker'е и не протекают в граф API).
const (
	// ActivityGitLabSyncMembers — синхронизировать участников GitLab по diff.
	ActivityGitLabSyncMembers = "GitLabSyncMembers"
	// ActivityGitLabRestoreMembers — компенсация: восстановить прежних участников.
	ActivityGitLabRestoreMembers = "GitLabRestoreMembers"
	// ActivityVaultSyncOwners — синхронизировать политики доступа Vault по diff.
	ActivityVaultSyncOwners = "VaultSyncOwners"
	// ActivityVaultRestoreOwners — компенсация: восстановить прежние политики.
	ActivityVaultRestoreOwners = "VaultRestoreOwners"
	// ActivityCatalogSetOwners — guarded-CAS запись набора владельцев в каталог
	// (ТОЧКА НЕВОЗВРАТА).
	ActivityCatalogSetOwners = "CatalogSetOwners"
	// ActivityIDMSyncOwnerRoles — выдать/отозвать роль владельца в IDM и
	// инвалидировать кэш решений по затронутым субъектам.
	ActivityIDMSyncOwnerRoles = "IDMSyncOwnerRoles"
)

// WorkflowID возвращает детерминированный идентификатор workflow для пары
// (project, name): повторный запуск той же смены не порождает второй
// конкурентный workflow (см. WorkflowIDReusePolicy на стороне запуска).
func WorkflowID(project, name string) string {
	return fmt.Sprintf("change-owners:%s:%s", project, name)
}

// ChangeOwnersInput — вход workflow «Изменение владельцев».
type ChangeOwnersInput struct {
	// ServiceID — идентификатор записи каталога (UUID в строковом виде).
	ServiceID string
	// Project — идентификатор проекта-владельца.
	Project string
	// Name — имя сервиса.
	Name string
	// Desired — полный желаемый набор владельцев (нормализован вызывающим).
	Desired []string
	// Previous — прежний набор владельцев (для diff и компенсаций).
	Previous []string
	// ExpectedVersion — ожидаемая версия владельцев для guarded-CAS каталога.
	ExpectedVersion int64
}

// SyncMembersInput — вход activities синхронизации участников/политик по diff.
type SyncMembersInput struct {
	Ref    provisioning.ResourceRef
	Add    []string
	Remove []string
}

// RestoreInput — вход компенсаций: восстановить прежний состав владельцев.
type RestoreInput struct {
	Ref      provisioning.ResourceRef
	Previous []string
}

// CatalogSetOwnersInput — вход activity guarded-CAS записи владельцев в каталог.
type CatalogSetOwnersInput struct {
	ServiceID       string
	Desired         []string
	ExpectedVersion int64
}

// IDMSyncInput — вход activity синхронизации ролей IDM по diff владельцев.
type IDMSyncInput struct {
	// Project — проект, доступ к ресурсу project:<Project> которого выдаётся/отзывается.
	Project string
	Add     []string
	Remove  []string
}

// activityOptions — единые таймауты/ретраи для activities смены владельцев.
// Транзиентные ошибки повторяются с экспоненциальным backoff; окончательные —
// возвращаются как non-retryable ApplicationError → ветка компенсации/алерт.
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

// Diff вычисляет детерминированный diff (add/remove) между прежним и желаемым
// наборами владельцев. Результаты отсортированы для детерминизма workflow.
func Diff(previous, desired []string) (add, remove []string) {
	prev := map[string]bool{}
	for _, o := range previous {
		prev[o] = true
	}
	want := map[string]bool{}
	for _, o := range desired {
		want[o] = true
	}
	for o := range want {
		if !prev[o] {
			add = append(add, o)
		}
	}
	for o := range prev {
		if !want[o] {
			remove = append(remove, o)
		}
	}
	slices.Sort(add)
	slices.Sort(remove)
	return add, remove
}

// ChangeOwnersWorkflow оркеструет смену владельцев (Saga): GitLab → Vault →
// запись владельцев в каталог (точка невозврата) → синхронизация ролей IDM +
// инвалидация кэша. Сбой ДО точки невозврата → идемпотентные компенсации в
// обратном порядке (восстановление прежнего состава). Сбой ПОСЛЕ → не молчаливый
// откат: алерт оператору (ADR-0005/0008). Пустой diff → no-op без обращения к
// интеграциям.
func ChangeOwnersWorkflow(ctx workflow.Context, in ChangeOwnersInput) error {
	ctx = workflow.WithActivityOptions(ctx, activityOptions())
	logger := workflow.GetLogger(ctx)
	ref := provisioning.ResourceRef{ServiceID: in.ServiceID, Project: in.Project, Name: in.Name}

	add, remove := Diff(in.Previous, in.Desired)
	if len(add) == 0 && len(remove) == 0 {
		// Идемпотентный no-op: состав не меняется — интеграции не трогаем.
		logger.Info("change-owners: пустой diff, no-op", "service_id", in.ServiceID)
		return nil
	}

	// comps — компенсации успешно выполненных шагов до точки невозврата.
	var comps []func(workflow.Context) error
	addComp := func(activityName string, arg any) {
		comps = append(comps, func(c workflow.Context) error {
			return workflow.ExecuteActivity(c, activityName, arg).Get(c, nil)
		})
	}

	// failBeforePoint выполняет компенсации в обратном порядке (в «отвязанном»
	// контексте) и возвращает ошибку. owners в каталоге не изменены.
	failBeforePoint := func(cause error) error {
		dctx, cancel := workflow.NewDisconnectedContext(ctx)
		defer cancel()
		dctx = workflow.WithActivityOptions(dctx, activityOptions())
		if !runCompensations(dctx, logger, comps) {
			logger.Error("change-owners: ALERT оператору — компенсация не удалась полностью",
				"service_id", in.ServiceID, "project", in.Project, "name", in.Name, "err", cause)
		}
		return fmt.Errorf("change-owners: смена владельцев не удалась: %w", cause)
	}

	// 1. GitLab: синхронизация участников по diff.
	sync := SyncMembersInput{Ref: ref, Add: add, Remove: remove}
	if err := workflow.ExecuteActivity(ctx, ActivityGitLabSyncMembers, sync).Get(ctx, nil); err != nil {
		return failBeforePoint(err)
	}
	addComp(ActivityGitLabRestoreMembers, RestoreInput{Ref: ref, Previous: in.Previous})

	// 2. Vault: синхронизация политик доступа по diff.
	if err := workflow.ExecuteActivity(ctx, ActivityVaultSyncOwners, sync).Get(ctx, nil); err != nil {
		return failBeforePoint(err)
	}
	addComp(ActivityVaultRestoreOwners, RestoreInput{Ref: ref, Previous: in.Previous})

	// 3. Каталог: guarded-CAS запись владельцев. ТОЧКА НЕВОЗВРАТА. Конфликт версии
	// (non-retryable) до этого момента → компенсации GitLab/Vault.
	catIn := CatalogSetOwnersInput{ServiceID: in.ServiceID, Desired: in.Desired, ExpectedVersion: in.ExpectedVersion}
	if err := workflow.ExecuteActivity(ctx, ActivityCatalogSetOwners, catIn).Get(ctx, nil); err != nil {
		return failBeforePoint(err)
	}

	// 4. IDM: синхронизация ролей + инвалидация кэша. ПОСЛЕ точки невозврата —
	// без молчаливого отката: ретраи идемпотентны (RetryPolicy), при исчерпании —
	// алерт оператору; каталог остаётся источником правды (ADR-0005/0008).
	idmIn := IDMSyncInput{Project: in.Project, Add: add, Remove: remove}
	if err := workflow.ExecuteActivity(ctx, ActivityIDMSyncOwnerRoles, idmIn).Get(ctx, nil); err != nil {
		logger.Error("change-owners: ALERT оператору — синхронизация ролей IDM не удалась после точки невозврата",
			"service_id", in.ServiceID, "project", in.Project, "name", in.Name, "err", err)
		return fmt.Errorf("change-owners: синхронизация ролей IDM не удалась: %w", err)
	}
	return nil
}

// runCompensations выполняет компенсации в обратном порядке. Возвращает true,
// если все успешны. Компенсации идемпотентны — повторный прогон безопасен.
func runCompensations(ctx workflow.Context, logger log.Logger, comps []func(workflow.Context) error) bool {
	ok := true
	for i := len(comps) - 1; i >= 0; i-- {
		if err := comps[i](ctx); err != nil {
			ok = false
			logger.Error("change-owners: компенсация шага не удалась", "step", i, "err", err)
		}
	}
	return ok
}
