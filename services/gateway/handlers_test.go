// Тесты доменных REST-ручек периметра: маппинг gRPC-кодов в HTTP, форма JSON
// по OpenAPI и неразглашение внутренних ошибок клиенту. Сеть не используется —
// gRPC-клиент projects подменяется стабом.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	projectsv1 "github.com/YuriB9/idp/pkg/api/projects/v1"
)

// stubProjectsClient — стаб gRPC-клиента каталога: возвращает заранее заданные
// ответы/ошибки, не выходя в сеть.
type stubProjectsClient struct {
	getResp    *projectsv1.GetServiceResponse
	listResp   *projectsv1.ListServicesResponse
	createResp *projectsv1.CreateServiceResponse
	err        error

	// gotCreate фиксирует аргументы последнего CreateService для проверок.
	gotCreate *projectsv1.CreateServiceRequest
	gotList   *projectsv1.ListServicesRequest
}

func (s *stubProjectsClient) GetService(_ context.Context, _ *projectsv1.GetServiceRequest, _ ...grpc.CallOption) (*projectsv1.GetServiceResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.getResp, nil
}

func (s *stubProjectsClient) ListServices(_ context.Context, in *projectsv1.ListServicesRequest, _ ...grpc.CallOption) (*projectsv1.ListServicesResponse, error) {
	s.gotList = in
	if s.err != nil {
		return nil, s.err
	}
	return s.listResp, nil
}

func (s *stubProjectsClient) CreateService(_ context.Context, in *projectsv1.CreateServiceRequest, _ ...grpc.CallOption) (*projectsv1.CreateServiceResponse, error) {
	s.gotCreate = in
	if s.err != nil {
		return nil, s.err
	}
	return s.createResp, nil
}

// newTestRouter собирает роутер с доменными ручками поверх переданного стаба.
func newTestRouter(client projectsv1.ProjectsServiceClient) http.Handler {
	api := &servicesAPI{client: client, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := chi.NewRouter()
	api.register(r)
	return r
}

// TestErrorMappingAndNonDisclosure проверяет детерминированный маппинг
// gRPC-кодов в HTTP и что внутренний текст gRPC-ошибки не уходит клиенту.
func TestErrorMappingAndNonDisclosure(t *testing.T) {
	t.Parallel()

	const secret = "внутренняя деталь: dsn=postgres://secret"
	tests := []struct {
		name     string
		grpcErr  error
		wantCode int
	}{
		{"not found → 404", status.Error(codes.NotFound, secret), http.StatusNotFound},
		{"failed precondition → 409", status.Error(codes.FailedPrecondition, secret), http.StatusConflict},
		{"already exists → 409", status.Error(codes.AlreadyExists, secret), http.StatusConflict},
		{"invalid argument → 400", status.Error(codes.InvalidArgument, secret), http.StatusBadRequest},
		{"internal → 500", status.Error(codes.Internal, secret), http.StatusInternalServerError},
		{"unknown → 500", status.Error(codes.Unavailable, secret), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := newTestRouter(&stubProjectsClient{err: tt.grpcErr})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/projects/p1/services/svc", nil)
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("код = %d, ожидалось %d", rec.Code, tt.wantCode)
			}
			body := rec.Body.String()
			if strings.Contains(body, secret) || strings.Contains(body, "dsn=") {
				t.Fatalf("внутренние детали утекли в ответ: %q", body)
			}
			var e errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error == "" {
				t.Fatalf("ответ не соответствует схеме Error: body=%q err=%v", body, err)
			}
		})
	}
}

