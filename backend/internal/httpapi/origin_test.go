package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStrictOriginMiddlewareRejectsUnsafeCrossOriginRequests(t *testing.T) {
	nextCalled := false
	next := StrictOriginMiddleware("https://app.example.test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "https://evil.example.test")
	rec := httptest.NewRecorder()

	next.ServeHTTP(rec, req)

	if nextCalled {
		t.Fatal("next handler was called for cross-origin unsafe request")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
}

func TestStrictOriginMiddlewareAllowsConfiguredOrigin(t *testing.T) {
	next := StrictOriginMiddleware("https://app.example.test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "https://app.example.test")
	rec := httptest.NewRecorder()

	next.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestStrictOriginMiddlewareAllowsSafeRequestsWithoutOrigin(t *testing.T) {
	next := StrictOriginMiddleware("https://app.example.test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	rec := httptest.NewRecorder()

	next.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestStrictOriginRequiredMiddlewareAllowsNoOriginSameOriginFetch(t *testing.T) {
	next := StrictOriginRequiredMiddleware("https://app.example.test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-1/result", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()

	next.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestStrictOriginRequiredMiddlewareRejectsNoOriginCrossSiteFetch(t *testing.T) {
	nextCalled := false
	next := StrictOriginRequiredMiddleware("https://app.example.test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-1/result", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()

	next.ServeHTTP(rec, req)

	if nextCalled {
		t.Fatal("next handler was called for cross-site request without Origin")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestStrictOriginRequiredMiddlewareRejectsBadOriginEvenWithSameOriginFetch(t *testing.T) {
	nextCalled := false
	next := StrictOriginRequiredMiddleware("https://app.example.test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-1/result", nil)
	req.Header.Set("Origin", "https://evil.example.test")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()

	next.ServeHTTP(rec, req)

	if nextCalled {
		t.Fatal("next handler was called for disallowed Origin")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
