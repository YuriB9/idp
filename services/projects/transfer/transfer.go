// Package transfer — общий контракт workflow «Перенос сервиса» (ADR-0008,
// ADR-0013). Пакет публичный и не зависит от слоя хранения/транспорта: его
// импортируют и сервис projects (для запуска workflow), и DevInfra worker (для
// регистрации workflow и реализации activities). Тело workflow детерминировано —
// весь ввод-вывод вынесен в activities, вызываемые по строковым именам.
//
// Семантика — смена проекта-владельца сервиса: каталог active→transferring
// (компенсируемо) → перенос репозитория GitLab в новую группу (ТОЧКА НЕВОЗВРАТА)
// → миграция путей/политик Vault → обновление метаданных Harbor → каталог
// transferring→active (project=target) → перенос ролей владельцев в IDM. Сбой ДО
// точки невозврата → идемпотентная компенсация (каталог transferring→active);
// сбой ПОСЛЕ → форвард-only ретраи + алерт оператору без молчаливого отката
// (ADR-0005/0013).
package transfer

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// WorkflowName — зарегистрированное имя workflow «Перенос сервиса».
const WorkflowName = "TransferServiceWorkflow"

// Имена activities. Workflow вызывает их по строковым именам (реализации с
// клиентами GitLab/Vault/Harbor/каталога/IDM живут в worker'е и не протекают в
// граф API).
const (
	// ActivityCatalogBeginTransfer — guarded-CAS ACTIVE→TRANSFERRING (компенсируемо).
	ActivityCatalogBeginTransfer = "CatalogBeginTransfer"
	// ActivityCatalogCommitTransfer — guarded-CAS TRANSFERRING→ACTIVE + project=target.
	ActivityCatalogCommitTransfer = "CatalogCommitTransfer"
	// ActivityCatalogAbortTransfer — компенсация: guarded-CAS TRANSFERRING→ACTIVE.
	ActivityCatalogAbortTransfer = "CatalogAbortTransfer"
	// ActivityGitLabTransferRepo — перенос репозитория в группу target (ТОЧКА
	// НЕВОЗВРАТА: чистая компенсация в MVP не моделируется).
	ActivityGitLabTransferRepo = "GitLabTransferRepo"
	// ActivityVaultMigratePaths — миграция путей: копия секретов source→target +
	// новые политики + очистка старых.
	ActivityVaultMigratePaths = "VaultMigratePaths"
	// ActivityHarborUpdateMetadata — обновление метаданных/прав директории под target.
	ActivityHarborUpdateMetadata = "HarborUpdateMetadata"
	// ActivityTransferOwnerRoles — перенос ролей владельцев: revoke owner:source +
	// assign owner:target + инвалидация кэша по затронутым субъектам.
	ActivityTransferOwnerRoles = "TransferOwnerRoles"
)

// WorkflowID возвращает детерминированный идентификатор workflow для пары
// (source, name): повторный запуск того же переноса не порождает второй
// конкурентный workflow (см. WorkflowIDReusePolicy на стороне запуска).
func WorkflowID(source, name string) string {
	return fmt.Sprintf("transfer:%s:%s", source, name)
}

// TransferInput — вход workflow «Перенос сервиса».
type TransferInput struct {
	// ServiceID — идентификатор записи каталога (UUID в строковом виде).
	ServiceID string
	// Source — исходный проект-владелец.
	Source string
	// Target — целевой проект-владелец.
	Target string
	// Name — имя сервиса (сохраняется при переносе).
	Name string
	// Owners — текущий набор владельцев (для переноса ролей в IDM).
	Owners []string
}

// TransferRef — общий аргумент activities переноса инфраструктуры.
type TransferRef struct {
	ServiceID string
	Source    string
	Target    string
	Name      string
}

// CatalogTransferInput — вход activities смены project в каталоге.
type CatalogTransferInput struct {
	ServiceID string
	Target    string
}

// TransferRolesInput — вход activity переноса ролей владельцев между проектами.
type TransferRolesInput struct {
	Source string
	Target string
	Owners []string
}

// activityOptions — единые таймауты/ретраи для activities переноса. Транзиентные
// ошибки повторяются с экспоненциальным backoff; окончательные — возвращаются как
// non-retryable ApplicationError → ветка компенсации/алерт.
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

