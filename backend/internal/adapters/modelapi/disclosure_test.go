package modelapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// specs/2026-11-14-desk-journal.md JR1.4: the client of the app carries what its call gives to the gateway beside the Job; the
// client of the site sends no such header whatever its context holds.
func TestTheClientOfTheAppNamesWhatItsCallGives(t *testing.T) {
	var mu sync.Mutex
	var seen []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n")
	}))
	defer server.Close()
	var named disclosure.Named
	named.Chapter("11111111-1111-1111-1111-111111111111", disclosure.Whole, 0, 0)
	ctx := disclosure.With(jobs.WithJobID(t.Context(), "job-of-the-cards"), named)
	app, err := New(Config{BaseURL: server.URL + "/v1", Key: "key-test-gateway-0002", Model: "canonkeeper-cards", CarryJob: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.Background().Complete(ctx, "инструкция", "вход"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MODEL_TEST_KEY", "key-test-site-0002")
	site, err := New(Config{BaseURL: server.URL, KeyEnv: "MODEL_TEST_KEY", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := site.Background().Complete(ctx, "инструкция", "вход"); err != nil {
		t.Fatal(err)
	}
	want, _ := named.Encode()
	if len(seen) != 2 || seen[0].Get(disclosure.Header) != want || seen[1].Get(disclosure.Header) != "" {
		t.Fatalf("the headers = %q / %q", seen[0].Get(disclosure.Header), seen[1].Get(disclosure.Header))
	}
}

// JR1.6: a refusal of the journal of the gateway is a typed error of its own at once — no repeat, no stop of the reading.
func TestARefusalOfTheJournalIsTypedAndNotRepeated(t *testing.T) {
	for _, code := range []string{disclosure.CodeUnavailable, disclosure.CodeUnnamed} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":{"type":%q,"code":%q,"message":"no"}}`, code, code)
		}))
		client, err := New(Config{BaseURL: server.URL, Key: "key-test-gateway-0003", Model: "canonkeeper-cards", CarryJob: true, Stops: true, RetryPause: time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = client.Background().Complete(t.Context(), "инструкция", "вход")
		server.Close()
		if disclosure.CodeOf(err) != code || calls != 1 {
			t.Fatalf("%s: %v after %d calls", code, err, calls)
		}
		if _, stopped := sagamemory.StopOf(err); stopped {
			t.Fatalf("%s is a stop of the reading", code)
		}
	}
}
