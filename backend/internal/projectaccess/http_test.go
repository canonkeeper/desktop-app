package projectaccess

import (
	"encoding/json"
	"net/http"
	"testing"

	"canonkeeper/backend/internal/httpapi"
)

func TestWriteProblemClosedDecisionContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		decision   Decision
		wantStatus int
		wantTitle  string
		wantDetail string
		wantCode   string
	}{
		{name: "unauthenticated", decision: DecisionUnauthenticated, wantStatus: http.StatusUnauthorized, wantTitle: "Unauthorized", wantDetail: "authentication required", wantCode: "auth.session_required"},
		{name: "not found", decision: DecisionNotFound, wantStatus: http.StatusNotFound, wantTitle: "Not Found", wantDetail: "resource not found", wantCode: "not_found"},
		{name: "forbidden", decision: DecisionForbidden, wantStatus: http.StatusForbidden, wantTitle: "Forbidden", wantDetail: "access denied", wantCode: "forbidden"},
		{name: "unknown fails closed", decision: Decision("unknown"), wantStatus: http.StatusInternalServerError, wantTitle: "Internal Server Error", wantDetail: "unexpected authorization decision", wantCode: "server.error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &trackingResponseWriter{header: make(http.Header)}
			WriteProblem(writer, tt.decision, "request-123")
			if writer.status != tt.wantStatus {
				t.Fatalf("WriteProblem() status = %d, want %d", writer.status, tt.wantStatus)
			}
			if got := writer.header.Get("Content-Type"); got != "application/problem+json" {
				t.Fatalf("Content-Type = %q", got)
			}
			var problem httpapi.Problem
			if err := json.Unmarshal(writer.body, &problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			want := httpapi.Problem{
				Type: "about:blank", Title: tt.wantTitle, Status: tt.wantStatus,
				Detail: tt.wantDetail, Code: tt.wantCode, RequestID: "request-123",
			}
			if problem != want {
				t.Fatalf("problem = %#v, want %#v", problem, want)
			}
		})
	}
}

func TestWriteProblemAllowedDoesNotWrite(t *testing.T) {
	t.Parallel()

	writer := &trackingResponseWriter{header: make(http.Header)}
	WriteProblem(writer, DecisionAllowed, "request-123")
	if writer.wroteHeader || len(writer.body) != 0 || len(writer.header) != 0 {
		t.Fatalf("allowed decision wrote response: %#v", writer)
	}
}

type trackingResponseWriter struct {
	header      http.Header
	status      int
	body        []byte
	wroteHeader bool
}

func (w *trackingResponseWriter) Header() http.Header { return w.header }

func (w *trackingResponseWriter) WriteHeader(status int) {
	w.status = status
	w.wroteHeader = true
}

func (w *trackingResponseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	w.body = append(w.body, body...)
	return len(body), nil
}
