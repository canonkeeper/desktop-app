package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
