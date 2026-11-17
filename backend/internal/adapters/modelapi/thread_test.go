package modelapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/modelthread"
)

// threadValue is the value of the thread by the formula of the spec, written apart from the client.
func threadValue(generation int, parts ...string) string {
	sum := sha256.Sum256([]byte("ck-thread\x00" + strings.Join(parts, "\x00") + fmt.Sprintf("\x00%d", generation)))
	return "ck-" + hex.EncodeToString(sum[:])[:32]
}

const answered = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n"

// heard is what a fake provider heard of each request: its thread, the headers of a session and the body.
type heard struct {
	thread, session string
	body            string
}

type listener struct {
	mu   sync.Mutex
	seen []heard
}

func (ear *listener) hear(r *http.Request) int {
	body, _ := io.ReadAll(r.Body)
	ear.mu.Lock()
	defer ear.mu.Unlock()
	ear.seen = append(ear.seen, heard{thread: r.Header.Get("Thread-Id"), session: r.Header.Get("Session_id") + r.Header.Get("Session-Id"), body: string(body)})
	return len(ear.seen)
}

func (ear *listener) threads() []string {
	ear.mu.Lock()
	defer ear.mu.Unlock()
	threads := make([]string, len(ear.seen))
	for index, request := range ear.seen {
		threads[index] = request.thread
	}
	return threads
}

func siteClient(t *testing.T, url string) *Client {
	t.Helper()
	t.Setenv("MODEL_THREAD_TEST_KEY", "key-test-site-thread-0001")
	client, err := New(Config{BaseURL: url, KeyEnv: "MODEL_THREAD_TEST_KEY", Model: "m", Parallel: 8})
	if err != nil {
		t.Fatal(err)
	}
	client.pause = func(int) time.Duration { return 0 }
	return client
}

// specs/2026-11-17-model-cache.md MC1.1: the client of the site names the thread of a call in Thread-Id exactly when its
// context holds one, by the formula, with no header of a session and no prompt_cache_key; without a thread the request is
// the one it was. The client of the app names none, whatever its context holds.
func TestTheClientOfTheSiteNamesTheThreadOfItsCall(t *testing.T) {
	ear := &listener{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ear.hear(r)
		fmt.Fprint(w, answered)
	}))
	defer server.Close()
	site := siteClient(t, server.URL)
	threaded := modelthread.With(t.Context(), "cards", "job-thread-site", "card", "7")
	if _, _, err := site.Background().Complete(threaded, "инструкция", "вход"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := site.Background().Complete(t.Context(), "инструкция", "вход"); err != nil {
		t.Fatal(err)
	}
	app, err := New(Config{BaseURL: server.URL, Key: "key-test-gateway-thread-0001", Model: "canonkeeper-cards", CarryJob: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.Background().Complete(threaded, "инструкция", "вход"); err != nil {
		t.Fatal(err)
	}
	want := threadValue(0, "cards", "job-thread-site", "card", "7")
	if threads := ear.threads(); len(threads) != 3 || threads[0] != want || threads[1] != "" || threads[2] != "" {
		t.Fatalf("the threads heard = %q, want %q, none, none", threads, want)
	}
	for _, request := range ear.seen {
		if request.session != "" || strings.Contains(request.body, "prompt_cache_key") {
			t.Fatalf("a request named a session or a key of the cache: %+v", request)
		}
	}
	if ear.seen[0].body != ear.seen[1].body {
		t.Fatalf("the thread changed the body: %q and %q", ear.seen[0].body, ear.seen[1].body)
	}
}

// MC1.2: a refusal 409 of codex-lb whose thread lost its account — by the code of its JSON — is sent again at once, once a
// call and outside the count of the attempts, with the next generation of the thread; the next call of the thread goes
// with that generation too.
func TestARefusalOfTheThreadIsSentAgainAtOnceWithTheNextGeneration(t *testing.T) {
	for _, code := range []string{"previous_response_owner_unavailable", "conversation_owner_conflict", "quota_failover_requires_context"} {
		t.Run(code, func(t *testing.T) {
			ear := &listener{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if ear.hear(r) == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					fmt.Fprintf(w, `{"error":{"code":%q,"message":"The previous response owner is no longer allowed for this key"}}`, code)
					return
				}
				fmt.Fprint(w, answered)
			}))
			defer server.Close()
			site := siteClient(t, server.URL)
			site.pause = func(int) time.Duration { return time.Hour }
			ctx := modelthread.With(t.Context(), "names", "job-refused-"+code)
			if text, usage, err := site.Background().Complete(ctx, "инструкция", "вход"); err != nil || text != "ok" || usage.Calls != 1 {
				t.Fatalf("Complete() = %q, %+v, %v", text, usage, err)
			}
			if _, _, err := site.Background().Complete(ctx, "инструкция", "вход"); err != nil {
				t.Fatal(err)
			}
			first, next := threadValue(0, "names", "job-refused-"+code), threadValue(1, "names", "job-refused-"+code)
			if threads := ear.threads(); len(threads) != 3 || threads[0] != first || threads[1] != next || threads[2] != next {
				t.Fatalf("the threads heard = %q, want %q then %q twice", threads, first, next)
			}
		})
	}
}

