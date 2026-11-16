// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/appwork"
	"canonkeeper/backend/internal/domain/coresteps"
	"canonkeeper/backend/internal/domain/modeljournal"
	"canonkeeper/backend/internal/jobs"
)

// The marks of the permits of the tests: a test looks for them, never prints a value.
const (
	turnPermitMark    = "permit-mark-of-the-turn"
	readingPermitMark = "permit-mark-of-the-reading"
	siteHostOfTheTest = "site.example.test"
)

// siteAnswer is what the site of the test answers one request: a refusal, or a stream of a turn, or the answer of a step.
type siteAnswer struct {
	refusal *appwork.Refusal
	text    string
}

// accountOfTheTest is the helper of the account as the gateway sees it: the works of the Jobs, the permits of the reading,
// and a site whose answers the test sets; it hears what the gateway tells it.
type accountOfTheTest struct {
	mu        sync.Mutex
	unlinked  bool
	works     map[string]JobWork
	answers   []siteAnswer
	hold      chan struct{}
	asked     []string
	bodies    [][]byte
	permits   []string
	refused   []appwork.Answer
	accepted  []string
	dropped   []string
	remember  map[string]string
	renewals  int
	running   atomic.Int64
	most      atomic.Int64
	readingAt atomic.Int64
	readMost  atomic.Int64
	// journal and recordsAt: how many records lay when each request reached the site (JR1.1).
	journal   *keptJournal
	recordsAt []int
}

func newAccountOfTheTest() *accountOfTheTest {
	return &accountOfTheTest{works: map[string]JobWork{}, remember: map[string]string{}}
}

func (account *accountOfTheTest) Linked() bool {
	account.mu.Lock()
	defer account.mu.Unlock()
	return !account.unlinked
}

func (account *accountOfTheTest) Host() string { return siteHostOfTheTest }

func (account *accountOfTheTest) WorkOf(_ context.Context, jobID string) (JobWork, bool) {
	account.mu.Lock()
	defer account.mu.Unlock()
	work, ok := account.works[jobID]
	return work, ok
}

func (account *accountOfTheTest) Materials(context.Context, string) (string, error) { return "", nil }

func (account *accountOfTheTest) Reading(_ context.Context, _ string, renew bool) (string, error) {
	account.mu.Lock()
	defer account.mu.Unlock()
	if renew {
		account.renewals++
	}
	return readingPermitMark, nil
}

func (account *accountOfTheTest) Refused(_, _ string, answer appwork.Answer) {
	account.mu.Lock()
	defer account.mu.Unlock()
	account.refused = append(account.refused, answer)
}

func (account *accountOfTheTest) Accepted(jobID string) {
	account.mu.Lock()
	defer account.mu.Unlock()
	account.accepted = append(account.accepted, jobID)
}

func (account *accountOfTheTest) Dropped(jobID string) {
	account.mu.Lock()
	defer account.mu.Unlock()
	account.dropped = append(account.dropped, jobID)
}

func (account *accountOfTheTest) RefusedNow(purpose, _ string) string {
	account.mu.Lock()
	defer account.mu.Unlock()
	return account.remember[purpose]
}

// next is the answer of the site to the next request, the last one again when the test set no more.
func (account *accountOfTheTest) next(kind, permit string, body []byte) siteAnswer {
	account.mu.Lock()
	if account.journal != nil {
		account.recordsAt = append(account.recordsAt, len(account.journal.sent()))
	}
	account.asked = append(account.asked, kind)
	account.bodies = append(account.bodies, body)
	account.permits = append(account.permits, permit)
	answer := siteAnswer{text: "ответ"}
	if len(account.answers) > 0 {
		answer = account.answers[0]
		if len(account.answers) > 1 {
			account.answers = account.answers[1:]
		}
	}
	hold := account.hold
	account.mu.Unlock()
	running := account.running.Add(1)
	for most := account.most.Load(); running > most && !account.most.CompareAndSwap(most, running); most = account.most.Load() {
	}
	if kind != "chat" && kind != string(coresteps.Scan) {
		reading := account.readingAt.Add(1)
		for most := account.readMost.Load(); reading > most && !account.readMost.CompareAndSwap(most, reading); most = account.readMost.Load() {
		}
		defer account.readingAt.Add(-1)
	}
	defer account.running.Add(-1)
	if hold != nil {
		<-hold
	}
	return answer
}

