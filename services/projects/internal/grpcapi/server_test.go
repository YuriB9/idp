package grpcapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	projectsv1 "github.com/YuriB9/idp/pkg/api/projects/v1"
	"github.com/YuriB9/idp/pkg/errs"
	"github.com/YuriB9/idp/services/projects/internal/repository"
)

// fakeCatalog — управляемый стаб usecase-слоя для транспортных тестов.
type fakeCatalog struct {
	svc       repository.Service
	getErr    error
	createErr error
	listErr   error
	listItem  []repository.Service
	listNext  string

	createCalled bool

	// поля для SetServiceOwners
	ownersErr    error
	ownersOut    []string
	ownersVerOut int64
	ownersCalled bool

	// поля для DecommissionService
	decommErr    error
	decommOut    repository.Service
	decommCalled bool

	// поля для TransferService
	transferErr    error
	transferOut    repository.Service
	transferCalled bool
}

func (f *fakeCatalog) Get(context.Context, string, string) (repository.Service, error) {
	return f.svc, f.getErr
}

func (f *fakeCatalog) List(context.Context, string, int, string) ([]repository.Service, string, error) {
	return f.listItem, f.listNext, f.listErr
}

func (f *fakeCatalog) CreateService(context.Context, string, string) (repository.Service, error) {
	f.createCalled = true
	return f.svc, f.createErr
}

func (f *fakeCatalog) SetServiceOwners(_ context.Context, _, _ string, _ []string, _ int64) ([]string, int64, error) {
	f.ownersCalled = true
	return f.ownersOut, f.ownersVerOut, f.ownersErr
}

func (f *fakeCatalog) DecommissionService(_ context.Context, _, _ string, _ bool) (repository.Service, error) {
	f.decommCalled = true
	return f.decommOut, f.decommErr
}

