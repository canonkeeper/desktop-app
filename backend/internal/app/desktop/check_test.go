// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/llmgateway"
	"canonkeeper/backend/internal/domain/aisettings"
	domain "canonkeeper/backend/internal/domain/desktop"
	"canonkeeper/backend/internal/domain/modeljournal"
	aiservice "canonkeeper/backend/internal/services/aisettings"
)

// The marks of the test: the key, and a word only the provider says. Neither may stand in a row, a log or settings.json.
const (
	checkKey  = "key-test-check-north-9c1e5a"
	checkMark = "Северный-ответ-пробы"
)

// checkProvider is a provider of Responses or Chat Completions for «Проверить связь»: it tells the probes by their words —
// the call of probe_ping, the long text, the long answer, the calibration, the short answer — answers as a model that fits
// both roles, and counts input tokens as 50 and 0.3 a character of the words of the author's turn. It writes the long answer
// as a model does, part after part, and waits for the client to close the request before its end (closed counts that). Its
// flags make it a model without tools, one that sees only the end of a long text, one that cuts a long answer at its limit
// before 4 000 tokens, or a provider that refuses with status; hold keeps the requests of a probe ("*" — of every probe)
// until they end.
type checkProvider struct {
	mu        sync.Mutex
	chat      bool
	noTools   bool
	endOnly   bool
	cutAnswer bool
	status    int
	hold      string
	asked     map[string]int
	closed    int
	ended     chan struct{}
}

func (stub *checkProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Tools []json.RawMessage `json:"tools"`
		Input []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(raw, &body)
	var turn strings.Builder
	for _, item := range body.Input {
		for _, part := range item.Content {
			turn.WriteString(part.Text)
		}
	}
	for _, message := range body.Messages {
		if text, ok := message.Content.(string); ok && message.Role == "user" {
			turn.WriteString(text)
		}
	}
	text := turn.String()
	probe := "short"
	switch {
	case len(body.Tools) > 0:
		probe = "tool"
	case strings.Contains(text, "шифр смотрителя"):
		probe = "long_input"
	case strings.Contains(text, "летопись"):
		probe = "long_output"
	case len([]rune(text)) > 1_000:
		probe = "calibrate"
	}
	stub.mu.Lock()
	if stub.asked == nil {
		stub.asked = map[string]int{}
	}
	stub.asked[probe]++
	status, hold := stub.status, stub.hold == "*" || stub.hold == probe
	stub.mu.Unlock()
	if hold {
		<-r.Context().Done()
		stub.mu.Lock()
		if stub.ended != nil {
			close(stub.ended)
			stub.ended = nil
		}
		stub.mu.Unlock()
		return
	}
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"message":"`+checkMark+`"}}`)
		return
	}
	input := 50 + len([]rune(text))*3/10
	answer, output, call := checkMark+" Готово", 3, false
	switch probe {
	case "tool":
		call = !stub.noTools
	case "long_input":
		answer = checkMark + " Сорок семь бакенов"
		if stub.endOnly {
			answer = checkMark + " В тексте только рыбаки."
		}
	case "long_output":
		stub.long(w, r, input)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if stub.chat {
		delta, finish := map[string]any{"content": answer}, "stop"
		if call {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function",
				"function": map[string]any{"name": "probe_ping", "arguments": `{"word":"маяк","count":3}`}}}}
			finish = "tool_calls"
		}
		chunk, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
			"usage":   map[string]any{"prompt_tokens": input, "completion_tokens": output},
		})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
		return
	}
	usage := fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d}`, input, output, input+output)
	if call {
		item := `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"probe_ping","arguments":"{\"word\":\"маяк\",\"count\":3}","status":"completed"}`
		fmt.Fprintf(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n", item)
		fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[%s],\"usage\":%s}}\n\n", item, usage)
		return
	}
	delta, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": answer})
	fmt.Fprintf(w, "event: response.output_text.delta\ndata: %s\n\n", delta)
	fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":%s}}\n\n", usage)
}

