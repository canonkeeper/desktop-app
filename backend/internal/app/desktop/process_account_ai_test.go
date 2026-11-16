// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
	"canonkeeper/backend/internal/domain/coresteps"
)

// The marks of the tests of the helper of the account: the ids of the permits the stub of the site gives, and the words of a
// question. The tests look for them, never print a value.
const (
	permitMarkPrefix = "permit-mark-of-the-account-"
	questionMark     = "Где стоит застава у перевала Северного предела?"
)

// siteRefusal is a refusal the stub of the site answers an operation with.
type siteRefusal struct {
	status int
	code   string
	retry  string
}

// stubPermit is a permit of the stub: its id, kind, ref, state, outcome, the requests it took and whether an answer of the
// model went under it.
type stubPermit struct {
	id, kind, ref, state, outcome string
	requests                      int
	answered                      bool
}

// accountAISite is the site of the account as the app meets it (specs/2026-11-13-desk-permits.md, specs/2026-11-13-desk-proxy.md):
// the account with its helper, the permits by ref — a known ref answers the same permit and spends nothing —, the stream of a
// turn in Responses and the steps of the core; a test sets its refusals and holds a turn.
type accountAISite struct {
	*siteOfTheTest
	t *testing.T

	mu          sync.Mutex
	permits     map[string]*stubPermit
	refs        map[string]string
	closes      []string
	turns       []heard
	turnBodies  [][]byte
	steps       []string
	stepPermits []string
	turnRefusal *siteRefusal
	stepRefusal map[string]*siteRefusal
	holdTurn    chan struct{}
	held        chan struct{}
	// holdAll holds every request of a turn until the test closes it; arrivals hear the permit of each request it holds.
	holdAll  chan struct{}
	arrivals chan string
	// refuseNew refuses the open of a ref the stub does not know yet — a refusal of the tier at the site.
	refuseNew *siteRefusal
	// down takes every connection and hangs up: the site is not reached.
	down bool
	// drops are the operations — open, close — whose next answer is lost: the stub does the operation and hangs up.
	drops map[string]int
}

func newAccountAISite(t *testing.T) *accountAISite {
	t.Helper()
	site := &accountAISite{
		siteOfTheTest: newSiteOfTheTest(t), t: t, permits: map[string]*stubPermit{}, refs: map[string]string{},
		stepRefusal: map[string]*siteRefusal{}, drops: map[string]int{},
	}
	site.set(site.serve)
	return site
}

var permitPath = regexp.MustCompile(`^/api/v1/app/permits/([^/]+)/(close|units)(?:/([^/]+))?$`)

func (site *accountAISite) serve(w http.ResponseWriter, r *http.Request, request heard) {
	site.mu.Lock()
	down := site.down
	site.mu.Unlock()
	if down {
		hangUp(w)
		return
	}
	switch {
	case request.method == http.MethodGet && request.path == accountPath:
		site.account(w)
	case request.method == http.MethodPost && request.path == "/api/v1/app/permits":
		if site.dropping("open") {
			site.open(httptest.NewRecorder(), request)
			hangUp(w)
			return
		}
		site.open(w, request)
	case permitPath.MatchString(request.path):
		match := permitPath.FindStringSubmatch(request.path)
		if match[2] == "close" && site.dropping("close") {
			site.permit(httptest.NewRecorder(), request, match[1], match[2])
			hangUp(w)
			return
		}
		site.permit(w, request, match[1], match[2])
	case request.method == http.MethodPost && request.path == "/api/v1/app/ai/responses":
		site.turn(w, r, request)
	case request.method == http.MethodPost && strings.HasPrefix(request.path, "/api/v1/app/ai/steps/"):
		site.step(w, request, strings.TrimPrefix(request.path, "/api/v1/app/ai/steps/"))
	default:
		theSite(w, r, request)
	}
}

