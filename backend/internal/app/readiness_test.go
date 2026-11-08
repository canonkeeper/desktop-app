package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadiness(t *testing.T) {
	rawPingError := errors.New("raw ping failure")
	tests := []struct {
		name           string
		productError   error
		workerEnabled  bool
		workerError    error
		chatDown       bool
		wantStatus     int
		wantBody       string
		wantWorkerPing bool
	}{
		{
			name:       "ok without worker",
			wantStatus: http.StatusOK,
			wantBody:   `{"status":"ok"}`,
		},
		{
			name:           "ok with worker",
			workerEnabled:  true,
			wantStatus:     http.StatusOK,
			wantBody:       `{"status":"ok"}`,
			wantWorkerPing: true,
		},
		{
			name:           "degraded when worker database is unavailable",
			workerEnabled:  true,
			workerError:    rawPingError,
			wantStatus:     http.StatusOK,
			wantBody:       `{"status":"degraded","reason":"worker_database_unavailable"}`,
			wantWorkerPing: true,
		},
		{
			name:           "degraded when chat runtime is unavailable",
			workerEnabled:  true,
			chatDown:       true,
			wantStatus:     http.StatusOK,
			wantBody:       `{"status":"degraded","reason":"chat_runtime_unavailable"}`,
			wantWorkerPing: true,
		},
		{
			name:          "down when product database is unavailable",
			productError:  rawPingError,
			workerEnabled: true,
			wantStatus:    http.StatusServiceUnavailable,
			wantBody:      `{"status":"down","reason":"product_database_unavailable"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			productPings := 0
			workerPings := 0
			productPing := func(ctx context.Context) error {
				productPings++
				assertReadinessDeadline(t, ctx)
				return test.productError
			}
			var workerPing databasePing
			if test.workerEnabled {
				workerPing = func(ctx context.Context) error {
					workerPings++
					assertReadinessDeadline(t, ctx)
					return test.workerError
				}
			}
			handler := withReadiness(http.NotFoundHandler(), productPing, workerPing, func() bool { return !test.chatDown })
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if response.Body.String() != test.wantBody {
				t.Fatalf("body = %q, want %q", response.Body.String(), test.wantBody)
			}
			if response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", response.Header().Get("Content-Type"))
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
			}
			if strings.Contains(response.Body.String(), rawPingError.Error()) {
				t.Fatal("response exposes raw Ping error")
			}
			if productPings != 1 {
				t.Fatalf("product pings = %d, want 1", productPings)
			}
			wantWorkerPings := 0
			if test.wantWorkerPing {
				wantWorkerPings = 1
			}
			if workerPings != wantWorkerPings {
				t.Fatalf("worker pings = %d, want %d", workerPings, wantWorkerPings)
			}
		})
	}
}

func TestReadinessForwardsOtherRequests(t *testing.T) {
	next := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusAccepted)
		_, _ = fmt.Fprintf(response, "%s %s", request.Method, request.URL.Path)
	})
	handler := withReadiness(next, func(context.Context) error {
		t.Fatal("forwarded request must not ping the database")
		return nil
	}, nil, func() bool {
		t.Fatal("forwarded request must not probe the chat runtime")
		return true
	})

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/healthz", nil),
		httptest.NewRequest(http.MethodPost, "/readyz", nil),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("%s %s status = %d, want %d", request.Method, request.URL.Path, response.Code, http.StatusAccepted)
		}
		wantBody := fmt.Sprintf("%s %s", request.Method, request.URL.Path)
		if response.Body.String() != wantBody {
			t.Fatalf("%s %s body = %q, want %q", request.Method, request.URL.Path, response.Body.String(), wantBody)
		}
	}
}

func assertReadinessDeadline(t *testing.T, ctx context.Context) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("Ping context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > readinessDatabaseTimeout {
		t.Fatalf("Ping deadline remaining = %s, want (0, %s]", remaining, readinessDatabaseTimeout)
	}
}
