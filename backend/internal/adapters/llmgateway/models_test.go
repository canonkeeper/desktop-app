// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"canonkeeper/backend/internal/domain/aisettings"
)

// lister is a stub of the models of a provider: it answers GET /v1/models with its body, or refuses with status and the
// code of its refusal.
type lister struct {
	mu     sync.Mutex
	status int
	code   string
	body   string
	asked  []http.Header
	paths  []string
}

func (stub *lister) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	stub.mu.Lock()
	stub.asked = append(stub.asked, r.Header.Clone())
	stub.paths = append(stub.paths, r.Method+" "+r.URL.Path)
	status, code, body := stub.status, stub.code, stub.body
	stub.mu.Unlock()
	if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
		http.NotFound(w, r)
		return
	}
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"code":"`+code+`","message":"words of the provider north-provider-words"}}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func (stub *lister) headers() []http.Header {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]http.Header(nil), stub.asked...)
}

// specs/2026-11-13-desk-probes.md PB1.1: the gateway asks the address of the settings for its models with the key of the
// author and gives the names by the alphabet, each once; 404 is a provider that gives no list, 401 a key it refused, no
// connection a provider that did not answer — never the words of the provider, nor the key; the journal keeps nothing.
func TestTheModelsOfTheProviderAreAskedAtItsAddressAlone(t *testing.T) {
	stand := openStand(t)
	stub := &lister{body: `{"object":"list","data":[{"id":"north-wide"},{"id":"North-chat"},{"id":"north-cards"},{"id":"north-wide"},{"id":""},{"id":"bad\u0007name"}]}`}
	server := startProvider(t, stub)
	const key = "key-test-models-north-0001"
	stand.settings.set(t, custom(server, "chat"), key)

	got := stand.gateway.Models(context.Background())
	want := aisettings.Models{Outcome: aisettings.ModelsListed, Names: []string{"north-cards", "North-chat", "north-wide"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the models = %+v", got)
	}
	if asked := stub.headers(); len(asked) != 1 || asked[0].Get("Authorization") != "Bearer "+key {
		t.Fatalf("the provider got %v", asked)
	}

	many := make([]string, 0, 700)
	for index := range 700 {
		many = append(many, fmt.Sprintf(`{"id":"model-%03d"}`, 699-index))
	}
	stub.body = `{"data":[` + strings.Join(many, ",") + `]}`
	if got := stand.gateway.Models(context.Background()); len(got.Names) != aisettings.MaxModels || got.Names[0] != "model-000" || got.Names[499] != "model-499" {
		t.Fatalf("a long list kept %d names from %v", len(got.Names), got.Names[:1])
	}

	for status, reason := range map[int]aisettings.Reason{
		http.StatusUnauthorized: aisettings.ReasonKeyRefused, http.StatusForbidden: aisettings.ReasonKeyRefused,
		http.StatusTooManyRequests: aisettings.ReasonRateLimited, http.StatusInternalServerError: aisettings.ReasonOther,
	} {
		stub.status = status
		if got := stand.gateway.Models(context.Background()); !reflect.DeepEqual(got, aisettings.Models{Outcome: aisettings.ModelsFailed, Reason: reason}) {
			t.Fatalf("%d = %+v", status, got)
		}
	}
	// PB2.4: an exhausted quota is no wait, in the list as in the check (waits).
	stub.status, stub.code = http.StatusTooManyRequests, "insufficient_quota"
	if got := stand.gateway.Models(context.Background()); !reflect.DeepEqual(got, aisettings.Models{Outcome: aisettings.ModelsFailed, Reason: aisettings.ReasonOther}) {
		t.Fatalf("an exhausted quota = %+v", got)
	}
	stub.code = ""
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		stub.status = status
		if got := stand.gateway.Models(context.Background()); !reflect.DeepEqual(got, aisettings.Models{Outcome: aisettings.ModelsUnlisted}) {
			t.Fatalf("%d = %+v", status, got)
		}
	}
	stub.status, stub.body = 0, `not json`
	if got := stand.gateway.Models(context.Background()); got.Outcome != aisettings.ModelsFailed || got.Reason != aisettings.ReasonOther {
		t.Fatalf("a body no client reads = %+v", got)
	}

	// No connection: the port of a listener that is closed.
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := closed.Addr().String()
	_ = closed.Close()
	gone := custom(server, "chat")
	gone.BaseURL = text("http://" + address + "/v1")
	stand.settings.set(t, gone, key)
	if got := stand.gateway.Models(context.Background()); !reflect.DeepEqual(got, aisettings.Models{Outcome: aisettings.ModelsFailed, Reason: aisettings.ReasonUnreachable}) {
		t.Fatalf("no connection = %+v", got)
	}

	if rows := stand.journal.all(); len(rows) != 0 {
		t.Fatalf("the list left rows in the journal: %+v", rows)
	}
	if strings.Contains(stand.log.String(), key) || strings.Contains(stand.log.String(), "north-provider-words") {
		t.Fatal("the log holds the key or the words of the provider")
	}
}

// PB1.1: the request goes only to the address of the settings — a redirect of the provider is not followed and the key
// goes nowhere else — and a local server gets no key, even one the author kept for another variant.
func TestTheModelsGoNowhereButTheAddressAndALocalServerGetsNoKey(t *testing.T) {
	stand := openStand(t)
	elsewhere := &lister{body: `{"data":[{"id":"elsewhere"}]}`}
	far := startProvider(t, elsewhere)
	redirect := startProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, far.URL+"/v1/models", http.StatusFound)
	}))
	stand.settings.set(t, custom(redirect, "chat"), "key-test-redirect-0001")
	if got := stand.gateway.Models(context.Background()); got.Outcome != aisettings.ModelsFailed || got.Reason != aisettings.ReasonOther {
		t.Fatalf("a redirect = %+v", got)
	}
	if len(elsewhere.headers()) != 0 {
		t.Fatal("the request followed the redirect")
	}

	local := &lister{body: `{"object":"list","data":[{"id":"qwen3:8b"},{"id":"gemma4"}]}`}
	server := startProvider(t, local)
	stand.settings.set(t, aisettings.OwnDraft{Preset: "ollama"}, "key-test-kept-for-another-0002")
	stand.settings.mu.Lock()
	stand.settings.settings.Own.BaseURL = server.URL + "/v1"
	stand.settings.mu.Unlock()
	if got := stand.gateway.Models(context.Background()); !reflect.DeepEqual(got.Names, []string{"gemma4", "qwen3:8b"}) {
		t.Fatalf("the models of a local server = %+v", got)
	}
	if asked := local.headers(); len(asked) != 1 || asked[0].Get("Authorization") != "" {
		t.Fatalf("a local server got Authorization %q", asked[0].Get("Authorization"))
	}
}
