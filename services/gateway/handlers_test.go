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

	idmv1 "github.com/YuriB9/idp/pkg/api/idm/v1"
	projectsv1 "github.com/YuriB9/idp/pkg/api/projects/v1"
)

// stubProjectsClient — стаб gRPC-клиента каталога: возвращает заранее заданные
// ответы/ошибки, не выходя в сеть.
type stubProjectsClient struct {
	getResp    *projectsv1.GetServiceResponse
	listResp   *projectsv1.ListServicesResponse
	createResp *projectsv1.CreateServiceResponse
	ownersResp *projectsv1.SetServiceOwnersResponse
	decommResp *projectsv1.DecommissionServiceResponse
	err        error

	// gotCreate фиксирует аргументы последнего CreateService для проверок.
	gotCreate *projectsv1.CreateServiceRequest
	gotList   *projectsv1.ListServicesRequest
	gotOwners *projectsv1.SetServiceOwnersRequest
	gotDecomm *projectsv1.DecommissionServiceRequest
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

func (s *stubProjectsClient) SetServiceOwners(_ context.Context, in *projectsv1.SetServiceOwnersRequest, _ ...grpc.CallOption) (*projectsv1.SetServiceOwnersResponse, error) {
	s.gotOwners = in
	if s.err != nil {
		return nil, s.err
	}
	return s.ownersResp, nil
}

func (s *stubProjectsClient) DecommissionService(_ context.Context, in *projectsv1.DecommissionServiceRequest, _ ...grpc.CallOption) (*projectsv1.DecommissionServiceResponse, error) {
	s.gotDecomm = in
	if s.err != nil {
		return nil, s.err
	}
	return s.decommResp, nil
}

// stubIDMClient — стаб gRPC-клиента IDM: возвращает заранее заданное решение
// или ошибку, не выходя в сеть.
type stubIDMClient struct {
	allowed bool
	err     error
	gotReq  *idmv1.CheckAccessRequest
}

func (s *stubIDMClient) CheckAccess(_ context.Context, in *idmv1.CheckAccessRequest, _ ...grpc.CallOption) (*idmv1.CheckAccessResponse, error) {
	s.gotReq = in
	if s.err != nil {
		return nil, s.err
	}
	return &idmv1.CheckAccessResponse{Allowed: s.allowed}, nil
}

// newTestRouter собирает роутер с доменными ручками поверх стаба projects и
// разрешающего по умолчанию стаба IDM (RBAC-тесты задают свой idm-стаб через
// newTestRouterWithIDM).
func newTestRouter(client projectsv1.ProjectsServiceClient) http.Handler {
	return newTestRouterWithIDM(client, &stubIDMClient{allowed: true})
}

// newTestRouterWithIDM собирает роутер с явными стабами projects и IDM.
func newTestRouterWithIDM(client projectsv1.ProjectsServiceClient, idm idmv1.AccessServiceClient) http.Handler {
	api := &servicesAPI{client: client, idm: idm, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
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
		{"aborted → 409", status.Error(codes.Aborted, secret), http.StatusConflict},
		{"already exists → 409", status.Error(codes.AlreadyExists, secret), http.StatusConflict},
		{"failed precondition → 422", status.Error(codes.FailedPrecondition, secret), http.StatusUnprocessableEntity},
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
			stub:     &stubProjectsClient{err: status.Error(codes.Aborted, "занято")},
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
		Owners: []string{"alice", "bob"}, OwnersVersion: 4,
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
	if got.Project != "p1" || got.Name != "svc" || got.Status != "active" {
		t.Fatalf("тело = %+v", got)
	}
	if got.OwnersVersion != 4 || len(got.Owners) != 2 || got.Owners[0] != "alice" {
		t.Fatalf("владельцы в ответе = %v v%d", got.Owners, got.OwnersVersion)
	}
}

// TestSetServiceOwners проверяет happy-path, валидацию тела и маппинг конфликта.
func TestSetServiceOwners(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		body     string
		stub     *stubProjectsClient
		wantCode int
		wantCall bool
	}{
		{
			name:     "happy-path 200",
			body:     `{"owners":["alice","bob"],"owners_version":4}`,
			stub:     &stubProjectsClient{ownersResp: &projectsv1.SetServiceOwnersResponse{Owners: []string{"alice", "bob"}, OwnersVersion: 5}},
			wantCode: http.StatusOK,
			wantCall: true,
		},
		{
			name:     "пустой владелец → 400 без gRPC",
			body:     `{"owners":[""],"owners_version":4}`,
			stub:     &stubProjectsClient{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "дубли владельцев → 400 без gRPC",
			body:     `{"owners":["a","a"],"owners_version":4}`,
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
			name:     "конфликт версии → 409",
			body:     `{"owners":["a"],"owners_version":1}`,
			stub:     &stubProjectsClient{err: status.Error(codes.Aborted, "версия устарела")},
			wantCode: http.StatusConflict,
			wantCall: true,
		},
		{
			name:     "сервис не найден → 404",
			body:     `{"owners":["a"],"owners_version":0}`,
			stub:     &stubProjectsClient{err: status.Error(codes.NotFound, "нет сервиса")},
			wantCode: http.StatusNotFound,
			wantCall: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := newTestRouter(tt.stub)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/projects/p1/services/svc/owners", strings.NewReader(tt.body))
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("код = %d, ожидалось %d (body=%q)", rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantCall && (tt.stub.gotOwners == nil || tt.stub.gotOwners.GetProject() != "p1" || tt.stub.gotOwners.GetName() != "svc") {
				t.Fatalf("SetServiceOwners вызван некорректно: %+v", tt.stub.gotOwners)
			}
			if !tt.wantCall && tt.stub.gotOwners != nil {
				t.Fatalf("gRPC не должен вызываться при невалидном теле")
			}
			if tt.wantCode == http.StatusOK {
				var got setOwnersResult
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("ответ не парсится: %v", err)
				}
				if got.OwnersVersion != 5 || len(got.Owners) != 2 {
					t.Fatalf("тело = %+v", got)
				}
			}
		})
	}
}

// TestSetOwnersRBACDeny: при отказе RBAC change_owners → 403, gRPC не вызывается.
func TestSetOwnersRBACDeny(t *testing.T) {
	t.Parallel()
	projects := &stubProjectsClient{}
	router := newTestRouterWithIDM(projects, &stubIDMClient{allowed: false})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/projects/p1/services/svc/owners", strings.NewReader(`{"owners":["a"],"owners_version":0}`)))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("код = %d, ожидалось 403", rec.Code)
	}
	if projects.gotOwners != nil {
		t.Fatalf("при отказе RBAC доменный gRPC не должен вызываться")
	}
}