func (account *accountOfTheTest) Responses(_ context.Context, permitID string, body []byte) (io.ReadCloser, error) {
	answer := account.next("chat", permitID, body)
	if answer.refusal != nil {
		return nil, answer.refusal
	}
	return io.NopCloser(strings.NewReader(siteStream(answer.text))), nil
}

func (account *accountOfTheTest) Step(_ context.Context, step, permitID string, body []byte) (StepAnswer, error) {
	answer := account.next(step, permitID, body)
	if answer.refusal != nil {
		return StepAnswer{}, answer.refusal
	}
	return StepAnswer{Text: answer.text, Model: "site-cards", Usage: StepUsage{InputTokens: 120, CachedInputTokens: 20, OutputTokens: 30, ReasoningOutputTokens: 5}}, nil
}

func (account *accountOfTheTest) heard() (asked []string, permits []string, bodies [][]byte) {
	account.mu.Lock()
	defer account.mu.Unlock()
	return append([]string(nil), account.asked...), append([]string(nil), account.permits...), append([][]byte(nil), account.bodies...)
}

// siteStream is a stream of the site as its provider sends it: one answer with usage.
func siteStream(text string) string {
	delta, _ := json.Marshal(text)
	message := `{"type":"message","role":"assistant","id":"msg-1","phase":"final_answer","content":[{"type":"output_text","text":` + string(delta) + `}]}`
	return "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-1\"}}\n\n" +
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":" + message + "}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":" + string(delta) + "}\n\n" +
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":" + message + "}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":400,\"input_tokens_details\":{\"cached_tokens\":100},\"output_tokens\":7}}}\n\n"
}

// accountStand is a gateway whose settings name the helper of the account, a turn of the account bound to its thread, and the
// account of the test.
func accountStand(t *testing.T) (*stand, *accountOfTheTest) {
	t.Helper()
	stand := openStand(t)
	stand.settings.mu.Lock()
	stand.settings.settings = aisettings.Settings{Source: aisettings.SourceAccount, Account: &aisettings.Account{
		ChatModel: "site-chat", CardsModel: "site-cards", ContextWindow: 400_000, CompactLimit: 300_000,
	}}
	stand.settings.mu.Unlock()
	account := newAccountOfTheTest()
	account.journal = stand.journal
	account.works["job-turn"] = JobWork{Source: appwork.SourceAccount, Kind: appwork.KindTurn, PermitID: turnPermitMark, ChatID: "chat-1"}
	stand.gateway.UseAccount(account)
	t.Cleanup(stand.gateway.Threads().Bind("thread-1", "job-turn"))
	return stand, account
}

// AI2.4, AI1.7, AI4.2: a request of Codex in a turn of the account goes to the stream of the site under the permit of its turn,
// without the fields the site needs not and with nothing the gateway would put for «Свой ИИ»; the stream comes to Codex as
// the site sent it; the row of the journal names the profile account, the host of the site and the usage — no text.
func TestATurnOfTheAccountGoesToTheSiteUnderItsPermit(t *testing.T) {
	stand, account := accountStand(t)
	response, stream := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1", "originator": "codex_cli_rs"})
	if response.StatusCode != http.StatusOK || stream != siteStream("ответ") {
		t.Fatalf("the turn = %d %q", response.StatusCode, stream)
	}
	asked, permits, bodies := account.heard()
	if len(asked) != 1 || asked[0] != "chat" || permits[0] != turnPermitMark {
		t.Fatalf("the site heard %v", asked)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(bodies[0], &body); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"client_metadata", "prompt_cache_key", "prompt_cache_retention", "safety_identifier", "user", "metadata", "max_output_tokens"} {
		if _, kept := body[field]; kept {
			t.Errorf("the site got the field %s", field)
		}
	}
	if string(body["reasoning"]) != `{"effort":"xhigh","summary":"auto"}` || string(body["stream"]) != "true" {
		t.Fatalf("the gateway changed the request: %s %s", body["reasoning"], body["stream"])
	}
	rows := stand.journal.all()
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	row := rows[0]
	if row.Profile != "account" || row.ProviderHost != siteHostOfTheTest || row.Model != "site-chat" || row.JobID != "job-turn" ||
		row.Outcome != modeljournal.OutcomeCompleted || row.Tokens == nil || row.Tokens.Input != 400 || row.Tokens.Output != 7 || row.RefusalCode != "" {
		t.Fatalf("the row = %+v", row)
	}
	if strings.Contains(stand.log.String(), authorText) || strings.Contains(stand.log.String(), turnPermitMark) {
		t.Fatal("the log keeps the text or the permit")
	}
}

