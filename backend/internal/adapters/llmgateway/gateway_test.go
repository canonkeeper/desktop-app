// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/coresteps"
	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modeljournal"
)

// The text of the author in the requests of the tests: no row of the journal and no log may hold it.
const authorText = "Северный предел: застава у перевала"

// heldSettings are the settings and the key of the author as the backend holds them, changed by the test as the window
// would.
type heldSettings struct {
	mu       sync.Mutex
	settings aisettings.Settings
	key      string
}

func (held *heldSettings) AI() aisettings.Settings {
	held.mu.Lock()
	defer held.mu.Unlock()
	return held.settings
}

func (held *heldSettings) Key() (string, bool) {
	held.mu.Lock()
	defer held.mu.Unlock()
	return held.key, held.key != ""
}

func (held *heldSettings) set(t *testing.T, draft aisettings.OwnDraft, key string) {
	t.Helper()
	settings, err := aisettings.Draft{Source: "own", Own: &draft}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	held.mu.Lock()
	defer held.mu.Unlock()
	held.settings, held.key = settings, key
}

func text(value string) *string { return &value }
func number(value int) *int     { return &value }

// custom is «Другой, совместимый с OpenAI» at the stub with both models.
func custom(stub *httptest.Server, protocol string) aisettings.OwnDraft {
	return aisettings.OwnDraft{
		Preset: "custom", BaseURL: text(stub.URL + "/v1"), Protocol: text(protocol),
		ChatModel: text("north-chat"), CardsModel: text("north-cards"),
	}
}

// keptJournal is the journal of the tests: the records of the sendings, which it can refuse to write, and the rows.
type keptJournal struct {
	mu       sync.Mutex
	rows     []modeljournal.Request
	sendings []modeljournal.Sending
	refuse   atomic.Bool
}

func (journal *keptJournal) Send(_ context.Context, sending modeljournal.Sending) error {
	if journal.refuse.Load() {
		return errors.New("the journal of the test refuses")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.sendings = append(journal.sendings, sending)
	return nil
}

func (journal *keptJournal) sent() []modeljournal.Sending {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	return append([]modeljournal.Sending(nil), journal.sendings...)
}

func (journal *keptJournal) Record(_ context.Context, request modeljournal.Request) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.rows = append(journal.rows, request)
	return nil
}

func (journal *keptJournal) all() []modeljournal.Request {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	return append([]modeljournal.Request(nil), journal.rows...)
}

// seen is one request a stub provider got.
type seen struct {
	path    string
	header  http.Header
	body    map[string]json.RawMessage
	model   string
	raw     string
	auth    string
	started time.Time
}

// provider is a stub of a provider of Responses or Chat Completions: it records what it got and answers a stream of one
// short answer with usage; status, when set, refuses instead.
type provider struct {
	mu       sync.Mutex
	got      []seen
	chat     bool
	status   int
	retry    string
	hold     time.Duration
	running  atomic.Int64
	most     atomic.Int64
	noRoutes bool
}

func (stub *provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]json.RawMessage
	_ = json.Unmarshal(raw, &body)
	var model string
	_ = json.Unmarshal(body["model"], &model)
	stub.mu.Lock()
	stub.got = append(stub.got, seen{path: r.URL.Path, header: r.Header.Clone(), body: body, model: model, raw: string(raw), auth: r.Header.Get("Authorization"), started: time.Now()})
	stub.mu.Unlock()
	now := stub.running.Add(1)
	defer stub.running.Add(-1)
	for {
		most := stub.most.Load()
		if now <= most || stub.most.CompareAndSwap(most, now) {
			break
		}
	}
	if stub.hold > 0 {
		time.Sleep(stub.hold)
	}
	wantPath := "/v1/responses"
	if stub.chat {
		wantPath = "/v1/chat/completions"
	}
	if r.URL.Path != wantPath || stub.noRoutes {
		http.NotFound(w, r)
		return
	}
	if stub.status != 0 {
		if stub.retry != "" {
			w.Header().Set("Retry-After", stub.retry)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(stub.status)
		_, _ = io.WriteString(w, `{"error":{"type":"rate_limit_exceeded","code":"rate_limit_exceeded","message":"slow down"}}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if stub.chat {
		_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"Ответ"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":4}}}`+"\n\ndata: [DONE]\n\n")
		return
	}
	_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Ответ\"}\n\n"+
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"input_tokens_details\":{\"cached_tokens\":4},\"output_tokens\":3,\"total_tokens\":13}}}\n\n")
}

