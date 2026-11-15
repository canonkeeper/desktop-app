// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/modeljournal"
)

// probed is a stub of a provider for the probes: it answers a request with a tool by the call of it, another by a word,
// with usage; cut ends the answer at its limit, status refuses with a message; hold keeps every request until it is closed
// or the request ends.
type probed struct {
	mu      sync.Mutex
	chat    bool
	cut     bool
	status  int
	code    string
	message string
	hold    chan struct{}
	got     []seen
}

func (stub *probed) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]json.RawMessage
	_ = json.Unmarshal(raw, &body)
	var model string
	_ = json.Unmarshal(body["model"], &model)
	stub.mu.Lock()
	stub.got = append(stub.got, seen{path: r.URL.Path, header: r.Header.Clone(), body: body, model: model, raw: string(raw), started: time.Now()})
	status, code, message, cut, hold := stub.status, stub.code, stub.message, stub.cut, stub.hold
	stub.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		refusal := map[string]string{"message": message}
		if code != "" {
			refusal["code"] = code
		}
		encoded, _ := json.Marshal(map[string]any{"error": refusal})
		_, _ = w.Write(encoded)
		return
	}
	_, tool := body["tools"]
	w.Header().Set("Content-Type", "text/event-stream")
	if stub.chat {
		delta := map[string]any{"content": "Готово"}
		finish := "stop"
		if tool {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function",
				"function": map[string]any{"name": "probe_ping", "arguments": `{"word":"маяк","count":3}`}}}}
			finish = "tool_calls"
		}
		if cut {
			finish = "length"
		}
		chunk, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
			"usage": map[string]any{"prompt_tokens": len(raw) / 4, "completion_tokens": 7,
				"completion_tokens_details": map[string]any{"reasoning_tokens": 2}},
		})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
		return
	}
	usage := fmt.Sprintf(`{"input_tokens":%d,"output_tokens":7,"total_tokens":%d,"output_tokens_details":{"reasoning_tokens":2}}`, len(raw)/4, len(raw)/4+7)
	if tool {
		item := `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"probe_ping","arguments":"{\"word\":\"маяк\",\"count\":3}","status":"completed"}`
		fmt.Fprintf(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n", item)
		fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[%s],\"usage\":%s}}\n\n", item, usage)
		return
	}
	fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Готово\"}\n\n")
	if cut {
		fmt.Fprintf(w, "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_1\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":%s}}\n\n", usage)
		return
	}
	fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":%s}}\n\n", usage)
}

func (stub *probed) requests() []seen {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]seen(nil), stub.got...)
}

func (stub *probed) set(change func(*probed)) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	change(stub)
}

// specs/2026-11-13-desk-probes.md PB2.2: a probe goes the way the work goes — the alias of its role, the model, the key and
// the limit of the author, the protocol of his provider — with the words of the probe alone, and leaves a row of the
// journal with the purpose probe and without its text. Both protocols give the answer, the call and the usage with the
// tokens of the reasoning, which a long answer does not count (PB2.3 (4)).
func TestAProbeGoesTheWayTheWorkGoes(t *testing.T) {
	for _, chat := range []bool{false, true} {
		stand := openStand(t)
		stub := &probed{chat: chat}
		protocol := "responses"
		if chat {
			protocol = "chat"
		}
		const key = "key-test-probe-north-0001"
		stand.settings.set(t, custom(startProvider(t, stub), protocol), key)

		short, err := stand.gateway.Probe(context.Background(), checkOfTheTest, aisettings.Step{Probe: aisettings.ProbeShort, Role: aisettings.RoleChat, Model: "north-chat"})
		if err != nil || short.Failure != aisettings.FailureNone || short.Text != "Готово" || short.Usage == nil || short.Usage.Input <= 0 ||
			short.Usage.Reasoning != 2 || short.Cut {
			t.Fatalf("%s: the short probe = %+v, %v", protocol, short, err)
		}
		tool, err := stand.gateway.Probe(context.Background(), checkOfTheTest, aisettings.Step{Probe: aisettings.ProbeTool, Role: aisettings.RoleChat, Model: "north-chat"})
		if passed, _ := aisettings.Judge(aisettings.Step{Probe: aisettings.ProbeTool}, tool); err != nil || !passed {
			t.Fatalf("%s: the probe of the tool = %+v, %v", protocol, tool, err)
		}
		stub.set(func(stub *probed) { stub.cut = true })
		cut, err := stand.gateway.Probe(context.Background(), checkOfTheTest, aisettings.Step{Probe: aisettings.ProbeLongOutput, Role: aisettings.RoleBooks, Model: "north-cards"})
		if err != nil || !cut.Cut || cut.Failure != aisettings.FailureNone {
			t.Fatalf("%s: an answer cut at its limit = %+v, %v", protocol, cut, err)
		}

		got := stub.requests()
		if len(got) != 3 || got[0].model != "north-chat" || got[2].model != "north-cards" || got[0].header.Get("Authorization") != "Bearer "+key {
			t.Fatalf("%s: the provider got %+v", protocol, got)
		}
		question := aisettings.QuestionOf(aisettings.ProbeShort)
		if !strings.Contains(got[0].raw, "Ответь одним словом") || strings.Contains(got[0].raw, authorText) {
			t.Fatalf("%s: the short probe asked %s", protocol, got[0].raw)
		}
		var limit int
		if field := map[bool]string{false: "max_output_tokens", true: "max_completion_tokens"}[chat]; json.Unmarshal(got[2].body[field], &limit) != nil || limit != 16_000 {
			if json.Unmarshal(got[2].body["max_tokens"], &limit) != nil || limit != 16_000 {
				t.Fatalf("%s: the limit of the author did not go: %s", protocol, got[2].raw[:min(len(got[2].raw), 400)])
			}
		}
		rows := stand.journal.all()
		if len(rows) != 3 {
			t.Fatalf("%s: rows %+v", protocol, rows)
		}
		for index, row := range rows {
			if row.Purpose != modeljournal.PurposeProbe || row.JobID != "" || row.Profile != "own" || row.RequestBytes <= 0 {
				t.Errorf("%s: row %d = %+v", protocol, index, row)
			}
			if encoded, _ := json.Marshal(row); strings.Contains(string(encoded), question.Input) || strings.Contains(string(encoded), "Готово") {
				t.Errorf("%s: row %d holds a text", protocol, index)
			}
		}
		if rows[2].Outcome != modeljournal.OutcomeIncomplete || rows[2].Model != "north-cards" {
			t.Errorf("%s: the cut row = %+v", protocol, rows[2])
		}
		if log := stand.log.String(); strings.Contains(log, key) || strings.Contains(log, "Готово") || strings.Contains(log, "Проверка") {
			t.Fatalf("%s: the log holds the key or a text", protocol)
		}
	}
}

