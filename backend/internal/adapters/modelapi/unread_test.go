package modelapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// specs/2026-11-15-desk-reading-rest.md RR1.1: the client of the app names why no answer came — a refusal the gateway named
// refused, the provider that did not answer after the last attempt — by the types of the domain; the refusals of the gateway
// itself and whatever it did not name stay untyped, a stop and a cut answer are as before, and the client of the site keeps
// its errors.
func TestTheClientOfTheAppNamesWhyNoAnswerCame(t *testing.T) {
	refusal := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			fmt.Fprint(w, body)
		}
	}
	stream := func(events string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, events)
		}
	}
	for _, answer := range []struct {
		name    string
		handler http.HandlerFunc
		want    error
		calls   int32
	}{
		{"a refusal named refused", refusal(http.StatusBadRequest, `{"error":{"type":"x","code":"context_length_exceeded","reason":"refused","message":"too long"}}`), sagamemory.ErrRefused, 1},
		{"a refusal named refused in the stream", stream("data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"upstream_error\",\"reason\":\"refused\",\"message\":\"upstream (HTTP 400): too long\"}}}\n\n"), sagamemory.ErrRefused, 1},
		{"429 to the last attempt", refusal(http.StatusTooManyRequests, `{"error":{"type":"rate_limit_exceeded","code":"rate_limit_exceeded","message":"slow down"}}`), sagamemory.ErrNoAnswer, 6},
		{"503 to the last attempt", refusal(http.StatusServiceUnavailable, `{"error":{"type":"x","code":"x","message":"down"}}`), sagamemory.ErrNoAnswer, 6},
		{"502 of the gateway", refusal(http.StatusBadGateway, `{"error":{"type":"upstream_error","code":"upstream_error","message":"the provider could not be asked"}}`), sagamemory.ErrNoAnswer, 6},
		{"a stream torn mid-answer", stream("data: {\"type\":\"response.output_text.delta\",\"delta\":\"{\\\"persons\"}\n\n"), sagamemory.ErrNoAnswer, 6},
		{"helper.not_connected", refusal(http.StatusBadRequest, `{"error":{"type":"helper.not_connected","code":"helper.not_connected","message":"the helper is not connected"}}`), nil, 1},
		{"401 of the gateway", refusal(http.StatusUnauthorized, `{"error":{"type":"unauthorized","code":"unauthorized","message":"no token"}}`), nil, 1},
		{"403 of the gateway", refusal(http.StatusForbidden, `{"error":{"type":"forbidden","code":"forbidden","message":"origin"}}`), nil, 1},
		{"421 of the gateway", refusal(http.StatusMisdirectedRequest, `{"error":{"type":"misdirected","code":"misdirected","message":"host"}}`), nil, 1},
		{"a 400 nobody named", refusal(http.StatusBadRequest, `{"error":{"type":"invalid_request","code":"invalid_request","message":"bad"}}`), nil, 1},
	} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			answer.handler(w, r)
		}))
		app, err := New(Config{BaseURL: server.URL + "/v1", Key: "key-test-gateway-0005", Model: "canonkeeper-cards", CarryJob: true, Stops: true})
		if err != nil {
			t.Fatal(err)
		}
		app.pause = func(int) time.Duration { return 0 }
		_, _, err = app.Background().Complete(t.Context(), "инструкция", "вход")
		refused, silent := errors.Is(err, sagamemory.ErrRefused), errors.Is(err, sagamemory.ErrNoAnswer)
		_, stopped := sagamemory.StopOf(err)
		switch {
		case err == nil || stopped || errors.Is(err, sagamemory.ErrAnswerCut):
			t.Errorf("%s = %v", answer.name, err)
		case answer.want == nil && (refused || silent):
			t.Errorf("%s is typed: %v", answer.name, err)
		case answer.want != nil && !errors.Is(err, answer.want), refused && silent:
			t.Errorf("%s = %v; want %v", answer.name, err, answer.want)
		case calls.Load() != answer.calls:
			t.Errorf("%s was asked %d times; want %d", answer.name, calls.Load(), answer.calls)
		}
		// The client of the site has no types of the reading: the same answers end as they ended.
		site, _ := New(Config{BaseURL: server.URL, Model: "m"})
		site.pause = func(int) time.Duration { return 0 }
		if _, _, err := site.Background().Complete(t.Context(), "", "x"); err == nil || errors.Is(err, sagamemory.ErrRefused) || errors.Is(err, sagamemory.ErrNoAnswer) {
			t.Errorf("the site on %s = %v", answer.name, err)
		}
		server.Close()
	}
}

// RR1.1: a stop and a cut answer are what they were — neither refused nor no answer.
func TestAStopAndACutAnswerAreNoOtherReason(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"funds": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{"error":{"type":"http_402","code":"http_402","reason":"funds","message":"no money"}}`)
		},
		"cut": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n")
		},
	} {
		server := httptest.NewServer(handler)
		app, _ := New(Config{BaseURL: server.URL + "/v1", Key: "key-test-gateway-0006", Model: "canonkeeper-cards", CarryJob: true, Stops: true})
		app.pause = func(int) time.Duration { return 0 }
		_, _, err := app.Background().Complete(t.Context(), "инструкция", "вход")
		_, stopped := sagamemory.StopOf(err)
		if errors.Is(err, sagamemory.ErrRefused) || errors.Is(err, sagamemory.ErrNoAnswer) ||
			name == "funds" && !stopped || name == "cut" && !errors.Is(err, sagamemory.ErrAnswerCut) {
			t.Errorf("%s = %v", name, err)
		}
		server.Close()
	}
}