func (stub *provider) requests() []seen {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]seen(nil), stub.got...)
}

func startProvider(t *testing.T, stub http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	return server
}

// stand is a gateway of the test with its settings, journal and log.
type stand struct {
	gateway  *Gateway
	settings *heldSettings
	journal  *keptJournal
	log      *lockedLog
}

type lockedLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (locked *lockedLog) Write(data []byte) (int, error) {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.buffer.Write(data)
}

func (locked *lockedLog) String() string {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.buffer.String()
}

func openStand(t *testing.T) *stand {
	t.Helper()
	settings := &heldSettings{settings: aisettings.None()}
	written := &lockedLog{}
	gateway, err := Open(settings, log.New(written, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gateway.Close() })
	journal := &keptJournal{}
	gateway.UseJournal(journal)
	// A request of Codex goes only in a turn (specs/2026-11-14-desk-journal.md JR1.2): the requests of the tests are of a turn.
	t.Cleanup(gateway.Threads().Bind(threadOfTheTest, jobOfTheTest))
	return &stand{gateway: gateway, settings: settings, journal: journal, log: written}
}

// The turn the requests of Codex of the tests go in, and what a call of the core of the tests names (JR1.2, JR1.4).
const (
	threadOfTheTest = "thread-of-the-test"
	jobOfTheTest    = "job-of-the-test"
	checkOfTheTest  = "95000000-0000-4000-8000-0000000000c0"
)

var defaultNamed = disclosure.Named{Names: 1}

// named is the context of a call of the core of the tests that names what it gives.
func named(ctx context.Context) context.Context { return disclosure.With(ctx, defaultNamed) }

// chatBody is a request of Codex: the alias of the chat, its instructions and what Codex adds for itself.
func chatBody(extra string) string {
	return `{"model":"canonkeeper-chat","instructions":"Codex base","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"` +
		authorText + `"}]}],"stream":true,"store":false,"client_metadata":{"thread_id":"t-1"},"prompt_cache_key":"t-1","prompt_cache_retention":"24h",` +
		`"safety_identifier":"s","user":"u","metadata":{"a":"b"},"reasoning":{"effort":"xhigh","summary":"auto"}` + extra + `}`
}

// coreBody is a call of the core: the alias of the reading of the books and the words of a step.
func coreBody(t *testing.T, step coresteps.Name) string {
	t.Helper()
	found, ok := coresteps.Named(string(step))
	if !ok {
		t.Fatal(step)
	}
	words, _ := json.Marshal(found.Instructions)
	return `{"model":"canonkeeper-cards","instructions":` + string(words) + `,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"` +
		authorText + `"}]}],"stream":true,"store":false}`
}

// post sends a request to the gateway as Codex or the core would; headers add to the token of the run.
func (stand *stand) post(t *testing.T, body string, headers map[string]string) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, stand.gateway.BaseURL()+"/responses", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+stand.gateway.Token())
	request.Header.Set("Content-Type", "application/json")
	// A request of Codex goes in the turn of the tests, a call of the core names what it gives; a header given empty is not sent.
	if strings.Contains(body, `"model":"`+ChatModel+`"`) {
		request.Header.Set("thread-id", threadOfTheTest)
	} else {
		named, _ := defaultNamed.Encode()
		request.Header.Set(disclosure.Header, named)
	}
	for name, value := range headers {
		if value == "" {
			request.Header.Del(name)
			continue
		}
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response, string(raw)
}

