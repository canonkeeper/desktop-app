// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/coresteps"
	"canonkeeper/backend/internal/domain/readingconsent"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// tornProvider begins its answer of every request of the reading and does not end it, as its mode says: the connection reset
// (reset) or closed without the end of the body (fin) after one piece of the answer, in the protocol of the mode, or an error
// event of Responses that refuses the request mid-answer. read hears that the client of the app got the piece: the connection
// is broken only once the answer began for the client too.
type tornProvider struct {
	mode string
	read chan struct{}
}

func (stub *tornProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	chat := strings.HasPrefix(stub.mode, "chat")
	if chat != (r.URL.Path == "/v1/chat/completions") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	if chat {
		_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"{\"summary"}}]}`+"\n\n")
	} else {
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"{\\\"summary\"}\n\n")
	}
	if stub.mode == "responses error" {
		_, _ = io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"status\":400,\"error\":{\"type\":\"invalid_request_error\",\"code\":\"invalid_request_error\",\"message\":\"the request was refused\"}}\n\n")
		return
	}
	w.(http.Flusher).Flush()
	select {
	case <-stub.read:
	case <-time.After(5 * time.Second):
	case <-r.Context().Done():
	}
	connection, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	if tcp, ok := connection.(*net.TCPConn); ok && strings.HasSuffix(stub.mode, "reset") {
		_ = tcp.SetLinger(0)
	}
	_ = connection.Close()
}

// specs/2026-11-15-desk-reading-no-answer.md NA1.1: «нет связи» is a provider the request did not reach — a connection that
// did not open, before the headers of an answer; an answer the provider began and did not end — the connection reset or
// closed mid-answer, in either protocol, or an error event mid-answer — is «провайдер не отвечает». The gateway hears it as
// a failure of the provider on every attempt, the table of the consent pauses the reading at no_answer on the last, and the
// client of the app ends with the stop of the same reason: the pause and the code of the Job are one.
func TestAnAnswerTornMidwayIsNoAnswer(t *testing.T) {
	for _, row := range []struct {
		name, protocol, mode string
		failure              aisettings.Failure
		pause                readingconsent.Pause
		stop                 sagamemory.Stop
	}{
		{"responses: the connection reset mid-answer", "responses", "responses reset", aisettings.FailureOther, readingconsent.PauseNoAnswer, sagamemory.StopNoAnswer},
		{"responses: the connection closed mid-answer", "responses", "responses fin", aisettings.FailureOther, readingconsent.PauseNoAnswer, sagamemory.StopNoAnswer},
		{"chat completions: the connection reset mid-answer", "chat", "chat reset", aisettings.FailureOther, readingconsent.PauseNoAnswer, sagamemory.StopNoAnswer},
		{"chat completions: the connection closed mid-answer", "chat", "chat fin", aisettings.FailureOther, readingconsent.PauseNoAnswer, sagamemory.StopNoAnswer},
		{"responses: an error event refuses the request mid-answer", "responses", "responses error", aisettings.FailureOther, readingconsent.PauseNoAnswer, sagamemory.StopNoAnswer},
		{"no connection before the answer", "responses", "", aisettings.FailureUnreachable, readingconsent.PauseConnection, sagamemory.StopConnection},
	} {
		stand := openStand(t)
		stub := &tornProvider{mode: row.mode, read: make(chan struct{}, 1)}
		draft := custom(startProvider(t, stub), row.protocol)
		if row.mode == "" {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			draft.BaseURL = text("http://" + listener.Addr().String() + "/v1")
			_ = listener.Close()
		}
		stand.settings.set(t, draft, "key-test-torn-0001")
		reading := &readingOfTheTest{}
		stand.gateway.UseReading(reading)
		client, err := modelapi.New(modelapi.Config{
			BaseURL: stand.gateway.BaseURL(), Key: stand.gateway.Token(), Model: CardsModel, CarryJob: true, Stops: true, RetryPause: time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		card, _ := coresteps.Named(string(coresteps.Card))
		_, _, err = client.Background().Stream(named(jobs.WithJobID(t.Context(), "job-of-the-cards")), card.Instructions, "Глава 1. Сигнальные огни", func(sofar string) {
			if sofar != "" {
				select {
				case stub.read <- struct{}{}:
				default:
				}
			}
		})
		if stop, ok := sagamemory.StopOf(err); !ok || stop != row.stop {
			t.Errorf("%s: the client ended with %v; want the stop %s", row.name, err, row.stop)
		}
		// The client hears the torn answer before the gateway ends its own request: the last attempt is heard a moment later.
		var failures []aisettings.Failure
		var lasts []bool
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			reading.mu.Lock()
			failures, lasts = append([]aisettings.Failure(nil), reading.failures...), append([]bool(nil), reading.lasts...)
			reading.mu.Unlock()
			if len(failures) >= 6 || time.Now().After(deadline) {
				break
			}
		}
		if len(failures) != 6 || !lasts[5] || lasts[4] {
			t.Errorf("%s: the gateway heard %v %v; want the six attempts of one request", row.name, failures, lasts)
			continue
		}
		for index, failure := range failures {
			if failure != row.failure {
				t.Errorf("%s: attempt %d was heard as %s; want %s", row.name, index+1, failure, row.failure)
			}
			pause, stops := readingconsent.PauseOf(failure, lasts[index])
			if stops != lasts[index] || stops && pause != row.pause {
				t.Errorf("%s: attempt %d pauses %q, %v; want %s on the last alone", row.name, index+1, pause, stops, row.pause)
			}
		}
	}
}

// specs/2026-11-15-desk-reading-no-answer.md NA2.1: the table of the gateway and the repeats of the client of the app are one —
// a provider that refuses every attempt with a refusal of a state that passes (408, 409, 423, 425) does not mark its chapter:
// the gateway hears a failure of the provider, the client repeats it through its attempts, and the last pauses the reading at
// no_answer with the stop of the same reason. A refusal of the request (400) stays refused after one attempt.
func TestARefusalAStateThatPassesGaveIsNoAnswer(t *testing.T) {
	for _, row := range []struct {
		status   int
		failure  aisettings.Failure
		attempts int
	}{
		{http.StatusRequestTimeout, aisettings.FailureOther, 6}, {http.StatusConflict, aisettings.FailureOther, 6},
		{http.StatusLocked, aisettings.FailureOther, 6}, {http.StatusTooEarly, aisettings.FailureOther, 6},
		{http.StatusBadRequest, aisettings.FailureRefused, 1},
	} {
		stand := openStand(t)
		server := startProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(row.status)
			_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"invalid_request_error","message":"not now"}}`)
		}))
		stand.settings.set(t, custom(server, "responses"), "key-test-passes-0001")
		reading := &readingOfTheTest{}
		stand.gateway.UseReading(reading)
		client, err := modelapi.New(modelapi.Config{
			BaseURL: stand.gateway.BaseURL(), Key: stand.gateway.Token(), Model: CardsModel, CarryJob: true, Stops: true, RetryPause: time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		card, _ := coresteps.Named(string(coresteps.Card))
		_, _, err = client.Background().Complete(named(jobs.WithJobID(t.Context(), "job-of-the-cards")), card.Instructions, "Глава 1. Сигнальные огни")
		stop, stopped := sagamemory.StopOf(err)
		if row.attempts == 6 && (!stopped || stop != sagamemory.StopNoAnswer) || row.attempts == 1 && !errors.Is(err, sagamemory.ErrRefused) {
			t.Errorf("%d: the client ended with %v", row.status, err)
		}
		reading.mu.Lock()
		failures, lasts := append([]aisettings.Failure(nil), reading.failures...), append([]bool(nil), reading.lasts...)
		reading.mu.Unlock()
		if len(failures) != row.attempts || failures[len(failures)-1] != row.failure || row.attempts == 6 && !lasts[5] {
			t.Errorf("%d: the gateway heard %v %v", row.status, failures, lasts)
			continue
		}
		if pause, stops := readingconsent.PauseOf(failures[len(failures)-1], lasts[len(lasts)-1]); row.attempts == 6 && (!stops || pause != readingconsent.PauseNoAnswer) ||
			row.attempts == 1 && stops {
			t.Errorf("%d: the last attempt pauses %q, %v", row.status, pause, stops)
		}
	}
}