// account is getAppAccount with the helper of the site and its counters: the questions of the day are the permits of turns.
func (site *accountAISite) account(w http.ResponseWriter) {
	site.mu.Lock()
	questions := 0
	for _, permit := range site.permits {
		if permit.kind == "chat_turn" {
			questions++
		}
	}
	site.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"name":"Северный","email":"a…@example.test","tier":{"code":"writer","title":"Писатель","until":"2026-12-13T00:00:00Z"},
		"helper":{"chatModel":"site-chat","cardsModel":"site-cards","contextWindow":400000,"compactLimit":300000},
		"quota":{"day":"2026-11-13","resetsAt":"2026-11-13T21:00:00Z","chatTurns":{"used":%d,"limit":20},"activeTurns":{"used":0,"limit":2},
		"imports":{"used":0,"limit":5},"reading":{"used":3200000,"limit":40000000},"readingWork":{"used":100,"limit":320000000}}}`, questions)
}

// dropping takes one lost answer of the operation, if the test set one.
func (site *accountAISite) dropping(operation string) bool {
	site.mu.Lock()
	defer site.mu.Unlock()
	if site.drops[operation] == 0 {
		return false
	}
	site.drops[operation]--
	return true
}

// hangUp takes the connection of a request and closes it without an answer.
func hangUp(w http.ResponseWriter) {
	if hijacker, ok := w.(http.Hijacker); ok {
		if connection, _, err := hijacker.Hijack(); err == nil {
			_ = connection.Close()
			return
		}
	}
	panic(http.ErrAbortHandler)
}

// expireUnused ends the permits of turns that took no request as the lease of the site does: expired, unused — their unit
// came back. It answers how many.
func (site *accountAISite) expireUnused() int {
	site.mu.Lock()
	defer site.mu.Unlock()
	expired := 0
	for _, permit := range site.permits {
		if permit.kind == "chat_turn" && permit.state == "open" && permit.requests == 0 {
			permit.state, permit.outcome = "expired", "expired_unused"
			expired++
		}
	}
	return expired
}

func refuseAsTheSite(w http.ResponseWriter, refusal siteRefusal) {
	w.Header().Set("Content-Type", "application/problem+json")
	if refusal.retry != "" {
		w.Header().Set("Retry-After", refusal.retry)
	}
	w.WriteHeader(refusal.status)
	fmt.Fprintf(w, `{"code":%q,"status":%d}`, refusal.code, refusal.status)
}

func (site *accountAISite) writePermit(w http.ResponseWriter, status int, permit *stubPermit) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	outcome := "null"
	if permit.outcome != "" {
		outcome = `"` + permit.outcome + `"`
	}
	// The unit comes back by the rule of the site (desktoptest.ReturnsOnClose); a lease that ended unused gives it back too.
	returned := permit.outcome == "expired_unused" || permit.state == "closed" && desktoptest.Returned(permit.kind, permit.outcome, permit.answered)
	fmt.Fprintf(w, `{"id":%q,"kind":%q,"state":%q,"outcome":%s,"day":"2026-11-13","validUntil":"2026-11-13T12:05:00Z","returned":%v}`,
		permit.id, permit.kind, permit.state, outcome, returned)
}

func (site *accountAISite) open(w http.ResponseWriter, request heard) {
	site.mu.Lock()
	defer site.mu.Unlock()
	kind, ref := request.body["kind"], request.body["ref"]
	if id, known := site.refs[ref]; known {
		site.writePermit(w, http.StatusOK, site.permits[id])
		return
	}
	if site.refuseNew != nil {
		refuseAsTheSite(w, *site.refuseNew)
		return
	}
	permit := &stubPermit{id: fmt.Sprintf("%s%02d", permitMarkPrefix, len(site.permits)+1), kind: kind, ref: ref, state: "open"}
	site.permits[permit.id], site.refs[ref] = permit, permit.id
	site.writePermit(w, http.StatusCreated, permit)
}

func (site *accountAISite) permit(w http.ResponseWriter, request heard, id, operation string) {
	site.mu.Lock()
	defer site.mu.Unlock()
	permit := site.permits[id]
	if permit == nil {
		refuseAsTheSite(w, siteRefusal{status: http.StatusNotFound, code: "app.permit_not_found"})
		return
	}
	switch operation {
	case "close":
		if permit.state == "open" {
			permit.state, permit.outcome = "closed", request.body["outcome"]
		}
		site.closes = append(site.closes, permit.kind+" "+request.body["outcome"])
		site.writePermit(w, http.StatusOK, permit)
	default:
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"day":"2026-11-13","returned":true}`)
	}
}

