// Файл iam_admin_server.go — gRPC-сервер IamAdminService: читающий каталог RBAC
// для IAM-админки (ADR-0014). Только чтение модели; внутренние ошибки клиенту не
// раскрываются (деталь — в лог по ключу slog "err"), недоступность БД → Unavailable
// (fail-closed). Мутации привязок обслуживает roleAdminServer (переиспользуется).
package main

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/pkg/logger"
	"github.com/YuriB9/idp/services/idm/internal/repository"
)

// catalogReader — read-only зависимость транспорта от каталога RBAC. Чтение без
// побочных эффектов на кэш решений (ADR-0014).
type catalogReader interface {
	ListRoles(ctx context.Context) ([]repository.Role, error)
	ListPermissions(ctx context.Context) ([]repository.Permission, error)
	GetRolePermissions(ctx context.Context, role string) ([]repository.Permission, error)
	ListSubjectsWithRoles(ctx context.Context, pageSize int, pageToken string) ([]repository.SubjectRoles, string, error)
	GetSubjectRoles(ctx context.Context, subject string) ([]string, error)
}

// iamAdminServer реализует gRPC IamAdminService поверх каталога. Транспортная
// ответственность: валидация запроса, маппинг доменных типов в proto и ошибок в
// gRPC-коды без утечки деталей.
type iamAdminServer struct {
	idmv1.UnimplementedIamAdminServiceServer
	catalog catalogReader
	log     *slog.Logger
}

// ListRoles возвращает все роли каталога.
func (s *iamAdminServer) ListRoles(ctx context.Context, _ *idmv1.ListRolesRequest) (*idmv1.ListRolesResponse, error) {
	roles, err := s.catalog.ListRoles(ctx)
	if err != nil {
		return nil, s.mapErr("ListRoles", err)
	}
	out := make([]*idmv1.Role, 0, len(roles))
	for _, r := range roles {
		out = append(out, &idmv1.Role{Name: r.Name, System: r.System})
	}
	return &idmv1.ListRolesResponse{Roles: out}, nil
}

// ListPermissions возвращает все права каталога.
func (s *iamAdminServer) ListPermissions(ctx context.Context, _ *idmv1.ListPermissionsRequest) (*idmv1.ListPermissionsResponse, error) {
	perms, err := s.catalog.ListPermissions(ctx)
	if err != nil {
		return nil, s.mapErr("ListPermissions", err)
	}
	return &idmv1.ListPermissionsResponse{Permissions: toProtoPermissions(perms)}, nil
}

// GetRolePermissions возвращает права роли. Пустое имя → InvalidArgument;
// несуществующая роль → NotFound.
func (s *iamAdminServer) GetRolePermissions(ctx context.Context, req *idmv1.GetRolePermissionsRequest) (*idmv1.GetRolePermissionsResponse, error) {
	if req.GetRole() == "" {
		return nil, status.Error(codes.InvalidArgument, "role обязателен")
	}
	perms, err := s.catalog.GetRolePermissions(ctx, req.GetRole())
	if err != nil {
		return nil, s.mapErr("GetRolePermissions", err)
	}
	return &idmv1.GetRolePermissionsResponse{Permissions: toProtoPermissions(perms)}, nil
}

// ListSubjectsWithRoles возвращает страницу субъектов с их ролями (keyset).
// Повреждённый курсор → InvalidArgument.
func (s *iamAdminServer) ListSubjectsWithRoles(ctx context.Context, req *idmv1.ListSubjectsWithRolesRequest) (*idmv1.ListSubjectsWithRolesResponse, error) {
	items, next, err := s.catalog.ListSubjectsWithRoles(ctx, int(req.GetPageSize()), req.GetPageToken())
	if err != nil {
		return nil, s.mapErr("ListSubjectsWithRoles", err)
	}
	out := make([]*idmv1.SubjectRoles, 0, len(items))
	for _, it := range items {
		out = append(out, &idmv1.SubjectRoles{Subject: it.Subject, Roles: it.Roles})
	}
	return &idmv1.ListSubjectsWithRolesResponse{Subjects: out, NextPageToken: next}, nil
}

// GetSubjectRoles возвращает роли субъекта (пусто, если ролей нет). Пустой subject
// → InvalidArgument.
func (s *iamAdminServer) GetSubjectRoles(ctx context.Context, req *idmv1.GetSubjectRolesRequest) (*idmv1.GetSubjectRolesResponse, error) {
	if req.GetSubject() == "" {
		return nil, status.Error(codes.InvalidArgument, "subject обязателен")
	}
	roles, err := s.catalog.GetSubjectRoles(ctx, req.GetSubject())
	if err != nil {
		return nil, s.mapErr("GetSubjectRoles", err)
	}
	return &idmv1.GetSubjectRolesResponse{Roles: roles}, nil
}

// toProtoPermissions маппит доменные права в proto-представление.
func toProtoPermissions(perms []repository.Permission) []*idmv1.Permission {
	out := make([]*idmv1.Permission, 0, len(perms))
	for _, p := range perms {
		out = append(out, &idmv1.Permission{Action: p.Action, Resource: p.Resource, System: p.System})
	}
	return out
}

// mapErr маппит доменную ошибку чтения каталога в gRPC-код без утечки деталей.
// ErrNotFound → NotFound; ErrValidation (повреждённый курсор) → InvalidArgument;
// прочее (недоступность БД и т.п.) → Unavailable (fail-closed, деталь — в лог).
func (s *iamAdminServer) mapErr(method string, err error) error {
	switch {
	case errors.Is(err, errs.ErrNotFound):
		return status.Error(codes.NotFound, "роль не найдена")
	case errors.Is(err, errs.ErrValidation):
		return status.Error(codes.InvalidArgument, "некорректный аргумент")
	default:
		s.log.Error("idm: ошибка чтения каталога IAM", slog.String("method", method), logger.Err(err))
		return status.Error(codes.Unavailable, "каталог IAM временно недоступен")
	}
}
