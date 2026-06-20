// Package decommission — общий контракт workflow «Вывод из эксплуатации»
// (soft delete, ADR-0008, ADR-0012). Пакет публичный и не зависит от слоя
// хранения/транспорта: его импортируют и сервис projects (для запуска workflow),
// и DevInfra worker (для регистрации workflow и реализации activities). Тело
// workflow детерминировано — весь ввод-вывод вынесен в activities, вызываемые по
// строковым именам.
//
// Семантика — soft-delete с отзывом доступа: предусловие снятой нагрузки K8s →
// GitLab archive + revoke → Harbor read-only + robot revoke → Vault revoke
// SecretID (НЕОБРАТИМО, точка невозврата) → guarded-CAS ACTIVE→DECOMMISSIONED в
// каталоге. Сбой ДО точки невозврата → идемпотентные компенсации (Harbor→writable,
// GitLab→unarchive); сбой ПОСЛЕ → форвард-only ретраи + алерт оператору без
// молчаливого отката (ADR-0005).
package decommission

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/YuriB9/idp/services/projects/provisioning"
)

// WorkflowName — зарегистрированное имя workflow «Вывод из эксплуатации».
const WorkflowName = "DecommissionWorkflow"

// Имена activities. Workflow вызывает их по строковым именам (реализации с
// клиентами K8s/GitLab/Harbor/Vault/каталога живут в worker'е и не протекают в
// граф API).
const (
	// ActivityEnsureLoadDrained — предварительная проверка снятой нагрузки K8s
	// (предусловие, ADR-0012). Граница под будущий K8s-worker.
	ActivityEnsureLoadDrained = "EnsureLoadDrained"
	// ActivityGitLabArchive — архивировать репозиторий и отозвать доступы.
	ActivityGitLabArchive = "GitLabArchive"
	// ActivityGitLabUnarchive — компенсация: разархивировать и восстановить доступы.
	ActivityGitLabUnarchive = "GitLabUnarchive"
	// ActivityHarborSetReadOnly — перевести директорию образов в read-only и
	// отозвать Robot.
	ActivityHarborSetReadOnly = "HarborSetReadOnly"
	// ActivityHarborSetWritable — компенсация: вернуть директорию в writable.
	ActivityHarborSetWritable = "HarborSetWritable"
	// ActivityVaultRevokeSecretID — отозвать активные SecretID/токены (НЕОБРАТИМО,
	// точка невозврата). Имя activity, а не секрет (подавляем gosec G101).
	ActivityVaultRevokeSecretID = "VaultRevokeSecretID" //nolint:gosec // G101: имя activity, не секрет
	// ActivityCatalogDecommission — guarded-CAS перевод ACTIVE→DECOMMISSIONED.
	ActivityCatalogDecommission = "CatalogDecommission"
)

// WorkflowID возвращает детерминированный идентификатор workflow для пары
// (project, name): повторный запуск того же вывода не порождает второй
// конкурентный workflow (см. WorkflowIDReusePolicy на стороне запуска).
func WorkflowID(project, name string) string {
	return fmt.Sprintf("decommission:%s:%s", project, name)
}

// DecommissionInput — вход workflow «Вывод из эксплуатации».
type DecommissionInput struct {
	// ServiceID — идентификатор записи каталога (UUID в строковом виде).
	ServiceID string
	// Project — идентификатор проекта-владельца.
	Project string
	// Name — имя сервиса.
	Name string
	// LoadDrained — явное предусловие снятой нагрузки из K8s (ADR-0012).
	LoadDrained bool
}

// EnsureLoadDrainedInput — вход предварительной проверки снятой нагрузки.
type EnsureLoadDrainedInput struct {
	Ref         provisioning.ResourceRef
	LoadDrained bool
}

// activityOptions — единые таймауты/ретраи для activities вывода из эксплуатации.
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

