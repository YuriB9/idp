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
	"github.com/YuriB9/idp/services/projects/provisioning"
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
func (s *Starter) StartCreateService(ctx context.Context, serviceID, project, name string) error {
	opts := client.StartWorkflowOptions{
		ID:                       provisioning.WorkflowID(project, name),
		TaskQueue:                s.taskQueue,
		WorkflowIDReusePolicy:    enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
	}
	in := provisioning.CreateServiceInput{ServiceID: serviceID, Project: project, Name: name}
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
