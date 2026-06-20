package grpcapi

import (
	"fmt"
	"time"

	projectsv1 "github.com/YuriB9/idp/pkg/api/projects/v1"
	"github.com/YuriB9/idp/services/projects/internal/repository"
)

// decommissionedAtToProto форматирует момент вывода из эксплуатации в RFC3339;
// для не выведенных сервисов (nil) — пустая строка.
func decommissionedAtToProto(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// statusToProto переводит доменный статус в proto-enum. Неизвестное значение —
// ошибка (не молчаливый UNSPECIFIED), чтобы рассинхрон не утёк наружу.
func statusToProto(s repository.Status) (projectsv1.ServiceStatus, error) {
	switch s {
	case repository.StatusCreating:
		return projectsv1.ServiceStatus_SERVICE_STATUS_CREATING, nil
	case repository.StatusActive:
		return projectsv1.ServiceStatus_SERVICE_STATUS_ACTIVE, nil
	case repository.StatusDecommissioned:
		return projectsv1.ServiceStatus_SERVICE_STATUS_DECOMMISSIONED, nil
	case repository.StatusFailed:
		return projectsv1.ServiceStatus_SERVICE_STATUS_FAILED, nil
	default:
		return projectsv1.ServiceStatus_SERVICE_STATUS_UNSPECIFIED, fmt.Errorf("grpcapi: неизвестный статус %q", s)
	}
}

// serviceToProto переводит доменную запись в proto-сообщение.
func serviceToProto(s repository.Service) (*projectsv1.Service, error) {
	st, err := statusToProto(s.Status)
	if err != nil {
		return nil, err
	}
	return &projectsv1.Service{
		Project:          s.Project,
		Name:             s.Name,
		Status:           st,
		Owners:           s.Owners,
		OwnersVersion:    s.OwnersVersion,
		DecommissionedAt: decommissionedAtToProto(s.DecommissionedAt),
	}, nil
}
