package modelapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// specs/2026-11-13-desk-gateway.md GW3.1, GW3.2: the client of the app calls the local gateway with the token it holds by
// value — no variable of the environment — and names the Job of its context in JobHeader; the client of the site sends
// no such header, whatever its context holds.
func TestTheClientOfTheAppNamesItsJobToTheGateway(t *testing.T) {
	var mu sync.Mutex
	var seen []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n")
	}))
	defer server.Close()
	ctx := jobs.WithJobID(t.Context(), "job-of-the-cards")
	app, err := New(Config{BaseURL: server.URL + "/v1", Key: "key-test-gateway-0001", Model: "canonkeeper-cards", CarryJob: true})
	if err != nil {
		t.Fatal(err)
	}
	if text, _, err := app.Background().Complete(ctx, "инструкция", "вход"); err != nil || text != "ok" {
		t.Fatalf("the app = %q, %v", text, err)
	}
	t.Setenv("MODEL_TEST_KEY", "key-test-site-0001")
	site, err := New(Config{BaseURL: server.URL, KeyEnv: "MODEL_TEST_KEY", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := site.Background().Complete(ctx, "инструкция", "вход"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0].Get("Authorization") != "Bearer key-test-gateway-0001" || seen[0].Get(JobHeader) != "job-of-the-cards" {
		t.Fatalf("the gateway got %v", seen)
	}
	if seen[1].Get(JobHeader) != "" || seen[1].Get("Authorization") != "Bearer key-test-site-0001" {
		t.Fatalf("the provider of the site got %v", seen[1])
	}
	if _, _, err := app.Background().Complete(t.Context(), "инструкция", "вход"); err != nil || seen[2].Get(JobHeader) != "" {
		t.Fatalf("a call outside a Job named %q", seen[2].Get(JobHeader))
	}
}

// GW3.3: an answer cut by its limit is a typed error at once — the builder decides, the client repeats nothing — and its
// tokens are counted.
func TestACutAnswerIsATypedErrorWithoutRetries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"{\\\"events\\\":[\"}\n\n"+
			"data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},"+
			"\"usage\":{\"input_tokens\":900,\"output_tokens\":16000}}}\n\n")
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	client.pause = func(int) time.Duration { return 0 }
	_, usage, err := client.Background().Complete(t.Context(), "", "x")
	var cut *IncompleteError
	if !errors.Is(err, ErrIncomplete) || !errors.As(err, &cut) || cut.Reason != "max_output_tokens" {
		t.Fatalf("a cut answer = %v", err)
	}
	// specs/2026-11-13-desk-consent.md CN3.6: the builders of the reading know it typed.
	if !errors.Is(err, sagamemory.ErrAnswerCut) {
		t.Fatalf("a cut answer is not sagamemory.ErrAnswerCut: %v", err)
	}
	if calls.Load() != 1 || usage.Calls != 1 || usage.InputTokens != 900 || usage.OutputTokens != 16000 {
		t.Fatalf("calls %d, usage %+v", calls.Load(), usage)
	}
}

// GW3.3: a refusal with Retry-After is repeated after the provider's wait, not the client's own pause.
func TestARefusalWaitsForItsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n")
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	client.pause = func(int) time.Duration { return time.Hour }
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	started := time.Now()
	if text, _, err := client.Background().Complete(ctx, "", "x"); err != nil || text != "ok" || calls.Load() != 2 {
		t.Fatalf("Complete() = %q, %v after %d calls", text, err, calls.Load())
	}
	if waited := time.Since(started); waited < time.Second || waited > 10*time.Second {
		t.Fatalf("the client waited %s", waited)
	}
}
