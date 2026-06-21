// Тесты ручек справочника субъектов периметра (ADR-0016): RBAC fail-closed на
// (read, iam:directory), семантика 503 при недоступном Keycloak, валидация ввода
// (400), батч-резолв с осиротевшими, и обогащение GET /iam/subjects (с правом/
// без права/деградация). Сеть не используется — клиенты IDM подменяются стабами.
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
)

// accessStub — стаб AccessService с решением по (resource, action).
type accessStub struct {
	decide func(resource, action string) bool
	err    error
}

func (s *accessStub) CheckAccess(_ context.Context, in *idmv1.CheckAccessRequest, _ ...grpc.CallOption) (*idmv1.CheckAccessResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &idmv1.CheckAccessResponse{Allowed: s.decide(in.GetResource(), in.GetAction())}, nil
}

// stubIdentityClient — стаб gRPC-клиента справочника субъектов.
type stubIdentityClient struct {
	search       *idmv1.SearchSubjectsResponse
	resolve      *idmv1.ResolveSubjectsResponse
	err          error
	searchCalls  int
	resolveCalls int
}

func (s *stubIdentityClient) SearchSubjects(_ context.Context, _ *idmv1.SearchSubjectsRequest, _ ...grpc.CallOption) (*idmv1.SearchSubjectsResponse, error) {
	s.searchCalls++
	if s.err != nil {
		return nil, s.err
	}
	return s.search, nil
}

func (s *stubIdentityClient) ResolveSubjects(_ context.Context, _ *idmv1.ResolveSubjectsRequest, _ ...grpc.CallOption) (*idmv1.ResolveSubjectsResponse, error) {
	s.resolveCalls++
	if s.err != nil {
		return nil, s.err
	}
	return s.resolve, nil
}

// newDirRouter собирает роутер со стабами Access/IamAdmin/Identity.
func newDirRouter(idm idmv1.AccessServiceClient, iamAdmin idmv1.IamAdminServiceClient, identity idmv1.IdentityServiceClient) http.Handler {
	api := &servicesAPI{
		idm:       idm,
		iamAdmin:  iamAdmin,
		roleAdmin: &stubRoleAdminClient{},
		identity:  identity,
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r := chi.NewRouter()
	api.register(r)
	return r
}

// allowAll/denyAll — частые решатели RBAC.
func allowAll(string, string) bool { return true }
func denyAll(string, string) bool  { return false }

func TestDirectorySearch_Happy(t *testing.T) {
	t.Parallel()
	id := &stubIdentityClient{search: &idmv1.SearchSubjectsResponse{
		Subjects:   []*idmv1.SubjectIdentity{{Subject: "u-1", Username: "ivan", Email: "i@e", Found: true}},
		NextCursor: "cur-2",
	}}
	router := newDirRouter(&accessStub{decide: allowAll}, &stubIamAdminClient{}, id)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/iam/directory/subjects?search=iv", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("код = %d, ожидалось 200 (body=%q)", rec.Code, rec.Body.String())
	}
	var got directoryListView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не парсится: %v", err)
	}
	if len(got.Subjects) != 1 || got.Subjects[0].Subject != "u-1" || got.NextCursor != "cur-2" {
		t.Fatalf("неожиданное тело: %+v", got)
	}
}

func TestDirectorySearch_Deny403(t *testing.T) {
	t.Parallel()
	id := &stubIdentityClient{}
	router := newDirRouter(&accessStub{decide: denyAll}, &stubIamAdminClient{}, id)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/iam/directory/subjects?search=iv", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("код = %d, ожидалось 403", rec.Code)
	}
	if id.searchCalls != 0 {
		t.Fatalf("при отказе RBAC справочник не должен вызываться")
	}
}

func TestDirectorySearch_RBACUnavailable403(t *testing.T) {
	t.Parallel()
	id := &stubIdentityClient{}
	router := newDirRouter(&accessStub{err: status.Error(codes.Unavailable, "idm down")}, &stubIamAdminClient{}, id)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/iam/directory/subjects?search=iv", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("недоступность IDM должна давать 403 (fail-closed), got %d", rec.Code)
	}
}

func TestDirectorySearch_EmptySearch400(t *testing.T) {
	t.Parallel()
	id := &stubIdentityClient{}
	router := newDirRouter(&accessStub{decide: allowAll}, &stubIamAdminClient{}, id)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/iam/directory/subjects", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("пустой search должен давать 400, got %d", rec.Code)
	}
	if id.searchCalls != 0 {
		t.Fatalf("при битом вводе справочник не должен вызываться")
	}
}