// events are the types of the events of a stream of the gateway.
func events(stream string) []string {
	var types []string
	scanner := bufio.NewScanner(strings.NewReader(stream))
	for scanner.Scan() {
		if kind, ok := strings.CutPrefix(scanner.Text(), "event: "); ok {
			types = append(types, kind)
		}
	}
	return types
}

func completed(stream string) bool {
	types := events(stream)
	return len(types) > 0 && types[len(types)-1] == "response.completed"
}

// GW1.2: both protocols bring the events of Responses to the client; the aliases lead to the models of their roles; a
// change of the models and of the key in the settings holds from the next request, without a restart.
func TestTheGatewayLeadsBothProtocolsToTheModelsOfTheirRoles(t *testing.T) {
	stand := openStand(t)
	responses := &provider{}
	chat := &provider{chat: true}
	responsesServer, chatServer := startProvider(t, responses), startProvider(t, chat)

	stand.settings.set(t, custom(responsesServer, "responses"), "key-test-first-0001")
	response, stream := stand.post(t, chatBody(""), nil)
	if response.StatusCode != http.StatusOK || !completed(stream) || !strings.Contains(stream, "Ответ") {
		t.Fatalf("Responses = %d %s", response.StatusCode, stream)
	}
	response, stream = stand.post(t, coreBody(t, coresteps.Card), nil)
	if response.StatusCode != http.StatusOK || !completed(stream) {
		t.Fatalf("a card by Responses = %d %s", response.StatusCode, stream)
	}
	got := responses.requests()
	if len(got) != 2 || got[0].model != "north-chat" || got[1].model != "north-cards" || got[0].auth != "Bearer key-test-first-0001" {
		t.Fatalf("the provider of Responses got %+v", got)
	}

	draft := custom(chatServer, "chat")
	draft.ChatModel, draft.CardsModel = text("north-chat-2"), text("north-cards-2")
	stand.settings.set(t, draft, "key-test-second-0002")
	response, stream = stand.post(t, chatBody(""), nil)
	if response.StatusCode != http.StatusOK || !completed(stream) || !strings.Contains(stream, "Ответ") {
		t.Fatalf("Chat Completions = %d %s", response.StatusCode, stream)
	}
	response, stream = stand.post(t, coreBody(t, coresteps.Names), nil)
	if response.StatusCode != http.StatusOK || !completed(stream) {
		t.Fatalf("the names by Chat Completions = %d %s", response.StatusCode, stream)
	}
	got = chat.requests()
	if len(got) != 2 || got[0].model != "north-chat-2" || got[1].model != "north-cards-2" || got[1].auth != "Bearer key-test-second-0002" {
		t.Fatalf("the provider of Chat Completions got %+v", got)
	}
	if len(responses.requests()) != 2 {
		t.Fatal("the old address got a request after the change")
	}
}

