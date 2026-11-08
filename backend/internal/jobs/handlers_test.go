package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/observability"
	"canonkeeper/backend/internal/projects"

	"github.com/go-chi/chi/v5"
)

func TestJobHandlersProjectSupportCodeOfCreatingRequestOnEveryPoll(t *testing.T) {
	const creatorRequestID = "00000000-0000-4000-8000-000000000901"
	jobError := Error{Code: "worker.handler_failed", Message: "The background job failed."}
	failedJob := Job{
		ID:               "job-1",
		ProjectID:        testProjectID,
		Kind:             KindCanonTextIndex,
		Status:           StatusFailed,
		Subject:          CanonTextIndexSubject{Type: KindCanonTextIndex, ProjectID: testProjectID},
		Error:            &jobError,
		CreatorUserID:    "user-1",
		CreatorRequestID: creatorRequestID,
		CreatedAt:        jobTestNow(),
		UpdatedAt:        jobTestNow(),
	}
	withoutCreatorRequest := failedJob
	withoutCreatorRequest.CreatorRequestID = ""

	for _, tc := range []struct {
		name   string
		job    Job
		method string
		target string
		list   bool
		want   string
	}{
		{name: "get", job: failedJob, method: http.MethodGet, target: "/jobs/job-1", want: creatorRequestID},
		{name: "list", job: failedJob, method: http.MethodGet, target: "/projects/" + testProjectID + "/jobs", list: true, want: creatorRequestID},
		{name: "cancel", job: failedJob, method: http.MethodPost, target: "/jobs/job-1/cancel", want: creatorRequestID},
		{name: "job without creating request", job: withoutCreatorRequest, method: http.MethodGet, target: "/jobs/job-1", want: "job-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeJobHandlerService{job: tc.job, page: JobPage{Jobs: []Job{tc.job}}}
			pollRequestIDs := []string{
				"00000000-0000-4000-8000-000000000911",
				"00000000-0000-4000-8000-000000000912",
			}
			nextPoll := 0
			handler := observability.RequestLogger(io.Discard, observability.RequestLoggerOptions{
				NewRequestID: func() string {
					requestID := pollRequestIDs[nextPoll]
					nextPoll++
					return requestID
				},
			})(newJobTestRouter(service, "user-1"))

			for poll := range pollRequestIDs {
				req := httptest.NewRequest(tc.method, tc.target, nil)
				req.Header.Set(observability.RequestIDHeader, "unsafe-client-request-id")
				if tc.method == http.MethodPost {
					authorizeJobRequest(req)
				}
				rec := httptest.NewRecorder()

				handler.ServeHTTP(rec, req)

				if rec.Code != http.StatusOK {
					t.Fatalf("poll %d status = %d, want %d; body: %s", poll, rec.Code, http.StatusOK, rec.Body.String())
				}
				var response struct {
					Error *httpapi.Problem `json:"error"`
					Data  []struct {
						Error *httpapi.Problem `json:"error"`
					} `json:"data"`
				}
				if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				problem := response.Error
				if tc.list && len(response.Data) == 1 {
					problem = response.Data[0].Error
				}
				if problem == nil || problem.RequestID != tc.want {
					t.Fatalf("poll %d job error = %#v, want support code %q", poll, problem, tc.want)
				}
			}
		})
	}
}

func TestCancelJobConcealsTargetBoundJobsWithoutResolver(t *testing.T) {
	for _, status := range []Status{StatusQueued, StatusCanceling, StatusSucceeded} {
		t.Run(string(status), func(t *testing.T) {
			store := newJobStoreForTest()
			store.roles[projectActorKey("user-1", "project-1")] = projects.RoleViewer
			cancelCalls := 0
			store.beforeCancelJobStatus = func(CancelJobStatusStoreInput) {
				cancelCalls++
			}
			store.jobs["job-1"] = Job{
				ID:            "job-1",
				ProjectID:     "project-1",
				Kind:          KindCanonTextIndex,
				Status:        status,
				Subject:       CanonTextIndexSubject{Type: KindCanonTextIndex, ProjectID: "project-1"},
				CanCancel:     status == StatusQueued,
				CreatorUserID: "user-1",
				CreatedAt:     jobTestNow(),
				UpdatedAt:     jobTestNow(),
			}
			service := NewService(store, fixedJobClock{now: jobTestNow()})
			handlers := NewHandlers(service, fakeJobHandlerCurrentUser{user: auth.User{ID: "user-1"}}, []string{"https://app.example.test"})
			router := chi.NewRouter()
			handlers.Mount(router)
			req := httptest.NewRequest(http.MethodPost, "/jobs/job-1/cancel", nil)
			authorizeJobRequest(req)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
			}
			if cancelCalls != 0 {
				t.Fatalf("cancel mutation calls = %d, want 0", cancelCalls)
			}
			if got := store.jobs["job-1"].Status; got != status {
				t.Fatalf("stored status = %q, want unchanged %q", got, status)
			}
		})
	}
}