// AI1.4, AI2.3: a request of Codex outside a turn of the account — no Job in the registry (turn.unbound, the rule of every
// source: specs/2026-11-14-desk-journal.md JR1.2), or a Job without a permit — is refused by the gateway itself: nothing goes to
// the site, no row is written, and Codex does not repeat it.
func TestARequestOfCodexOutsideATurnGoesNowhere(t *testing.T) {
	stand, account := accountStand(t)
	account.works["job-without"] = JobWork{Source: appwork.SourceAccount, Kind: appwork.KindTurn}
	t.Cleanup(stand.gateway.Threads().Bind("thread-2", "job-without"))
	for thread, code := range map[string]string{"": codeUnbound, "thread-unknown": codeUnbound, "thread-2": codeNoTurn} {
		response, body := stand.post(t, chatBody(""), map[string]string{"thread-id": thread})
		if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, code) {
			t.Fatalf("%q = %d %s", thread, response.StatusCode, body)
		}
	}
	if asked, _, _ := account.heard(); len(asked) != 0 || len(stand.journal.all()) != 0 {
		t.Fatalf("the site heard %v, rows %d", asked, len(stand.journal.all()))
	}
	account.mu.Lock()
	account.unlinked = true
	account.mu.Unlock()
	if response, body := stand.post(t, chatBody(""), nil); response.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body, codeNotConnected) {
		t.Fatalf("an unlinked app = %d %s", response.StatusCode, body)
	}
}

// AI1.4: a Job keeps the source it took at its start — a turn of «Свой ИИ» goes to the provider of the author after the
// settings chose the account, a turn of the account goes to the site after they chose «Свой ИИ».
func TestAJobKeepsTheSourceOfItsStart(t *testing.T) {
	stand, account := accountStand(t)
	own := &provider{}
	server := startProvider(t, own)
	settings, err := aisettings.Draft{Source: "own", Own: &aisettings.OwnDraft{
		Preset: "custom", BaseURL: text(server.URL + "/v1"), Protocol: text("responses"), ChatModel: text("north-chat"), CardsModel: text("north-cards"),
	}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	account.works["job-own"] = JobWork{Source: appwork.SourceOwn, Kind: appwork.KindTurn, ChatID: "chat-2"}
	t.Cleanup(stand.gateway.Threads().Bind("thread-own", "job-own"))
	stand.settings.mu.Lock()
	stand.settings.settings.Own, stand.settings.key = settings.Own, "key-test-own-0001"
	stand.settings.mu.Unlock()
	if response, stream := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-own"}); response.StatusCode != http.StatusOK || !completed(stream) {
		t.Fatalf("a turn of «Свой ИИ» under the account = %d %s", response.StatusCode, stream)
	}
	if len(own.requests()) != 1 {
		t.Fatal("the turn of «Свой ИИ» did not go to its provider")
	}
	stand.settings.mu.Lock()
	stand.settings.settings.Source = aisettings.SourceOwn
	stand.settings.mu.Unlock()
	if response, _ := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"}); response.StatusCode != http.StatusOK {
		t.Fatalf("a turn of the account under «Свой ИИ» = %d", response.StatusCode)
	}
	if asked, _, _ := account.heard(); len(asked) != 1 || len(own.requests()) != 1 {
		t.Fatalf("the site heard %v, the provider %d", asked, len(own.requests()))
	}
}

