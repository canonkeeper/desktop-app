package modelapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// specs/2026-11-15-desk-reading-no-answer.md NA1.1: a request of the reading the gateway of the app let go under the check of
// the consent — its answer carries CheckedHeader — that nobody answers through the last attempt of the client (a wait, a 5xx,
// a 502 of the gateway, a stream torn mid-answer, a response.failed without a reason) ends in the typed stop «провайдер не
// отвечает», which the builders end their Job with: the gateway paused the reading on that attempt. An answer on a later
// attempt stops nothing; no connection keeps its own stop and a refusal of the request its type. Without the mark — the way
// of the helper of the account — the client names no answer as before (RR1.1), and the client of the site keeps its errors.
func TestAProviderThatDoesNotAnswerStopsTheAppWhereTheGatewayPausedIt(t *testing.T) {
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
	}{
		{"429", refusal(http.StatusTooManyRequests, `{"error":{"type":"rate_limit_exceeded","code":"rate_limit_exceeded","message":"slow down"}}`)},
		{"503", refusal(http.StatusServiceUnavailable, `{"error":{"type":"upstream_error","code":"upstream_error","message":"overloaded"}}`)},
		{"502 of the gateway", refusal(http.StatusBadGateway, `{"error":{"type":"upstream_error","code":"upstream_error","message":"the provider could not be asked"}}`)},
		{"a stream torn mid-answer", stream("data: {\"type\":\"response.output_text.delta\",\"delta\":\"{\\\"summary\"}\n\n")},
		{"response.failed without a reason", stream("data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"stream_incomplete\",\"message\":\"upstream stream disconnected before [DONE]\"}}}\n\n")},
	} {
		var mu sync.Mutex
		var lasts []string
		marked := atomic.Bool{}
		marked.Store(true)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			lasts = append(lasts, r.Header.Get(LastAttemptHeader))
			mu.Unlock()
			if marked.Load() {
				w.Header().Set(CheckedHeader, "1")
			}
			answer.handler(w, r)
		}))
		app, err := New(Config{BaseURL: server.URL + "/v1", Key: "key-test-gateway-0007", Model: "canonkeeper-cards", CarryJob: true, Stops: true})
		if err != nil {
			t.Fatal(err)
		}
		app.pause = func(int) time.Duration { return 0 }
		_, _, err = app.Background().Complete(t.Context(), "инструкция", "вход")
		if stop, ok := sagamemory.StopOf(err); !ok || stop != sagamemory.StopNoAnswer || stop.Code() != "reading.no_answer" ||
			errors.Is(err, sagamemory.ErrNoAnswer) || errors.Is(err, sagamemory.ErrRefused) {
			t.Errorf("%s = %v; want the stop «провайдер не отвечает»", answer.name, err)
		}
		if len(lasts) != 6 || lasts[5] != "1" || lasts[4] != "" {
			t.Errorf("%s: the attempts said %q", answer.name, lasts)
		}

		// The way of the helper of the account carries no mark: no answer is named as it was.
		marked.Store(false)
		_, _, err = app.Background().Complete(t.Context(), "инструкция", "вход")
		if _, stopped := sagamemory.StopOf(err); stopped || !errors.Is(err, sagamemory.ErrNoAnswer) {
			t.Errorf("%s without the mark = %v; want no answer", answer.name, err)
		}

		// The client of the site has no types of the reading, whatever the headers.
		marked.Store(true)
		site, _ := New(Config{BaseURL: server.URL, Model: "m"})
		site.pause = func(int) time.Duration { return 0 }
		if _, _, err := site.Background().Complete(t.Context(), "", "x"); err == nil || errors.Is(err, sagamemory.ErrNoAnswer) {
			t.Errorf("the site on %s = %v", answer.name, err)
		} else if _, stopped := sagamemory.StopOf(err); stopped {
			t.Errorf("the site on %s stopped: %v", answer.name, err)
		}
		server.Close()
	}

	// An answer on the second attempt stops nothing; no connection keeps its stop; a refusal of the request keeps its type.
	for _, step := range []struct {
		name    string
		handler func(call int32, w http.ResponseWriter)
		check   func(text string, err error, calls int32) bool
	}{
		{"an answer on the second attempt", func(call int32, w http.ResponseWriter) {
			if call == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"error":{"type":"upstream_error","code":"upstream_error","message":"overloaded"}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Ответ\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n")
		}, func(text string, err error, calls int32) bool {
			return err == nil && text == "Ответ" && calls == 2
		}},
		{"no connection", func(_ int32, w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":{"type":"connection_error","code":"connection_error","reason":"connection","message":"no connection"}}`)
		}, func(_ string, err error, calls int32) bool {
			stop, ok := sagamemory.StopOf(err)
			return ok && stop == sagamemory.StopConnection && calls == 6
		}},
		{"a refusal of the request", func(_ int32, w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"type":"x","code":"context_length_exceeded","reason":"refused","message":"too long"}}`)
		}, func(_ string, err error, calls int32) bool {
			return errors.Is(err, sagamemory.ErrRefused) && calls == 1
		}},
	} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(CheckedHeader, "1")
			step.handler(calls.Add(1), w)
		}))
		app, err := New(Config{BaseURL: server.URL + "/v1", Key: "key-test-gateway-0008", Model: "canonkeeper-cards", CarryJob: true, Stops: true})
		if err != nil {
			t.Fatal(err)
		}
		app.pause = func(int) time.Duration { return 0 }
		if text, _, err := app.Background().Complete(t.Context(), "инструкция", "вход"); !step.check(text, err, calls.Load()) {
			t.Errorf("%s = %q, %v after %d calls", step.name, text, err, calls.Load())
		}
		server.Close()
	}
}

// specs/2026-11-15-desk-reading-no-answer.md NA2.1: a refusal of a state that passes — a timeout (408), a conflict (409), a
// lock (423), too early (425) — is no refusal of the request: the client of the app repeats it, as a 5xx, and on the last
// attempt of a request the gateway let go it ends in the stop «провайдер не отвечает». The client of the site repeats it no
// more than before: once asked, its error as it was.
func TestTheAppRepeatsARefusalAStateThatPassesGave(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusConflict, http.StatusLocked, http.StatusTooEarly} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set(CheckedHeader, "1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"type":"x","code":"x","message":"not now"}}`)
		}))
		app, err := New(Config{BaseURL: server.URL + "/v1", Key: "key-test-gateway-0009", Model: "canonkeeper-cards", CarryJob: true, Stops: true})
		if err != nil {
			t.Fatal(err)
		}
		app.pause = func(int) time.Duration { return 0 }
		_, _, err = app.Background().Complete(t.Context(), "инструкция", "вход")
		if stop, ok := sagamemory.StopOf(err); !ok || stop != sagamemory.StopNoAnswer || calls.Load() != 6 {
			t.Errorf("%d: the app = %v after %d calls; want six attempts and the stop «провайдер не отвечает»", status, err, calls.Load())
		}
		calls.Store(0)
		site, _ := New(Config{BaseURL: server.URL, Model: "m"})
		site.pause = func(int) time.Duration { return 0 }
		if _, _, err := site.Background().Complete(t.Context(), "", "x"); err == nil || calls.Load() != 1 {
			t.Errorf("%d: the site = %v after %d calls; want one call, as before", status, err, calls.Load())
		}
		server.Close()
	}
}

// specs/2026-11-15-desk-reading-queue.md RQ3.1: what a repeat cures is one rule, aisettings.Passes. The client of the app — a
// core adapter the site builds too, so it does not import the settings of the app — repeats a wait, a failure and exactly the
// refusals of a state that passes; a status Passes gains or loses is a status the app repeats or stops repeating.
func TestTheAppRepeatsWhatTheOneRuleSaysARepeatCures(t *testing.T) {
	for status := 400; status < 600; status++ {
		if want := aisettings.Passes(status) || status == http.StatusTooManyRequests || status >= 500; repeatsInApp(status) != want {
			t.Errorf("the client of the app repeats %d = %v; the rule of what a repeat cures says %v", status, repeatsInApp(status), want)
		}
	}
}