// TestSetOwnersRBACAction проверяет, что действие RBAC = change_owners.
func TestSetOwnersRBACAction(t *testing.T) {
	t.Parallel()
	idm := &stubIDMClient{allowed: true}
	router := newTestRouterWithIDM(&stubProjectsClient{
		ownersResp: &projectsv1.SetServiceOwnersResponse{Owners: []string{"a"}, OwnersVersion: 1},
	}, idm)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/projects/demo/services/svc/owners", strings.NewReader(`{"owners":["a"],"owners_version":0}`)))

	if idm.gotReq.GetResource() != "project:demo" || idm.gotReq.GetAction() != "change_owners" {
		t.Fatalf("некорректная форма запроса RBAC: %+v", idm.gotReq)
	}
}

// TestDecommissionService проверяет happy-path, маппинг предусловия (422) и
// конфликта (409), а также RBAC-действие decommission.
func TestDecommissionService(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		body     string
		stub     *stubProjectsClient
		wantCode int
		wantCall bool
	}{
		{
			name: "happy-path 200",
			body: `{"load_drained":true}`,
			stub: &stubProjectsClient{decommResp: &projectsv1.DecommissionServiceResponse{Service: &projectsv1.Service{
				Project: "p1", Name: "svc", Status: projectsv1.ServiceStatus_SERVICE_STATUS_DECOMMISSIONED, DecommissionedAt: "2026-06-20T00:00:00Z",
			}}},
			wantCode: http.StatusOK,
			wantCall: true,
		},
		{
			name:     "предусловие (нагрузка не снята) → 422",
			body:     `{"load_drained":false}`,
			stub:     &stubProjectsClient{err: status.Error(codes.FailedPrecondition, "нагрузка не снята")},
			wantCode: http.StatusUnprocessableEntity,
			wantCall: true,
		},
		{
			name:     "конкурентный конфликт → 409",
			body:     `{"load_drained":true}`,
			stub:     &stubProjectsClient{err: status.Error(codes.Aborted, "конфликт")},
			wantCode: http.StatusConflict,
			wantCall: true,
		},
		{
			name:     "битый JSON → 400",
			body:     `{`,
			stub:     &stubProjectsClient{},
			wantCode: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := newTestRouter(tt.stub)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/projects/p1/services/svc/decommission", strings.NewReader(tt.body))
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("код = %d, ожидалось %d (body=%q)", rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantCall && (tt.stub.gotDecomm == nil || tt.stub.gotDecomm.GetProject() != "p1" || tt.stub.gotDecomm.GetName() != "svc") {
				t.Fatalf("DecommissionService вызван некорректно: %+v", tt.stub.gotDecomm)
			}
			if !tt.wantCall && tt.stub.gotDecomm != nil {
				t.Fatalf("gRPC не должен вызываться при невалидном теле")
			}
			if tt.wantCode == http.StatusOK {
				var got serviceSummary
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("ответ не парсится: %v", err)
				}
				if got.Status != "decommissioned" || got.DecommissionedAt == "" {
					t.Fatalf("тело = %+v", got)
				}
			}
		})
	}
}