// long writes the long answer: 400 parts of a chronicle — 15 200 characters, more than 4 000 tokens by the calibration of
// the stub — and the end only after the client waited for it; a model that cuts its answer stops at 100 parts, under 4 000
// tokens, at its limit.
func (stub *checkProvider) long(w http.ResponseWriter, r *http.Request, input int) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	parts, finish, status, output := 400, "stop", "completed", 6_400
	if stub.cutAnswer {
		parts, finish, status, output = 100, "length", "incomplete", 1_600
	}
	part := checkMark + " Глава летописи. "
	for range parts {
		if stub.chat {
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": part}}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		} else {
			delta, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": part})
			fmt.Fprintf(w, "event: response.output_text.delta\ndata: %s\n\n", delta)
		}
		flusher.Flush()
	}
	if !stub.cutAnswer {
		select {
		case <-r.Context().Done():
			stub.mu.Lock()
			stub.closed++
			stub.mu.Unlock()
			return
		case <-time.After(5 * time.Second):
		}
	}
	if stub.chat {
		chunk, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
			"usage":   map[string]any{"prompt_tokens": input, "completion_tokens": output},
		})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
		return
	}
	usage := fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d}`, input, output, input+output)
	terminal := fmt.Sprintf(`{"id":"resp_1","status":%q,"usage":%s}`, status, usage)
	if stub.cutAnswer {
		terminal = fmt.Sprintf(`{"id":"resp_1","status":%q,"incomplete_details":{"reason":"max_output_tokens"},"usage":%s}`, status, usage)
	}
	fmt.Fprintf(w, "event: response.%s\ndata: {\"type\":\"response.%s\",\"response\":%s}\n\n", status, status, terminal)
}

func (stub *checkProvider) wasClosed() int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return stub.closed
}

func (stub *checkProvider) counts() map[string]int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	counts := map[string]int{}
	for probe, count := range stub.asked {
		counts[probe] = count
	}
	return counts
}

type checkJournal struct {
	mu       sync.Mutex
	rows     []modeljournal.Request
	sendings []modeljournal.Sending
}

func (journal *checkJournal) Send(_ context.Context, sending modeljournal.Sending) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.sendings = append(journal.sendings, sending)
	return nil
}

func (journal *checkJournal) Record(_ context.Context, request modeljournal.Request) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.rows = append(journal.rows, request)
	return nil
}

// checkStand is the check of the app over its real gateway, settings.json and the key the backend holds.
type checkStand struct {
	data    DataDir
	saved   *settings
	gateway *llmgateway.Gateway
	checks  *aiservice.Checks
	journal *checkJournal
	log     *bytes.Buffer
	logMu   *sync.Mutex
}

type lockedWriter struct {
	mu     *sync.Mutex
	buffer *bytes.Buffer
}

func (writer lockedWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.Write(data)
}

func openCheckStand(t *testing.T, address string, draft func(*aisettings.OwnDraft)) *checkStand {
	t.Helper()
	data := DataDir{Root: t.TempDir()}
	saved, err := loadSettings(data.Settings(), settingsNow)
	if err != nil {
		t.Fatal(err)
	}
	protocol, model := "responses", "north"
	own := aisettings.OwnDraft{Preset: "custom", BaseURL: &address, Protocol: &protocol, ChatModel: &model, CardsModel: &model}
	if draft != nil {
		draft(&own)
	}
	settings, err := aisettings.Draft{Source: "own", Own: &own}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if err := saved.KeepAI(settings); err != nil {
		t.Fatal(err)
	}
	secrets := newShellSecrets(func(ControlMessage) error { return nil }, domain.SecretsInFile, map[string]string{domain.SecretOwnAI: checkKey}, time.Second)
	buffer, mu := &bytes.Buffer{}, &sync.Mutex{}
	logger := log.New(lockedWriter{mu: mu, buffer: buffer}, "", 0)
	gateway, err := llmgateway.Open(ownAI{settings: saved, secrets: secrets}, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gateway.Close() })
	journal := &checkJournal{}
	gateway.UseJournal(journal)
	checks := &aiservice.Checks{Settings: saved, Keys: secrets, Prober: gateway, Reports: saved, Limit: time.Minute, Now: time.Now, Logger: logger}
	return &checkStand{data: data, saved: saved, gateway: gateway, checks: checks, journal: journal, log: buffer, logMu: mu}
}

func (stand *checkStand) run(t *testing.T) aiservice.Check {
	t.Helper()
	if _, err := stand.checks.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		check := stand.checks.Read()
		if check.State != aiservice.CheckRunning {
			return check
		}
		if time.Now().After(deadline) {
			t.Fatal("the check did not end")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// leaks fails the test when the key, a word of the provider or a word of a probe stands in the log, the journal or the data
// directory.
func (stand *checkStand) leaks(t *testing.T) {
	t.Helper()
	stand.logMu.Lock()
	written := stand.log.String()
	stand.logMu.Unlock()
	rows, _ := json.Marshal(stand.journal.rows)
	file, _ := os.ReadFile(stand.data.Settings())
	for where, text := range map[string]string{"the log": written, "the journal": string(rows), "settings.json": string(file)} {
		for _, mark := range []string{checkKey, checkMark, "бакен", "Проверка связи", "смотрителя"} {
			if strings.Contains(text, mark) {
				t.Errorf("%s holds %q", where, mark)
			}
		}
	}
}

func startCheckProvider(t *testing.T, stub http.Handler) string {
	t.Helper()
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	return server.URL + "/v1"
}

// specs/2026-11-13-desk-probes.md PB2.2–PB2.6: providers of Responses and of Chat Completions that pass every probe — «годится
// для чата и для чтения книг», the calibration in settings.json; one model in both roles is probed once; every probe leaves a
// row of the journal with the purpose probe and no text — the long answer is closed once it gave 4 000 tokens by the
// calibration, its row canceled without usage; a new model of a role makes the report stale; the key, the words of the
// provider and of the probes stand nowhere.
func TestAProviderThatPassesFitsBothRoles(t *testing.T) {
	for _, chat := range []bool{false, true} {
		stub := &checkProvider{chat: chat}
		protocol := "responses"
		if chat {
			protocol = "chat"
		}
		stand := openCheckStand(t, startCheckProvider(t, stub), func(own *aisettings.OwnDraft) { own.Protocol = &protocol })
		check := stand.run(t)
		if check.State != aiservice.CheckDone || check.Report == nil {
			t.Fatalf("%s: the check = %+v", protocol, check)
		}
		if check.Report.Chat != (aisettings.Verdict{Fit: aisettings.FitYes}) || check.Report.Books != (aisettings.Verdict{Fit: aisettings.FitYes}) {
			t.Fatalf("%s: the verdicts = %+v %+v; probes %+v", protocol, check.Report.Chat, check.Report.Books, check.Results)
		}
		if counts := stub.counts(); len(counts) != 5 || counts["short"] != 1 || counts["tool"] != 1 || counts["long_input"] != 1 ||
			counts["long_output"] != 1 || counts["calibrate"] != 1 {
			t.Fatalf("%s: the provider was asked %v", protocol, counts)
		}
		calibration := check.Report.Report.Calibration
		if calibration == nil || *calibration < 295 || *calibration > 305 {
			t.Fatalf("%s: the calibration = %v", protocol, calibration)
		}
		again, err := loadSettings(stand.data.Settings(), settingsNow)
		if err != nil {
			t.Fatal(err)
		}
		if kept, ok := again.Report(); !ok || kept.Calibration == nil || *kept.Calibration != *calibration || kept.Checked.ChatModel != "north" {
			t.Fatalf("%s: settings.json keeps %+v", protocol, kept)
		}
		rows := stand.journal.rows
		if len(rows) != 5 || stub.wasClosed() != 1 {
			t.Fatalf("%s: rows %+v; the long answer closed %d times", protocol, rows, stub.wasClosed())
		}
		for index, row := range rows {
			long := index == len(rows)-1
			if row.Purpose != modeljournal.PurposeProbe || row.Model != "north" || (row.Tokens == nil) != long ||
				(row.Outcome == modeljournal.OutcomeCanceled) != long {
				t.Errorf("%s: row %d %+v", protocol, index, row)
			}
		}
		stand.leaks(t)

		other, model := *stand.saved.AI().Own, "north-2"
		if err := stand.saved.KeepAI(aisettings.Settings{Source: aisettings.SourceOwn, Own: func() *aisettings.Own { other.ChatModel = model; return &other }()}); err != nil {
			t.Fatal(err)
		}
		state, err := aiservice.Read{Settings: stand.saved, Keys: stand.checks.Keys, Reports: stand.saved}.Execute(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if state.Check == nil || !state.Check.Stale || state.Check.Report.Checked.ChatModel != "north" {
			t.Fatalf("%s: getAiSettings after a new model = %+v", protocol, state.Check)
		}
	}
}

// PB2.4: a model without tools reads the books and does not chat; one that sees only the end of a long text, and one that
// cuts a long answer, say so; a refused key stops after the first probe; no connection and a wait of the provider say so.
func TestEachFailureOfTheProviderHasItsVerdict(t *testing.T) {
	// An address nothing listens on, taken at the moment it is asked: a port closed before the stubs of the other cases
	// start may be given to one of them, and the check would hear its answer instead of no connection.
	gone := func() string {
		closed, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := "http://" + closed.Addr().String() + "/v1"
		_ = closed.Close()
		return address
	}
	for _, check := range []struct {
		what   string
		stub   *checkProvider
		chat   aisettings.Verdict
		books  aisettings.Verdict
		probes int
	}{
		{"no tools", &checkProvider{chat: true, noTools: true}, aisettings.Verdict{Fit: aisettings.FitNo, Reason: aisettings.ReasonNoTools}, aisettings.Verdict{Fit: aisettings.FitYes}, 5},
		{"the end only", &checkProvider{endOnly: true}, aisettings.Verdict{Fit: aisettings.FitNo, Reason: aisettings.ReasonStartOnly},
			aisettings.Verdict{Fit: aisettings.FitNo, Reason: aisettings.ReasonStartOnly}, 5},
		{"a cut answer", &checkProvider{chat: true, cutAnswer: true}, aisettings.Verdict{Fit: aisettings.FitYes},
			aisettings.Verdict{Fit: aisettings.FitNo, Reason: aisettings.ReasonCutAnswer}, 5},
		{"a refused key", &checkProvider{status: http.StatusUnauthorized}, aisettings.Verdict{Fit: aisettings.FitNo, Reason: aisettings.ReasonKeyRefused},
			aisettings.Verdict{Fit: aisettings.FitNo, Reason: aisettings.ReasonKeyRefused}, 1},
		{"a wait", &checkProvider{status: http.StatusTooManyRequests}, aisettings.Verdict{Fit: aisettings.FitNo, Reason: aisettings.ReasonRateLimited},
			aisettings.Verdict{Fit: aisettings.FitNo, Reason: aisettings.ReasonRateLimited}, 5},
		{"no connection", nil, aisettings.Verdict{Fit: aisettings.FitNo, Reason: aisettings.ReasonUnreachable},
			aisettings.Verdict{Fit: aisettings.FitNo, Reason: aisettings.ReasonUnreachable}, 1},
	} {
		address, protocol := "", "responses"
		if check.stub != nil {
			address = startCheckProvider(t, check.stub)
			if check.stub.chat {
				protocol = "chat"
			}
		} else {
			address = gone()
		}
		stand := openCheckStand(t, address, func(own *aisettings.OwnDraft) { own.Protocol = &protocol })
		got := stand.run(t)
		if got.State != aiservice.CheckDone || got.Report == nil || got.Report.Chat != check.chat || got.Report.Books != check.books || len(got.Results) != check.probes {
			t.Errorf("%s: %+v; probes %+v", check.what, got.Report, got.Results)
		}
		if check.stub != nil && check.probes == 1 && len(check.stub.counts()) != 1 {
			t.Errorf("%s: the provider was asked %v after the first probe", check.what, check.stub.counts())
		}
		stand.leaks(t)
	}
}

// PB2.4: the window of the chat decides the verdict of a chat whose probes passed: 32 000 is too little, 100 000 works with
// poorer answers on long sagas; a limit of an answer under 16 000 does not read the books.
func TestTheWindowAndTheLimitDecideWhatThePassedProbesAllow(t *testing.T) {
	stub := &checkProvider{chat: true}
	address := startCheckProvider(t, stub)
	for _, check := range []struct {
		window, limit int
		chat, books   aisettings.Verdict
	}{
		{32_000, 16_000, aisettings.Verdict{Fit: aisettings.FitNo, Reason: aisettings.ReasonSmallWindow}, aisettings.Verdict{Fit: aisettings.FitYes}},
		{100_000, 16_000, aisettings.Verdict{Fit: aisettings.FitThin, Reason: aisettings.ReasonSmallWindow}, aisettings.Verdict{Fit: aisettings.FitYes}},
		{128_000, 8_000, aisettings.Verdict{Fit: aisettings.FitYes}, aisettings.Verdict{Fit: aisettings.FitNo, Reason: aisettings.ReasonSmallLimit}},
	} {
		stand := openCheckStand(t, address, func(own *aisettings.OwnDraft) {
			protocol := "chat"
			own.Protocol, own.Advanced.ContextWindow, own.Advanced.MaxOutput = &protocol, &check.window, &check.limit
		})
		got := stand.run(t)
		if got.Report == nil || got.Report.Chat != check.chat || got.Report.Books != check.books {
			t.Errorf("window %d, limit %d: %+v", check.window, check.limit, got.Report)
		}
	}
}

// PB2.1: a second check while the first runs is refused; cancelAiCheck stops the request at the provider and keeps nothing;
// a check longer than its term stops with that reason and keeps what it learned — late in the long answer the chat is
// known and the reading of the books is slow, late in the first probe both are.
func TestACheckStopsItsRequestsWhenCanceledOrLate(t *testing.T) {
	ended := make(chan struct{})
	stub := &checkProvider{hold: "*", ended: ended}
	stand := openCheckStand(t, startCheckProvider(t, stub), nil)
	if _, err := stand.checks.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := stand.checks.Start(context.Background()); err != aiservice.ErrCheckRunning {
		t.Fatalf("a second check = %v", err)
	}
	for len(stub.counts()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	stand.checks.Cancel()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the request at the provider did not end")
	}
	if check := stand.checks.Read(); check.State != aiservice.CheckCanceled {
		t.Fatalf("a canceled check = %+v", check)
	}
	if _, ok := stand.saved.Report(); ok {
		t.Fatal("a canceled check kept a report")
	}

	slow := aisettings.Verdict{Fit: aisettings.FitNo, Reason: aisettings.ReasonSlow}
	for _, check := range []struct {
		hold        string
		chat, books aisettings.Verdict
		probes      int
	}{
		{"long_output", aisettings.Verdict{Fit: aisettings.FitYes}, slow, 5},
		{"*", slow, slow, 1},
	} {
		late := openCheckStand(t, startCheckProvider(t, &checkProvider{hold: check.hold}), nil)
		// The probes before the one held end at once; the term leaves them room on a loaded machine.
		late.checks.Limit = 2 * time.Second
		got := late.run(t)
		if got.State != aiservice.CheckTimedOut || got.Report == nil || got.Report.Chat != check.chat || got.Report.Books != check.books ||
			len(got.Results) != check.probes {
			t.Fatalf("late with %s held = %+v; probes %+v", check.hold, got.Report, got.Results)
		}
		again, err := loadSettings(late.data.Settings(), settingsNow)
		if err != nil {
			t.Fatal(err)
		}
		if kept, ok := again.Report(); !ok || len(kept.Results) != check.probes || kept.Results[check.probes-1].Reason != aisettings.ReasonSlow {
			t.Fatalf("late with %s held, settings.json keeps %+v", check.hold, kept)
		}
		late.leaks(t)
	}
}