func unzipped(t *testing.T, raw []byte) []byte {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	body, _ := io.ReadAll(reader)
	return body
}

// turn is streamAppResponses: a refusal of the test, or a held turn until the test lets it go, or one answer in Responses.
func (site *accountAISite) turn(w http.ResponseWriter, r *http.Request, request heard) {
	site.mu.Lock()
	site.turns = append(site.turns, request)
	site.turnBodies = append(site.turnBodies, unzipped(site.t, request.raw))
	refusal, hold, held, all, arrivals := site.turnRefusal, site.holdTurn, site.held, site.holdAll, site.arrivals
	permit := site.permits[request.header.Get("X-CanonKeeper-Permit")]
	if refusal == nil && permit != nil {
		permit.requests++
	}
	site.mu.Unlock()
	if refusal != nil {
		refuseAsTheSite(w, *refusal)
		return
	}
	if permit == nil || permit.state != "open" {
		refuseAsTheSite(w, siteRefusal{status: http.StatusConflict, code: "app.permit_closed"})
		return
	}
	if all != nil {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-held\"}}\n\n")
		w.(http.Flusher).Flush()
		arrivals <- permit.id
		select {
		case <-all:
		case <-r.Context().Done():
			return
		}
	} else if hold != nil {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-held\"}}\n\n")
		w.(http.Flusher).Flush()
		close(held)
		select {
		case <-hold:
		case <-r.Context().Done():
			return
		}
	}
	site.mu.Lock()
	permit.answered = true
	site.mu.Unlock()
	streamSiteAnswer(w, fmt.Sprintf("resp-%d", len(site.turns)), "Застава стоит у перевала.")
}

// streamSiteAnswer streams one answer of the provider of the site as the site hands it on: created, the message, completed
// with its usage.
func streamSiteAnswer(w http.ResponseWriter, id, text string) {
	message := map[string]any{
		"type": "message", "role": "assistant", "id": "msg-" + id, "phase": "final_answer",
		"content": []map[string]any{{"type": "output_text", "text": text}},
	}
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": id}},
		{"type": "response.output_item.added", "item": message},
		{"type": "response.output_text.delta", "delta": text},
		{"type": "response.output_item.done", "item": message},
		{"type": "response.completed", "response": map[string]any{"id": id, "usage": map[string]any{
			"input_tokens": 100, "input_tokens_details": nil, "output_tokens": 10, "output_tokens_details": nil, "total_tokens": 110,
		}}},
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		encoded, _ := json.Marshal(event)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], encoded)
	}
}

