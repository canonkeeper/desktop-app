package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/observability"

	"github.com/go-chi/chi/v5"
)

func TestHandlersStartIndexingReturnsJobStartResponse(t *testing.T) {
	service := &startIndexingServiceForTest{
		job: jobs.Job{
			ID:        "job-1",
			ProjectID: "project-1",
			Kind:      jobs.KindCanonTextIndex,
			Status:    jobs.StatusQueued,
			Subject: jobs.CanonTextIndexSubject{
				Type: jobs.KindCanonTextIndex, ProjectID: "project-1",
				SourceView: governance.SourceViewDraft, CatalogRevision: 4,
			},
			CanCancel:     true,
			CreatorUserID: "user-1",
			CreatedAt:     startTestTime(),
			UpdatedAt:     startTestTime(),
		},
	}
	currentUser := &currentUserForIndexingHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
	router := chi.NewRouter()
	NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

	req := httptest.NewRequest(http.MethodPost, "/projects/project-1/indexing-runs", strings.NewReader(`{"sourceView":"draft"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	req.Header.Set("Idempotency-Key", "indexing-1")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("Location") != "/api/v1/jobs/job-1" {
		t.Fatalf("Location = %q, want API job link", rec.Header().Get("Location"))
	}
	if rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q, want 1", rec.Header().Get("Retry-After"))
	}
	var body struct {
		JobID string `json:"jobId"`
		Job   struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Subject struct {
				Type            string `json:"type"`
				ProjectID       string `json:"projectId"`
				SourceView      string `json:"sourceView"`
				CatalogRevision int64  `json:"catalogRevision"`
			} `json:"subject"`
		} `json:"job"`
		Links struct {
			Job string `json:"job"`
		} `json:"links"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.JobID != "job-1" || body.Job.ID != "job-1" || body.Job.Kind != string(jobs.KindCanonTextIndex) {
		t.Fatalf("body = %#v, want indexing job", body)
	}
	if body.Job.Subject.Type != string(jobs.KindCanonTextIndex) || body.Job.Subject.ProjectID != "project-1" ||
		body.Job.Subject.SourceView != string(governance.SourceViewDraft) || body.Job.Subject.CatalogRevision != 4 {
		t.Fatalf("job subject = %#v, want exact text-index subject", body.Job.Subject)
	}
	if body.Links.Job != "/api/v1/jobs/job-1" {
		t.Fatalf("links = %#v, want canonical job link", body.Links)
	}
	wantCall := startIndexingCallForTest{
		actor: Actor{UserID: "user-1"},
		input: StartIndexingInput{
			ProjectID:      "project-1",
			SourceView:     governance.SourceViewDraft,
			RequestMethod:  http.MethodPost,
			RequestPath:    "/projects/project-1/indexing-runs",
			IdempotencyKey: "indexing-1",
			SessionWriteProof: auth.SessionWriteProof{
				SessionHash:    "session-hash",
				CSRFSecretHash: "csrf-hash",
			},
		},
	}
	if len(service.calls) != 1 || service.calls[0] != wantCall {
		t.Fatalf("service calls = %#v, want user/project call", service.calls)
	}
}

func TestHandlersStartIndexingProjectsTerminalJobState(t *testing.T) {
	const requestID = "00000000-0000-4000-8000-000000005108"
	for _, tc := range []struct {
		name         string
		storedStatus jobs.Status
		publicStatus jobs.PublicStatus
		jobError     *jobs.Error
	}{
		{name: "canceling", storedStatus: jobs.StatusCanceling, publicStatus: jobs.PublicStatusCanceling},
		{name: "succeeded", storedStatus: jobs.StatusSucceeded, publicStatus: jobs.PublicStatusSucceeded},
		{name: "failed", storedStatus: jobs.StatusFailed, publicStatus: jobs.PublicStatusFailed, jobError: &jobs.Error{Code: "worker.handler_failed", Message: "The background job failed."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &startIndexingServiceForTest{job: jobs.Job{
				ID: "job-index-1", ProjectID: "project-1", Kind: jobs.KindCanonTextIndex,
				Status: tc.storedStatus, CanCancel: true, Error: tc.jobError,
				Subject: jobs.CanonTextIndexSubject{
					Type: jobs.KindCanonTextIndex, ProjectID: "project-1",
					SourceView: governance.SourceViewDraft, CatalogRevision: 4,
				},
				CreatorUserID: "user-1", CreatedAt: startTestTime(), UpdatedAt: startTestTime(),
			}}
			currentUser := &currentUserForIndexingHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
			router := chi.NewRouter()
			NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)
			req := httptest.NewRequest(http.MethodPost, "/projects/project-1/indexing-runs", strings.NewReader(`{"sourceView":"draft"}`))
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "https://app.example.test")
			req.Header.Set("X-CSRF-Token", "csrf-ok")
			req.Header.Set("Idempotency-Key", "indexing-replay-1")
			req.Header.Set(observability.RequestIDHeader, "unsafe-client-request-id")
			responseRecorder := httptest.NewRecorder()
			observability.RequestLogger(io.Discard, observability.RequestLoggerOptions{
				NewRequestID: func() string { return requestID },
			})(router).ServeHTTP(responseRecorder, req)
			if responseRecorder.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want %d; body: %s", responseRecorder.Code, http.StatusAccepted, responseRecorder.Body.String())
			}
			var response struct {
				Job struct {
					Status    string `json:"status"`
					CanCancel bool   `json:"canCancel"`
					Error     *struct {
						Code      string `json:"code"`
						RequestID string `json:"requestId"`
					} `json:"error"`
					Links struct {
						Cancel *string `json:"cancel"`
					} `json:"links"`
				} `json:"job"`
			}
			if err := json.NewDecoder(responseRecorder.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Job.Status != string(tc.publicStatus) {
				t.Fatalf("job status = %q, want %q", response.Job.Status, tc.publicStatus)
			}
			if response.Job.CanCancel || response.Job.Links.Cancel != nil {
				t.Fatalf("cancel projection = canCancel %v link %v, want false and absent", response.Job.CanCancel, response.Job.Links.Cancel)
			}
			if tc.jobError == nil {
				if response.Job.Error != nil {
					t.Fatalf("error = %#v, want null", response.Job.Error)
				}
			} else if response.Job.Error == nil || response.Job.Error.Code != tc.jobError.Code || response.Job.Error.RequestID != requestID {
				t.Fatalf("error = %#v, want exact code %q and current requestId %q", response.Job.Error, tc.jobError.Code, requestID)
			}
		})
	}
}