func (f *fakeCatalog) TransferService(_ context.Context, _, _, _ string) (repository.Service, error) {
	f.transferCalled = true
	return f.transferOut, f.transferErr
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stubIDM — стаб AccessChecker (IDM): заранее заданное решение или ошибка.
type stubIDM struct {
	allowed bool
	err     error
}

func (s stubIDM) CheckAccess(_ context.Context, _ *idmv1.CheckAccessRequest, _ ...grpc.CallOption) (*idmv1.CheckAccessResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &idmv1.CheckAccessResponse{Allowed: s.allowed}, nil
}

// allowIDM — стаб IDM, всегда разрешающий (для тестов, не проверяющих RBAC).
func allowIDM() stubIDM { return stubIDM{allowed: true} }

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

			srv := New(tc.fake, allowIDM(), discardLogger())
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

// TestCreateServiceMapping покрывает CreateService: успех возвращает id+CREATING,
// валидация и доменные ошибки маппятся в gRPC-коды без утечки деталей.
func TestCreateServiceMapping(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	tests := []struct {
		name     string
		req      *projectsv1.CreateServiceRequest
		fake     *fakeCatalog
		wantCode codes.Code
	}{
		{
			name:     "успех → CREATING",
			req:      &projectsv1.CreateServiceRequest{Project: "p", Name: "n"},
			fake:     &fakeCatalog{svc: repository.Service{ID: id, Project: "p", Name: "n", Status: repository.StatusCreating}},
			wantCode: codes.OK,
		},
		{
			name:     "пустой name → InvalidArgument",
			req:      &projectsv1.CreateServiceRequest{Project: "p"},
			fake:     &fakeCatalog{},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "конфликт → Aborted",
			req:      &projectsv1.CreateServiceRequest{Project: "p", Name: "n"},
			fake:     &fakeCatalog{createErr: fmt.Errorf("обёртка: %w", errs.ErrConflict)},
			wantCode: codes.Aborted,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := New(tc.fake, allowIDM(), discardLogger())
			resp, err := srv.CreateService(context.Background(), tc.req)
			if tc.wantCode == codes.OK {
				if err != nil {
					t.Fatalf("неожиданная ошибка: %v", err)
				}
				if resp.GetStatus() != projectsv1.ServiceStatus_SERVICE_STATUS_CREATING {
					t.Fatalf("статус = %v, ожидали CREATING", resp.GetStatus())
				}
				if resp.GetId() != id.String() {
					t.Fatalf("id = %q, ожидали %q", resp.GetId(), id.String())
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
		})
	}
}

// TestCreateServiceRBAC проверяет defense-in-depth: отказ RBAC и недоступность
// IDM маппятся в PermissionDenied, доменная запись каталога не выполняется,
// внутренние детали наружу не раскрываются.
func TestCreateServiceRBAC(t *testing.T) {
	t.Parallel()

	const secret = "внутренняя деталь: idm down"
	tests := []struct {
		name string
		idm  stubIDM
	}{
		{name: "отказ RBAC → PermissionDenied", idm: stubIDM{allowed: false}},
		{name: "IDM недоступен → PermissionDenied (fail-closed)", idm: stubIDM{err: status.Error(codes.Unavailable, secret)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeCatalog{svc: repository.Service{ID: uuid.New(), Project: "p", Name: "n", Status: repository.StatusCreating}}
			srv := New(fake, tc.idm, discardLogger())

			_, err := srv.CreateService(context.Background(), &projectsv1.CreateServiceRequest{Project: "p", Name: "n"})
			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.PermissionDenied {
				t.Fatalf("ожидали PermissionDenied, получили %v", err)
			}
			if fake.createCalled {
				t.Fatal("при отказе RBAC запись каталога не должна создаваться")
			}
			if strings.Contains(st.Message(), secret) {
				t.Fatalf("сообщение клиенту утекло: %q", st.Message())
			}
		})
	}
}

// TestGetServiceOwnersInResponse проверяет, что owners и owners_version
// отражаются в ответе GetService.
func TestGetServiceOwnersInResponse(t *testing.T) {
	t.Parallel()

	fake := &fakeCatalog{svc: repository.Service{
		Project: "p", Name: "n", Status: repository.StatusActive,
		Owners: []string{"alice", "bob"}, OwnersVersion: 7,
	}}
	srv := New(fake, allowIDM(), discardLogger())
	resp, err := srv.GetService(context.Background(), &projectsv1.GetServiceRequest{Project: "p", Name: "n"})
	if err != nil {
		t.Fatalf("GetService: %v", err)
	}
	if resp.GetOwnersVersion() != 7 || len(resp.GetOwners()) != 2 || resp.GetOwners()[0] != "alice" {
		t.Fatalf("owners в ответе = %v v%d", resp.GetOwners(), resp.GetOwnersVersion())
	}
}

// TestSetServiceOwnersMapping покрывает SetServiceOwners: успех возвращает
// owners+version; валидация и доменные ошибки (конфликт версии, NotFound)
// маппятся в gRPC-коды.
func TestSetServiceOwnersMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		req      *projectsv1.SetServiceOwnersRequest
		fake     *fakeCatalog
		wantCode codes.Code
	}{
		{
			name:     "успех",
			req:      &projectsv1.SetServiceOwnersRequest{Project: "p", Name: "n", Owners: []string{"alice"}, ExpectedVersion: 1},
			fake:     &fakeCatalog{ownersOut: []string{"alice"}, ownersVerOut: 2},
			wantCode: codes.OK,
		},
		{
			name:     "пустой name → InvalidArgument",
			req:      &projectsv1.SetServiceOwnersRequest{Project: "p"},
			fake:     &fakeCatalog{},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "пустой владелец → InvalidArgument",
			req:      &projectsv1.SetServiceOwnersRequest{Project: "p", Name: "n", Owners: []string{""}},
			fake:     &fakeCatalog{},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "конфликт версии → Aborted",
			req:      &projectsv1.SetServiceOwnersRequest{Project: "p", Name: "n", Owners: []string{"a"}, ExpectedVersion: 1},
			fake:     &fakeCatalog{ownersErr: fmt.Errorf("обёртка: %w", errs.ErrConflict)},
			wantCode: codes.Aborted,
		},
		{
			name:     "не найдено → NotFound",
			req:      &projectsv1.SetServiceOwnersRequest{Project: "p", Name: "n", Owners: []string{"a"}, ExpectedVersion: 0},
			fake:     &fakeCatalog{ownersErr: fmt.Errorf("обёртка: %w", errs.ErrNotFound)},
			wantCode: codes.NotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := New(tc.fake, allowIDM(), discardLogger())
			resp, err := srv.SetServiceOwners(context.Background(), tc.req)
			if tc.wantCode == codes.OK {
				if err != nil {
					t.Fatalf("неожиданная ошибка: %v", err)
				}
				if resp.GetOwnersVersion() != 2 || len(resp.GetOwners()) != 1 {
					t.Fatalf("ответ owners=%v v%d", resp.GetOwners(), resp.GetOwnersVersion())
				}
				return
			}
			st, ok := status.FromError(err)
			if !ok || st.Code() != tc.wantCode {
				t.Fatalf("код = %v, ожидали %v", st.Code(), tc.wantCode)
			}
		})
	}
}