// GW1.1, GW1.2: a Host other than its own, a request of a page, one without the token, another route and a model that is
// no alias are refused before the provider; the token is nowhere in the log.
func TestTheGatewayKeepsItsDoor(t *testing.T) {
	stand := openStand(t)
	stub := &provider{}
	stand.settings.set(t, custom(startProvider(t, stub), "responses"), "key-test-door-0001")
	send := func(method, path, host string, headers map[string]string, body string) int {
		t.Helper()
		request, _ := http.NewRequest(method, stand.gateway.BaseURL()+path, strings.NewReader(body))
		if host != "" {
			request.Host = host
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		return response.StatusCode
	}
	token := map[string]string{"Authorization": "Bearer " + stand.gateway.Token()}
	for _, check := range []struct {
		what string
		got  int
		want int
	}{
		{"another Host", send(http.MethodPost, "/responses", fmt.Sprintf("localhost:%d", stand.gateway.Port()), token, chatBody("")), http.StatusMisdirectedRequest},
		{"an Origin", send(http.MethodPost, "/responses", "", map[string]string{"Authorization": token["Authorization"], "Origin": "http://127.0.0.1:20000"}, chatBody("")), http.StatusForbidden},
		{"no token", send(http.MethodPost, "/responses", "", nil, chatBody("")), http.StatusUnauthorized},
		{"another token", send(http.MethodPost, "/responses", "", map[string]string{"Authorization": "Bearer " + strings.Repeat("0", 64)}, chatBody("")), http.StatusUnauthorized},
		{"another route", send(http.MethodPost, "/chat/completions", "", token, chatBody("")), http.StatusNotFound},
		{"another method", send(http.MethodGet, "/responses", "", token, ""), http.StatusNotFound},
		{"a model that is no alias", send(http.MethodPost, "/responses", "", token, strings.Replace(chatBody(""), "canonkeeper-chat", "north-chat", 1)), http.StatusBadRequest},
		{"words of the core that are no step", send(http.MethodPost, "/responses", "", token, strings.Replace(chatBody(""), "canonkeeper-chat", "canonkeeper-cards", 1)), http.StatusBadRequest},
	} {
		if check.got != check.want {
			t.Errorf("%s = %d, want %d", check.what, check.got, check.want)
		}
	}
	if got := stub.requests(); len(got) != 0 {
		t.Fatalf("the provider got %d requests", len(got))
	}
	if len(stand.gateway.Token()) != 64 || strings.Contains(stand.log.String(), stand.gateway.Token()) {
		t.Fatal("the token is not 32 bytes or stands in the log")
	}
	if rows := stand.journal.all(); len(rows) != 0 {
		t.Fatalf("refused requests left rows %+v", rows)
	}
}

// GW1.3: «Определить» probes an address without the author's text — no /responses there is Chat Completions — keeps the
// answer for the address and probes a new address again.
func TestAutoFindsTheProtocolOnceForAnAddress(t *testing.T) {
	stand := openStand(t)
	chat := &provider{chat: true}
	responses := &provider{}
	chatServer, responsesServer := startProvider(t, chat), startProvider(t, responses)
	stand.settings.set(t, custom(chatServer, "auto"), "")
	for range 2 {
		if response, stream := stand.post(t, chatBody(""), nil); response.StatusCode != http.StatusOK || !completed(stream) {
			t.Fatalf("auto at a provider of Chat = %d %s", response.StatusCode, stream)
		}
	}
	got := chat.requests()
	if len(got) != 3 || got[0].path != "/v1/responses" || got[1].path != "/v1/chat/completions" || got[2].path != "/v1/chat/completions" {
		t.Fatalf("the provider of Chat got %+v", got)
	}
	if strings.Contains(got[0].raw, authorText) || got[0].raw != "{}" {
		t.Fatalf("the probe carried %q", got[0].raw)
	}
	stand.settings.set(t, custom(responsesServer, "auto"), "")
	if response, stream := stand.post(t, chatBody(""), nil); response.StatusCode != http.StatusOK || !completed(stream) {
		t.Fatalf("auto at a provider of Responses = %d %s", response.StatusCode, stream)
	}
	if got := responses.requests(); len(got) != 2 || got[0].raw != "{}" || got[1].path != "/v1/responses" || got[1].model != "north-chat" {
		t.Fatalf("the provider of Responses got %+v", got)
	}
	probes := 0
	for _, row := range stand.journal.all() {
		if row.Purpose == modeljournal.PurposeProbe {
			probes++
		}
	}
	if probes != 2 {
		t.Fatalf("probes in the journal = %d, want 2", probes)
	}
}

// keyed is a provider that refuses a request with a key it does not know before it looks at the path: 401, or 429 while
// it is busy, then the provider behind it.
type keyed struct {
	key    string
	status atomic.Int64
	next   http.Handler
}

func (stub *keyed) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if status := int(stub.status.Load()); status != 0 || r.Header.Get("Authorization") != "Bearer "+stub.key {
		if status == 0 {
			status = http.StatusUnauthorized
		}
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(status)
		return
	}
	stub.next.ServeHTTP(w, r)
}

