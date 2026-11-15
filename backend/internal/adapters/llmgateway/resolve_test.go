// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/coresteps"
	"canonkeeper/backend/internal/domain/modeljournal"
	"canonkeeper/backend/internal/domain/readingconsent"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// probedProvider answers the probe of «Определить» — /responses with an empty request — with its status and records what it
// got; it answers nothing else.
type probedProvider struct {
	mu     sync.Mutex
	status int
	got    []string
}

func (stub *probedProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	stub.mu.Lock()
	stub.got = append(stub.got, r.URL.Path+" "+string(raw))
	status := stub.status
	stub.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"error":{"message":"no"}}`)
}

func (stub *probedProvider) requests() []string {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]string(nil), stub.got...)
}

// specs/2026-11-15-desk-reading-no-answer.md NA1.1, specs/2026-11-13-desk-consent.md CN3.3, CN3.4: with the protocol «auto»
// not found yet — the first request after a start — a request of the reading is checked before «Определить», and a probe that
// failed is heard by the same table as a refusal of the provider with the rule of the last attempt, its reserve released
// without a row or a count — the request never went (specs/2026-11-14-desk-journal.md JR1.1), «Определить» has its own record
// and row: a provider not reached pauses the reading at «связь» on the last attempt, a refused key at once, a 5xx and a wait
// at «провайдер не отвечает» on the last; the client of the app ends with the stop of the same reason. A request the check did
// not let go sends the provider no probe at all. The probe carries no text of the author; the chat is not checked.
func TestTheCheckGoesBeforeTheProbeOfTheProtocol(t *testing.T) {
	for _, row := range []struct {
		name     string
		status   int
		failure  aisettings.Failure
		attempts int
		pause    readingconsent.Pause
		stop     sagamemory.Stop
	}{
		{"the provider not reached", 0, aisettings.FailureUnreachable, 6, readingconsent.PauseConnection, sagamemory.StopConnection},
		{"401", http.StatusUnauthorized, aisettings.FailureKey, 1, readingconsent.PauseKey, sagamemory.StopKey},
		{"503", http.StatusServiceUnavailable, aisettings.FailureOther, 6, readingconsent.PauseNoAnswer, sagamemory.StopNoAnswer},
		{"429", http.StatusTooManyRequests, aisettings.FailureRate, 6, readingconsent.PauseNoAnswer, sagamemory.StopNoAnswer},
	} {
		stand := openStand(t)
		stub := &probedProvider{status: row.status}
		draft := custom(startProvider(t, stub), "auto")
		if row.status == 0 {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			draft.BaseURL = text("http://" + listener.Addr().String() + "/v1")
			_ = listener.Close()
		}
		stand.settings.set(t, draft, "key-test-probe-0001")
		reading := &readingOfTheTest{}
		stand.gateway.UseReading(reading)
		client, err := modelapi.New(modelapi.Config{
			BaseURL: stand.gateway.BaseURL(), Key: stand.gateway.Token(), Model: CardsModel, CarryJob: true, Stops: true, RetryPause: time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		card, _ := coresteps.Named(string(coresteps.Card))
		_, _, err = client.Background().Complete(named(jobs.WithJobID(t.Context(), "job-of-the-cards")), card.Instructions, authorText)
		if stop, ok := sagamemory.StopOf(err); !ok || stop != row.stop {
			t.Errorf("%s: the client ended with %v; want the stop %s", row.name, err, row.stop)
		}
		reading.mu.Lock()
		asked, settled, released := len(reading.asked), len(reading.settled), reading.released
		failures, lasts := append([]aisettings.Failure(nil), reading.failures...), append([]bool(nil), reading.lasts...)
		reading.mu.Unlock()
		if asked != row.attempts || settled != 0 || released != row.attempts || len(failures) != row.attempts {
			t.Errorf("%s: checked %d, settled %d, released %d, heard %v; want %d attempts", row.name, asked, settled, released, failures, row.attempts)
			continue
		}
		for _, record := range stand.journal.sent() {
			if record.Purpose != modeljournal.PurposeProbe || !record.Named.Protocol {
				t.Errorf("%s: a record of a request that never went: %+v", row.name, record)
			}
		}
		for index := range failures {
			if failures[index] != row.failure {
				t.Errorf("%s: attempt %d heard %s", row.name, index+1, failures[index])
			}
			pause, stops := readingconsent.PauseOf(failures[index], lasts[index])
			want := lasts[index] || row.pause == readingconsent.PauseKey
			if stops != want || stops && pause != row.pause {
				t.Errorf("%s: attempt %d (last %v) pauses %q, %v; want %s", row.name, index+1, lasts[index], pause, stops, row.pause)
			}
		}
		if !lasts[len(lasts)-1] && row.attempts == 6 {
			t.Errorf("%s: the last attempt was not heard as the last: %v", row.name, lasts)
		}
		for _, got := range stub.requests() {
			if got != "/v1/responses {}" {
				t.Errorf("%s: the provider got %q; want the probe alone, without the text of the author", row.name, got)
			}
		}
	}

	// A request the check did not let go sends the provider nothing, not even the probe; the chat goes unchecked as before.
	stand := openStand(t)
	stub := &probedProvider{status: http.StatusServiceUnavailable}
	stand.settings.set(t, custom(startProvider(t, stub), "auto"), "key-test-probe-0002")
	reading := &readingOfTheTest{refuse: "no_answer"}
	stand.gateway.UseReading(reading)
	if response, body := stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-of-the-cards"}); response.StatusCode != http.StatusConflict ||
		len(stub.requests()) != 0 {
		t.Fatalf("a request the check refused = %d %s, the provider got %v", response.StatusCode, body, stub.requests())
	}
	if response, _ := stand.post(t, chatBody(""), nil); response.StatusCode != http.StatusServiceUnavailable || len(stub.requests()) != 1 ||
		len(reading.asked) != 1 || len(reading.failures) != 0 {
		t.Fatalf("the chat = %d, the provider got %v, checked %d, heard %v", response.StatusCode, stub.requests(), len(reading.asked), reading.failures)
	}
}