// AI2.5, AI2.6: the one table of the gateway — a lasting refusal of the site is answered with a status Codex does not
// repeat and is heard as the reason of the Job; a refusal of the provider goes with its status and Retry-After; no answer of
// the site is a refusal Codex repeats; ai.busy is waited for and Codex sees the answer; a request the site took clears the
// reason. The gateway repeats nothing else.
func TestTheSiteIsAnsweredByTheOneTable(t *testing.T) {
	cases := []struct {
		code       string
		status     int
		wantStatus int
		reason     string
		retry      string
	}{
		{appwork.CodeDayFence, 403, 400, appwork.ReasonDayFence, ""},
		{appwork.CodePermitClosed, 409, 400, appwork.ReasonPermitEnded, ""},
		{appwork.CodePermitNotFound, 404, 400, appwork.ReasonPermitEnded, ""},
		{appwork.CodeUnavailable, 503, 400, appwork.ReasonUnavailable, ""},
		{appwork.CodeValidation, 400, 400, appwork.ReasonUnfit, ""},
		{appwork.CodeUnsupported, 400, 400, appwork.ReasonUnfit, ""},
		{appwork.CodeTooLarge, 413, 400, appwork.ReasonTooLarge, ""},
		{appwork.CodeProviderRefused, 429, 429, appwork.ReasonFailed, "7"},
		{appwork.CodeProviderFailed, 502, 502, appwork.ReasonFailed, ""},
	}
	for _, refusal := range cases {
		t.Run(refusal.code, func(t *testing.T) {
			stand, account := accountStand(t)
			wait := time.Duration(0)
			if refusal.retry != "" {
				wait = 7 * time.Second
			}
			account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: refusal.status, Code: refusal.code, RetryAfter: wait}}}
			response, body := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"})
			if response.StatusCode != refusal.wantStatus || !strings.Contains(body, refusal.code) || response.Header.Get("Retry-After") != refusal.retry {
				t.Fatalf("%s = %d %s %q", refusal.code, response.StatusCode, body, response.Header.Get("Retry-After"))
			}
			if asked, _, _ := account.heard(); len(asked) != 1 {
				t.Fatalf("the site heard %v", asked)
			}
			account.mu.Lock()
			heard := account.refused
			account.mu.Unlock()
			if len(heard) != 1 || heard[0].Reason != refusal.reason {
				t.Fatalf("the reason heard = %+v", heard)
			}
			rows := stand.journal.all()
			if len(rows) != 1 || rows[0].RefusalCode != refusal.code || rows[0].Outcome != modeljournal.OutcomeFailed {
				t.Fatalf("the rows = %+v", rows)
			}
		})
	}

	stand, account := accountStand(t)
	account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: 429, Code: appwork.CodeBusy, RetryAfter: time.Second}}, {text: "дождался"}}
	response, stream := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"})
	if response.StatusCode != http.StatusOK || stream != siteStream("дождался") {
		t.Fatalf("ai.busy = %d %s", response.StatusCode, stream)
	}
	if asked, _, _ := account.heard(); len(asked) != 2 {
		t.Fatalf("the site heard %v", asked)
	}

	stand, account = accountStand(t)
	account.answers = []siteAnswer{{refusal: nil, text: ""}}
	account.answers[0].refusal = nil
	failing := &failingAccount{accountOfTheTest: account}
	stand.gateway.UseAccount(failing)
	response, body := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"})
	if response.StatusCode != http.StatusBadGateway || !strings.Contains(body, codeUnreachable) {
		t.Fatalf("no answer of the site = %d %s", response.StatusCode, body)
	}
	account.mu.Lock()
	heard := account.refused
	account.mu.Unlock()
	if len(heard) != 1 || heard[0].Reason != appwork.ReasonUnreachable {
		t.Fatalf("no answer is heard as %+v", heard)
	}
}

// AI2.5: the reason of a Job is the refusal of its last request — a request the site took after a refusal clears it, for a
// turn and for a step of the reading alike.
func TestARequestTheSiteTookClearsTheReason(t *testing.T) {
	stand, account := accountStand(t)
	account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: 502, Code: appwork.CodeProviderFailed}}, {text: "ответ"}}
	if response, _ := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"}); response.StatusCode != http.StatusBadGateway {
		t.Fatalf("the refused request = %d", response.StatusCode)
	}
	if response, _ := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"}); response.StatusCode != http.StatusOK {
		t.Fatalf("the taken request = %d", response.StatusCode)
	}
	account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: 502, Code: appwork.CodeProviderFailed}}, {text: "карточка"}}
	stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-cards"})
	if response, _ := stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-cards"}); response.StatusCode != http.StatusOK {
		t.Fatalf("the taken step = %d", response.StatusCode)
	}
	account.mu.Lock()
	refused, accepted := len(account.refused), append([]string(nil), account.accepted...)
	account.mu.Unlock()
	if refused != 2 || len(accepted) != 2 || accepted[0] != "job-turn" || accepted[1] != "job-cards" {
		t.Fatalf("refused %d, the requests taken were heard as %v", refused, accepted)
	}
}

// AI2.5, AI2.9: a link that broke mid-turn — the pass dropped by another call — is the reason of the turn: the next request of
// Codex is refused by the gateway, nothing goes to the site, and the turn ends «связь с аккаунтом прервана».
func TestALinkThatBrokeMidTurnIsTheReasonOfTheTurn(t *testing.T) {
	stand, account := accountStand(t)
	account.mu.Lock()
	account.unlinked = true
	account.mu.Unlock()
	if response, body := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"}); response.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body, codeNotConnected) {
		t.Fatalf("an unlinked app = %d %s", response.StatusCode, body)
	}
	account.mu.Lock()
	heard := append([]appwork.Answer(nil), account.refused...)
	account.mu.Unlock()
	if len(heard) != 1 || heard[0].Reason != appwork.ReasonUnlinked {
		t.Fatalf("the reason heard = %+v", heard)
	}
	if asked, _, _ := account.heard(); len(asked) != 0 {
		t.Fatal("a request of an unlinked app went to the site")
	}
}