// MC1.2: six calls of one thread refused together go again with one new generation, and so does the next call: the thread
// stays one.
func TestCallsOfOneThreadRefusedTogetherStayOneThread(t *testing.T) {
	ear := &listener{}
	const together = 6
	var refused sync.WaitGroup
	refused.Add(together)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ear.hear(r)
		if r.Header.Get("Thread-Id") == threadValue(0, "cards", "job-together", "card", "3") {
			// Each first request waits for the others: all six are refused before any goes again.
			refused.Done()
			refused.Wait()
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"error":{"code":"previous_response_owner_unavailable","message":"gone"}}`)
			return
		}
		fmt.Fprint(w, answered)
	}))
	defer server.Close()
	site := siteClient(t, server.URL)
	ctx := modelthread.With(t.Context(), "cards", "job-together", "card", "3")
	var calls sync.WaitGroup
	failures := make(chan error, together)
	for range together {
		calls.Add(1)
		go func() {
			defer calls.Done()
			if _, _, err := site.Background().Complete(ctx, "инструкция", "вход"); err != nil {
				failures <- err
			}
		}()
	}
	calls.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if _, _, err := site.Background().Complete(ctx, "инструкция", "вход"); err != nil {
		t.Fatal(err)
	}
	next := threadValue(1, "cards", "job-together", "card", "3")
	threads := ear.threads()
	if len(threads) != 2*together+1 {
		t.Fatalf("%d requests, want %d", len(threads), 2*together+1)
	}
	for _, thread := range threads {
		if thread != next && thread != threadValue(0, "cards", "job-together", "card", "3") {
			t.Fatalf("a request went with another thread: %q", threads)
		}
	}
	if threads[len(threads)-1] != next {
		t.Fatalf("the next call went with %q, want %q", threads[len(threads)-1], next)
	}
}

// MC1.2: another 409, and a second refusal of the thread in one call, are what they were — the refusal of the call, no repeat.
func TestAnotherConflictAndASecondRefusalOfTheThreadEndTheCall(t *testing.T) {
	for name, body := range map[string]string{
		"another code": `{"error":{"code":"conflict_of_the_test","message":"no"}}`,
		"twice":        `{"error":{"code":"conversation_owner_conflict","message":"no"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			ear := &listener{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ear.hear(r)
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			site := siteClient(t, server.URL)
			ctx := modelthread.With(t.Context(), "layer", "job-conflict-"+name, "layer_reduce")
			_, usage, err := site.Background().Complete(ctx, "инструкция", "вход")
			if err == nil || !strings.Contains(err.Error(), "model API status 409") || usage.Calls != 0 {
				t.Fatalf("Complete() = %+v, %v", usage, err)
			}
			want := 1
			if name == "twice" {
				want = 2
			}
			if threads := ear.threads(); len(threads) != want {
				t.Fatalf("%d requests, want %d", len(threads), want)
			}
		})
	}
}

// MC1.1: a turn of the assistant (its rounds and the attempts of each) and a look at a drawing go with the thread of their
// context in every request.
func TestTheRoundsOfATurnAndALookGoWithTheirThread(t *testing.T) {
	ear := &listener{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch ear.hear(r) {
		case 1:
			http.Error(w, "busy", http.StatusBadGateway)
		case 2:
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"c1\",\"name\":\"look\",\"arguments\":\"{}\"}}\n\n"+
				"data: {\"type\":\"response.completed\",\"response\":{}}\n\n")
		case 3:
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"готово\"}]}}\n\n"+
				"data: {\"type\":\"response.completed\",\"response\":{}}\n\n")
		default:
			fmt.Fprint(w, answered)
		}
	}))
	defer server.Close()
	site := siteClient(t, server.URL)
	voice := modelthread.With(t.Context(), "voice", "call-north")
	text, _, err := site.Voice().Act(voice, "инструкция", "вход", []Function{{Name: "look", Parameters: map[string]any{"type": "object"}}},
		func(context.Context, string, string, string) (Output, error) { return Output{Text: "видно"}, nil })
	if err != nil || text != "готово" {
		t.Fatalf("Act() = %q, %v", text, err)
	}
	look := modelthread.With(t.Context(), "critic", "image-north")
	if text, _, err := site.Vision().Look(look, "инструкция", "факты", []byte("\x89PNG\r\n\x1a\n")); err != nil || text != "ok" {
		t.Fatalf("Look() = %q, %v", text, err)
	}
	turn, picture := threadValue(0, "voice", "call-north"), threadValue(0, "critic", "image-north")
	if threads := ear.threads(); len(threads) != 4 || threads[0] != turn || threads[1] != turn || threads[2] != turn || threads[3] != picture {
		t.Fatalf("the threads heard = %q", threads)
	}
}

// MC1.2: the map of the generations keeps only threads past generation 0 and no more than 10 000 of them; a thread forgotten
// for a new one goes with generation 0 again.
func TestTheGenerationsOfTheProcessAreBounded(t *testing.T) {
	all := &threadGenerations{of: map[[32]byte]int{}}
	for index := range maxThreads + 5 {
		if next := all.refused(NewThread([]string{"bound", fmt.Sprint(index)}).key, 0); next != 1 {
			t.Fatalf("the generation after a refusal of 0 = %d", next)
		}
	}
	if len(all.of) != maxThreads {
		t.Fatalf("the map holds %d threads", len(all.of))
	}
	if all.current(NewThread([]string{"never refused"}).key) != 0 || len(all.of) != maxThreads {
		t.Fatal("a thread never refused has a generation or a place in the map")
	}
	key := NewThread([]string{"bound", "again"}).key
	if all.refused(key, 4) != 5 || all.refused(key, 1) != 5 || all.refused(key, 5) != 6 {
		t.Fatal("a refusal moved the thread back or not past the generation used")
	}
}