// TestSetServiceOwnersRBAC: отказ RBAC и недоступность IDM → PermissionDenied,
// доменная операция не выполняется.
func TestSetServiceOwnersRBAC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		idm  stubIDM
	}{
		{name: "отказ RBAC", idm: stubIDM{allowed: false}},
		{name: "IDM недоступен (fail-closed)", idm: stubIDM{err: status.Error(codes.Unavailable, "down")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeCatalog{ownersOut: []string{"a"}, ownersVerOut: 1}
			srv := New(fake, tc.idm, discardLogger())
			_, err := srv.SetServiceOwners(context.Background(), &projectsv1.SetServiceOwnersRequest{Project: "p", Name: "n", Owners: []string{"a"}, ExpectedVersion: 0})
			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.PermissionDenied {
				t.Fatalf("ожидали PermissionDenied, получили %v", err)
			}
			if fake.ownersCalled {
				t.Fatal("при отказе RBAC доменная операция не должна выполняться")
			}
		})
	}
}

// TestDecommissionServiceMapping покрывает DecommissionService: успех возвращает
// итоговое состояние; предусловие → FailedPrecondition, конфликт → Aborted,
// NotFound → NotFound, валидация → InvalidArgument.
func TestDecommissionServiceMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		req      *projectsv1.DecommissionServiceRequest
		fake     *fakeCatalog
		wantCode codes.Code
	}{
		{
			name:     "успех → DECOMMISSIONED",
			req:      &projectsv1.DecommissionServiceRequest{Project: "p", Name: "n", LoadDrained: true},
			fake:     &fakeCatalog{decommOut: repository.Service{Project: "p", Name: "n", Status: repository.StatusDecommissioned}},
			wantCode: codes.OK,
		},
		{
			name:     "пустой name → InvalidArgument",
			req:      &projectsv1.DecommissionServiceRequest{Project: "p"},
			fake:     &fakeCatalog{},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "предусловие → FailedPrecondition",
			req:      &projectsv1.DecommissionServiceRequest{Project: "p", Name: "n", LoadDrained: false},
			fake:     &fakeCatalog{decommErr: fmt.Errorf("обёртка: %w", errs.ErrPrecondition)},
			wantCode: codes.FailedPrecondition,
		},
		{
			name:     "конфликт → Aborted",
			req:      &projectsv1.DecommissionServiceRequest{Project: "p", Name: "n", LoadDrained: true},
			fake:     &fakeCatalog{decommErr: fmt.Errorf("обёртка: %w", errs.ErrConflict)},
			wantCode: codes.Aborted,
		},
		{
			name:     "не найдено → NotFound",
			req:      &projectsv1.DecommissionServiceRequest{Project: "p", Name: "n", LoadDrained: true},
			fake:     &fakeCatalog{decommErr: fmt.Errorf("обёртка: %w", errs.ErrNotFound)},
			wantCode: codes.NotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := New(tc.fake, allowIDM(), discardLogger())
			resp, err := srv.DecommissionService(context.Background(), tc.req)
			if tc.wantCode == codes.OK {
				if err != nil {
					t.Fatalf("неожиданная ошибка: %v", err)
				}
				if resp.GetService().GetStatus() != projectsv1.ServiceStatus_SERVICE_STATUS_DECOMMISSIONED {
					t.Fatalf("статус = %v, ожидали DECOMMISSIONED", resp.GetService().GetStatus())
				}
				return
			}
			st, ok := status.FromError(err)
			if !ok || st.Code() != tc.wantCode {
				t.Fatalf("код = %v, ожидали %v", st.Code(), tc.wantCode)
			}
		})
	}
}

