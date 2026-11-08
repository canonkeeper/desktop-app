package ratelimit_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/ratelimit"
)

func TestRateLimiterDeniesSamePrincipalProjectOperationOverBudget(t *testing.T) {
	clock := newRateLimitTestClock(time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC))
	limiter := ratelimit.NewMemoryLimiter(ratelimit.MemoryLimiterConfig{
		Limit:  2,
		Window: time.Minute,
		Now:    clock.Now,
	})
	key := ratelimit.AuthenticatedKey("user-1", "project-1", "startProjectIndexing")

	mustAllowRateLimit(t, limiter, key)
	mustAllowRateLimit(t, limiter, key)

	decision, err := limiter.Allow(context.Background(), key)
	if err != nil {
		t.Fatalf("allow over budget: %v", err)
	}
	if decision.Allowed {
		t.Fatal("third request with same principal/project/operation was allowed")
	}
	if decision.RetryAfter != time.Minute {
		t.Fatalf("RetryAfter = %v, want %v", decision.RetryAfter, time.Minute)
	}
	if !decision.RetryAt.Equal(clock.Now().Add(time.Minute)) {
		t.Fatalf("RetryAt = %v, want %v", decision.RetryAt, clock.Now().Add(time.Minute))
	}
}

func TestRateLimiterKeepsDifferentOperationsSeparate(t *testing.T) {
	clock := newRateLimitTestClock(time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC))
	limiter := ratelimit.NewMemoryLimiter(ratelimit.MemoryLimiterConfig{
		Limit:  1,
		Window: time.Minute,
		Now:    clock.Now,
	})

	mustAllowRateLimit(t, limiter, ratelimit.AuthenticatedKey("user-1", "project-1", "startProjectIndexing"))
	mustAllowRateLimit(t, limiter, ratelimit.AuthenticatedKey("user-1", "project-1", "importBookFile"))
}

func TestRateLimiterAppliesOperationRuleInsteadOfDefault(t *testing.T) {
	clock := newRateLimitTestClock(time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC))
	limiter := ratelimit.NewMemoryLimiter(ratelimit.MemoryLimiterConfig{
		Limit:  3,
		Window: time.Minute,
		Operations: map[string]ratelimit.Rule{
			"registerUser": {Limit: 1, Window: time.Hour},
		},
		Now: clock.Now,
	})
	register := ratelimit.AnonymousKey("registerUser", "203.0.113.7")

	mustAllowRateLimit(t, limiter, register)
	decision, err := limiter.Allow(context.Background(), register)
	if err != nil {
		t.Fatalf("allow over operation budget: %v", err)
	}
	if decision.Allowed || decision.RetryAfter != time.Hour {
		t.Fatalf("second registerUser decision = %+v, want denied for an hour", decision)
	}

	other := ratelimit.AnonymousKey("listProjects", "203.0.113.7")
	for range 3 {
		mustAllowRateLimit(t, limiter, other)
	}
	if decision, err := limiter.Allow(context.Background(), other); err != nil || decision.Allowed || decision.RetryAfter != time.Minute {
		t.Fatalf("default rule decision = %+v, %v, want denied for a minute", decision, err)
	}
}

func TestRateLimiterRejectsInvalidOperationRule(t *testing.T) {
	limiter := ratelimit.NewMemoryLimiter(ratelimit.MemoryLimiterConfig{
		Limit:      3,
		Window:     time.Minute,
		Operations: map[string]ratelimit.Rule{"loginUser": {Limit: 0, Window: time.Minute}},
	})

	if _, err := limiter.Allow(context.Background(), ratelimit.AnonymousKey("loginUser", "203.0.113.7")); err != ratelimit.ErrInvalidConfig {
		t.Fatalf("Allow() error = %v, want ErrInvalidConfig", err)
	}
}

