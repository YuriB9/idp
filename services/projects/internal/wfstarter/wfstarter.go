// Package wfstarter — адаптер запуска Temporal-workflow «Создание сервиса» из
// API-процесса. Реализует usecase.WorkflowStarter поверх client.Client,
// инкапсулируя детерминированный WorkflowID, очередь задач и политику
// переиспользования ID (идемпотентность повторного запуска).
package wfstarter

import (
	"context"
	"fmt"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/YuriB9/idp/services/projects/changeowners"
	"github.com/YuriB9/idp/services/projects/decommission"
	"github.com/YuriB9/idp/services/projects/provisioning"
	"github.com/YuriB9/idp/services/projects/transfer"
)

// Starter запускает workflow создания на заданной task-queue.
type Starter struct {
	client    client.Client
	taskQueue string
}

// New создаёт адаптер запуска. taskQueue должна совпадать с очередью worker'а.
func New(c client.Client, taskQueue string) *Starter {
	if taskQueue == "" {
		taskQueue = provisioning.DefaultTaskQueue
	}
	return &Starter{client: c, taskQueue: taskQueue}
}

// StartCreateService запускает workflow с детерминированным WorkflowID. Политика
// REJECT_DUPLICATE не даёт стартовать второй конкурентный workflow для того же
// сервиса; повторный запуск после терминального статуса допускается.
func (s *Starter) StartCreateService(ctx context.Context, serviceID, project, name string, owners []string) error {
	opts := client.StartWorkflowOptions{
		ID:                       provisioning.WorkflowID(project, name),
		TaskQueue:                s.taskQueue,
		WorkflowIDReusePolicy:    enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
	}
	in := provisioning.CreateServiceInput{ServiceID: serviceID, Project: project, Name: name, Owners: owners}
	if _, err := s.client.ExecuteWorkflow(ctx, opts, provisioning.CreateServiceWorkflow, in); err != nil {
		return fmt.Errorf("wfstarter: запуск workflow создания: %w", err)
	}
	return nil
}

// StartChangeOwners запускает workflow «Изменение владельцев» с детерминированным
// WorkflowID. Политика та же, что у создания: не стартовать второй конкурентный
// workflow для того же сервиса; повторный запуск после терминального статуса
// допускается.
func (s *Starter) StartChangeOwners(ctx context.Context, serviceID, project, name string, desired, previous []string, expectedVersion int64) error {
	opts := client.StartWorkflowOptions{
		ID:                       changeowners.WorkflowID(project, name),
		TaskQueue:                s.taskQueue,
		WorkflowIDReusePolicy:    enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
	}
	in := changeowners.ChangeOwnersInput{
		ServiceID:       serviceID,
		Project:         project,
		Name:            name,
		Desired:         desired,
		Previous:        previous,
		ExpectedVersion: expectedVersion,
	}
	if _, err := s.client.ExecuteWorkflow(ctx, opts, changeowners.ChangeOwnersWorkflow, in); err != nil {
		return fmt.Errorf("wfstarter: запуск workflow смены владельцев: %w", err)
	}
	return nil
}

// StartDecommission запускает workflow «Вывод из эксплуатации» с детерминированным
// WorkflowID. Политика та же, что у создания/смены владельцев: не стартовать
// второй конкурентный workflow для того же сервиса; повторный запуск после
// терминального статуса допускается.
func (s *Starter) StartDecommission(ctx context.Context, serviceID, project, name string, loadDrained bool) error {
	opts := client.StartWorkflowOptions{
		ID:                       decommission.WorkflowID(project, name),
		TaskQueue:                s.taskQueue,
		WorkflowIDReusePolicy:    enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
	}
	in := decommission.DecommissionInput{
		ServiceID:   serviceID,
		Project:     project,
		Name:        name,
		LoadDrained: loadDrained,
	}
	if _, err := s.client.ExecuteWorkflow(ctx, opts, decommission.DecommissionWorkflow, in); err != nil {
		return fmt.Errorf("wfstarter: запуск workflow вывода из эксплуатации: %w", err)
	}
	return nil
}

// StartTransfer запускает workflow «Перенос сервиса» с детерминированным
// WorkflowID на пару (source, name). Политика та же, что у прочих сценариев: не
// стартовать второй конкурентный workflow для того же сервиса; повторный запуск
// после терминального статуса допускается.
func (s *Starter) StartTransfer(ctx context.Context, serviceID, source, target, name string, owners []string) error {
	opts := client.StartWorkflowOptions{
		ID:                       transfer.WorkflowID(source, name),
		TaskQueue:                s.taskQueue,
		WorkflowIDReusePolicy:    enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
	}
	in := transfer.TransferInput{
		ServiceID: serviceID,
		Source:    source,
		Target:    target,
		Name:      name,
		Owners:    owners,
	}
	if _, err := s.client.ExecuteWorkflow(ctx, opts, transfer.TransferServiceWorkflow, in); err != nil {
		return fmt.Errorf("wfstarter: запуск workflow переноса: %w", err)
	}
	return nil
}
