// Package grpcapi реализует gRPC ProjectsService поверх usecase-слоя каталога.
// Транспортная ответственность: валидация запроса, маппинг доменных ошибок в
// gRPC-коды и доменных типов в proto. Внутренние ошибки клиенту не раскрываются
// (никакого err.Error() наружу).
package grpcapi

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	projectsv1 "github.com/YuriB9/idp/pkg/api/projects/v1"
	"github.com/YuriB9/idp/pkg/auth"
	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/pkg/logger"
	"github.com/YuriB9/idp/services/projects/internal/repository"
)

// Catalog — зависимость транспорта от usecase-слоя.
type Catalog interface {
	Get(ctx context.Context, project, name string) (repository.Service, error)
	List(ctx context.Context, project string, pageSize int, pageToken string) ([]repository.Service, string, error)
	CreateService(ctx context.Context, project, name string, owners []string) (repository.Service, error)
	SetServiceOwners(ctx context.Context, project, name string, owners []string, expectedVersion int64) ([]string, int64, error)
	DecommissionService(ctx context.Context, project, name string, loadDrained bool) (repository.Service, error)
	TransferService(ctx context.Context, project, name, target string) (repository.Service, error)
}

// AccessChecker — зависимость от IDM (RBAC CheckAccess). Совместима с
// сгенерированным idmv1.AccessServiceClient; в тестах подменяется стабом.
type AccessChecker interface {
	CheckAccess(ctx context.Context, in *idmv1.CheckAccessRequest, opts ...grpc.CallOption) (*idmv1.CheckAccessResponse, error)
}

// Server реализует projectsv1.ProjectsServiceServer.
type Server struct {
	projectsv1.UnimplementedProjectsServiceServer
	catalog Catalog
	idm     AccessChecker
	log     *slog.Logger
}

// New создаёт gRPC-реализацию каталога. idm — клиент RBAC IDM (CheckAccess).
func New(catalog Catalog, idm AccessChecker, log *slog.Logger) *Server {
	return &Server{catalog: catalog, idm: idm, log: log}
}

// GetService возвращает запись каталога. Отсутствие → codes.NotFound.
func (s *Server) GetService(ctx context.Context, req *projectsv1.GetServiceRequest) (*projectsv1.GetServiceResponse, error) {
	if req.GetProject() == "" || req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "project и name обязательны")
	}
	svc, err := s.catalog.Get(ctx, req.GetProject(), req.GetName())
	if err != nil {
		return nil, s.mapError(ctx, "GetService", err)
	}
	st, err := statusToProto(svc.Status)
	if err != nil {
		return nil, s.mapError(ctx, "GetService", err)
	}
	return &projectsv1.GetServiceResponse{
		Project:          svc.Project,
		Name:             svc.Name,
		Status:           st,
		Owners:           svc.Owners,
		OwnersVersion:    svc.OwnersVersion,
		DecommissionedAt: decommissionedAtToProto(svc.DecommissionedAt),
	}, nil
}

// ListServices возвращает страницу сервисов проекта с keyset-пагинацией.
func (s *Server) ListServices(ctx context.Context, req *projectsv1.ListServicesRequest) (*projectsv1.ListServicesResponse, error) {
	if req.GetProject() == "" {
		return nil, status.Error(codes.InvalidArgument, "project обязателен")
	}
	items, next, err := s.catalog.List(ctx, req.GetProject(), int(req.GetPageSize()), req.GetPageToken())
	if err != nil {
		return nil, s.mapError(ctx, "ListServices", err)
	}
	out := make([]*projectsv1.Service, 0, len(items))
	for _, it := range items {
		p, perr := serviceToProto(it)
		if perr != nil {
			return nil, s.mapError(ctx, "ListServices", perr)
		}
		out = append(out, p)
	}
	return &projectsv1.ListServicesResponse{Services: out, NextPageToken: next}, nil
}

