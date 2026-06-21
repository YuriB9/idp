// Файл iam_catalog_server.go — gRPC-сервер IamCatalogService: структурные мутации
// каталога RBAC для динамической IAM-админки (ADR-0015). Создание/удаление ролей и
// прав, правка набора прав роли (attach/detach). Любая мутация идёт через
// usecase.CatalogManager, который после успешной записи ШИРОКО инвалидирует кэш
// решений (поколение). Внутренние ошибки клиенту не раскрываются (деталь — в лог по
// ключу slog "err"); недоступность БД/кэша → Unavailable (fail-closed). Системные
// (сидированные) роли/права защищены от удаления и правки набора прав
// (FailedPrecondition).
package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/pkg/logger"
	"github.com/YuriB9/idp/services/idm/internal/repository"
)

// catalogManager — зависимость транспорта от usecase-слоя структурных мутаций
// каталога (с обязательной широкой инвалидацией кэша после записи).
type catalogManager interface {
	CreateRole(ctx context.Context, name string) (repository.Role, error)
	DeleteRole(ctx context.Context, name string) error
	CreatePermission(ctx context.Context, action, resource string) (repository.Permission, error)
	DeletePermission(ctx context.Context, action, resource string) error
	AttachPermission(ctx context.Context, role, action, resource string) ([]repository.Permission, error)
	DetachPermission(ctx context.Context, role, action, resource string) ([]repository.Permission, error)
}

// iamCatalogServer реализует gRPC IamCatalogService поверх usecase-слоя.
// Транспортная ответственность: валидация запроса, маппинг доменных типов в proto
// и доменных ошибок в gRPC-коды без утечки деталей.
type iamCatalogServer struct {
	idmv1.UnimplementedIamCatalogServiceServer
	catalog catalogManager
	log     *slog.Logger
}

// CreateRole создаёт пользовательскую роль. Пустое/битое имя → InvalidArgument;
// дубль → AlreadyExists.
func (s *iamCatalogServer) CreateRole(ctx context.Context, req *idmv1.CreateRoleRequest) (*idmv1.CreateRoleResponse, error) {
	if err := validateToken("name", req.GetName()); err != nil {
		return nil, err
	}
	role, err := s.catalog.CreateRole(ctx, req.GetName())
	if err != nil {
		return nil, s.mapErr("CreateRole", err)
	}
	return &idmv1.CreateRoleResponse{Role: &idmv1.Role{Name: role.Name, System: role.System}}, nil
}

// DeleteRole удаляет роль. Системная → FailedPrecondition; несуществующая → NotFound.
func (s *iamCatalogServer) DeleteRole(ctx context.Context, req *idmv1.DeleteRoleRequest) (*idmv1.DeleteRoleResponse, error) {
	if err := validateToken("name", req.GetName()); err != nil {
		return nil, err
	}
	if err := s.catalog.DeleteRole(ctx, req.GetName()); err != nil {
		return nil, s.mapErr("DeleteRole", err)
	}
	return &idmv1.DeleteRoleResponse{}, nil
}

// CreatePermission создаёт пользовательское право. Пустые/битые поля →
// InvalidArgument; дубль пары → AlreadyExists.
func (s *iamCatalogServer) CreatePermission(ctx context.Context, req *idmv1.CreatePermissionRequest) (*idmv1.CreatePermissionResponse, error) {
	if err := validatePair(req.GetAction(), req.GetResource()); err != nil {
		return nil, err
	}
	perm, err := s.catalog.CreatePermission(ctx, req.GetAction(), req.GetResource())
	if err != nil {
		return nil, s.mapErr("CreatePermission", err)
	}
	return &idmv1.CreatePermissionResponse{
		Permission: &idmv1.Permission{Action: perm.Action, Resource: perm.Resource, System: perm.System},
	}, nil
}