// AI4.2, migration 0151: a code of a refusal of the site of another shape than the journal keeps is written as none — the row,
// and the count of the reading written with it, is never lost for it.
func TestARefusalCodeTheJournalDoesNotKeepIsWrittenAsNone(t *testing.T) {
	stand, account := accountStand(t)
	account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: 409, Code: "The Site Refused"}}}
	if response, _ := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"}); response.StatusCode != http.StatusBadGateway {
		t.Fatalf("a refusal of an unknown code = %d", response.StatusCode)
	}
	reading := &readingOfTheTest{}
	stand.gateway.UseReading(reading)
	stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-cards"})
	rows := stand.journal.all()
	reading.mu.Lock()
	settled := append([]modeljournal.Request(nil), reading.settled...)
	reading.mu.Unlock()
	if len(rows) != 1 || rows[0].RefusalCode != "" || len(settled) != 1 || settled[0].RefusalCode != "" {
		t.Fatalf("the rows %+v, settled %+v", rows, settled)
	}
}

// failingAccount is a site that never answers.
type failingAccount struct{ *accountOfTheTest }

func (failing *failingAccount) Responses(context.Context, string, []byte) (io.ReadCloser, error) {
	return nil, io.ErrUnexpectedEOF
}

func (failing *failingAccount) Step(context.Context, string, string, []byte) (StepAnswer, error) {
	return StepAnswer{}, io.ErrUnexpectedEOF
}

// AI2.6: a refusal the app remembers is given by the gateway itself, before any place: nothing goes to the site.
func TestARefusalTheAppRemembersGoesNowhere(t *testing.T) {
	stand, account := accountStand(t)
	account.remember["chat"] = appwork.CodeDayFence
	response, body := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"})
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, appwork.CodeDayFence) {
		t.Fatalf("a remembered fence = %d %s", response.StatusCode, body)
	}
	if asked, _, _ := account.heard(); len(asked) != 0 || len(stand.journal.all()) != 0 {
		t.Fatal("a remembered refusal went to the site")
	}
}

// AI3.1: a call of the core goes as a step of the site with its words and its input, under the permit of the reading of its
// Job, and comes back to the client of the core as the stream of Responses it reads, with the usage of the site.
func TestACallOfTheCoreIsAStepOfTheSite(t *testing.T) {
	stand, account := accountStand(t)
	account.answers = []siteAnswer{{text: `{"summary":"карточка"}`}}
	client, err := modelapi.New(modelapi.Config{
		BaseURL: stand.gateway.BaseURL(), Key: stand.gateway.Token(), CarryJob: true, Stops: true, RetryPause: time.Millisecond,
		Model: CardsModel, CardsModel: CardsModel, Parallel: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	card, _ := coresteps.Named(string(coresteps.Card))
	text, usage, err := client.Background().Complete(named(jobs.WithJobID(context.Background(), "job-cards")), card.Instructions, authorText)
	if err != nil || text != `{"summary":"карточка"}` || usage.InputTokens != 120 || usage.CachedInputTokens != 20 || usage.OutputTokens != 30 {
		t.Fatalf("the step = %q %+v %v", text, usage, err)
	}
	asked, permits, bodies := account.heard()
	if len(asked) != 1 || asked[0] != string(coresteps.Card) || permits[0] != readingPermitMark {
		t.Fatalf("the site heard %v", asked)
	}
	var step struct {
		Instruction string `json:"instruction"`
		Input       string `json:"input"`
	}
	if err := json.Unmarshal(bodies[0], &step); err != nil || step.Instruction != card.Instructions || step.Input != authorText {
		t.Fatalf("the step went as %v", err)
	}
	rows := stand.journal.all()
	if len(rows) != 1 || rows[0].Purpose != string(coresteps.Card) || rows[0].Model != "site-cards" || rows[0].Tokens.Output != 30 {
		t.Fatalf("the rows = %+v", rows)
	}
}

// AI3.2: a permit of the reading the site ended mid-Job is replaced once by the next number and the step goes again.
func TestAPermitOfTheReadingEndedMidJobIsReplacedOnce(t *testing.T) {
	stand, account := accountStand(t)
	account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: 409, Code: appwork.CodePermitClosed}}, {text: "карточка"}}
	response, stream := stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-cards"})
	if response.StatusCode != http.StatusOK || !completed(stream) {
		t.Fatalf("the step = %d %s", response.StatusCode, stream)
	}
	account.mu.Lock()
	renewals := account.renewals
	account.mu.Unlock()
	if asked, _, _ := account.heard(); len(asked) != 2 || renewals != 1 {
		t.Fatalf("the site heard %v, renewals %d", asked, renewals)
	}
}