func TestHandlersStartIndexingRequiresCSRFAndOriginBeforeService(t *testing.T) {
	service := &startIndexingServiceForTest{}
	currentUser := &currentUserForIndexingHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
	router := chi.NewRouter()
	NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

	req := httptest.NewRequest(http.MethodPost, "/projects/project-1/indexing-runs", strings.NewReader(`{"sourceView":"draft"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.test")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	req.Header.Set("Idempotency-Key", "indexing-1")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if len(service.calls) != 0 {
		t.Fatalf("service was called before origin rejection: %#v", service.calls)
	}
}

func TestIndexingCSRFMiddlewareAuthenticatesSessionBeforeCheckingCSRF(t *testing.T) {
	infrastructureErr := errors.New("session store unavailable")
	tests := []struct {
		name            string
		cookieValue     string
		csrfToken       string
		authenticateErr error
		wantStatus      int
		wantCode        string
	}{
		{name: "missing session without CSRF", authenticateErr: auth.ErrUnauthenticated, wantStatus: http.StatusUnauthorized, wantCode: "auth.session_required"},
		{name: "missing session with CSRF", csrfToken: "csrf-ok", authenticateErr: auth.ErrUnauthenticated, wantStatus: http.StatusUnauthorized, wantCode: "auth.session_required"},
		{name: "expired session", cookieValue: "expired-session", csrfToken: "csrf-ok", authenticateErr: auth.ErrUnauthenticated, wantStatus: http.StatusUnauthorized, wantCode: "auth.session_required"},
		{name: "revoked session", cookieValue: "revoked-session", csrfToken: "csrf-ok", authenticateErr: auth.ErrUnauthenticated, wantStatus: http.StatusUnauthorized, wantCode: "auth.session_required"},
		{name: "valid session without CSRF", cookieValue: "active-session", wantStatus: http.StatusForbidden, wantCode: "csrf.required"},
		{name: "valid session with invalid CSRF", cookieValue: "active-session", csrfToken: "csrf-wrong", wantStatus: http.StatusForbidden, wantCode: "csrf.invalid"},
		{name: "authentication infrastructure error", cookieValue: "active-session", csrfToken: "csrf-ok", authenticateErr: infrastructureErr, wantStatus: http.StatusInternalServerError, wantCode: "server.error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentUser := &currentUserForIndexingHandlerTest{
				userID:          "user-1",
				csrfToken:       "csrf-ok",
				authenticateErr: tt.authenticateErr,
			}
			nextCalled := false
			handler := (Handlers{currentUser: currentUser}).CSRFMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				nextCalled = true
			}))
			req := httptest.NewRequest(http.MethodPost, "/projects/project-1/indexing-runs", nil)
			if tt.cookieValue != "" {
				req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: tt.cookieValue})
			}
			if tt.csrfToken != "" {
				req.Header.Set("X-CSRF-Token", tt.csrfToken)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			var problem struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&problem); err != nil {
				t.Fatalf("decode problem response: %v", err)
			}
			if problem.Code != tt.wantCode {
				t.Fatalf("problem code = %q, want %q", problem.Code, tt.wantCode)
			}
			if nextCalled {
				t.Fatal("next handler was called after authentication rejection")
			}
		})
	}
}

func TestHandlersStartIndexingMapsViewerForbidden(t *testing.T) {
	service := &startIndexingServiceForTest{err: ErrForbidden}
	currentUser := &currentUserForIndexingHandlerTest{userID: "viewer-1", csrfToken: "csrf-ok"}
	router := chi.NewRouter()
	NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

	req := httptest.NewRequest(http.MethodPost, "/projects/project-1/indexing-runs", strings.NewReader(`{"sourceView":"draft"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	req.Header.Set("Idempotency-Key", "indexing-1")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

type startIndexingServiceForTest struct {
	job   jobs.Job
	err   error
	calls []startIndexingCallForTest
}

type startIndexingCallForTest struct {
	actor Actor
	input StartIndexingInput
}

func (s *startIndexingServiceForTest) StartIndexing(_ context.Context, actor Actor, input StartIndexingInput) (jobs.Job, error) {
	s.calls = append(s.calls, startIndexingCallForTest{actor: actor, input: input})
	if s.err != nil {
		return jobs.Job{}, s.err
	}
	return s.job, nil
}

type currentUserForIndexingHandlerTest struct {
	userID          string
	csrfToken       string
	authenticateErr error
}

func (s *currentUserForIndexingHandlerTest) CurrentUser(context.Context, string) (auth.User, error) {
	if s.userID == "" {
		return auth.User{}, auth.ErrSessionNotFound
	}
	return auth.User{ID: s.userID}, nil
}

func (s *currentUserForIndexingHandlerTest) ValidateCSRF(_ context.Context, _ string, token string) error {
	if token != s.csrfToken {
		return auth.ErrInvalidCSRF
	}
	return nil
}

func (s *currentUserForIndexingHandlerTest) AuthenticateWrite(ctx context.Context, cookie, token string) (auth.User, auth.SessionWriteProof, error) {
	if s.authenticateErr != nil {
		return auth.User{}, auth.SessionWriteProof{}, s.authenticateErr
	}
	user, err := s.CurrentUser(ctx, cookie)
	if err != nil {
		return auth.User{}, auth.SessionWriteProof{}, err
	}
	if err := s.ValidateCSRF(ctx, cookie, token); err != nil {
		return auth.User{}, auth.SessionWriteProof{}, err
	}
	return user, auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"}, nil
}

func TestHandlersStartIndexingRejectsMissingIdempotencyKey(t *testing.T) {
	service := &startIndexingServiceForTest{}
	currentUser := &currentUserForIndexingHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
	router := chi.NewRouter()
	NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

	req := httptest.NewRequest(http.MethodPost, "/projects/project-1/indexing-runs", strings.NewReader(`{"sourceView":"draft"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if len(service.calls) != 0 {
		t.Fatalf("service was called without idempotency key: %#v", service.calls)
	}
}

func TestHandlersStartIndexingRejectsInvalidIdempotencyKeyLength(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{name: "too short after trim", key: " short "},
		{name: "too long", key: strings.Repeat("a", 129)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &startIndexingServiceForTest{}
			currentUser := &currentUserForIndexingHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
			router := chi.NewRouter()
			NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

			req := httptest.NewRequest(http.MethodPost, "/projects/project-1/indexing-runs", strings.NewReader(`{"sourceView":"draft"}`))
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "https://app.example.test")
			req.Header.Set("X-CSRF-Token", "csrf-ok")
			req.Header.Set("Idempotency-Key", tc.key)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if len(service.calls) != 0 {
				t.Fatalf("service was called with invalid idempotency key: %#v", service.calls)
			}
		})
	}
}

// The request restarts a whole text version: the former scope and book fields are rejected.
func TestHandlersStartIndexingRejectsScopeFields(t *testing.T) {
	service := &startIndexingServiceForTest{}
	currentUser := &currentUserForIndexingHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
	router := chi.NewRouter()
	NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

	req := httptest.NewRequest(http.MethodPost, "/projects/project-1/indexing-runs", strings.NewReader(`{"scope":"changed_content","sourceView":"draft","bookId":"book-1"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	req.Header.Set("Idempotency-Key", "indexing-1")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if len(service.calls) != 0 {
		t.Fatalf("service was called for a scoped request: %#v", service.calls)
	}
}

func TestHandlersStartIndexingMapsUnexpectedErrorsSafely(t *testing.T) {
	service := &startIndexingServiceForTest{err: errors.New("raw manuscript SHOULD_NOT_LEAK")}
	currentUser := &currentUserForIndexingHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
	router := chi.NewRouter()
	NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

	req := httptest.NewRequest(http.MethodPost, "/projects/project-1/indexing-runs", strings.NewReader(`{"sourceView":"draft"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	req.Header.Set("Idempotency-Key", "indexing-1")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "SHOULD_NOT_LEAK") {
		t.Fatalf("problem response leaked raw error: %s", rec.Body.String())
	}
}