// TestDecommissionServiceRBAC: отказ RBAC и недоступность IDM → PermissionDenied,
// доменная операция не выполняется.
func TestDecommissionServiceRBAC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		idm  stubIDM
	}{
		{name: "отказ RBAC", idm: stubIDM{allowed: false}},
		{name: "IDM недоступен (fail-closed)", idm: stubIDM{err: status.Error(codes.Unavailable, "down")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeCatalog{decommOut: repository.Service{Project: "p", Name: "n", Status: repository.StatusDecommissioned}}
			srv := New(fake, tc.idm, discardLogger())
			_, err := srv.DecommissionService(context.Background(), &projectsv1.DecommissionServiceRequest{Project: "p", Name: "n", LoadDrained: true})
			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.PermissionDenied {
				t.Fatalf("ожидали PermissionDenied, получили %v", err)
			}
			if fake.decommCalled {
				t.Fatal("при отказе RBAC доменная операция не должна выполняться")
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
		{name: "transferring", in: repository.StatusTransferring, want: projectsv1.ServiceStatus_SERVICE_STATUS_TRANSFERRING},
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

// actionIDM — стаб IDM, отказывающий по конкретному действию (для проверки
// двусторонней авторизации переноса: deny на source vs deny на target).
type actionIDM struct {
	denyAction string
}

func (s actionIDM) CheckAccess(_ context.Context, in *idmv1.CheckAccessRequest, _ ...grpc.CallOption) (*idmv1.CheckAccessResponse, error) {
	return &idmv1.CheckAccessResponse{Allowed: in.GetAction() != s.denyAction}, nil
}

// TestTransferServiceMapping покрывает TransferService: успех возвращает итоговое
// состояние; валидация (пустой target / target==project) → InvalidArgument;
// предусловие → FailedPrecondition; занятое имя/конфликт → Aborted; NotFound.
func TestTransferServiceMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		req      *projectsv1.TransferServiceRequest
		fake     *fakeCatalog
		wantCode codes.Code
	}{
		{
			name:     "успех → итоговое состояние",
			req:      &projectsv1.TransferServiceRequest{Project: "demo", Name: "n", TargetProject: "demo2"},
			fake:     &fakeCatalog{transferOut: repository.Service{Project: "demo", Name: "n", Status: repository.StatusActive}},
			wantCode: codes.OK,
		},
		{
			name:     "пустой target → InvalidArgument",
			req:      &projectsv1.TransferServiceRequest{Project: "demo", Name: "n"},
			fake:     &fakeCatalog{},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "target == project → InvalidArgument",
			req:      &projectsv1.TransferServiceRequest{Project: "demo", Name: "n", TargetProject: "demo"},
			fake:     &fakeCatalog{},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "недопустимый статус → FailedPrecondition",
			req:      &projectsv1.TransferServiceRequest{Project: "demo", Name: "n", TargetProject: "demo2"},
			fake:     &fakeCatalog{transferErr: fmt.Errorf("обёртка: %w", errs.ErrPrecondition)},
			wantCode: codes.FailedPrecondition,
		},
		{
			name:     "занятое имя/конфликт → Aborted",
			req:      &projectsv1.TransferServiceRequest{Project: "demo", Name: "n", TargetProject: "demo2"},
			fake:     &fakeCatalog{transferErr: fmt.Errorf("обёртка: %w", errs.ErrConflict)},
			wantCode: codes.Aborted,
		},
		{
			name:     "не найдено → NotFound",
			req:      &projectsv1.TransferServiceRequest{Project: "demo", Name: "n", TargetProject: "demo2"},
			fake:     &fakeCatalog{transferErr: fmt.Errorf("обёртка: %w", errs.ErrNotFound)},
			wantCode: codes.NotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := New(tc.fake, allowIDM(), discardLogger())
			resp, err := srv.TransferService(context.Background(), tc.req)
			if tc.wantCode == codes.OK {
				if err != nil {
					t.Fatalf("неожиданная ошибка: %v", err)
				}
				if resp.GetService().GetName() != "n" {
					t.Fatalf("ответ = %+v", resp.GetService())
				}
				return
			}
			st, ok := status.FromError(err)
			if !ok || st.Code() != tc.wantCode {
				t.Fatalf("код = %v, ожидали %v", st.Code(), tc.wantCode)
			}
		})
	}
}

// TestTransferServiceRBAC: отказ по ЛЮБОМУ из двух прав (transfer на source ИЛИ
// transfer_in на target) и недоступность IDM → PermissionDenied, доменная
// операция не выполняется (fail-closed, ADR-0013).
func TestTransferServiceRBAC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		idm  AccessChecker
	}{
		{name: "нет права transfer на source", idm: actionIDM{denyAction: "transfer"}},
		{name: "нет права transfer_in на target", idm: actionIDM{denyAction: "transfer_in"}},
		{name: "IDM недоступен (fail-closed)", idm: stubIDM{err: status.Error(codes.Unavailable, "down")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeCatalog{transferOut: repository.Service{Project: "demo", Name: "n", Status: repository.StatusActive}}
			srv := New(fake, tc.idm, discardLogger())
			_, err := srv.TransferService(context.Background(), &projectsv1.TransferServiceRequest{Project: "demo", Name: "n", TargetProject: "demo2"})
			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.PermissionDenied {
				t.Fatalf("ожидали PermissionDenied, получили %v", err)
			}
			if fake.transferCalled {
				t.Fatal("при отказе RBAC доменная операция не должна выполняться")
			}
		})
	}
}