func TestDirectorySearch_KeycloakDown503(t *testing.T) {
	t.Parallel()
	id := &stubIdentityClient{err: status.Error(codes.Unavailable, "keycloak down")}
	router := newDirRouter(&accessStub{decide: allowAll}, &stubIamAdminClient{}, id)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/iam/directory/subjects?search=iv", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("недоступность Keycloak должна давать 503, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestDirectoryResolve_HappyAndOrphan(t *testing.T) {
	t.Parallel()
	id := &stubIdentityClient{resolve: &idmv1.ResolveSubjectsResponse{Subjects: []*idmv1.SubjectIdentity{
		{Subject: "u-1", Username: "ivan", Found: true},
		{Subject: "u-x", Found: false},
	}}}
	router := newDirRouter(&accessStub{decide: allowAll}, &stubIamAdminClient{}, id)

	body := strings.NewReader(`{"subjects":["u-1","u-x"]}`)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/iam/directory/subjects/resolve", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("код = %d, ожидалось 200 (body=%q)", rec.Code, rec.Body.String())
	}
	var got directoryResolveView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не парсится: %v", err)
	}
	if len(got.Subjects) != 2 || got.Subjects[1].Found {
		t.Fatalf("осиротевший должен иметь found=false: %+v", got.Subjects)
	}
}

func TestDirectoryResolve_Empty400(t *testing.T) {
	t.Parallel()
	id := &stubIdentityClient{}
	router := newDirRouter(&accessStub{decide: allowAll}, &stubIamAdminClient{}, id)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/iam/directory/subjects/resolve", strings.NewReader(`{"subjects":[]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("пустой список должен давать 400, got %d", rec.Code)
	}
	if id.resolveCalls != 0 {
		t.Fatalf("при пустом списке справочник не должен вызываться")
	}
}

// directoryReader разрешает iam:global, а iam:directory — по флагу.
func directoryReader(allowDirectory bool) func(resource, action string) bool {
	return func(resource, _ string) bool {
		if resource == iamDirectoryResource {
			return allowDirectory
		}
		return true
	}
}

func TestListSubjects_EnrichedWithDirectoryRight(t *testing.T) {
	t.Parallel()
	iam := &stubIamAdminClient{subjects: &idmv1.ListSubjectsWithRolesResponse{
		Subjects: []*idmv1.SubjectRoles{
			{Subject: "u-1", Roles: []string{"iam-admin"}},
			{Subject: "u-x", Roles: []string{"viewer"}},
		},
	}}
	id := &stubIdentityClient{resolve: &idmv1.ResolveSubjectsResponse{Subjects: []*idmv1.SubjectIdentity{
		{Subject: "u-1", Username: "ivan", Email: "i@e", Found: true},
		{Subject: "u-x", Found: false},
	}}}
	router := newDirRouter(&accessStub{decide: directoryReader(true)}, iam, id)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/iam/subjects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("код = %d, ожидалось 200", rec.Code)
	}
	var got subjectListView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("ответ не парсится: %v", err)
	}
	if len(got.Subjects) != 2 {
		t.Fatalf("ожидали 2 субъекта: %+v", got.Subjects)
	}
	if got.Subjects[0].Identity == nil || got.Subjects[0].Identity.Username != "ivan" {
		t.Fatalf("первый субъект должен быть обогащён: %+v", got.Subjects[0])
	}
	if got.Subjects[1].Identity == nil || got.Subjects[1].Identity.Found {
		t.Fatalf("второй субъект должен быть осиротевшим (found=false): %+v", got.Subjects[1])
	}
}

func TestListSubjects_RawWithoutDirectoryRight(t *testing.T) {
	t.Parallel()
	iam := &stubIamAdminClient{subjects: &idmv1.ListSubjectsWithRolesResponse{
		Subjects: []*idmv1.SubjectRoles{{Subject: "u-1", Roles: []string{"iam-admin"}}},
	}}
	id := &stubIdentityClient{}
	router := newDirRouter(&accessStub{decide: directoryReader(false)}, iam, id)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/iam/subjects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("код = %d, ожидалось 200", rec.Code)
	}
	var got subjectListView
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Subjects[0].Identity != nil {
		t.Fatalf("без права iam:directory идентичность не должна раскрываться: %+v", got.Subjects[0])
	}
	if id.resolveCalls != 0 {
		t.Fatalf("без права справочник не должен вызываться")
	}
}

func TestListSubjects_DegradeWhenKeycloakDown(t *testing.T) {
	t.Parallel()
	iam := &stubIamAdminClient{subjects: &idmv1.ListSubjectsWithRolesResponse{
		Subjects: []*idmv1.SubjectRoles{{Subject: "u-1", Roles: []string{"iam-admin"}}},
	}}
	id := &stubIdentityClient{err: status.Error(codes.Unavailable, "keycloak down")}
	router := newDirRouter(&accessStub{decide: directoryReader(true)}, iam, id)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/iam/subjects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("деградация: список ролей должен отдаваться 200, got %d", rec.Code)
	}
	var got subjectListView
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Subjects[0].Identity != nil {
		t.Fatalf("при недоступном Keycloak идентичность опускается: %+v", got.Subjects[0])
	}
}