// GW1.3: a probe the provider refused for the key or for the rate tells nothing of the path: the refusal goes to the
// client with its status, nothing is kept, and the next request with the right key probes the same address again.
func TestAutoKeepsNothingOfAProbeRefusedBeforeThePath(t *testing.T) {
	stand := openStand(t)
	chat := &provider{chat: true}
	door := &keyed{key: "key-test-right-0001", next: chat}
	server := startProvider(t, door)
	stand.settings.set(t, custom(server, "auto"), "key-test-wrong-0001")
	if response, _ := stand.post(t, chatBody(""), nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a probe with a wrong key = %d", response.StatusCode)
	}
	door.status.Store(http.StatusTooManyRequests)
	stand.settings.set(t, custom(server, "auto"), "key-test-right-0001")
	if response, _ := stand.post(t, chatBody(""), nil); response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") != "7" {
		t.Fatalf("a probe of a busy provider = %d %q", response.StatusCode, response.Header.Get("Retry-After"))
	}
	door.status.Store(0)
	if response, stream := stand.post(t, chatBody(""), nil); response.StatusCode != http.StatusOK || !completed(stream) {
		t.Fatalf("the right key = %d %s", response.StatusCode, stream)
	}
	if got := chat.requests(); len(got) != 2 || got[0].path != "/v1/responses" || got[1].path != "/v1/chat/completions" {
		t.Fatalf("the provider of Chat got %+v", got)
	}
}

// GW1.4: the provider gets no field and no header the client keeps for itself; a local server without a key gets no
// Authorization at all, and a built-in tool of the provider is refused by the module before it.
func TestTheProviderGetsOnlyWhatItNeeds(t *testing.T) {
	stand := openStand(t)
	responses := &provider{}
	stand.settings.set(t, custom(startProvider(t, responses), "responses"), "key-test-fields-0001")
	clientHeaders := map[string]string{
		"thread-id": threadOfTheTest, "session-id": "s-1", "x-client-request-id": "t-1", "x-codex-window-id": "w-1",
		"x-codex-turn-metadata": "{}", "originator": "codex_cli_rs", "User-Agent": "codex_cli_rs/0.167.2", modelapi.JobHeader: "job-1",
	}
	if response, stream := stand.post(t, chatBody(""), clientHeaders); response.StatusCode != http.StatusOK || !completed(stream) {
		t.Fatalf("the request = %d %s", response.StatusCode, stream)
	}
	got := responses.requests()[0]
	// The fields of GW1.4 by their names in the spec, not by the list of the gateway: a field taken off that list fails here.
	for _, field := range []string{"client_metadata", "prompt_cache_key", "prompt_cache_retention", "safety_identifier", "user", "metadata"} {
		if !strings.Contains(chatBody(""), `"`+field+`":`) {
			t.Fatalf("the request of the test carries no %s", field)
		}
		if _, kept := got.body[field]; kept {
			t.Errorf("the provider got the field %s", field)
		}
	}
	for name := range clientHeaders {
		if name != "User-Agent" && got.header.Get(name) != "" {
			t.Errorf("the provider got the header %s", name)
		}
	}
	if strings.Contains(strings.ToLower(got.header.Get("User-Agent")), "codex") {
		t.Errorf("the provider got the User-Agent %q", got.header.Get("User-Agent"))
	}
	var reasoning struct {
		Effort  string `json:"effort"`
		Summary string `json:"summary"`
	}
	var limit int
	if json.Unmarshal(got.body["reasoning"], &reasoning) != nil || reasoning.Effort != "medium" || reasoning.Summary != "auto" ||
		json.Unmarshal(got.body["max_output_tokens"], &limit) != nil || limit != 16_000 {
		t.Fatalf("the effort and the limit of the author = %s %s", got.body["reasoning"], got.body["max_output_tokens"])
	}

	chat := &provider{chat: true}
	ollama := aisettings.OwnDraft{Preset: "ollama", ChatModel: text("qwen"), CardsModel: text("qwen"), Advanced: aisettings.AdvancedDraft{Reasoning: text("none")}}
	stand.settings.set(t, ollama, "key-test-kept-for-another-0001")
	stand.settings.mu.Lock()
	stand.settings.settings.Own.BaseURL = startProvider(t, chat).URL + "/v1"
	stand.settings.mu.Unlock()
	if response, stream := stand.post(t, chatBody(""), nil); response.StatusCode != http.StatusOK || !completed(stream) {
		t.Fatalf("Ollama = %d %s", response.StatusCode, stream)
	}
	local := chat.requests()[0]
	if local.auth != "" {
		t.Fatalf("a local server got Authorization %q", local.auth)
	}
	if _, kept := local.body["reasoning_effort"]; kept {
		t.Fatal("«Нет» of the author still reasons")
	}

	builtIn := chatBody(`,"tools":[{"type":"web_search"}]`)
	if response, body := stand.post(t, builtIn, nil); response.StatusCode != http.StatusBadRequest || !strings.Contains(body, "unsupported_capability") {
		t.Fatalf("a built-in tool = %d %s", response.StatusCode, body)
	}
	if len(chat.requests()) != 1 {
		t.Fatal("the provider got the request with a built-in tool")
	}
}