// lengthy is a stub of a provider that writes a long answer of many parts and then waits for the client to close the
// request, as a provider still writing would; a client that waits for the end gets it after wait, with usage.
type lengthy struct {
	chat   bool
	parts  int
	part   string
	wait   time.Duration
	mu     sync.Mutex
	closed bool
}

func (stub *lengthy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	for range stub.parts {
		if stub.chat {
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": stub.part}}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		} else {
			delta, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": stub.part})
			fmt.Fprintf(w, "event: response.output_text.delta\ndata: %s\n\n", delta)
		}
		flusher.Flush()
	}
	select {
	case <-r.Context().Done():
		stub.mu.Lock()
		stub.closed = true
		stub.mu.Unlock()
		return
	case <-time.After(stub.wait):
	}
	if stub.chat {
		chunk, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 120, "completion_tokens": 3_000},
		})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
		return
	}
	fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":120,\"output_tokens\":3000,\"total_tokens\":3120}}}\n\n")
}

func (stub *lengthy) wasClosed() bool {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return stub.closed
}

// PB2.3 (4), PB2.1: a long answer is read as far as its step says is enough and its request is closed then — the provider
// does not have to write the rest, which would cost the author minutes of the term; the probe passes by its text, without
// the usage the provider did not send, and its row of the journal is canceled.
func TestALongAnswerIsReadAsFarAsEnough(t *testing.T) {
	for _, chat := range []bool{false, true} {
		protocol := map[bool]string{false: "responses", true: "chat"}[chat]
		stand := openStand(t)
		stub := &lengthy{chat: chat, parts: 400, part: "Глава летописи острова. ", wait: 3 * time.Second}
		stand.settings.set(t, custom(startProvider(t, stub), protocol), "key-test-long-answer-0001")
		step := aisettings.Step{Probe: aisettings.ProbeLongOutput, Role: aisettings.RoleBooks, Model: "north-cards", Enough: 2_000}
		started := time.Now()
		answer, err := stand.gateway.Probe(context.Background(), checkOfTheTest, step)
		if passed, reason := aisettings.Judge(step, answer); err != nil || !passed || answer.Usage != nil || answer.Cut {
			t.Fatalf("%s: the long answer = %v %q, %v, usage %+v", protocol, passed, reason, err, answer.Usage)
		}
		if took := time.Since(started); took >= stub.wait {
			t.Fatalf("%s: the probe waited %s for the end of the answer", protocol, took)
		}
		deadline := time.Now().Add(2 * time.Second)
		for !stub.wasClosed() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if !stub.wasClosed() {
			t.Fatalf("%s: the request of the long answer was not closed", protocol)
		}
		rows := stand.journal.all()
		if len(rows) != 1 || rows[0].Purpose != modeljournal.PurposeProbe || rows[0].Outcome != modeljournal.OutcomeCanceled || rows[0].Tokens != nil {
			t.Fatalf("%s: rows %+v", protocol, rows)
		}
	}
}