// AI3.5: a refusal of a step that stops the reading names its stop to the client of the core — which does not repeat it —
// and pauses the consent of the saga; an app the site does not take is remembered.
func TestARefusalOfAStepStopsTheReading(t *testing.T) {
	cases := map[string]string{
		appwork.CodeAccountCharacters: "characters",
		appwork.CodeReadingFence:      "reading_fence",
		appwork.CodeStepInstruction:   "unfit",
		appwork.CodeProviderFailed:    "unavailable",
		appwork.CodeUnavailable:       "unavailable",
	}
	for code, stop := range cases {
		t.Run(code, func(t *testing.T) {
			stand, account := accountStand(t)
			reading := &readingOfTheTest{}
			stand.gateway.UseReading(reading)
			account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: 409, Code: code}}}
			response, body := stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-cards"})
			if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, `"reason":"`+stop+`"`) {
				t.Fatalf("%s = %d %s", code, response.StatusCode, body)
			}
			reading.mu.Lock()
			stops, settled := reading.stops, reading.settled
			reading.mu.Unlock()
			if len(stops) != 1 || stops[0] != stop || len(settled) != 1 || settled[0].RefusalCode != code {
				t.Fatalf("the consent heard %v, settled %+v", stops, settled)
			}
			if len(stand.journal.all()) != 0 {
				t.Fatal("a request of the reading wrote its row past the check")
			}
		})
	}
}

// AI3.5, AI3.6: a step of the reading of a kind the site does not take from this version, which the app remembers, goes
// nowhere — and pauses the consent «приложение не подходит серверу», as the first refusal did: an author who pressed
// «Продолжить» in the same run sees the pause again, not a reading that says it reads while its Jobs fail. A consent that
// is not let go is not paused again.
func TestARememberedRefusalOfTheReadingPausesItsConsent(t *testing.T) {
	stand, account := accountStand(t)
	reading := &readingOfTheTest{}
	stand.gateway.UseReading(reading)
	account.remember[string(coresteps.Card)] = appwork.CodeAppUnfit
	response, body := stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-cards"})
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, `"reason":"unfit"`) {
		t.Fatalf("a remembered refusal of the reading = %d %s", response.StatusCode, body)
	}
	reading.mu.Lock()
	stops := reading.stops
	reading.mu.Unlock()
	if len(stops) != 1 || stops[0] != "unfit" {
		t.Fatalf("the consent heard %v", stops)
	}
	if asked, _, _ := account.heard(); len(asked) != 0 || len(stand.journal.all()) != 0 {
		t.Fatal("a remembered refusal went to the site or wrote a row")
	}
	reading.mu.Lock()
	reading.refuse = "unfit"
	reading.mu.Unlock()
	if response, body := stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-cards"}); response.StatusCode != http.StatusConflict ||
		!strings.Contains(body, codeReadingPaused) {
		t.Fatalf("a paused consent = %d %s", response.StatusCode, body)
	}
	if reading.mu.Lock(); len(reading.stops) != 1 {
		t.Fatalf("a paused consent was paused again: %v", reading.stops)
	}
	reading.mu.Unlock()
}

// AI3.4: a step of the reading goes only once the check of the consent let it go.
func TestAStepOfTheReadingWaitsForTheConsent(t *testing.T) {
	stand, account := accountStand(t)
	stand.gateway.UseReading(&readingOfTheTest{refuse: "consent"})
	response, body := stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-cards"})
	if response.StatusCode != http.StatusConflict || !strings.Contains(body, codeReadingPaused) {
		t.Fatalf("without the consent = %d %s", response.StatusCode, body)
	}
	if asked, _, _ := account.heard(); len(asked) != 0 {
		t.Fatal("a step went to the site without the consent")
	}
	// The scan of a turn is the author's own action: no consent is asked.
	words, _ := coresteps.Named(string(coresteps.Scan))
	scan, _ := json.Marshal(words.Instructions)
	body2 := `{"model":"canonkeeper-cards","instructions":` + string(scan) + `,"input":"` + authorText + `","stream":true}`
	if response, _ := stand.post(t, body2, map[string]string{modelapi.JobHeader: "job-turn"}); response.StatusCode != http.StatusOK {
		t.Fatalf("the scan of a turn = %d", response.StatusCode)
	}
	if asked, permits, _ := account.heard(); len(asked) != 1 || asked[0] != string(coresteps.Scan) || permits[0] != turnPermitMark {
		t.Fatalf("the scan went as %v", asked)
	}
}