// GW1.5: twenty requests at once with «Запросов одновременно» 4 — the provider never has more than four; the others wait
// their turn. A 429 of the provider comes to the client as it is, with its Retry-After, and is not repeated.
func TestTheProviderGetsAtMostTheAuthorsNumberAtOnce(t *testing.T) {
	stand := openStand(t)
	stub := &provider{hold: 50 * time.Millisecond}
	draft := custom(startProvider(t, stub), "responses")
	draft.Advanced.Parallel = number(4)
	stand.settings.set(t, draft, "key-test-gate-0001")
	var wait sync.WaitGroup
	failed := atomic.Int64{}
	for index := range 20 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			body := chatBody("")
			if index%2 == 1 {
				body = coreBody(t, coresteps.Card)
			}
			if response, stream := stand.post(t, body, nil); response.StatusCode != http.StatusOK || !completed(stream) {
				failed.Add(1)
			}
		}()
	}
	wait.Wait()
	if failed.Load() != 0 || len(stub.requests()) != 20 || stub.most.Load() > 4 || stub.most.Load() < 2 {
		t.Fatalf("failed %d, requests %d, at most %d at once", failed.Load(), len(stub.requests()), stub.most.Load())
	}

	limited := &provider{status: http.StatusTooManyRequests, retry: "17"}
	stand.settings.set(t, custom(startProvider(t, limited), "responses"), "key-test-gate-0001")
	response, body := stand.post(t, chatBody(""), nil)
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") != "17" || !strings.Contains(body, "rate_limit_exceeded") {
		t.Fatalf("a 429 of the provider = %d %q %s", response.StatusCode, response.Header.Get("Retry-After"), body)
	}
	if len(limited.requests()) != 1 {
		t.Fatalf("the gateway repeated the refusal: %d requests", len(limited.requests()))
	}
}

// GW1.6: without «Свой ИИ», without a model of the role or without the key the variant needs, the helper is not
// connected: 400 helper.not_connected — a status Codex does not repeat — and the provider is not asked.
func TestWithoutTheHelperNothingGoesToTheProvider(t *testing.T) {
	stand := openStand(t)
	stub := &provider{}
	server := startProvider(t, stub)
	refused := func(what string) {
		t.Helper()
		response, body := stand.post(t, chatBody(""), nil)
		if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, codeNotConnected) {
			t.Fatalf("%s = %d %s", what, response.StatusCode, body)
		}
	}
	refused("no settings")
	draft := custom(server, "responses")
	draft.CardsModel = nil
	stand.settings.set(t, draft, "key-test-none-0001")
	refused("no model of the reading")
	stand.settings.set(t, aisettings.OwnDraft{Preset: "deepseek", ChatModel: text("a"), CardsModel: text("b")}, "")
	refused("DeepSeek without its key")
	if len(stub.requests()) != 0 || len(stand.journal.all()) != 0 {
		t.Fatal("a request went to the provider or into the journal")
	}
}