func TestRateLimitAnonymousAuthKeyUsesIPBucketOnly(t *testing.T) {
	clock := newRateLimitTestClock(time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC))
	limiter := ratelimit.NewMemoryLimiter(ratelimit.MemoryLimiterConfig{
		Limit:  1,
		Window: time.Minute,
		Now:    clock.Now,
	})
	keyFunc := httpapi.NewRateLimitKeyFunc(httpapi.RateLimitKeyOptions{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "203.0.113.10:49152"
	req.AddCookie(&http.Cookie{Name: "ck_session", Value: "forged-session-a"})
	key := keyFunc(req)

	if key.PrincipalID != ratelimit.AnonymousPrincipal {
		t.Fatalf("PrincipalID = %q, want anonymous principal", key.PrincipalID)
	}
	if key.Operation != "loginUser" {
		t.Fatalf("Operation = %q, want loginUser", key.Operation)
	}
	if !strings.Contains(key.BucketID, "ip:203.0.113.10") {
		t.Fatalf("BucketID = %q, want IP bucket", key.BucketID)
	}
	if strings.Contains(key.BucketID, "session:") {
		t.Fatalf("BucketID = %q, want no anonymous session bucket", key.BucketID)
	}

	mustAllowRateLimit(t, limiter, key)
	otherReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	otherReq.RemoteAddr = "203.0.113.10:49152"
	otherReq.AddCookie(&http.Cookie{Name: "ck_session", Value: "forged-session-b"})
	otherKey := keyFunc(otherReq)
	if otherKey != key {
		t.Fatalf("anonymous keys with different forged cookies differ: %+v vs %+v", otherKey, key)
	}
	decision, err := limiter.Allow(context.Background(), otherKey)
	if err != nil {
		t.Fatalf("allow same anonymous key: %v", err)
	}
	if decision.Allowed {
		t.Fatal("same anonymous auth IP bucket was allowed over budget")
	}

	differentIPReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	differentIPReq.RemoteAddr = "203.0.113.11:49152"
	differentIPReq.AddCookie(&http.Cookie{Name: "ck_session", Value: "forged-session-a"})
	mustAllowRateLimit(t, limiter, keyFunc(differentIPReq))
}

func TestRateLimitKeyOmitsSessionSecret(t *testing.T) {
	secret := "raw-session-value-that-must-not-appear"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "203.0.113.10:49152"
	req.AddCookie(&http.Cookie{Name: "ck_session", Value: secret})

	key := httpapi.NewRateLimitKeyFunc(httpapi.RateLimitKeyOptions{})(req)
	encoded := key.String()

	if strings.Contains(encoded, secret) {
		t.Fatalf("limiter key exposed raw session value: %q", encoded)
	}
}

func TestRateLimitMiddlewareWritesProblemAndRetryAfter(t *testing.T) {
	limiter := &recordingLimiter{
		decision: ratelimit.Decision{
			Allowed:    false,
			RetryAfter: 3 * time.Second,
			RetryAt:    time.Date(2026, 7, 6, 10, 0, 3, 0, time.UTC),
		},
	}
	key := ratelimit.AuthenticatedKey("user-1", "project-1", "startProjectIndexing")
	handler := httpapi.RateLimitMiddleware(limiter, func(*http.Request) ratelimit.Key {
		return key
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run after denied rate limit decision")
	}))

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/project-1/indexing-runs", nil)
	req.Header.Set("X-Request-ID", "req-rate-1")
	handler.ServeHTTP(recorder, req)

	result := recorder.Result()
	defer result.Body.Close()

	if result.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusTooManyRequests)
	}
	if result.Header.Get("Retry-After") != "3" {
		t.Fatalf("Retry-After = %q, want 3", result.Header.Get("Retry-After"))
	}
	if limiter.key != key {
		t.Fatalf("limiter key = %+v, want %+v", limiter.key, key)
	}

	var problem httpapi.Problem
	if err := json.NewDecoder(result.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Status != http.StatusTooManyRequests {
		t.Fatalf("problem status = %d, want %d", problem.Status, http.StatusTooManyRequests)
	}
	if problem.Code != "rate_limit.exceeded" {
		t.Fatalf("problem code = %q, want rate_limit.exceeded", problem.Code)
	}
	if problem.RequestID != "req-rate-1" {
		t.Fatalf("problem requestId = %q, want req-rate-1", problem.RequestID)
	}
}

type rateLimitTestClock struct {
	now time.Time
}

func newRateLimitTestClock(now time.Time) *rateLimitTestClock {
	return &rateLimitTestClock{now: now}
}

func (clock *rateLimitTestClock) Now() time.Time {
	return clock.now
}

func mustAllowRateLimit(t *testing.T, limiter ratelimit.Limiter, key ratelimit.Key) {
	t.Helper()

	decision, err := limiter.Allow(context.Background(), key)
	if err != nil {
		t.Fatalf("allow rate limit key: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("rate limit key denied unexpectedly: retry after %v", decision.RetryAfter)
	}
}

type recordingLimiter struct {
	decision ratelimit.Decision
	key      ratelimit.Key
}

func (limiter *recordingLimiter) Allow(_ context.Context, key ratelimit.Key) (ratelimit.Decision, error) {
	limiter.key = key
	return limiter.decision, nil
}