// DeletePermission удаляет право. Системное → FailedPrecondition; несуществующее →
// NotFound.
func (s *iamCatalogServer) DeletePermission(ctx context.Context, req *idmv1.DeletePermissionRequest) (*idmv1.DeletePermissionResponse, error) {
	if err := validatePair(req.GetAction(), req.GetResource()); err != nil {
		return nil, err
	}
	if err := s.catalog.DeletePermission(ctx, req.GetAction(), req.GetResource()); err != nil {
		return nil, s.mapErr("DeletePermission", err)
	}
	return &idmv1.DeletePermissionResponse{}, nil
}

// AttachPermission прикрепляет право к роли (идемпотентно). Роль/право не найдены →
// NotFound; системная роль → FailedPrecondition.
func (s *iamCatalogServer) AttachPermission(ctx context.Context, req *idmv1.AttachPermissionRequest) (*idmv1.AttachPermissionResponse, error) {
	if err := validateToken("role", req.GetRole()); err != nil {
		return nil, err
	}
	if err := validatePair(req.GetAction(), req.GetResource()); err != nil {
		return nil, err
	}
	perms, err := s.catalog.AttachPermission(ctx, req.GetRole(), req.GetAction(), req.GetResource())
	if err != nil {
		return nil, s.mapErr("AttachPermission", err)
	}
	return &idmv1.AttachPermissionResponse{
		RolePermissions: &idmv1.RolePermissions{Role: req.GetRole(), Permissions: toProtoPermissions(perms)},
	}, nil
}

// DetachPermission открепляет право от роли (идемпотентно). Роль не найдена →
// NotFound; системная роль → FailedPrecondition.
func (s *iamCatalogServer) DetachPermission(ctx context.Context, req *idmv1.DetachPermissionRequest) (*idmv1.DetachPermissionResponse, error) {
	if err := validateToken("role", req.GetRole()); err != nil {
		return nil, err
	}
	if err := validatePair(req.GetAction(), req.GetResource()); err != nil {
		return nil, err
	}
	perms, err := s.catalog.DetachPermission(ctx, req.GetRole(), req.GetAction(), req.GetResource())
	if err != nil {
		return nil, s.mapErr("DetachPermission", err)
	}
	return &idmv1.DetachPermissionResponse{
		RolePermissions: &idmv1.RolePermissions{Role: req.GetRole(), Permissions: toProtoPermissions(perms)},
	}, nil
}

// validateToken проверяет одиночный строковый аргумент: непустой, валидный UTF-8,
// без NUL. Иначе → InvalidArgument (битое значение не должно давать 500 на уровне БД).
func validateToken(field, value string) error {
	if value == "" {
		return status.Errorf(codes.InvalidArgument, "%s обязателен", field)
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return status.Errorf(codes.InvalidArgument, "%s содержит недопустимые символы", field)
	}
	return nil
}

// validatePair проверяет пару (action, resource) права.
func validatePair(action, resource string) error {
	if err := validateToken("action", action); err != nil {
		return err
	}
	return validateToken("resource", resource)
}

// mapErr маппит доменную ошибку мутации каталога в gRPC-код без утечки деталей
// (reuse конвенции ADR-0012/0013): ErrConflict → AlreadyExists; ErrPrecondition →
// FailedPrecondition; ErrNotFound → NotFound; ErrValidation → InvalidArgument;
// прочее (недоступность БД/кэша) → Unavailable (fail-closed, деталь — в лог).
func (s *iamCatalogServer) mapErr(method string, err error) error {
	switch {
	case errors.Is(err, errs.ErrConflict):
		return status.Error(codes.AlreadyExists, "ресурс уже существует")
	case errors.Is(err, errs.ErrPrecondition):
		return status.Error(codes.FailedPrecondition, "ресурс системный и защищён от изменения")
	case errors.Is(err, errs.ErrNotFound):
		return status.Error(codes.NotFound, "ресурс не найден")
	case errors.Is(err, errs.ErrValidation):
		return status.Error(codes.InvalidArgument, "некорректный аргумент")
	default:
		s.log.Error("idm: ошибка мутации каталога IAM", slog.String("method", method), logger.Err(err))
		return status.Error(codes.Unavailable, "каталог IAM временно недоступен")
	}
}
