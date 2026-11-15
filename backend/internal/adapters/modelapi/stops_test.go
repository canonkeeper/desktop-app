package modelapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// specs/2026-11-13-desk-consent.md CN3.4: the client of the app hands its caller a typed stop and does not repeat the
// funds, the key, the model and a refusal of the check of the gateway; the client of the site repeats and fails as before.
func TestTheClientOfTheAppStopsOnAStopOfTheGateway(t *testing.T) {
	for _, refusal := range []struct {
		status int
		body   string
		want   sagamemory.Stop
	}{
		{http.StatusPaymentRequired, `{"error":{"type":"http_402","code":"http_402","reason":"funds","message":"Insufficient Balance"}}`, sagamemory.StopFunds},
		{http.StatusTooManyRequests, `{"error":{"type":"insufficient_quota","code":"insufficient_quota","reason":"funds","message":"quota"}}`, sagamemory.StopFunds},
		{http.StatusUnauthorized, `{"error":{"type":"invalid_api_key","code":"invalid_api_key","reason":"key","message":"key"}}`, sagamemory.StopKey},
		{http.StatusNotFound, `{"error":{"type":"model_not_found","code":"model_not_found","reason":"model","message":"no model"}}`, sagamemory.StopModel},
		{http.StatusConflict, `{"error":{"type":"reading.paused","code":"reading.paused","reason":"sum","message":"paused"}}`, sagamemory.StopPaused},
	} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(refusal.status)
			fmt.Fprint(w, refusal.body)
		}))
		app, err := New(Config{BaseURL: server.URL + "/v1", Key: "key-test-gateway-0002", Model: "canonkeeper-cards", CarryJob: true, Stops: true})
		if err != nil {
			t.Fatal(err)
		}
		app.pause = func(int) time.Duration { return 0 }
		_, _, err = app.Background().Complete(t.Context(), "инструкция", "вход")
		if stop, ok := sagamemory.StopOf(err); !ok || stop != refusal.want || calls.Load() != 1 {
			t.Errorf("%d %s = %v after %d calls; want %s at once", refusal.status, refusal.body, err, calls.Load(), refusal.want)
		}
		site, _ := New(Config{BaseURL: server.URL, Model: "m"})
		site.pause = func(int) time.Duration { return 0 }
		calls.Store(0)
		_, _, err = site.Background().Complete(t.Context(), "", "x")
		retried := refusal.status == http.StatusTooManyRequests
		if _, typed := sagamemory.StopOf(err); typed || err == nil || retried && calls.Load() != 6 || !retried && calls.Load() != 1 {
			t.Errorf("the site on %d = %v after %d calls", refusal.status, err, calls.Load())
		}
		server.Close()
	}
}

// CN3.4: no connection becomes the stop «связь» only once the repeats of the client of the app on one request are over; its
// last attempt says so to the gateway, which pauses the reading then.
func TestNoConnectionStopsTheAppAfterItsRepeats(t *testing.T) {
	var mu sync.Mutex
	var last []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = append(last, r.Header.Get(LastAttemptHeader))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"error":{"type":"connection_error","code":"connection_error","reason":"connection","message":"no connection"}}`)
	}))
	defer server.Close()
	app, err := New(Config{BaseURL: server.URL + "/v1", Key: "key-test-gateway-0003", Model: "canonkeeper-cards", CarryJob: true, Stops: true, RetryPause: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, _, err = app.Background().Complete(t.Context(), "инструкция", "вход")
	if stop, ok := sagamemory.StopOf(err); !ok || stop != sagamemory.StopConnection {
		t.Fatalf("no connection = %v", err)
	}
	if len(last) != 6 || last[5] != "1" || last[0] != "" || last[4] != "" {
		t.Fatalf("the attempts said %q", last)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("the short pauses of the test took %s", took)
	}
}

// CN3.4: a refusal a provider of Chat Completions says in the stream comes from the gateway as response.failed with the
// reason of its stop: the client of the app stops at once there too; the client of the site repeats it as before.
func TestAStopInTheStreamStopsTheApp(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"http_402\",\"reason\":\"funds\",\"message\":\"upstream http_402 (HTTP 402): no money\"}}}\n\n")
	}))
	defer server.Close()
	app, err := New(Config{BaseURL: server.URL + "/v1", Key: "key-test-gateway-0004", Model: "canonkeeper-cards", CarryJob: true, Stops: true})
	if err != nil {
		t.Fatal(err)
	}
	app.pause = func(int) time.Duration { return 0 }
	if _, _, err := app.Background().Complete(t.Context(), "инструкция", "вход"); calls.Load() != 1 {
		t.Fatalf("the app = %v after %d calls", err, calls.Load())
	} else if stop, ok := sagamemory.StopOf(err); !ok || stop != sagamemory.StopFunds {
		t.Fatalf("the app = %v", err)
	}
	calls.Store(0)
	site, _ := New(Config{BaseURL: server.URL, Model: "m"})
	site.pause = func(int) time.Duration { return 0 }
	if _, _, err := site.Background().Complete(t.Context(), "", "x"); err == nil || calls.Load() != 6 {
		t.Fatalf("the site = %v after %d calls", err, calls.Load())
	}
}

// specs/2026-11-13-desk-account-ai.md AI3.5: a refusal of the site of the account the gateway names as a stop — the characters
// of the tier, the fence of the work of the reading, an app the site does not take, the reading of the site unavailable — is
// a typed stop at once, never repeated: ai.provider_failed of a step the site repeated already.
func TestTheClientOfTheAppStopsOnAStopOfTheAccount(t *testing.T) {
	for reason, want := range map[string]sagamemory.Stop{
		"characters": sagamemory.StopCharacters, "reading_fence": sagamemory.StopReadingFence, "unfit": sagamemory.StopUnfit,
		"unavailable": sagamemory.StopUnavailable,
	} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, `{"error":{"type":"x","code":"ai.provider_failed","reason":%q,"message":"refused"}}`, reason)
		}))
		app, err := New(Config{BaseURL: server.URL + "/v1", Key: "key-test-gateway-0003", Model: "canonkeeper-cards", CarryJob: true, Stops: true})
		if err != nil {
			t.Fatal(err)
		}
		app.pause = func(int) time.Duration { return 0 }
		_, _, err = app.Background().Complete(t.Context(), "инструкция", "вход")
		if stop, ok := sagamemory.StopOf(err); !ok || stop != want || calls.Load() != 1 || stop.Code() != "reading."+reason {
			t.Errorf("%s = %v after %d calls", reason, err, calls.Load())
		}
		server.Close()
	}
}
