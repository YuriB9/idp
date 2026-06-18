package grpcapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	projectsv1 "github.com/YuriB9/idp/pkg/api/projects/v1"
	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/services/projects/internal/repository"
)

// fakeCatalog — управляемый стаб usecase-слоя для транспортных тестов.
type fakeCatalog struct {
	svc      repository.Service
	getErr   error
	listErr  error
	listItem []repository.Service
	listNext string
}

func (f *fakeCatalog) Get(context.Context, string, string) (repository.Service, error) {
	return f.svc, f.getErr
}

func (f *fakeCatalog) List(context.Context, string, int, string) ([]repository.Service, string, error) {
	return f.listItem, f.listNext, f.listErr
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestGetServiceMapping покрывает маппинг результата/ошибок GetService в gRPC-коды.
func TestGetServiceMapping(t *testing.T) {
	t.Parallel()

	// Секретная деталь внутренней ошибки не должна утечь клиенту.
	const secret = "secret-internal-dsn-detail"

	tests := []struct {
		name     string
		req      *projectsv1.GetServiceRequest
		fake     *fakeCatalog
		wantCode codes.Code
	}{
		{
			name:     "успех",
			req:      &projectsv1.GetServiceRequest{Project: "p", Name: "n"},
			fake:     &fakeCatalog{svc: repository.Service{Project: "p", Name: "n", Status: repository.StatusActive}},
			wantCode: codes.OK,
		},
		{
			name:     "пустой project → InvalidArgument",
			req:      &projectsv1.GetServiceRequest{Name: "n"},
			fake:     &fakeCatalog{},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "не найдено → NotFound",
			req:      &projectsv1.GetServiceRequest{Project: "p", Name: "n"},
			fake:     &fakeCatalog{getErr: fmt.Errorf("обёртка: %w", errs.ErrNotFound)},
			wantCode: codes.NotFound,
		},
		{
			name:     "внутренняя ошибка → Internal без утечки",
			req:      &projectsv1.GetServiceRequest{Project: "p", Name: "n"},
			fake:     &fakeCatalog{getErr: fmt.Errorf("боль с %s", secret)},
			wantCode: codes.Internal,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := New(tc.fake, discardLogger())
			resp, err := srv.GetService(context.Background(), tc.req)

			if tc.wantCode == codes.OK {
				if err != nil {
					t.Fatalf("неожиданная ошибка: %v", err)
				}
				if resp.GetStatus() != projectsv1.ServiceStatus_SERVICE_STATUS_ACTIVE {
					t.Fatalf("статус ответа = %v, ожидали ACTIVE", resp.GetStatus())
				}
				return
			}

			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("ожидали gRPC-статус, получили %v", err)
			}
			if st.Code() != tc.wantCode {
				t.Fatalf("код = %v, ожидали %v", st.Code(), tc.wantCode)
			}
			// Внутренние детали не раскрываются клиенту.
			if strings.Contains(st.Message(), secret) {
				t.Fatalf("сообщение клиенту утекло: %q", st.Message())
			}
		})
	}
}

// TestStatusToProto проверяет строгий маппинг статусов и ошибку на неизвестном.
func TestStatusToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      repository.Status
		want    projectsv1.ServiceStatus
		wantErr bool
	}{
		{name: "creating", in: repository.StatusCreating, want: projectsv1.ServiceStatus_SERVICE_STATUS_CREATING},
		{name: "active", in: repository.StatusActive, want: projectsv1.ServiceStatus_SERVICE_STATUS_ACTIVE},
		{name: "decommissioned", in: repository.StatusDecommissioned, want: projectsv1.ServiceStatus_SERVICE_STATUS_DECOMMISSIONED},
		{name: "failed", in: repository.StatusFailed, want: projectsv1.ServiceStatus_SERVICE_STATUS_FAILED},
		{name: "неизвестный → ошибка", in: repository.Status("bogus"), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := statusToProto(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ожидали ошибку для %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if got != tc.want {
				t.Fatalf("statusToProto(%q) = %v, ожидали %v", tc.in, got, tc.want)
			}
		})
	}
}
