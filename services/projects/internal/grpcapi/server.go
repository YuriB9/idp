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
	CreateService(ctx context.Context, project, name string) (repository.Service, error)
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
		Project: svc.Project,
		Name:    svc.Name,
		Status:  st,
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
	// RBAC (defense-in-depth): проверка права create через IDM CheckAccess до
	// любых доменных записей и запуска workflow. authorize возвращает готовый
	// gRPC-статус PermissionDenied — наружу без раскрытия деталей.
	if err := s.authorize(ctx, req.GetProject()); err != nil {
		return nil, err
	}
	svc, err := s.catalog.CreateService(ctx, req.GetProject(), req.GetName())
	if err != nil {
		return nil, s.mapError(ctx, "CreateService", err)
	}
	st, err := statusToProto(svc.Status)
	if err != nil {
		return nil, s.mapError(ctx, "CreateService", err)
	}
	return &projectsv1.CreateServiceResponse{Id: svc.ID.String(), Status: st}, nil
}

// authorize проверяет право субъекта на создание сервиса в проекте через IDM
// CheckAccess. subject берётся из claims (auth.ClaimsFromContext). Отказ ИЛИ
// недоступность/ошибка IDM → PermissionDenied (fail-closed); внутренние детали
// наружу не раскрываются (только лог по ключу slog "err").
func (s *Server) authorize(ctx context.Context, project string) error {
	var subject string
	if claims, ok := auth.ClaimsFromContext(ctx); ok {
		subject = claims.Subject
	}
	resp, err := s.idm.CheckAccess(ctx, &idmv1.CheckAccessRequest{
		Subject:  subject,
		Resource: "project:" + project,
		Action:   "create",
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
		return status.Error(codes.FailedPrecondition, "конфликт состояния")
	case errors.Is(err, errs.ErrValidation):
		return status.Error(codes.InvalidArgument, "некорректный запрос")
	default:
		s.log.Error("projects: внутренняя ошибка", slog.String("method", method), logger.Err(err))
		return status.Error(codes.Internal, "внутренняя ошибка")
	}
}
