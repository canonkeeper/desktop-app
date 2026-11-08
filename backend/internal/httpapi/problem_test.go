package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteProblemWritesProblemJSON(t *testing.T) {
	recorder := httptest.NewRecorder()

	WriteProblem(recorder, "req-123", http.StatusForbidden, "Forbidden", "missing role", "auth.forbidden")

	result := recorder.Result()
	defer result.Body.Close()

	if result.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want %q", result.Header.Get("Content-Type"), "application/problem+json")
	}
	if result.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusForbidden)
	}

	var got Problem
	if err := json.NewDecoder(result.Body).Decode(&got); err != nil {
		t.Fatalf("decode problem response: %v", err)
	}

	want := Problem{
		Type:      "about:blank",
		Title:     "Forbidden",
		Status:    http.StatusForbidden,
		Detail:    "missing role",
		Code:      "auth.forbidden",
		RequestID: "req-123",
	}
	if got != want {
		t.Fatalf("problem = %+v, want %+v", got, want)
	}
}

func TestWriteProblemUsesMiddlewareRequestIDWhenCallerOmitsIt(t *testing.T) {
	recorder := httptest.NewRecorder()
	recorder.Header().Set("X-Request-ID", "7fff61aa-fcba-43e6-abd0-ecfbab09f403")
	WriteProblem(recorder, "", http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	var got Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.RequestID != recorder.Header().Get("X-Request-ID") {
		t.Fatalf("requestId = %q, want the middleware correlation", got.RequestID)
	}
}

func TestProblemJSONTags(t *testing.T) {
	body, err := json.Marshal(Problem{
		Type:      "about:blank",
		Title:     "Bad Request",
		Status:    http.StatusBadRequest,
		Code:      "validation.failed",
		RequestID: "req-456",
	})
	if err != nil {
		t.Fatalf("marshal problem: %v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("unmarshal problem: %v", err)
	}

	for _, field := range []string{"type", "title", "status", "code", "requestId"} {
		if _, exists := fields[field]; !exists {
			t.Fatalf("field %q missing from problem JSON: %s", field, body)
		}
	}
	if _, exists := fields["detail"]; exists {
		t.Fatalf("detail should be omitted when empty: %s", body)
	}
}