// TransferServiceWorkflow оркеструет перенос сервиса в другой проект (Saga):
// каталог active→transferring → GitLab transfer (ТОЧКА НЕВОЗВРАТА) → Vault
// миграция путей → Harbor метаданные → каталог transferring→active (project=target)
// → перенос ролей владельцев IDM. Сбой ДО точки невозврата → идемпотентная
// компенсация (каталог transferring→active). Сбой ПОСЛЕ → не молчаливый откат:
// форвард-only ретраи (RetryPolicy), при исчерпании — алерт оператору; сервис
// может остаться в transferring до ручного довыполнения, каталог = целевой
// источник правды (ADR-0005/0013). Тело детерминировано: I/O только через activities.
func TransferServiceWorkflow(ctx workflow.Context, in TransferInput) error {
	ctx = workflow.WithActivityOptions(ctx, activityOptions())
	logger := workflow.GetLogger(ctx)
	ref := TransferRef{ServiceID: in.ServiceID, Source: in.Source, Target: in.Target, Name: in.Name}
	catIn := CatalogTransferInput{ServiceID: in.ServiceID, Target: in.Target}

	// 0. Каталог: guarded-CAS ACTIVE→TRANSFERRING. Компенсируемо до точки невозврата
	// (каталог transferring→active). Занятое имя/недопустимый статус → ошибка ДО
	// любых внешних побочных эффектов.
	if err := workflow.ExecuteActivity(ctx, ActivityCatalogBeginTransfer, catIn).Get(ctx, nil); err != nil {
		return fmt.Errorf("transfer: начало переноса не удалось: %w", err)
	}

	// abortBeforePoint выполняет компенсацию каталога (в «отвязанном» контексте) и
	// возвращает ошибку. Внешние системы ещё не затронуты (точка невозврата — на
	// шаге 1).
	abortBeforePoint := func(cause error) error {
		dctx, cancel := workflow.NewDisconnectedContext(ctx)
		defer cancel()
		dctx = workflow.WithActivityOptions(dctx, activityOptions())
		if err := workflow.ExecuteActivity(dctx, ActivityCatalogAbortTransfer, catIn).Get(dctx, nil); err != nil {
			logger.Error("transfer: ALERT оператору — компенсация каталога не удалась",
				"service_id", in.ServiceID, "source", in.Source, "target", in.Target, "name", in.Name, "err", err)
		}
		return fmt.Errorf("transfer: перенос не удался: %w", cause)
	}

	// 1. GitLab: перенос репозитория в группу target. ТОЧКА НЕВОЗВРАТА (необратимо):
	// чистая компенсация transfer-back в MVP не моделируется. Сбой ДО завершения →
	// компенсация каталога (репозиторий ещё не перенесён).
	if err := workflow.ExecuteActivity(ctx, ActivityGitLabTransferRepo, ref).Get(ctx, nil); err != nil {
		return abortBeforePoint(err)
	}

	// alertAfterPoint фиксирует алерт оператору для форвард-only шагов после точки
	// невозврата: молчаливого отката нет, доступ/репозиторий уже частично перенесены.
	alertAfterPoint := func(step string, cause error) error {
		logger.Error("transfer: ALERT оператору — шаг после точки невозврата не удался",
			"step", step, "service_id", in.ServiceID, "source", in.Source, "target", in.Target,
			"name", in.Name, "err", cause)
		return fmt.Errorf("transfer: %s не удался после точки невозврата: %w", step, cause)
	}

	// 2. Vault: миграция путей (копия секретов + новые политики + очистка старых).
	if err := workflow.ExecuteActivity(ctx, ActivityVaultMigratePaths, ref).Get(ctx, nil); err != nil {
		return alertAfterPoint("миграция Vault", err)
	}

	// 3. Harbor: обновление метаданных/прав под target.
	if err := workflow.ExecuteActivity(ctx, ActivityHarborUpdateMetadata, ref).Get(ctx, nil); err != nil {
		return alertAfterPoint("обновление Harbor", err)
	}

	// 4. Каталог: guarded-CAS TRANSFERRING→ACTIVE + project=target. Конфликт
	// guarded-CAS после точки невозврата → алерт (без отката инфраструктуры).
	if err := workflow.ExecuteActivity(ctx, ActivityCatalogCommitTransfer, catIn).Get(ctx, nil); err != nil {
		return alertAfterPoint("фиксация каталога", err)
	}

	// 5. IDM: перенос ролей владельцев + инвалидация кэша. Форвард-only, идемпотентно.
	roles := TransferRolesInput{Source: in.Source, Target: in.Target, Owners: in.Owners}
	if err := workflow.ExecuteActivity(ctx, ActivityTransferOwnerRoles, roles).Get(ctx, nil); err != nil {
		return alertAfterPoint("перенос ролей IDM", err)
	}
	return nil
}
