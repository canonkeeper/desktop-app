package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/ratelimit"
)

// SEC-03 / R1.3: behind the proxy one client exhausting an anonymous budget must not lock out another client, and the
// anonymous CSRF token, the browser's reports and the corrections of a turn get their own tighter budgets.
func TestHTTPRateLimitsGiveEachClientOwnAuthBudgets(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	config := RateLimits(nil)
	config.Now = func() time.Time { return now }
	handler := httpapi.RateLimitMiddleware(
		ratelimit.NewMemoryLimiter(config),
		httpapi.NewRateLimitKeyFunc(httpapi.RateLimitKeyOptions{}),
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	send := func(method, path, client string) *http.Response {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "127.0.0.1:41000"
		req.Header.Set("X-Real-IP", client)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Result()
	}

	cases := []struct {
		method     string
		path       string
		limit      int
		retryAfter string
	}{
		// specs/2026-10-30-chat-steering.md R1.8.
		{method: http.MethodPost, path: "/api/v1/chats/chat-1/turns/turn-1/steer", limit: 20, retryAfter: "60"},
		{method: http.MethodGet, path: "/api/v1/auth/csrf", limit: 60, retryAfter: "60"},
		{method: http.MethodPost, path: "/api/v1/client-errors", limit: 30, retryAfter: "60"},
		{method: http.MethodGet, path: "/api/v1/projects", limit: 600, retryAfter: "60"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			for attempt := range tc.limit {
				if status := send(tc.method, tc.path, "203.0.113.7").StatusCode; status != http.StatusNoContent {
					t.Fatalf("attempt %d status = %d, want %d", attempt+1, status, http.StatusNoContent)
				}
			}
			denied := send(tc.method, tc.path, "203.0.113.7")
			if denied.StatusCode != http.StatusTooManyRequests || denied.Header.Get("Retry-After") != tc.retryAfter {
				t.Fatalf("over budget status = %d Retry-After = %q, want 429 and %s", denied.StatusCode, denied.Header.Get("Retry-After"), tc.retryAfter)
			}
			if status := send(tc.method, tc.path, "198.51.100.42").StatusCode; status != http.StatusNoContent {
				t.Fatalf("another client status = %d, want %d", status, http.StatusNoContent)
			}
		})
	}
}
