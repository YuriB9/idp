// Файл identity_server.go — gRPC-сервер IdentityService: справочник субъектов из
// каталога идентичностей Keycloak (ADR-0016). Только чтение (поиск/резолв) через
// usecase-фасад поверх клиента Keycloak Admin REST и отдельного кэша
// идентичностей. Внутренние ошибки и секрет сервис-аккаунта клиенту не
// раскрываются (деталь — в лог по ключу slog "err"); недоступность Keycloak →
// Unavailable (деградация: справочник не критичен для CheckAccess). Кэш
// идентичностей не затрагивает decision-cache RBAC.
package main

import (
	"context"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	"github.com/YuriB9/idp/pkg/logger"
	"github.com/YuriB9/idp/services/idm/internal/identity"
)

// directory — зависимость транспорта от usecase-фасада справочника.
type directory interface {
	Search(ctx context.Context, query, cursor string, pageSize int) ([]identity.Identity, string, error)
	Resolve(ctx context.Context, subjects []string) ([]identity.Identity, error)
}

// identityServer реализует gRPC IdentityService поверх фасада справочника.
type identityServer struct {
	idmv1.UnimplementedIdentityServiceServer
	dir directory
	log *slog.Logger
}

// SearchSubjects ищет пользователей каталога. Пустой/слишком короткий query или
// битый курсор → InvalidArgument; недоступность Keycloak → Unavailable.
func (s *identityServer) SearchSubjects(ctx context.Context, req *idmv1.SearchSubjectsRequest) (*idmv1.SearchSubjectsResponse, error) {
	ids, next, err := s.dir.Search(ctx, req.GetQuery(), req.GetCursor(), int(req.GetPageSize()))
	if err != nil {
		return nil, s.mapErr("SearchSubjects", err)
	}
	return &idmv1.SearchSubjectsResponse{Subjects: toProtoIdentities(ids), NextCursor: next}, nil
}

// ResolveSubjects резолвит набор канонических ключей (sub) в идентичности.
// Пустой список → InvalidArgument; недоступность Keycloak → Unavailable;
// «осиротевшие» субъекты возвращаются с found=false.
func (s *identityServer) ResolveSubjects(ctx context.Context, req *idmv1.ResolveSubjectsRequest) (*idmv1.ResolveSubjectsResponse, error) {
	ids, err := s.dir.Resolve(ctx, req.GetSubjects())
	if err != nil {
		return nil, s.mapErr("ResolveSubjects", err)
	}
	return &idmv1.ResolveSubjectsResponse{Subjects: toProtoIdentities(ids)}, nil
}

// toProtoIdentities маппит доменные идентичности в proto-представление.
func toProtoIdentities(ids []identity.Identity) []*idmv1.SubjectIdentity {
	out := make([]*idmv1.SubjectIdentity, 0, len(ids))
	for _, id := range ids {
		out = append(out, &idmv1.SubjectIdentity{
			Subject:     id.Subject,
			Username:    id.Username,
			Email:       id.Email,
			DisplayName: id.DisplayName,
			Enabled:     id.Enabled,
			Found:       id.Found,
		})
	}
	return out
}

// mapErr маппит ошибку справочника в gRPC-код без утечки деталей/секрета.
// ErrValidation → InvalidArgument; недоступность каталога → Unavailable (на
// периметре станет 503); деталь — в лог по ключу slog "err".
func (s *identityServer) mapErr(method string, err error) error {
	switch {
	case identity.IsValidation(err):
		return status.Error(codes.InvalidArgument, "некорректный аргумент")
	case identity.IsUnavailable(err):
		s.log.Error("idm: каталог идентичностей недоступен", slog.String("method", method), logger.Err(err))
		return status.Error(codes.Unavailable, "каталог идентичностей временно недоступен")
	default:
		s.log.Error("idm: ошибка справочника субъектов", slog.String("method", method), logger.Err(err))
		return status.Error(codes.Unavailable, "каталог идентичностей временно недоступен")
	}
}
