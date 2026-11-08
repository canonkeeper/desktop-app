package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/ratelimit"
)

func TestRateLimitKeyUsesStableOperationForUnmatchedRoutes(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/raw-project-id/unregistered/raw-child-id", nil)
	req.RemoteAddr = "192.0.2.20:1234"

	key := NewRateLimitKeyFunc(RateLimitKeyOptions{})(req)

	if key.Operation != "unmatchedRoute" {
		t.Fatalf("operation = %q, want unmatchedRoute", key.Operation)
	}
	for _, raw := range []string{"raw-project-id", "raw-child-id", "/projects/raw-project-id"} {
		if strings.Contains(key.String(), raw) {
			t.Fatalf("rate limit key %q leaked high-cardinality unmatched path fragment %q", key.String(), raw)
		}
	}
}

func TestJobCancelRouteInfoIsRegisteredForLoggingAndRateLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/job-1/cancel", nil)
	req.RemoteAddr = "192.0.2.21:1234"

	routeInfo := RequestRouteInfo(req)
	if routeInfo.OperationID != "cancelJob" {
		t.Fatalf("route operation = %q, want cancelJob", routeInfo.OperationID)
	}
	if routeInfo.PathTemplate != "/jobs/{jobId}/cancel" {
		t.Fatalf("route path template = %q, want /jobs/{jobId}/cancel", routeInfo.PathTemplate)
	}

	key := NewRateLimitKeyFunc(RateLimitKeyOptions{
		PrincipalID: func(*http.Request) string {
			return "user-1"
		},
	})(req)
	wantKey := ratelimit.AuthenticatedKey("user-1", "", "cancelJob")
	if key != wantKey {
		t.Fatalf("rate limit key = %+v, want %+v", key, wantKey)
	}
}