// DecommissionWorkflow оркеструет вывод сервиса из эксплуатации (Saga):
// предусловие снятой нагрузки → GitLab archive → Harbor read-only → Vault revoke
// SecretID (ТОЧКА НЕВОЗВРАТА) → guarded-CAS ACTIVE→DECOMMISSIONED. Сбой ДО точки
// невозврата → идемпотентные компенсации в обратном порядке (Harbor→writable,
// GitLab→unarchive), каталог не трогаем. Сбой ПОСЛЕ → не молчаливый откат:
// форвард-only ретраи (RetryPolicy), при исчерпании — алерт оператору
// (ADR-0005/0012). Тело детерминировано: I/O только через activities.
func DecommissionWorkflow(ctx workflow.Context, in DecommissionInput) error {
	ctx = workflow.WithActivityOptions(ctx, activityOptions())
	logger := workflow.GetLogger(ctx)
	ref := provisioning.ResourceRef{ServiceID: in.ServiceID, Project: in.Project, Name: in.Name}

	// 0. Предусловие: нагрузка снята из K8s. Проверяется ДО любых побочных
	// эффектов; невыполнение (non-retryable) → workflow завершается ошибкой
	// предусловия, отзывы доступа не выполняются.
	pre := EnsureLoadDrainedInput{Ref: ref, LoadDrained: in.LoadDrained}
	if err := workflow.ExecuteActivity(ctx, ActivityEnsureLoadDrained, pre).Get(ctx, nil); err != nil {
		return fmt.Errorf("decommission: предусловие снятой нагрузки не выполнено: %w", err)
	}

	// comps — компенсации успешно выполненных шагов ДО точки невозврата.
	var comps []func(workflow.Context) error
	addComp := func(activityName string, arg any) {
		comps = append(comps, func(c workflow.Context) error {
			return workflow.ExecuteActivity(c, activityName, arg).Get(c, nil)
		})
	}

	// failBeforePoint выполняет компенсации в обратном порядке (в «отвязанном»
	// контексте) и возвращает ошибку. Доступ необратимо не отзывался, каталог
	// не изменён.
	failBeforePoint := func(cause error) error {
		dctx, cancel := workflow.NewDisconnectedContext(ctx)
		defer cancel()
		dctx = workflow.WithActivityOptions(dctx, activityOptions())
		if !runCompensations(dctx, logger, comps) {
			logger.Error("decommission: ALERT оператору — компенсация не удалась полностью",
				"service_id", in.ServiceID, "project", in.Project, "name", in.Name, "err", cause)
		}
		return fmt.Errorf("decommission: вывод из эксплуатации не удался: %w", cause)
	}

	// 1. GitLab: archive репозитория + отзыв доступов. Обратимо → компенсация.
	if err := workflow.ExecuteActivity(ctx, ActivityGitLabArchive, ref).Get(ctx, nil); err != nil {
		return failBeforePoint(err)
	}
	addComp(ActivityGitLabUnarchive, ref)

	// 2. Harbor: read-only + отзыв Robot. Обратимо → компенсация.
	if err := workflow.ExecuteActivity(ctx, ActivityHarborSetReadOnly, ref).Get(ctx, nil); err != nil {
		return failBeforePoint(err)
	}
	addComp(ActivityHarborSetWritable, ref)

	// 3. Vault: отзыв активных SecretID/токенов. ТОЧКА НЕВОЗВРАТА (необратимо):
	// отозванный токен не вернуть. Сбой ДО завершения → компенсации GitLab/Harbor.
	if err := workflow.ExecuteActivity(ctx, ActivityVaultRevokeSecretID, ref).Get(ctx, nil); err != nil {
		return failBeforePoint(err)
	}

	// 4. Каталог: guarded-CAS ACTIVE→DECOMMISSIONED. ПОСЛЕ точки невозврата —
	// без молчаливого отката: ретраи идемпотентны (RetryPolicy), при исчерпании
	// (в т.ч. конфликт guarded-CAS) — алерт оператору; доступ остаётся отозванным,
	// каталог = целевой источник правды (ADR-0005/0012).
	if err := workflow.ExecuteActivity(ctx, ActivityCatalogDecommission, ref).Get(ctx, nil); err != nil {
		logger.Error("decommission: ALERT оператору — перевод каталога в DECOMMISSIONED не удался после точки невозврата",
			"service_id", in.ServiceID, "project", in.Project, "name", in.Name, "err", err)
		return fmt.Errorf("decommission: перевод каталога в DECOMMISSIONED не удался: %w", err)
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
			logger.Error("decommission: компенсация шага не удалась", "step", i, "err", err)
		}
	}
	return ok
}