// TestCreateService проверяет happy-path и валидацию тела создания.
func TestCreateService(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		body     string
		stub     *stubProjectsClient
		wantCode int
		wantJSON *createServiceResult
	}{
		{
			name: "happy-path 201",
			body: `{"name":"svc"}`,
			stub: &stubProjectsClient{createResp: &projectsv1.CreateServiceResponse{
				Id: "id-1", Status: projectsv1.ServiceStatus_SERVICE_STATUS_CREATING,
			}},
			wantCode: http.StatusCreated,
			wantJSON: &createServiceResult{ID: "id-1", Status: "creating"},
		},
		{
			name:     "пустое имя → 400 без вызова gRPC",
			body:     `{"name":""}`,
			stub:     &stubProjectsClient{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "битый JSON → 400",
			body:     `{`,
			stub:     &stubProjectsClient{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "конфликт имени → 409",
			body:     `{"name":"dup"}`,
			stub:     &stubProjectsClient{err: status.Error(codes.FailedPrecondition, "занято")},
			wantCode: http.StatusConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := newTestRouter(tt.stub)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/projects/p1/services", strings.NewReader(tt.body))
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("код = %d, ожидалось %d (body=%q)", rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantJSON != nil {
				var got createServiceResult
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("ответ не парсится: %v", err)
				}
				if got != *tt.wantJSON {
					t.Fatalf("тело = %+v, ожидалось %+v", got, *tt.wantJSON)
				}
			}
			// При валидном теле gRPC должен быть вызван с project из пути.
			if tt.wantCode == http.StatusCreated || tt.wantCode == http.StatusConflict {
				if tt.stub.gotCreate == nil || tt.stub.gotCreate.GetProject() != "p1" {
					t.Fatalf("CreateService вызван некорректно: %+v", tt.stub.gotCreate)
				}
			}
			// Пустое имя/битый JSON не должны доходить до gRPC.
			if tt.name == "пустое имя → 400 без вызова gRPC" && tt.stub.gotCreate != nil {
				t.Fatalf("gRPC не должен вызываться при невалидном теле")
			}
		})
	}
}

// TestGetService проверяет happy-path чтения статуса.
func TestGetService(t *testing.T) {
	t.Parallel()
	router := newTestRouter(&stubProjectsClient{getResp: &projectsv1.GetServiceResponse{
		Project: "p1", Name: "svc", Status: projectsv1.ServiceStatus_SERVICE_STATUS_ACTIVE,
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/p1/services/svc", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("код = %d, ожидалось 200", rec.Code)
	}
	var got serviceSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не парсится: %v", err)
	}
	want := serviceSummary{Project: "p1", Name: "svc", Status: "active"}
	if got != want {
		t.Fatalf("тело = %+v, ожидалось %+v", got, want)
	}
}

// TestListServices проверяет happy-path листинга и проброс keyset-курсора.
func TestListServices(t *testing.T) {
	t.Parallel()
	stub := &stubProjectsClient{listResp: &projectsv1.ListServicesResponse{
		Services: []*projectsv1.Service{
			{Project: "p1", Name: "a", Status: projectsv1.ServiceStatus_SERVICE_STATUS_CREATING},
			{Project: "p1", Name: "b", Status: projectsv1.ServiceStatus_SERVICE_STATUS_ACTIVE},
		},
		NextPageToken: "cursor-2",
	}}
	router := newTestRouter(stub)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/p1/services?page_size=2&page_token=cursor-1", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("код = %d, ожидалось 200 (body=%q)", rec.Code, rec.Body.String())
	}
	var got serviceList
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не парсится: %v", err)
	}
	if len(got.Services) != 2 || got.NextPageToken != "cursor-2" {
		t.Fatalf("неожиданное тело: %+v", got)
	}
	// Курсор и размер должны проброситься в gRPC без интерпретации.
	if stub.gotList.GetPageToken() != "cursor-1" || stub.gotList.GetPageSize() != 2 {
		t.Fatalf("keyset проброшен некорректно: %+v", stub.gotList)
	}
}

// TestListServicesBadPageSize проверяет валидацию page_size без выхода в gRPC.
func TestListServicesBadPageSize(t *testing.T) {
	t.Parallel()
	stub := &stubProjectsClient{}
	router := newTestRouter(stub)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/projects/p1/services?page_size=abc", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("код = %d, ожидалось 400", rec.Code)
	}
	if stub.gotList != nil {
		t.Fatalf("gRPC не должен вызываться при некорректном page_size")
	}
}
