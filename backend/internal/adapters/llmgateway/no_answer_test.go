// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/coresteps"
	"canonkeeper/backend/internal/domain/readingconsent"
)

// silentProvider is a provider of Responses that does not answer as its mode says: 503, a wait (429), an exhausted quota, a
// stream torn mid-answer, a response.failed without a reason or with an exhausted quota; with no mode it answers.
type silentProvider struct {
	mu   sync.Mutex
	mode string
}

func (stub *silentProvider) set(mode string) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.mode = mode
}

func (stub *silentProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	stub.mu.Lock()
	mode := stub.mode
	stub.mu.Unlock()
	refuse := func(status int, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
	stream := func(events ...string) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			_, _ = io.WriteString(w, event)
		}
	}
	const delta = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"{\\\"summary\"}\n\n"
	switch mode {
	case "503":
		refuse(http.StatusServiceUnavailable, `{"error":{"type":"server_error","code":"server_error","message":"overloaded"}}`)
	case "429":
		refuse(http.StatusTooManyRequests, `{"error":{"type":"rate_limit_exceeded","code":"rate_limit_exceeded","message":"slow down"}}`)
	case "quota":
		refuse(http.StatusTooManyRequests, `{"error":{"type":"insufficient_quota","code":"insufficient_quota","message":"You exceeded your current quota"}}`)
	case "torn":
		stream(delta)
	case "failed":
		stream(delta, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_1\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"The server had an error\"}}}\n\n")
	case "failed quota":
		stream("event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_1\",\"status\":\"failed\",\"error\":{\"code\":\"insufficient_quota\",\"message\":\"You exceeded your current quota\"}}}\n\n")
	default:
		stream(delta, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":3,\"total_tokens\":13}}}\n\n")
	}
}

// specs/2026-11-15-desk-reading-no-answer.md NA1.1, NA1.4: a request of the reading the check let go carries the mark of the
// check on its answer, and the gateway hears how the provider did not answer it — a 5xx and a stream torn mid-answer as a
// failure of the provider, a wait as a wait, a response.failed of the provider without an error of the stream too — with
// whether it was the last attempt of the client: on it, and only on it, the table of the consent pauses the reading at
// «провайдер не отвечает». An exhausted quota stays the funds, in a response.failed too. The chat, scan, the materials and
// the probe of «Проверить связь» carry no mark and are heard by nobody.
func TestTheGatewayHearsAProviderThatDoesNotAnswer(t *testing.T) {
	stand := openStand(t)
	stub := &silentProvider{}
	stand.settings.set(t, custom(startProvider(t, stub), "responses"), "key-test-no-answer-0001")
	reading := &readingOfTheTest{}
	stand.gateway.UseReading(reading)
	for _, row := range []struct {
		mode    string
		failure aisettings.Failure
		pause   readingconsent.Pause
	}{
		{"503", aisettings.FailureOther, readingconsent.PauseNoAnswer},
		{"429", aisettings.FailureRate, readingconsent.PauseNoAnswer},
		{"torn", aisettings.FailureOther, readingconsent.PauseNoAnswer},
		{"failed", aisettings.FailureOther, readingconsent.PauseNoAnswer},
		{"quota", aisettings.FailureFunds, readingconsent.PauseFunds},
		{"failed quota", aisettings.FailureFunds, readingconsent.PauseFunds},
	} {
		stub.set(row.mode)
		for _, last := range []bool{false, true} {
			reading.mu.Lock()
			reading.failures, reading.lasts = nil, nil
			reading.mu.Unlock()
			headers := map[string]string{modelapi.JobHeader: "job-of-the-cards"}
			if last {
				headers[modelapi.LastAttemptHeader] = "1"
			}
			response, body := stand.post(t, coreBody(t, coresteps.Card), headers)
			if response.Header.Get(modelapi.CheckedHeader) == "" {
				t.Errorf("%s: the answer %d %s carries no mark of the check", row.mode, response.StatusCode, body)
			}
			reading.mu.Lock()
			failures, lasts := append([]aisettings.Failure(nil), reading.failures...), append([]bool(nil), reading.lasts...)
			reading.mu.Unlock()
			if len(failures) != 1 || failures[0] != row.failure || lasts[0] != last {
				t.Errorf("%s (last %v) was heard as %v %v; want %s", row.mode, last, failures, lasts, row.failure)
				continue
			}
			pause, stops := readingconsent.PauseOf(failures[0], lasts[0])
			if want := last || row.pause == readingconsent.PauseFunds; stops != want || stops && pause != row.pause {
				t.Errorf("%s (last %v) pauses %q, %v", row.mode, last, pause, stops)
			}
		}
	}

	// An answer carries the mark too and is heard as nothing.
	stub.set("")
	reading.failures = nil
	if response, body := stand.post(t, coreBody(t, coresteps.LayerBook), map[string]string{modelapi.JobHeader: "job-of-the-layer", modelapi.LastAttemptHeader: "1"}); response.StatusCode != http.StatusOK ||
		response.Header.Get(modelapi.CheckedHeader) == "" || !strings.Contains(body, "response.completed") || len(reading.failures) != 0 {
		t.Fatalf("an answer = %d %q %s, heard %v", response.StatusCode, response.Header.Get(modelapi.CheckedHeader), body, reading.failures)
	}

	// The chat, scan and the materials: no check, no mark, nobody hears them.
	stub.set("503")
	for _, request := range []struct {
		body    string
		headers map[string]string
	}{
		{chatBody(""), map[string]string{modelapi.LastAttemptHeader: "1"}},
		{coreBody(t, coresteps.Scan), map[string]string{modelapi.JobHeader: "job-of-the-turn", modelapi.LastAttemptHeader: "1"}},
		{coreBody(t, coresteps.Materials), map[string]string{modelapi.JobHeader: "job-of-the-materials", modelapi.LastAttemptHeader: "1"}},
	} {
		response, _ := stand.post(t, request.body, request.headers)
		if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get(modelapi.CheckedHeader) != "" {
			t.Errorf("%s = %d, mark %q", request.body[:40], response.StatusCode, response.Header.Get(modelapi.CheckedHeader))
		}
	}
	// «Проверить связь» says its own words and pauses nothing.
	if answer, err := stand.gateway.Probe(context.Background(), checkOfTheTest, aisettings.Step{Probe: aisettings.ProbeShort, Role: aisettings.RoleBooks, Model: "north-cards"}); err != nil || answer.Failure != aisettings.FailureOther {
		t.Errorf("the probe of a provider that does not answer = %+v, %v", answer, err)
	}
	if len(reading.failures) != 0 {
		t.Fatalf("the chat, scan, the materials or the probe were heard: %v", reading.failures)
	}

	// A request the check did not let go carries no mark: it is refused, and the client stops at its refusal.
	reading.refuse = "no_answer"
	if response, body := stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-of-the-cards"}); response.StatusCode != http.StatusConflict ||
		response.Header.Get(modelapi.CheckedHeader) != "" {
		t.Fatalf("a refused request = %d %q %s", response.StatusCode, response.Header.Get(modelapi.CheckedHeader), body)
	}
}