// CreateService фиксирует запись каталога (status=CREATING) и запускает workflow
// провизии. Возвращает идентификатор записи и стартовый статус CREATING.
func (s *Server) CreateService(ctx context.Context, req *projectsv1.CreateServiceRequest) (*projectsv1.CreateServiceResponse, error) {
	if req.GetProject() == "" || req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "project и name обязательны")
	}
	// Владельцы обязательны при создании (ADR-0023): пустой набор или пустой
	// элемент → InvalidArgument до доменной операции (defense-in-depth; основная
	// нормализация/проверка — в usecase).
	if len(req.GetOwners()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "при создании требуется хотя бы один владелец")
	}
	for _, o := range req.GetOwners() {
		if o == "" {
			return nil, status.Error(codes.InvalidArgument, "владелец не может быть пустым")
		}
	}
	// RBAC (defense-in-depth): проверка права create через IDM CheckAccess до
	// любых доменных записей и запуска workflow. authorize возвращает готовый
	// gRPC-статус PermissionDenied — наружу без раскрытия деталей.
	if err := s.authorize(ctx, req.GetProject(), "create"); err != nil {
		return nil, err
	}
	svc, err := s.catalog.CreateService(ctx, req.GetProject(), req.GetName(), req.GetOwners())
	if err != nil {
		return nil, s.mapError(ctx, "CreateService", err)
	}
	st, err := statusToProto(svc.Status)
	if err != nil {
		return nil, s.mapError(ctx, "CreateService", err)
	}
	return &projectsv1.CreateServiceResponse{Id: svc.ID.String(), Status: st}, nil
}

// SetServiceOwners декларативно меняет владельцев сервиса: валидирует запрос,
// проверяет право change_owners (fail-closed) и запускает workflow «Изменение
// владельцев». Конфликт версии → FailedPrecondition; отсутствие записи →
// NotFound; невалидный запрос → InvalidArgument. Внутренние детали наружу не
// раскрываются.
func (s *Server) SetServiceOwners(ctx context.Context, req *projectsv1.SetServiceOwnersRequest) (*projectsv1.SetServiceOwnersResponse, error) {
	if req.GetProject() == "" || req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "project и name обязательны")
	}
	for _, o := range req.GetOwners() {
		if o == "" {
			return nil, status.Error(codes.InvalidArgument, "владелец не может быть пустым")
		}
	}
	// RBAC (defense-in-depth): право change_owners до доменной операции и запуска
	// workflow.
	if err := s.authorize(ctx, req.GetProject(), "change_owners"); err != nil {
		return nil, err
	}
	owners, version, err := s.catalog.SetServiceOwners(ctx, req.GetProject(), req.GetName(), req.GetOwners(), req.GetExpectedVersion())
	if err != nil {
		return nil, s.mapError(ctx, "SetServiceOwners", err)
	}
	return &projectsv1.SetServiceOwnersResponse{Owners: owners, OwnersVersion: version}, nil
}

// DecommissionService выводит сервис из эксплуатации (soft-delete): валидирует
// запрос, проверяет право decommission (fail-closed) и запускает workflow «Вывод
// из эксплуатации». Идемпотентно: уже выведенный сервис → успех (no-op). Недопустимый
// исходный статус или неснятая нагрузка → FailedPrecondition; конкурентная смена
// статуса → Aborted; отсутствие записи → NotFound. Внутренние детали наружу не
// раскрываются.
func (s *Server) DecommissionService(ctx context.Context, req *projectsv1.DecommissionServiceRequest) (*projectsv1.DecommissionServiceResponse, error) {
	if req.GetProject() == "" || req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "project и name обязательны")
	}
	// RBAC (defense-in-depth): право decommission до доменной операции и запуска
	// workflow.
	if err := s.authorize(ctx, req.GetProject(), "decommission"); err != nil {
		return nil, err
	}
	svc, err := s.catalog.DecommissionService(ctx, req.GetProject(), req.GetName(), req.GetLoadDrained())
	if err != nil {
		return nil, s.mapError(ctx, "DecommissionService", err)
	}
	p, perr := serviceToProto(svc)
	if perr != nil {
		return nil, s.mapError(ctx, "DecommissionService", perr)
	}
	return &projectsv1.DecommissionServiceResponse{Service: p}, nil
}

