package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/observability"
	"canonkeeper/backend/internal/ratelimit"
)

func TestRequestRouteInfoNamesPathValuesOfTemplate(t *testing.T) {
	const (
		projectID = "11111111-1111-4111-8111-111111111111"
		jobID     = "22222222-2222-4222-8222-222222222222"
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/jobs/"+jobID+"/agent-timeline", nil)

	route := RequestRouteInfo(req)

	if route.OperationID != "getAgentTimeline" {
		t.Fatalf("route operation = %q, want getAgentTimeline", route.OperationID)
	}
	if len(route.PathValues) != 2 || route.PathValues["projectId"] != projectID || route.PathValues["jobId"] != jobID {
		t.Fatalf("path values = %v, want projectId and jobId", route.PathValues)
	}
}

func TestRequestLoggerAnswersHandlerPanicWithProblemAndKeepsServing(t *testing.T) {
	const jobID = "33333333-3333-4333-8333-333333333333"
	var output bytes.Buffer
	panicked := true
	handler := RequestLogger(&output)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if panicked {
			var missing map[string]int
			missing["job"]++
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil))

	requestID := recorder.Header().Get("X-Request-ID")
	var problem Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("panic response is not problem JSON: %v; body=%q", err, recorder.Body.String())
	}
	if recorder.Code != http.StatusInternalServerError || recorder.Header().Get("Content-Type") != "application/problem+json" ||
		problem.Code != "server.error" || problem.Status != http.StatusInternalServerError ||
		requestID == "" || problem.RequestID != requestID {
		t.Fatalf("panic response = %d %+v, want 500 server.error with request ID %q", recorder.Code, problem, requestID)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"requestId":"`+requestID+`"`) ||
		!strings.Contains(lines[0], `"operationId":"getJob"`) || !strings.Contains(lines[0], `"jobId":"`+jobID+`"`) ||
		!strings.Contains(lines[0], `"status":500`) || !strings.Contains(lines[0], `"errorCause":"panic"`) ||
		!strings.Contains(lines[0], `"panic":"assignment to entry in nil map"`) || !strings.Contains(lines[0], "goroutine") {
		t.Fatalf("request line after panic = %q", lines)
	}

	panicked = false
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("request after panic = %d, want 204", recorder.Code)
	}
}

// specs/2026-10-28-production-monitoring.md R5.1: a read the browser dropped while its session was being read used to
// be a 500 of the limiter in the journal — the page of a drawing cancels its reads. It is the client's 499.
func TestAReadDroppedAtTheSessionStepIsTheClients499(t *testing.T) {
	var output bytes.Buffer
	limiter := ratelimit.NewMemoryLimiter(ratelimit.MemoryLimiterConfig{Limit: 10, Window: time.Minute})
	// The session of a request whose context is cancelled is not read: the principal is empty, as with the product's reader.
	keyFunc := NewRateLimitKeyFunc(RateLimitKeyOptions{PrincipalID: func(r *http.Request) string {
		if r.Context().Err() != nil {
			return ""
		}
		return "0b4c2f2e-2d6c-4f43-9f5d-8b8f3c1a2e11"
	}})
	handler := RequestLogger(&output)(RateLimitMiddleware(limiter, keyFunc)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the handler ran for a request its client dropped before the limiter")
	})))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/11111111-1111-4111-8111-111111111111/images/7", nil).WithContext(ctx)
	request.RemoteAddr = "127.0.0.1:41000"

	handler.ServeHTTP(httptest.NewRecorder(), request)

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("request line is not JSON: %v\n%s", err, output.String())
	}
	if entry["operationId"] != "getProjectImage" || entry["status"] != float64(499) ||
		entry["errorClass"] != "context" || entry["errorCause"] != "canceled" || entry["userId"] != nil {
		t.Fatalf("request line = %v, want getProjectImage 499 context/canceled without a user", entry)
	}
}

// R5.2, R5.3 over a real connection: net/http cancels the context of a request when its client closes the connection, and
// the graceful stop of the server does not — the request of a live client that the stopping process failed stays 5xx.
func TestRequestLineTellsAClientThatLeftFromAStoppingProcess(t *testing.T) {
	const path = "/api/v1/jobs/33333333-3333-4333-8333-333333333333"
	serve := func(t *testing.T, handler http.HandlerFunc) (*httptest.Server, func() map[string]any) {
		output := &lockedBuffer{}
		server := httptest.NewUnstartedServer(RequestLogger(output)(handler))
		server.Start()
		t.Cleanup(server.Close)
		return server, func() map[string]any {
			t.Helper()
			var entry map[string]any
			deadline := time.Now().Add(5 * time.Second)
			for {
				if line := output.String(); strings.HasSuffix(line, "\n") {
					if err := json.Unmarshal([]byte(line), &entry); err != nil {
						t.Fatalf("request line is not JSON: %v\n%s", err, line)
					}
					return entry
				}
				if time.Now().After(deadline) {
					t.Fatal("no request line was written")
				}
				time.Sleep(time.Millisecond)
			}
		}
	}

	t.Run("the client closes the connection", func(t *testing.T) {
		started := make(chan struct{})
		server, line := serve(t, func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			// What a cancelled read gives is not always the cancellation itself.
			observability.ReportUnexpectedError(r.Context(), errors.New("read failed"))
			WriteProblem(w, "", http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
		})
		ctx, leave := context.WithCancel(context.Background())
		defer leave()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		answered := make(chan error, 1)
		go func() {
			response, err := server.Client().Do(request)
			if err == nil {
				_ = response.Body.Close()
			}
			answered <- err
		}()
		<-started
		leave()
		if err := <-answered; !errors.Is(err, context.Canceled) {
			t.Fatalf("the client's request ended with %v, want its own cancellation", err)
		}

		entry := line()
		if entry["status"] != float64(499) || entry["errorClass"] != "context" || entry["errorCause"] != "canceled" {
			t.Fatalf("request line = %v, want 499 context/canceled", entry)
		}
	})

	t.Run("the process stops", func(t *testing.T) {
		started, stopping := make(chan struct{}), make(chan struct{})
		process, stop := context.WithCancel(context.Background())
		defer stop()
		requestErrs := make(chan error, 1)
		server, line := serve(t, func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-stopping
			requestErrs <- r.Context().Err()
			// What the handler works with is cancelled by the stop of the process; the client still waits.
			observability.ReportUnexpectedError(r.Context(), process.Err())
			WriteProblem(w, "", http.StatusServiceUnavailable, "Service Unavailable", "the server is stopping", "server.stopping")
		})
		server.Config.RegisterOnShutdown(func() { close(stopping) })
		answered := make(chan int, 1)
		go func() {
			response, err := server.Client().Get(server.URL + path)
			if err != nil {
				answered <- 0
				return
			}
			_ = response.Body.Close()
			answered <- response.StatusCode
		}()
		<-started
		stop()
		stopped := make(chan error, 1)
		go func() { stopped <- server.Config.Shutdown(context.Background()) }()

		if status := <-answered; status != http.StatusServiceUnavailable {
			t.Fatalf("the live client was answered %d, want 503", status)
		}
		if err := <-stopped; err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		entry, requestErr := line(), <-requestErrs
		if requestErr != nil || entry["status"] != float64(http.StatusServiceUnavailable) ||
			entry["errorClass"] != "context" || entry["errorCause"] != "canceled" {
			t.Fatalf("request context = %v, request line = %v; want a live request and 503 context/canceled", requestErr, entry)
		}
	})
}

// lockedBuffer is the journal of a test server: its request lines are written from the goroutine of a connection.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
