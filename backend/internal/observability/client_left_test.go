package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// specs/2026-10-28-production-monitoring.md R5: the line of a request its client dropped is 499 with the cause of the
// cancellation, whatever the handler wrote into the closed connection and whatever error the cancelled read gave. A
// failure met with a live client, a timeout of the server, the stop of the process and a panic stay what they are.
func TestRequestLoggerWritesARequestItsClientDroppedAs499(t *testing.T) {
	type line struct {
		Status     int    `json:"status"`
		ErrorClass string `json:"errorClass"`
		ErrorCause string `json:"errorCause"`
		Panic      string `json:"panic"`
	}
	dropped := func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, cancel
	}
	live := func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }
	timedOut := func() (context.Context, context.CancelFunc) {
		return context.WithDeadline(context.Background(), time.Unix(0, 0))
	}
	answer := func(status int, report error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ReportUnexpectedError(r.Context(), report)
			w.WriteHeader(status)
		}
	}
	for _, tc := range []struct {
		name    string
		request func() (context.Context, context.CancelFunc)
		handler http.HandlerFunc
		want    line
	}{
		{
			// The limiter of the session step answers 500 and reports nothing: the reads of a drawing's page were these.
			name: "dropped before the session was read", request: dropped, handler: answer(http.StatusInternalServerError, nil),
			want: line{Status: 499, ErrorClass: "context", ErrorCause: "canceled"},
		},
		{
			name: "dropped during the read, the error names no cancellation", request: dropped,
			handler: answer(http.StatusInternalServerError, errors.New("read failed")),
			want:    line{Status: 499, ErrorClass: "context", ErrorCause: "canceled"},
		},
		{
			name: "dropped during the read, the error is the cancellation", request: dropped,
			handler: answer(http.StatusInternalServerError, context.Canceled),
			want:    line{Status: 499, ErrorClass: "context", ErrorCause: "canceled"},
		},
		{
			name: "dropped in the middle of the handler", request: live,
			handler: func(w http.ResponseWriter, r *http.Request) {
				r.Context().Value(dropKey{}).(context.CancelFunc)()
				ReportUnexpectedError(r.Context(), requestLogSQLStateError{state: "57014"})
				w.WriteHeader(http.StatusInternalServerError)
			},
			want: line{Status: 499, ErrorClass: "context", ErrorCause: "canceled"},
		},
		{
			name: "dropped, the answer was «unavailable»", request: dropped, handler: answer(http.StatusServiceUnavailable, nil),
			want: line{Status: 499, ErrorClass: "context", ErrorCause: "canceled"},
		},
		{
			name: "dropped after the work was done", request: dropped, handler: answer(http.StatusOK, nil),
			want: line{Status: http.StatusOK},
		},
		{
			name: "dropped, the answer is a refusal", request: dropped, handler: answer(http.StatusNotFound, nil),
			want: line{Status: http.StatusNotFound},
		},
		{
			name: "live client, the server failed", request: live, handler: answer(http.StatusInternalServerError, errors.New("read failed")),
			want: line{Status: http.StatusInternalServerError, ErrorClass: "internal", ErrorCause: "unclassified"},
		},
		{
			name: "live client, the server failed and reported nothing", request: live, handler: answer(http.StatusInternalServerError, nil),
			want: line{Status: http.StatusInternalServerError},
		},
		{
			// The stop of the process cancels what the handler works with, not the request: its client waits for an answer.
			name: "live client, the process stops", request: live, handler: answer(http.StatusServiceUnavailable, context.Canceled),
			want: line{Status: http.StatusServiceUnavailable, ErrorClass: "context", ErrorCause: "canceled"},
		},
		{
			name: "timeout of the server", request: timedOut, handler: answer(http.StatusInternalServerError, context.DeadlineExceeded),
			want: line{Status: http.StatusInternalServerError, ErrorClass: "context", ErrorCause: "deadline_exceeded"},
		},
		{
			name: "timeout of the server, nothing reported", request: timedOut, handler: answer(http.StatusInternalServerError, nil),
			want: line{Status: http.StatusInternalServerError},
		},
		{
			name: "timeout of the server, the client left as well", request: dropped,
			handler: answer(http.StatusInternalServerError, context.DeadlineExceeded),
			want:    line{Status: http.StatusInternalServerError, ErrorClass: "context", ErrorCause: "deadline_exceeded"},
		},
		{
			name: "panic, the client left as well", request: dropped,
			handler: func(http.ResponseWriter, *http.Request) { panic("handler invariant broken") },
			want:    line{Status: http.StatusInternalServerError, ErrorClass: "internal", ErrorCause: "panic", Panic: "handler invariant broken"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			handler := RequestLogger(&output, RequestLoggerOptions{
				NewRequestID: func() string { return "01234567-89ab-4cde-8fab-0123456789ab" },
				RouteInfo: func(*http.Request) RouteInfo {
					return RouteInfo{OperationID: "getProjectImage", PathTemplate: "/projects/{projectId}/images/{number}"}
				},
			})(tc.handler)
			ctx, cancel := tc.request()
			defer cancel()
			request := httptest.NewRequest(http.MethodGet, "/projects/project-1/images/1", nil)
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request.WithContext(context.WithValue(ctx, dropKey{}, cancel)))

			var got line
			if err := json.Unmarshal(output.Bytes(), &got); err != nil {
				t.Fatalf("request line is not JSON: %v\n%s", err, output.String())
			}
			if got != tc.want {
				t.Fatalf("request line = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// dropKey carries to a handler of the test the cancellation of its request: the client leaving in the middle of it.
type dropKey struct{}