// GW1.7: a request of Codex and a call of the core leave their rows: the chat with the Job its turn put for the thread,
// the step of the core by its words with the Job of its header — which the provider never sees — the host, the model, the
// tokens, the size and the outcome; no row holds the text of a request.
func TestEveryRequestLeavesItsRow(t *testing.T) {
	stand := openStand(t)
	stub := &provider{}
	server := startProvider(t, stub)
	stand.settings.set(t, custom(server, "responses"), "key-test-rows-0001")
	release := stand.gateway.Threads().Bind("thread-of-the-turn", "job-of-the-turn")
	chat := chatBody("")
	if response, _ := stand.post(t, chat, map[string]string{"thread-id": "thread-of-the-turn"}); response.StatusCode != http.StatusOK {
		t.Fatal(response.StatusCode)
	}
	release()
	// Outside its turn the thread sends nothing (specs/2026-11-14-desk-journal.md JR1.2).
	if response, body := stand.post(t, chat, map[string]string{"thread-id": "thread-of-the-turn"}); response.StatusCode != http.StatusBadRequest || !strings.Contains(body, codeUnbound) {
		t.Fatal(response.StatusCode, body)
	}
	if response, _ := stand.post(t, chat, nil); response.StatusCode != http.StatusOK {
		t.Fatal(response.StatusCode)
	}
	card := coreBody(t, coresteps.LayerVerify)
	if response, _ := stand.post(t, card, map[string]string{modelapi.JobHeader: "job-of-the-layer"}); response.StatusCode != http.StatusOK {
		t.Fatal(response.StatusCode)
	}
	rows := stand.journal.all()
	if len(rows) != 3 {
		t.Fatalf("rows %+v", rows)
	}
	host := strings.TrimPrefix(server.URL, "http://")
	want := []struct{ purpose, job, model string }{
		{modeljournal.PurposeChat, "job-of-the-turn", "north-chat"},
		{modeljournal.PurposeChat, jobOfTheTest, "north-chat"},
		{string(coresteps.LayerVerify), "job-of-the-layer", "north-cards"},
	}
	for index, row := range rows {
		if row.Purpose != want[index].purpose || row.JobID != want[index].job || row.Model != want[index].model ||
			row.ProviderHost != host || row.Profile != "own" || row.Outcome != modeljournal.OutcomeCompleted || row.At.IsZero() ||
			row.Tokens == nil || *row.Tokens != (modeljournal.Tokens{Input: 10, CachedInput: 4, Output: 3}) || row.RequestBytes <= 0 {
			t.Errorf("row %d = %+v", index, row)
		}
		if encoded, _ := json.Marshal(row); strings.Contains(string(encoded), authorText) || strings.Contains(string(encoded), "Codex base") {
			t.Errorf("row %d holds the text of the request", index)
		}
	}
	if rows[0].RequestBytes != int64(len(chat)) || rows[2].RequestBytes != int64(len(card)) {
		t.Fatalf("sizes %d %d", rows[0].RequestBytes, rows[2].RequestBytes)
	}
	for _, got := range stub.requests() {
		if got.header.Get(modelapi.JobHeader) != "" {
			t.Fatal("the provider got the Job")
		}
	}

	stand.settings.set(t, custom(startProvider(t, &provider{chat: true}), "chat"), "key-test-rows-0001")
	stand.post(t, chatBody(""), nil)
	if last := stand.journal.all()[3]; last.Tokens == nil || *last.Tokens != (modeljournal.Tokens{Input: 10, CachedInput: 4, Output: 3}) {
		t.Fatalf("the tokens of Chat Completions = %+v", last.Tokens)
	}
}