// PB2.2: the purpose probe belongs to the gateway — a request from outside that names it by a header or a field is refused
// before the provider, whatever its model.
func TestNoRequestFromOutsideIsAProbe(t *testing.T) {
	stand := openStand(t)
	stub := &probed{}
	stand.settings.set(t, custom(startProvider(t, stub), "responses"), "key-test-purpose-0001")
	for what, request := range map[string]struct {
		body    string
		headers map[string]string
	}{
		"a header":            {chatBody(""), map[string]string{"X-CanonKeeper-Purpose": "probe"}},
		"a header of a chat":  {chatBody(""), map[string]string{"X-CanonKeeper-Purpose": "chat"}},
		"a field":             {chatBody(`,"purpose":"probe"`), nil},
		"a field of the core": {strings.Replace(chatBody(`,"purpose":"probe"`), "canonkeeper-chat", "canonkeeper-cards", 1), nil},
	} {
		response, body := stand.post(t, request.body, request.headers)
		if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, "invalid_request") {
			t.Errorf("%s = %d %s", what, response.StatusCode, body)
		}
	}
	if len(stub.requests()) != 0 || len(stand.journal.all()) != 0 {
		t.Fatal("a request that named the purpose reached the provider or the journal")
	}
}

// PB2.4, PB2.6: how the provider failed a probe — no connection, the key, its wait, no such model, a refused request, its
// own failure — without its words.
func TestAProbeNamesHowTheProviderFailed(t *testing.T) {
	stand := openStand(t)
	stub := &probed{chat: true}
	server := startProvider(t, stub)
	stand.settings.set(t, custom(server, "chat"), "key-test-failures-0001")
	step := aisettings.Step{Probe: aisettings.ProbeShort, Role: aisettings.RoleChat, Model: "north-chat"}
	for _, check := range []struct {
		status  int
		code    string
		message string
		want    aisettings.Failure
	}{
		{http.StatusUnauthorized, "", "Incorrect API key provided", aisettings.FailureKey},
		{http.StatusForbidden, "", "forbidden", aisettings.FailureKey},
		{http.StatusTooManyRequests, "", "slow down", aisettings.FailureRate},
		// An exhausted quota is no wait: waiting does not help it (waits; the list of the models says the same); it is the funds
		// of the author, as a 402 is (specs/2026-11-13-desk-consent.md CN3.4).
		{http.StatusTooManyRequests, "insufficient_quota", "You exceeded your current quota", aisettings.FailureFunds},
		{http.StatusPaymentRequired, "", "Insufficient Balance", aisettings.FailureFunds},
		{http.StatusNotFound, "", "model 'north-chat' not found, try pulling it first", aisettings.FailureModel},
		{http.StatusBadRequest, "", "Model Not Exist", aisettings.FailureModel},
		{http.StatusBadRequest, "", "the request is not valid", aisettings.FailureRefused},
		{http.StatusNotFound, "", "Not Found", aisettings.FailureOther},
		{http.StatusInternalServerError, "", "words of the provider", aisettings.FailureOther},
	} {
		stub.set(func(stub *probed) { stub.status, stub.code, stub.message = check.status, check.code, check.message })
		answer, err := stand.gateway.Probe(context.Background(), checkOfTheTest, step)
		if err != nil || answer.Failure != check.want || answer.Text != "" {
			t.Errorf("%d %q = %+v, %v; want %s", check.status, check.message, answer, err, check.want)
		}
	}
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := closed.Addr().String()
	_ = closed.Close()
	gone := custom(server, "chat")
	gone.BaseURL = text("http://" + address + "/v1")
	stand.settings.set(t, gone, "key-test-failures-0001")
	if answer, err := stand.gateway.Probe(context.Background(), checkOfTheTest, step); err != nil || answer.Failure != aisettings.FailureUnreachable {
		t.Fatalf("no connection = %+v, %v", answer, err)
	}
	if strings.Contains(stand.log.String(), "words of the provider") {
		t.Fatal("the log holds the words of the provider")
	}
}

// PB2.1, PB2.2: a probe takes its place among «Запросов одновременно» like any request, and one that ends while it waits
// or runs is the end of the check, not a failure of the provider. The request of the chat holds the one place until the
// test lets it go, so the probe waits whatever the load of the machine.
func TestAProbeWaitsForItsPlaceAndEndsWithTheCheck(t *testing.T) {
	stand := openStand(t)
	release := make(chan struct{})
	stub := &probed{hold: release}
	draft := custom(startProvider(t, stub), "responses")
	draft.Advanced.Parallel = number(1)
	stand.settings.set(t, draft, "key-test-gate-0002")
	done := make(chan struct{})
	go func() {
		defer close(done)
		stand.post(t, chatBody(""), nil)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for len(stub.requests()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the request of the chat did not reach the provider")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := stand.gateway.Probe(ctx, checkOfTheTest, aisettings.Step{Probe: aisettings.ProbeShort, Role: aisettings.RoleBooks, Model: "north-cards"}); err == nil {
		t.Fatal("a probe that ended while it waited for its place is no end")
	}
	close(release)
	<-done
	if got := stub.requests(); len(got) != 1 {
		t.Fatalf("the provider got %d requests", len(got))
	}
}