func TestListProjectJobsAcceptsEveryDeclaredKindAndRejectsUnknown(t *testing.T) {
	unknown := Kind("not_a_job_kind")
	kinds := append(CurrentKinds(), unknown)
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			service := &fakeJobHandlerService{}
			router := newJobTestRouter(service, "user-1")
			req := httptest.NewRequest(http.MethodGet, "/projects/project-1/jobs?kind="+string(kind), nil)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			wantStatus := http.StatusOK
			wantCalls := 1
			if kind == unknown {
				wantStatus = http.StatusBadRequest
				wantCalls = 0
			}
			if rec.Code != wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, wantStatus)
			}
			if service.listCalls != wantCalls {
				t.Fatalf("ListProjectJobs calls = %d, want %d", service.listCalls, wantCalls)
			}
			if wantCalls == 1 && service.listInput.Kind != kind {
				t.Fatalf("kind passed to service = %q, want %q", service.listInput.Kind, kind)
			}
		})
	}
}

// UC1: the shelf asks for the Jobs in work with active=true; a malformed flag is a 400.
func TestListProjectJobsParsesActiveFilter(t *testing.T) {
	for _, tc := range []struct {
		query      string
		wantStatus int
		wantActive bool
	}{
		{query: "", wantStatus: http.StatusOK},
		{query: "?active=true", wantStatus: http.StatusOK, wantActive: true},
		{query: "?active=false&kind=import", wantStatus: http.StatusOK},
		{query: "?active=yes", wantStatus: http.StatusBadRequest},
		// The contract's boolean is the literal true or false, as the mock reads it.
		{query: "?active=1", wantStatus: http.StatusBadRequest},
		{query: "?active=TRUE", wantStatus: http.StatusBadRequest},
	} {
		t.Run(tc.query, func(t *testing.T) {
			service := &fakeJobHandlerService{}
			router := newJobTestRouter(service, "user-1")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/project-1/jobs"+tc.query, nil))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusOK && (service.listInput.ActiveOnly != tc.wantActive || service.listInput.Limit != defaultPageLimit) {
				t.Fatalf("input = %+v, want active=%v and the default limit", service.listInput, tc.wantActive)
			}
		})
	}
}

func TestListProjectJobsLogsCanceledRequestWithoutChangingGenericResponse(t *testing.T) {
	const secret = "SHOULD_NOT_LEAK"
	service := &fakeJobHandlerService{listErr: errors.Join(context.Canceled, errors.New(secret))}
	var output bytes.Buffer
	handler := httpapi.RequestLogger(&output)(newJobTestRouter(service, "user-1"))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/projects/"+testProjectID+"/jobs", nil),
	)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", recorder.Code, recorder.Body.String())
	}
	var problem struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "server.error" || problem.Detail != "unexpected server error" {
		t.Fatalf("problem = %#v, want generic server error", problem)
	}
	var entry struct {
		Status     int    `json:"status"`
		ErrorClass string `json:"errorClass"`
		ErrorCause string `json:"errorCause"`
	}
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Status != 499 || entry.ErrorClass != "context" || entry.ErrorCause != "canceled" {
		t.Fatalf("request log error fields = %#v", entry)
	}
	if strings.Contains(output.String(), secret) || strings.Contains(recorder.Body.String(), secret) {
		t.Fatalf("canceled request leaked secret canary")
	}
}

func newJobTestRouter(service *fakeJobHandlerService, actorUserID string) http.Handler {
	handlers := NewHandlers(
		service,
		fakeJobHandlerCurrentUser{user: auth.User{ID: actorUserID}},
		[]string{"https://app.example.test"},
	)
	router := chi.NewRouter()
	handlers.Mount(router)
	return router
}

type fakeJobHandlerService struct {
	job          Job
	cancelResult CancelResult
	page         JobPage
	listInput    ListProjectJobsInput
	listCalls    int
	listErr      error
}

func (s *fakeJobHandlerService) ListProjectJobs(_ context.Context, _ Actor, _ string, input ListProjectJobsInput) (JobPage, error) {
	s.listCalls++
	s.listInput = input
	return s.page, s.listErr
}

func (s *fakeJobHandlerService) GetJob(context.Context, Actor, string) (Job, error) {
	return s.job, nil
}

func (s *fakeJobHandlerService) CancelJob(context.Context, Actor, string) (CancelResult, error) {
	if s.cancelResult.Opaque || s.cancelResult.Job.ID != "" {
		return s.cancelResult, nil
	}
	return CancelResult{Job: s.job}, nil
}

type fakeJobHandlerCurrentUser struct {
	user auth.User
}

func (s fakeJobHandlerCurrentUser) CurrentUser(context.Context, string) (auth.User, error) {
	return s.user, nil
}

func (s fakeJobHandlerCurrentUser) ValidateCSRF(context.Context, string, string) error {
	return nil
}

func authorizeJobRequest(req *http.Request) {
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("X-CSRF-Token", "csrf-token")
}