// AI3.3, AI2.10: the reading holds at most all the places of the account but one, and a turn and the scan of a turn get
// that one while the cards hold theirs.
func TestTheReadingHoldsAllThePlacesButOne(t *testing.T) {
	stand, account := accountStand(t)
	account.hold = make(chan struct{})
	var done sync.WaitGroup
	for range accountReadingPlaces + 2 {
		done.Add(1)
		go func() {
			defer done.Done()
			stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-cards"})
		}()
	}
	waitFor(t, func() bool { return account.readingAt.Load() == accountReadingPlaces })
	time.Sleep(50 * time.Millisecond)
	if account.readingAt.Load() != accountReadingPlaces {
		t.Fatal("the reading took the place of the chat")
	}
	account.mu.Lock()
	held := account.hold
	account.hold = nil
	account.mu.Unlock()
	turned := make(chan int, 1)
	go func() {
		response, _ := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"})
		turned <- response.StatusCode
	}()
	select {
	case status := <-turned:
		if status != http.StatusOK {
			t.Fatalf("the turn = %d", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the turn waited for the card")
	}
	close(held)
	done.Wait()
	if account.readMost.Load() != accountReadingPlaces || account.most.Load() > accountPlaces {
		t.Fatalf("at once: reading %d, all %d", account.readMost.Load(), account.most.Load())
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); !condition(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the condition did not come")
		}
	}
}

// AI2.5 on the binary of the set: Codex of the app meets the one table of the gateway — every lasting refusal of the site
// ends the turn at once after one request, the reason of the table heard for its Job; ai.busy is waited for by the gateway
// and the turn answers, Codex never seeing it; no answer of the site is repeated by Codex and the reason is «нет связи с
// сервером»; a refusal after which the site took a request is no reason.
func TestCodexMeetsTheTableOfTheAccount(t *testing.T) {
	lasting := map[string]string{
		appwork.CodeDayFence: appwork.ReasonDayFence, appwork.CodePassInvalid: appwork.ReasonUnlinked,
		appwork.CodePermitClosed: appwork.ReasonPermitEnded, appwork.CodePermitNotFound: appwork.ReasonPermitEnded,
		appwork.CodeUnavailable: appwork.ReasonUnavailable, appwork.CodeValidation: appwork.ReasonUnfit,
		appwork.CodeUnsupported: appwork.ReasonUnfit, appwork.CodeTooLarge: appwork.ReasonTooLarge,
	}
	for code, reason := range lasting {
		t.Run(code, func(t *testing.T) {
			stand, account := accountStand(t)
			account.works["job-of-the-turn"] = JobWork{Source: appwork.SourceAccount, Kind: appwork.KindTurn, PermitID: turnPermitMark, ChatID: "chat-1"}
			account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: 409, Code: code}}}
			client := runtimeChat(t, stand, 400_000)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			calls := 0
			began := time.Now()
			result, err := client.Run(ctx, turnRequest(stand, &calls))
			asked, _, _ := account.heard()
			if err != nil || result.Status == agent.TurnCompleted || len(asked) != 1 || time.Since(began) > 30*time.Second {
				t.Fatalf("%s: %v %v after %d requests in %s", code, result.Status, err, len(asked), time.Since(began))
			}
			account.mu.Lock()
			heard := append([]appwork.Answer(nil), account.refused...)
			account.mu.Unlock()
			if len(heard) != 1 || heard[0].Reason != reason {
				t.Fatalf("%s: the reason heard = %+v", code, heard)
			}
		})
	}
	stand, account := accountStand(t)
	account.works["job-of-the-turn"] = JobWork{Source: appwork.SourceAccount, Kind: appwork.KindTurn, PermitID: turnPermitMark, ChatID: "chat-1"}
	account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: 429, Code: appwork.CodeBusy, RetryAfter: time.Second}}, {text: turnAnswer}}
	client := runtimeChat(t, stand, 400_000)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	calls := 0
	result, err := client.Run(ctx, turnRequest(stand, &calls))
	if err != nil || result.Status != agent.TurnCompleted || result.FinalResponse != turnAnswer {
		t.Fatalf("ai.busy = %+v %v", result, err)
	}
	account.mu.Lock()
	refused := len(account.refused)
	account.mu.Unlock()
	if refused != 0 {
		t.Fatalf("ai.busy was heard as a refusal %d times", refused)
	}
}

