package modelapi

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/modelthread"
)

// poolRefusal answers the refusal of the pool of codex-lb as it writes it: 429 with Retry-After, the headers of the pool
// and the body of the type requests.
func poolRefusal(w http.ResponseWriter, retryAfter string) {
	w.Header().Set("Retry-After", retryAfter)
	w.Header().Set("X-Ratelimit-Limit-Requests", "120")
	w.Header().Set("X-Ratelimit-Remaining-Requests", "0")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	fmt.Fprint(w, `{"error":{"type":"requests","code":"rate_limit_exceeded","param":null,"message":"Request pool limit reached for this API key: 120 requests per minute. Please try again in 3s."}}`)
}

// specs/2026-11-17-model-cache.md MC4.2: the client of the site waits out a refusal of the pool — known by its body — for
// the Retry-After it names, without a place of the gate and outside the count of the attempts: a call refused seven times
// goes through; every wait is a line of the journal with the class of the port and its seconds, and nothing else.
func TestTheClientOfTheSiteWaitsOutTheRefusalsOfThePool(t *testing.T) {
	var hits atomic.Int32
	firstRefused := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch hit := hits.Add(1); {
		case hit <= 7:
			if hit == 1 {
				close(firstRefused)
			}
			poolRefusal(w, "3")
		default:
			fmt.Fprint(w, answered)
		}
	}))
	defer server.Close()
	var journal bytes.Buffer
	t.Setenv("MODEL_POOL_TEST_KEY", "key-test-site-pool-0001")
	client, err := New(Config{BaseURL: server.URL, KeyEnv: "MODEL_POOL_TEST_KEY", Model: "m", Parallel: 1, WaitsPool: true,
		Logger: log.New(&journal, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	client.pause = func(int) time.Duration { return time.Hour }
	var mu sync.Mutex
	var asked []time.Duration
	client.poolWait = func(wait time.Duration) time.Duration {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, wait)
		if len(asked) == 1 {
			return 2 * time.Second
		}
		return time.Millisecond
	}
	done := make(chan error, 1)
	go func() {
		text, usage, err := client.Background().Complete(modelthread.With(t.Context(), "cards", "job-pool", "card", "1"), "инструкция", "вход")
		if err == nil && (text != "ok" || usage.Calls != 1) {
			err = fmt.Errorf("Complete() = %q, %+v", text, usage)
		}
		done <- err
	}()
	<-firstRefused
	// While the refused call waits, the one place of the gate is free for another call.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, answered) }))
	defer other.Close()
	started := time.Now()
	otherClient := *client
	otherClient.config.BaseURL = other.URL
	if _, _, err := otherClient.Interactive().Complete(t.Context(), "инструкция", "вход"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > time.Second {
		t.Fatalf("another call waited %s for the place a waiting call kept", took)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 8 || len(asked) != 7 || asked[0] != 3*time.Second {
		t.Fatalf("%d requests, the waits asked %v", hits.Load(), asked)
	}
	lines := strings.Split(strings.TrimSpace(journal.String()), "\n")
	if len(lines) != 7 || lines[0] != `{"event":"model_pool_wait","class":"background","seconds":3}` {
		t.Fatalf("the journal = %q", journal.String())
	}
	if strings.Contains(journal.String(), "job-pool") || strings.Contains(journal.String(), "ck-") || strings.Contains(journal.String(), "key-test") {
		t.Fatalf("the journal names the thread or the key: %q", journal.String())
	}
}

// MC4.2: the wait of the pool is its Retry-After, five seconds without one and a minute at most.
func TestTheWaitOfThePoolIsBounded(t *testing.T) {
	for asked, want := range map[time.Duration]time.Duration{0: 5 * time.Second, 3 * time.Second: 3 * time.Second, 5 * time.Minute: time.Minute} {
		if got := poolWaitOf(asked); got != want {
			t.Fatalf("the wait for %s = %s, want %s", asked, got, want)
		}
	}
}

// MC4.2: a 429 that is no refusal of the pool — the budget of the key, the fence of an account — is what it was, the
// headers of the pool on it or not: six attempts and the refusal of the call, no wait.
func TestOther429sKeepTheirSixAttempts(t *testing.T) {
	for name, body := range map[string]string{
		"api_key_limit_exceeded": `{"error":{"code":"api_key_limit_exceeded","message":"API key budget exhausted"}}`,
		"account_stream_cap":     `{"error":{"code":"account_stream_cap","type":"rate_limit_error","message":"Account stream capacity reached"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.Header().Set("Retry-After", "0")
				w.Header().Set("X-Ratelimit-Limit-Requests", "120")
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			t.Setenv("MODEL_POOL_TEST_KEY", "key-test-site-pool-0002")
			client, err := New(Config{BaseURL: server.URL, KeyEnv: "MODEL_POOL_TEST_KEY", Model: "m", WaitsPool: true})
			if err != nil {
				t.Fatal(err)
			}
			client.pause = func(int) time.Duration { return time.Millisecond }
			client.poolWait = func(time.Duration) time.Duration { t.Fatal("a 429 of another kind waited as the pool"); return 0 }
			_, _, err = client.Background().Complete(t.Context(), "инструкция", "вход")
			var refusal *PoolRefusal
			if err == nil || errors.As(err, &refusal) || !strings.Contains(err.Error(), name) || hits.Load() != attempts {
				t.Fatalf("Complete() = %v after %d requests", err, hits.Load())
			}
		})
	}
}

// MC3.3, MC4.2: a client without the wait of the pool — the client of the steps of apps — does not wait it out and hands its
// caller the typed refusal with the time of the pool at once.
func TestAClientWithoutTheWaitHandsTheRefusalOfThePool(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		poolRefusal(w, "7")
	}))
	defer server.Close()
	t.Setenv("MODEL_POOL_TEST_KEY", "key-test-site-pool-0003")
	client, err := New(Config{BaseURL: server.URL, KeyEnv: "MODEL_POOL_TEST_KEY", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	client.pause = func(int) time.Duration { return time.Hour }
	_, _, err = client.Interactive().Complete(t.Context(), "инструкция", "вход")
	var refusal *PoolRefusal
	if !errors.As(err, &refusal) || refusal.RetryAfter != 7*time.Second || hits.Load() != 1 {
		t.Fatalf("Complete() = %v after %d requests", err, hits.Load())
	}
}

// MC4.1: the port of the reading of books — cards, names, the layer — carries the key of the reading when one is named, in
// the class and with the model of the background; the materials and scan carry the key of the site; without a key of the
// reading every port carries the key of the site.
func TestThePortOfTheReadingCarriesTheKeyOfTheReading(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		mu.Unlock()
		fmt.Fprint(w, answered)
	}))
	defer server.Close()
	t.Setenv("MODEL_SITE_TEST_KEY", "key-test-site-0001")
	t.Setenv("MODEL_READING_TEST_KEY", "key-test-reading-0001")
	reading, err := New(Config{BaseURL: server.URL, KeyEnv: "MODEL_SITE_TEST_KEY", ReadingKeyEnv: "MODEL_READING_TEST_KEY", Model: "m", CardsModel: "cards"})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := New(Config{BaseURL: server.URL, KeyEnv: "MODEL_SITE_TEST_KEY", Model: "m", CardsModel: "cards"})
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range []Port{reading.Reading(), reading.Background(), reading.Interactive(), plain.Reading()} {
		if _, _, err := port.Complete(t.Context(), "инструкция", "вход"); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"key-test-reading-0001", "key-test-site-0001", "key-test-site-0001", "key-test-site-0001"}
	if strings.Join(keys, " ") != strings.Join(want, " ") {
		t.Fatalf("the keys heard = %q, want %q", keys, want)
	}
	if port := reading.Reading(); port.Model() != "cards" || port.class != Background {
		t.Fatalf("the port of the reading = %s, class %d", port.Model(), port.class)
	}
}