// step is runAppStep: a refusal of the test or the answer of the step whole.
func (site *accountAISite) step(w http.ResponseWriter, request heard, step string) {
	site.mu.Lock()
	site.steps = append(site.steps, step)
	site.stepPermits = append(site.stepPermits, request.header.Get("X-CanonKeeper-Permit"))
	refusal := site.stepRefusal[step]
	site.mu.Unlock()
	if refusal != nil {
		refuseAsTheSite(w, *refusal)
		return
	}
	text := `{}`
	switch coresteps.Name(step) {
	case coresteps.Card:
		text = `{"summary": "Пересказ главы", "characters": [{"name": "Вера", "p": 1}], "events": [{"event": "Сигнал над перевалом", "p": 1}]}`
	case coresteps.Names:
		text = `{"persons":[]}`
	case coresteps.LayerBook:
		text = `{"arcs":[],"threads":[]}`
	case coresteps.LayerReduce, coresteps.LayerGap:
		text = `{"open":[]}`
	case coresteps.LayerVerify:
		text = `{"v":"keep"}`
	}
	encoded, _ := json.Marshal(text)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"text":%s,"model":"site-cards","usage":{"calls":1,"inputTokens":5000,"cachedInputTokens":0,"outputTokens":500,"reasoningOutputTokens":0}}`, encoded)
}

func (site *accountAISite) closed() []string {
	site.mu.Lock()
	defer site.mu.Unlock()
	return append([]string(nil), site.closes...)
}

func (site *accountAISite) opened(kind string) int {
	site.mu.Lock()
	defer site.mu.Unlock()
	count := 0
	for _, permit := range site.permits {
		if permit.kind == kind {
			count++
		}
	}
	return count
}

func (site *accountAISite) stepsOf(step coresteps.Name) int {
	site.mu.Lock()
	defer site.mu.Unlock()
	count := 0
	for _, asked := range site.steps {
		if asked == string(step) {
			count++
		}
	}
	return count
}

// accountRun is a run of the program of the stub with the helper of the account chosen.
type accountRun struct {
	process *desktoptest.Process
	window  *client
	data    string
}

// launchAccount starts the program linked with the stub — the shell keeps the pass — and chooses the helper of the account.
func (site *accountAISite) launchAccount(t *testing.T, binary, resources, data string) accountRun {
	t.Helper()
	return site.launchAccountOf(t, binary, versionOfTheLink, resources, data)
}

// launchAccountOf is launchAccount of a program of the version.
func (site *accountAISite) launchAccountOf(t *testing.T, binary, version, resources, data string) accountRun {
	t.Helper()
	launch := desktoptest.Launch{Data: data, Resources: resources, Environment: []string{"SSL_CERT_FILE=" + site.certFile}}
	process := desktoptest.Begin(t, binary, launch, desktoptest.StartOf(launch, version, linkedRecords()))
	ready := process.Ready(2 * time.Minute)
	if ready.Type != "ready" {
		t.Fatalf("the backend answered %s %s instead of ready", ready.Type, ready.Reason)
	}
	process.Serve(linkedRecords(), desktoptest.KeepAll)
	window := &client{t: t, origin: fmt.Sprintf("http://127.0.0.1:%d", ready.Port), http: &http.Client{Timeout: desktoptest.Waited(2 * time.Minute)}}
	if response := window.open(ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), map[string]any{"source": "account"}); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings account = %d %s", response.StatusCode, body)
	}
	return accountRun{process: process, window: window, data: data}
}

// chat opens a chat of a new saga with a book.
func (run accountRun) chat(t *testing.T, title string) (readingWindow, string) {
	t.Helper()
	saga := openSaga(t, run.window, title, bookOf("Глава 1. Сигнальные огни", "Глава 2. Белый порт"))
	saga.data = run.data
	// The chat reads the published text once the book is imported and found by the search: the reading of the saga waits
	// for the consent then.
	awaitTrue(t, "the import of the book", 2*time.Minute, func() bool {
		var status struct {
			Preparation *struct {
				Waiting       string `json:"waiting"`
				Askable       bool   `json:"askable"`
				ChaptersTotal int    `json:"chaptersTotal"`
			} `json:"preparation"`
		}
		saga.read("/api/v1/projects/"+saga.project+"/indexing-status", &status)
		return status.Preparation != nil && status.Preparation.Waiting != "" && status.Preparation.Askable && status.Preparation.ChaptersTotal == 2
	})
	response, body := run.window.post("/api/v1/projects/"+saga.project+"/chats", map[string]string{"sourceView": "published", "scope": "project"}, "chat-of-"+saga.project)
	var chat struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &chat) != nil {
		t.Fatalf("createChatSession = %d %s", response.StatusCode, body)
	}
	return saga, chat.ID
}

// jobOf is getJob: its status, the code of its failure and whether its question came back.
func (run accountRun) jobOf(jobID string) (status, code, returned string) {
	var job struct {
		Status string `json:"status"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
		QuestionReturned string `json:"questionReturned"`
	}
	run.window.read("/api/v1/jobs/"+jobID, &job)
	if job.Error != nil {
		code = job.Error.Code
	}
	return job.Status, code, job.QuestionReturned
}