// fewerRepeats are the repeats of Codex a test of the binary makes fewer — a setting of the test, not a pause by the clock:
// Codex repeats a refusal it may repeat by its stream and by its request.
var fewerRepeats = []string{
	fmt.Sprintf("model_providers.%s.stream_max_retries=1", codexProvider),
	fmt.Sprintf("model_providers.%s.request_max_retries=1", codexProvider),
}

// AI2.5 on the binary of the set: no answer of the site — a broken connection — is a refusal Codex repeats, by its own
// repeats, and the reason of the turn is «нет связи с сервером»; a refusal of the provider of the site after which the site
// took the repeat is no reason, and the turn answers.
func TestCodexRepeatsNoAnswerOfTheSite(t *testing.T) {
	stand, account := accountStand(t)
	account.works["job-of-the-turn"] = JobWork{Source: appwork.SourceAccount, Kind: appwork.KindTurn, PermitID: turnPermitMark, ChatID: "chat-1"}
	stand.gateway.UseAccount(&failingAccount{accountOfTheTest: account})
	client := runtimeChat(t, stand, 400_000, fewerRepeats...)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	calls := 0
	result, err := client.Run(ctx, turnRequest(stand, &calls))
	account.mu.Lock()
	heard := append([]appwork.Answer(nil), account.refused...)
	account.mu.Unlock()
	if err != nil || result.Status == agent.TurnCompleted || len(heard) < 2 || heard[len(heard)-1].Reason != appwork.ReasonUnreachable {
		t.Fatalf("no answer of the site = %v %v, heard %+v", result.Status, err, heard)
	}

	stand, account = accountStand(t)
	account.works["job-of-the-turn"] = JobWork{Source: appwork.SourceAccount, Kind: appwork.KindTurn, PermitID: turnPermitMark, ChatID: "chat-1"}
	account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: 502, Code: appwork.CodeProviderFailed}}, {text: turnAnswer}}
	client = runtimeChat(t, stand, 400_000, fewerRepeats...)
	result, err = client.Run(ctx, turnRequest(stand, &calls))
	account.mu.Lock()
	accepted := append([]string(nil), account.accepted...)
	account.mu.Unlock()
	if err != nil || result.Status != agent.TurnCompleted || len(accepted) == 0 {
		t.Fatalf("a refusal of the provider repeated = %+v %v, taken %v", result, err, accepted)
	}
}

// specs/2026-11-15-desk-reading-rest.md RR1.1: a lasting refusal of a step of the site without a stop — the provider of the
// site did not take the request, a request too large — is named refused to the client of the core, which hands
// sagamemory.ErrRefused; a wait of the provider, a link that broke and a permit that ended are named nothing, and a stop stays
// a stop.
func TestALastingRefusalOfAStepIsNamedRefused(t *testing.T) {
	for _, refusal := range []struct {
		status int
		code   string
		reason string
	}{
		{http.StatusBadRequest, appwork.CodeProviderRefused, "refused"},
		{http.StatusUnprocessableEntity, appwork.CodeProviderRefused, "refused"},
		{http.StatusRequestEntityTooLarge, appwork.CodeTooLarge, "refused"},
		{http.StatusTooManyRequests, appwork.CodeProviderRefused, ""},
		{http.StatusBadGateway, appwork.CodeProviderRefused, ""},
		{http.StatusUnauthorized, appwork.CodePassInvalid, ""},
		{http.StatusConflict, appwork.CodePermitNotFound, ""},
		{http.StatusConflict, appwork.CodeReadingFence, "reading_fence"},
	} {
		stand, account := accountStand(t)
		stand.gateway.UseReading(&readingOfTheTest{})
		account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: refusal.status, Code: refusal.code}}}
		_, body := stand.post(t, coreBody(t, coresteps.Names), map[string]string{modelapi.JobHeader: "job-cards"})
		named := strings.Contains(body, `"reason"`)
		if refusal.reason == "" && named || refusal.reason != "" && !strings.Contains(body, `"reason":"`+refusal.reason+`"`) {
			t.Errorf("%d %s = %s; want the reason %q", refusal.status, refusal.code, body, refusal.reason)
		}
	}
}