// TransferService переносит сервис в другой проект (смена project-владельца):
// валидирует запрос, проверяет ДВА права (transfer на исходном проекте И
// transfer_in на целевом, fail-closed) и запускает workflow «Перенос». Идемпотентно:
// уже перенесённый сервис → успех (no-op). Недопустимый исходный статус →
// FailedPrecondition; занятое имя в target/конкурентная смена → Aborted; отсутствие
// записи → NotFound. Внутренние детали наружу не раскрываются.
func (s *Server) TransferService(ctx context.Context, req *projectsv1.TransferServiceRequest) (*projectsv1.TransferServiceResponse, error) {
	if req.GetProject() == "" || req.GetName() == "" || req.GetTargetProject() == "" {
		return nil, status.Error(codes.InvalidArgument, "project, name и target_project обязательны")
	}
	if req.GetProject() == req.GetTargetProject() {
		return nil, status.Error(codes.InvalidArgument, "target_project должен отличаться от project")
	}
	// RBAC (defense-in-depth, fail-closed): перенос затрагивает ДВА проекта —
	// требуется право transfer на исходном И transfer_in на целевом. Без права на
	// target нельзя «вынести» сервис в чужой проект (ADR-0013).
	if err := s.authorize(ctx, req.GetProject(), "transfer"); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, req.GetTargetProject(), "transfer_in"); err != nil {
		return nil, err
	}
	svc, err := s.catalog.TransferService(ctx, req.GetProject(), req.GetName(), req.GetTargetProject())
	if err != nil {
		return nil, s.mapError(ctx, "TransferService", err)
	}
	p, perr := serviceToProto(svc)
	if perr != nil {
		return nil, s.mapError(ctx, "TransferService", perr)
	}
	return &projectsv1.TransferServiceResponse{Service: p}, nil
}

// authorize проверяет право субъекта на действие над проектом через IDM
// CheckAccess. subject берётся из claims (auth.ClaimsFromContext). Отказ ИЛИ
// недоступность/ошибка IDM → PermissionDenied (fail-closed); внутренние детали
// наружу не раскрываются (только лог по ключу slog "err").
func (s *Server) authorize(ctx context.Context, project, action string) error {
	var subject string
	if claims, ok := auth.ClaimsFromContext(ctx); ok {
		subject = claims.Subject
	}
	resp, err := s.idm.CheckAccess(ctx, &idmv1.CheckAccessRequest{
		Subject:  subject,
		Resource: "project:" + project,
		Action:   action,
	})
	if err != nil {
		s.log.Warn("projects: RBAC недоступен/ошибка (fail-closed)", logger.Err(err))
		return status.Error(codes.PermissionDenied, "доступ запрещён")
	}
	if !resp.GetAllowed() {
		return status.Error(codes.PermissionDenied, "доступ запрещён")
	}
	return nil
}

// mapError переводит доменную ошибку в gRPC-статус. Детали внутренних ошибок не
// раскрываются клиенту, но логируются с единым ключом "err".
func (s *Server) mapError(_ context.Context, method string, err error) error {
	switch {
	case errors.Is(err, errs.ErrNotFound):
		return status.Error(codes.NotFound, "сервис не найден")
	case errors.Is(err, errs.ErrConflict):
		// Конкурентный конфликт guarded-CAS (gRPC-каноничный Aborted → HTTP 409,
		// ADR-0012). Сюда же относится конфликт версии владельцев.
		return status.Error(codes.Aborted, "конфликт состояния")
	case errors.Is(err, errs.ErrPrecondition):
		// Семантическое предусловие не выполнено (FailedPrecondition → HTTP 422).
		return status.Error(codes.FailedPrecondition, "предусловие не выполнено")
	case errors.Is(err, errs.ErrValidation):
		return status.Error(codes.InvalidArgument, "некорректный запрос")
	default:
		s.log.Error("projects: внутренняя ошибка", slog.String("method", method), logger.Err(err))
		return status.Error(codes.Internal, "внутренняя ошибка")
	}
}