// settledJobOf is jobOf once the count of the question is said: the end of a turn closes its permit after its own
// transaction, and until the site answers the question is «решается» (pending) — a read at once may see that.
func (run accountRun) settledJobOf(t *testing.T, jobID string) (status, code, returned string) {
	t.Helper()
	awaitTrue(t, "the count of the question of the turn "+jobID, 15*time.Second, func() bool {
		status, code, returned = run.jobOf(jobID)
		return returned != "pending"
	})
	return status, code, returned
}

// accountProgram is the program of the stub with short repeats and checks of the pauses.
func (site *accountAISite) accountProgram(t *testing.T) string {
	t.Helper()
	name := "account-ai-" + strings.ReplaceAll(strings.TrimPrefix(site.url, "https://"), ":", "-")
	return program(t, programVariant{name: name, version: versionOfTheLink, site: site.url, retryPause: "10ms", resumeEvery: "2s"})
}

// waitFor waits for a condition of a process test.
func awaitTrue(t *testing.T, what string, timeout time.Duration, condition func() bool) {
	t.Helper()
	for deadline := desktoptest.Deadline(timeout); !condition(); time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not come", what)
		}
	}
}

// specs/2026-11-13-desk-account-ai.md AI1.4, AI1.7, AI2.1, AI2.4, AI2.5, AI2.6, AI2.7, AI2.9, AI2.11, AI4.1, AI4.2: the app
// linked with a stub of the site and the helper of the account chosen. A question opens a permit of a turn as the last step of
// its start, goes through the gateway to the stream of the site under it — the body compressed, the fields the site needs
// not and the bound of an answer taken off, only the headers of the client to the site — and its end closes the permit
// answered; the question counts as the site counted it. The fence of the day of the site ends the next turn with its own
// reason without a repeat of Codex, closes it failed, the site returned it; the next question is refused at once, opening no
// permit. The journal names the profile and the host of the site and the code of the refusal; no mark of a permit, the pass
// or a question is in the output of the backend, the data directory or the environment and arguments of its processes.
func TestATurnOfTheAccountGoesThroughTheSiteUnderItsPermit(t *testing.T) {
	resources := processResources(t)
	site := newAccountAISite(t)
	data := filepath.Join(t.TempDir(), "data")
	run := site.launchAccount(t, site.accountProgram(t), resources, data)
	saga, chatID := run.chat(t, "Северный предел")

	turn, status, body := run.window.ask(chatID, questionMark, "question-of-the-account")
	if status != http.StatusAccepted || run.window.jobEnded(turn, 3*time.Minute) != "succeeded" {
		t.Fatalf("the question = %d %s%s", status, body, saga.journey())
	}
	if site.opened("chat_turn") != 1 || len(site.turns) == 0 {
		t.Fatalf("the site opened %d permits of turns and heard %d requests", site.opened("chat_turn"), len(site.turns))
	}
	site.mu.Lock()
	request, sent := site.turns[0], site.turnBodies[0]
	site.mu.Unlock()
	allowed := map[string]bool{"Authorization": true, "X-Canonkeeper-Permit": true, "Content-Type": true, "Content-Encoding": true,
		"Accept": true, "User-Agent": true, "Content-Length": true, "Accept-Encoding": true}
	for name := range request.header {
		if !allowed[name] {
			t.Errorf("the site got the header %s", name)
		}
	}
	if request.query != "" || request.origin != "" || request.cookie != "" || request.header.Get("Content-Encoding") != "gzip" ||
		request.authorization != "Bearer "+passMark || !strings.HasPrefix(request.header.Get("X-CanonKeeper-Permit"), permitMarkPrefix) {
		t.Fatalf("the request of the turn went as %q %q %q %v", request.query, request.origin, request.cookie, request.header.Get("Content-Encoding"))
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(sent, &fields) != nil || fields["input"] == nil {
		t.Fatal("the body of the turn is not the request of Codex")
	}
	for _, field := range []string{"max_output_tokens", "client_metadata", "prompt_cache_key", "metadata", "user", "safety_identifier"} {
		if _, kept := fields[field]; kept {
			t.Errorf("the site got the field %s", field)
		}
	}
	if closes := site.closed(); len(closes) != 1 || closes[0] != "chat_turn answered" {
		t.Fatalf("the closes = %v", closes)
	}
	if _, _, returned := run.settledJobOf(t, turn); returned != "no" {
		t.Fatalf("an answered question came back: %q", returned)
	}
	var counts struct {
		ChatTurns   struct{ Used, Limit int64 } `json:"chatTurns"`
		ActiveTurns struct{ Used, Limit int64 } `json:"activeTurns"`
	}
	run.window.read("/api/v1/account/quota", &counts)
	if counts.ChatTurns.Used != 1 || counts.ChatTurns.Limit != 20 || counts.ActiveTurns.Limit != 2 {
		t.Fatalf("getAccountQuota = %+v; want the counters of the site", counts)
	}

	// The fence of the day of the site: a lasting refusal, the reason of the turn, no repeat of Codex.
	site.mu.Lock()
	site.turnRefusal = &siteRefusal{status: http.StatusForbidden, code: "ai.daily_fence"}
	heardBefore := len(site.turns)
	site.mu.Unlock()
	fenced, status, body := run.window.ask(chatID, "Кто зажёг огни?", "question-of-the-fence")
	// Codex does not repeat the status of a lasting refusal: the turn ends at once, not after its repeats.
	if status != http.StatusAccepted || run.window.jobEnded(fenced, 45*time.Second) != "failed" {
		t.Fatalf("the fenced question = %d %s", status, body)
	}
	if _, code, returned := run.settledJobOf(t, fenced); code != "model.daily_fence" || returned != "yes" {
		t.Fatalf("the fenced turn = %q, came back %q", code, returned)
	}
	site.mu.Lock()
	repeats := len(site.turns) - heardBefore
	site.mu.Unlock()
	if repeats != 1 {
		t.Fatalf("Codex sent the fenced request %d times", repeats)
	}
	if closes := site.closed(); len(closes) != 2 || closes[1] != "chat_turn failed" {
		t.Fatalf("the closes = %v", closes)
	}
	permits := site.opened("chat_turn")
	_, status, body = run.window.ask(chatID, "А где Вера?", "question-after-the-fence")
	if status != http.StatusTooManyRequests || !containsCode(body, "ai.daily_fence") || site.opened("chat_turn") != permits {
		t.Fatalf("a question after the fence = %d %s, permits %d → %d", status, body, permits, site.opened("chat_turn"))
	}

	// The journal of the requests: the profile account, the host of the site, the code of the refusal.
	connection := database(t, data)
	var rows, refused int
	if err := connection.QueryRow(context.Background(), `
		SELECT count(*), count(*) FILTER (WHERE refusal_code = 'ai.daily_fence')
		FROM model_requests WHERE profile_name = 'account' AND purpose = 'chat' AND provider_host = $1`,
		strings.TrimPrefix(site.url, "https://")).Scan(&rows, &refused); err != nil {
		t.Fatal(err)
	}
	if rows < 2 || refused != 1 {
		t.Fatalf("the journal of the account: %d rows, %d refused", rows, refused)
	}
	var state struct {
		Network []struct {
			Host    string `json:"host"`
			Purpose string `json:"purpose"`
		} `json:"network"`
	}
	run.window.read("/api/v1/desktop/state", &state)
	if len(state.Network) != 1 || !strings.Contains(state.Network[0].Purpose, "вопросы помощнику и чтение книг") {
		t.Fatalf("the network = %+v", state.Network)
	}
	site.mu.Lock()
	refs := make([]string, 0, len(site.refs))
	for ref := range site.refs {
		refs = append(refs, ref)
	}
	site.mu.Unlock()
	if len(refs) < 2 {
		t.Fatalf("the refs = %d", len(refs))
	}
	marksAreNowhere(t, run, append([]string{permitMarkPrefix, passMark}, refs...), questionMark)
}

// marksAreNowhere looks for the marks — the ids of the permits, the pass and the refs — in the output of the backend, the
// files of its data directory (the homes of Codex among them), the cluster aside, and the environment and the arguments of
// its processes; the database is asked by queries over every table. The words of the question live in the thread of Codex
// and the chat, and only the output of the backend must not hold them.
func marksAreNowhere(t *testing.T, run accountRun, marks []string, question string) {
	t.Helper()
	for _, mark := range append(append([]string(nil), marks...), question) {
		if bytes.Contains(run.process.Stderr.Bytes(), []byte(mark)) {
			t.Error("the output of the backend holds a mark")
		}
	}
	_ = filepath.WalkDir(run.data, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() && entry.Name() == "pg" {
			return filepath.SkipDir
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if markIn(raw, marks) {
			t.Errorf("%s holds a mark", path)
		}
		return nil
	})
	for _, pid := range family(t, run.process.Command.Process.Pid, run.data) {
		for _, name := range []string{"environ", "cmdline"} {
			if raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), name)); err == nil && markIn(raw, marks) {
				t.Errorf("the %s of a process of the backend holds a mark", name)
			}
		}
	}
	if leaked := queryMarks(t, run.data, marks); leaked != 0 {
		t.Fatalf("%d rows of the database hold a mark", leaked)
	}
}