// TestDecommissionRBAC: отказ RBAC → 403 без gRPC; действие = decommission.
func TestDecommissionRBAC(t *testing.T) {
	t.Parallel()

	// Отказ → 403, доменный gRPC не вызывается.
	projects := &stubProjectsClient{}
	router := newTestRouterWithIDM(projects, &stubIDMClient{allowed: false})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/p1/services/svc/decommission", strings.NewReader(`{"load_drained":true}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("код = %d, ожидалось 403", rec.Code)
	}
	if projects.gotDecomm != nil {
		t.Fatalf("при отказе RBAC доменный gRPC не должен вызываться")
	}

	// Форма запроса RBAC = (project:demo, decommission).
	idm := &stubIDMClient{allowed: true}
	router2 := newTestRouterWithIDM(&stubProjectsClient{
		decommResp: &projectsv1.DecommissionServiceResponse{Service: &projectsv1.Service{Project: "demo", Name: "svc", Status: projectsv1.ServiceStatus_SERVICE_STATUS_DECOMMISSIONED}},
	}, idm)
	rec2 := httptest.NewRecorder()
	router2.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/projects/demo/services/svc/decommission", strings.NewReader(`{"load_drained":true}`)))
	if idm.gotReq.GetResource() != "project:demo" || idm.gotReq.GetAction() != "decommission" {
		t.Fatalf("некорректная форма запроса RBAC: %+v", idm.gotReq)
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

// TestRBACDenyForbids проверяет, что при allowed=false шлюз отвечает 403 и не
// вызывает доменный gRPC, а тело не раскрывает внутренних деталей.
func TestRBACDenyForbids(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{"create deny", http.MethodPost, "/projects/p1/services", `{"name":"svc"}`},
		{"list deny", http.MethodGet, "/projects/p1/services", ""},
		{"get deny", http.MethodGet, "/projects/p1/services/svc", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			projects := &stubProjectsClient{}
			router := newTestRouterWithIDM(projects, &stubIDMClient{allowed: false})

			rec := httptest.NewRecorder()
			var bodyReader io.Reader
			if tt.body != "" {
				bodyReader = strings.NewReader(tt.body)
			}
			router.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.target, bodyReader))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("код = %d, ожидалось 403", rec.Code)
			}
			if projects.gotCreate != nil || projects.gotList != nil {
				t.Fatalf("при отказе RBAC доменный gRPC не должен вызываться")
			}
			var eb errorBody
			if err := json.NewDecoder(rec.Body).Decode(&eb); err != nil {
				t.Fatalf("тело ошибки не распарсилось: %v", err)
			}
			if strings.Contains(eb.Error, "project:") || eb.Error == "" {
				t.Fatalf("тело раскрывает детали или пустое: %q", eb.Error)
			}
		})
	}
}

// TestRBACUnavailableFailClosed проверяет, что при ошибке вызова IDM шлюз
// отвечает 403 (fail-closed), а не пропускает запрос и не отдаёт детали.
func TestRBACUnavailableFailClosed(t *testing.T) {
	t.Parallel()

	projects := &stubProjectsClient{}
	idm := &stubIDMClient{err: status.Error(codes.Unavailable, "внутренняя деталь: idm down")}
	router := newTestRouterWithIDM(projects, idm)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/p1/services", strings.NewReader(`{"name":"svc"}`)))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("код = %d, ожидалось 403 (fail-closed)", rec.Code)
	}
	if projects.gotCreate != nil {
		t.Fatalf("при недоступном IDM доменный gRPC не должен вызываться")
	}
	var eb errorBody
	_ = json.NewDecoder(rec.Body).Decode(&eb)
	if strings.Contains(eb.Error, "idm down") {
		t.Fatalf("тело раскрывает внутренние детали: %q", eb.Error)
	}
}

// TestRBACRequestShape проверяет, что resource/action формируются корректно.
func TestRBACRequestShape(t *testing.T) {
	t.Parallel()

	idm := &stubIDMClient{allowed: true}
	router := newTestRouterWithIDM(&stubProjectsClient{
		createResp: &projectsv1.CreateServiceResponse{Id: "id-1", Status: projectsv1.ServiceStatus_SERVICE_STATUS_CREATING},
	}, idm)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/demo/services", strings.NewReader(`{"name":"svc"}`)))

	if idm.gotReq.GetResource() != "project:demo" || idm.gotReq.GetAction() != "create" {
		t.Fatalf("некорректная форма запроса RBAC: %+v", idm.gotReq)
	}
}