// AI2.7, AI2.3, AI2.9, DT3.8: the app has two workers of the chat, so of three questions two run at the site and the third
// waits in the queue. The stop of the app closes the permits of the two running turns failed before it cancels them — the
// site decides the return by what it saw — and sends nothing for the one no worker took: its lease gives the unit back. No
// close says canceled; every question is pending until the word of the site. After a restart a new question goes.
func TestTheStopOfTheAppClosesARunningTurnFailed(t *testing.T) {
	resources := processResources(t)
	site := newAccountAISite(t)
	data := filepath.Join(t.TempDir(), "data")
	binary := site.accountProgram(t)
	run := site.launchAccount(t, binary, resources, data)
	saga, chatID := run.chat(t, "Северный предел")
	second, third := run.anotherChat(t, saga.project, "chat-second"), run.anotherChat(t, saga.project, "chat-third")
	arrived, release := site.holdTurns(t)
	one := run.asked(t, chatID, questionMark, "question-held")
	two := run.asked(t, second, "Где стоит застава?", "question-held-two")
	arrived(2)
	queued := run.asked(t, third, "Кто ждал на стене?", "question-queued")
	awaitTrue(t, "the permit of the queued turn", time.Minute, func() bool { return site.opened("chat_turn") == 3 })
	run.process.End()
	if closes := site.closed(); len(closes) != 2 || closes[0] != "chat_turn failed" || closes[1] != "chat_turn failed" {
		t.Fatalf("the stop closed %v", closes)
	}
	release()

	again := site.launchAccount(t, binary, resources, data)
	for _, turn := range []string{one, two, queued} {
		status, _, returned := again.jobOf(turn)
		if (status != "canceled" && (turn == queued || status != "failed")) || returned != "pending" {
			t.Fatalf("the stopped turn is %s, its question came back %q", status, returned)
		}
	}
	answered, status, body := again.window.ask(chatID, "Кто зажёг огни?", "question-after-the-restart")
	if status != http.StatusAccepted || again.window.jobEnded(answered, 3*time.Minute) != "succeeded" {
		t.Fatalf("a question after the restart = %d %s", status, body)
	}
	if closes := site.closed(); len(closes) != 3 || closes[2] != "chat_turn answered" {
		t.Fatalf("the closes after the restart = %v", closes)
	}
	if bytes.Contains(again.process.Stderr.Bytes(), []byte("desktop_pending_not_recalled")) {
		t.Fatal("the questions left pending were not recalled at the start")
	}
}

// AI3.1, AI3.2, AI3.4, AI3.5, AI3.7: the reading of the books by the helper of the account goes only under its consent: the
// steps of the core go to the site under the permit of the reading of their Job, the end of the Job closes it done; a refusal
// of the characters of the tier pauses the reading at its first refusal with the limits in the status, and no step of the
// other chapters goes; once the site has room again the reading goes on by itself.
func TestTheReadingOfTheAccountGoesUnderItsConsent(t *testing.T) {
	resources := processResources(t)
	site := newAccountAISite(t)
	data := filepath.Join(t.TempDir(), "data")
	run := site.launchAccount(t, site.accountProgram(t), resources, data)
	saga, _ := run.chat(t, "Северный предел")
	awaitTrue(t, "the wait for the consent", 2*time.Minute, func() bool {
		status := saga.status()
		return status.Preparation != nil && status.Preparation.Waiting == "consent"
	})
	if steps := site.stepsOf(coresteps.Card); steps != 0 {
		t.Fatalf("%d cards were read without the consent", steps)
	}
	var estimate struct {
		Chapters int    `json:"chapters"`
		Unit     string `json:"unit"`
		Host     string `json:"host"`
		Account  *struct {
			Characters int64 `json:"characters"`
		} `json:"account"`
	}
	saga.read("/api/v1/projects/"+saga.project+"/reading/estimate", &estimate)
	if estimate.Chapters != 2 || estimate.Unit != "tokens" || estimate.Account == nil || estimate.Account.Characters <= 0 ||
		estimate.Host != strings.TrimPrefix(site.url, "https://") {
		t.Fatalf("the estimate of the account = %+v", estimate)
	}
	consent := saga.setConsent(map[string]any{"state": "allowed"})
	if consent.State != "allowed" || consent.Unit != "tokens" || consent.StopAt != nil {
		t.Fatalf("the consent of the account = %+v", consent)
	}
	saga.readThrough(3 * time.Minute)
	if site.stepsOf(coresteps.Card) != 2 || site.opened("reading") == 0 {
		t.Fatalf("cards %d, permits of the reading %d%s", site.stepsOf(coresteps.Card), site.opened("reading"), saga.journey())
	}
	site.mu.Lock()
	for index, step := range site.steps {
		if step != string(coresteps.Scan) && !strings.HasPrefix(site.stepPermits[index], permitMarkPrefix) {
			t.Errorf("the step %s went without a permit", step)
		}
	}
	site.mu.Unlock()
	awaitTrue(t, "the close of the reading", time.Minute, func() bool {
		for _, closed := range site.closed() {
			if closed == "reading done" {
				return true
			}
		}
		return false
	})

	// A second book, and the characters of the tier are over: the reading pauses at the first refusal.
	site.mu.Lock()
	site.stepRefusal[string(coresteps.Card)] = &siteRefusal{status: http.StatusConflict, code: "quota.account_characters"}
	site.mu.Unlock()
	cards := site.stepsOf(coresteps.Card)
	run.window.importFileAs(saga.project, "Второй том.md", bookOf("Глава 3. Ледяной перевал", "Глава 4. Застава", "Глава 5. Огни"), "import-of-the-second-book")
	paused := saga.pausedAt(3 * time.Minute)
	if paused.Pause == nil || *paused.Pause != "characters" || site.stepsOf(coresteps.Card)-cards != 1 {
		t.Fatalf("the pause = %+v after %d cards%s", paused, site.stepsOf(coresteps.Card)-cards, saga.journey())
	}
	var status struct {
		Reading *struct {
			Pause  *string `json:"pause"`
			Limits *struct {
				Characters struct{ Used, Limit int64 }
			} `json:"limits"`
		} `json:"reading"`
	}
	saga.read("/api/v1/projects/"+saga.project+"/indexing-status", &status)
	if status.Reading == nil || status.Reading.Limits == nil || status.Reading.Limits.Characters.Limit != 40000000 {
		t.Fatalf("the status of the pause = %+v", status.Reading)
	}
	// The site has room: the reading goes on by itself at the check of the limits.
	site.mu.Lock()
	delete(site.stepRefusal, string(coresteps.Card))
	site.mu.Unlock()
	saga.readThrough(3 * time.Minute)
	if site.stepsOf(coresteps.Card)-cards < 4 {
		t.Fatalf("the cards after the room = %d%s", site.stepsOf(coresteps.Card)-cards, saga.journey())
	}
}
