// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/coresteps"
	"canonkeeper/backend/internal/domain/modeljournal"
)

// readingOfTheTest is the check of the reading: it lets go or refuses, and hears what the gateway settles and how the
// provider refused.
type readingOfTheTest struct {
	mu       sync.Mutex
	refuse   string
	asked    []ReadingRequest
	settled  []modeljournal.Request
	answered []bool
	failures []aisettings.Failure
	lasts    []bool
	stops    []string
	released int
}

type ticketOfTheTest struct{ reading *readingOfTheTest }

func (reading *readingOfTheTest) Admit(_ context.Context, request ReadingRequest) (Ticket, string, error) {
	reading.mu.Lock()
	defer reading.mu.Unlock()
	reading.asked = append(reading.asked, request)
	if reading.refuse != "" {
		return nil, reading.refuse, nil
	}
	return ticketOfTheTest{reading: reading}, "", nil
}

func (ticket ticketOfTheTest) Settle(_ context.Context, entry modeljournal.Request, answered bool) {
	ticket.reading.mu.Lock()
	defer ticket.reading.mu.Unlock()
	ticket.reading.settled = append(ticket.reading.settled, entry)
	ticket.reading.answered = append(ticket.reading.answered, answered)
}

func (ticket ticketOfTheTest) Release(context.Context) {
	ticket.reading.mu.Lock()
	defer ticket.reading.mu.Unlock()
	ticket.reading.released++
}

func (ticket ticketOfTheTest) Stop(_ context.Context, reason string) {
	ticket.reading.mu.Lock()
	defer ticket.reading.mu.Unlock()
	ticket.reading.stops = append(ticket.reading.stops, reason)
}

func (ticket ticketOfTheTest) Failed(_ context.Context, failure aisettings.Failure, last bool) {
	ticket.reading.mu.Lock()
	defer ticket.reading.mu.Unlock()
	ticket.reading.failures = append(ticket.reading.failures, failure)
	ticket.reading.lasts = append(ticket.reading.lasts, last)
}

// specs/2026-11-13-desk-consent.md CN3.3, CN3.4: every request of the reading — and only of the reading — is asked of the
// check before the provider; a refused one goes nowhere, is answered 409 reading.paused with why and leaves no row; one let
// go is settled once with its tokens and its cost by the prices of its minute instead of a row of the journal; a refusal of
// the provider is heard with its kind and whether it was the last attempt of the client, and its answer names the reason.
func TestEveryRequestOfTheReadingIsCheckedBeforeTheProvider(t *testing.T) {
	stand := openStand(t)
	stub := &provider{}
	server := startProvider(t, stub)
	draft := custom(server, "responses")
	draft.Advanced = aisettings.AdvancedDraft{Currency: text("RUB"), PriceIn: text("30"), PriceOut: text("120")}
	stand.settings.set(t, draft, "key-test-reading-0001")
	reading := &readingOfTheTest{refuse: "sum"}
	stand.gateway.UseReading(reading)

	response, body := stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-of-the-cards"})
	if response.StatusCode != http.StatusConflict || !strings.Contains(body, `"code":"reading.paused"`) || !strings.Contains(body, `"reason":"sum"`) {
		t.Fatalf("a refused request = %d %s", response.StatusCode, body)
	}
	if len(stub.requests()) != 0 || len(stand.journal.all()) != 0 {
		t.Fatalf("a refused request went to the provider (%d) or the journal (%d)", len(stub.requests()), len(stand.journal.all()))
	}
	if len(reading.asked) != 1 || reading.asked[0].JobID != "job-of-the-cards" || reading.asked[0].Purpose != "card" ||
		reading.asked[0].Host != strings.TrimPrefix(server.URL, "http://") || reading.asked[0].RequestBytes <= 0 {
		t.Fatalf("the check was asked %+v", reading.asked)
	}

	reading.refuse = ""
	if response, body := stand.post(t, coreBody(t, coresteps.LayerVerify), map[string]string{modelapi.JobHeader: "job-of-the-layer"}); response.StatusCode != http.StatusOK {
		t.Fatalf("a request let go = %d %s", response.StatusCode, body)
	}
	if len(stub.requests()) != 1 || len(reading.settled) != 1 || len(stand.journal.all()) != 0 {
		t.Fatalf("let go: provider %d, settled %d, journal %d", len(stub.requests()), len(reading.settled), len(stand.journal.all()))
	}
	settled := reading.settled[0]
	// 10 in, 4 of them cached, 3 out at 30 and 120 rubles a million: (6 + 4)·30 + 3·120 = 660 millionths of a ruble.
	if settled.Purpose != "layer_verify" || settled.Tokens == nil || settled.Cost == nil || settled.Cost.Currency != "RUB" ||
		settled.Cost.Micros != 660 || settled.OutputCharacters != int64(len([]rune("Ответ"))) || !reading.answered[0] {
		t.Fatalf("the settled request = %+v, %+v", settled, settled.Cost)
	}

	// The chat, scan and the materials are never asked; their rows have their cost too.
	asked := len(reading.asked)
	for _, request := range []struct {
		body    string
		headers map[string]string
	}{
		{chatBody(""), nil},
		{coreBody(t, coresteps.Scan), map[string]string{modelapi.JobHeader: "job-of-the-turn"}},
		{coreBody(t, coresteps.Materials), map[string]string{modelapi.JobHeader: "job-of-the-materials"}},
	} {
		if response, body := stand.post(t, request.body, request.headers); response.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d %s", request.body[:40], response.StatusCode, body)
		}
	}
	rows := stand.journal.all()
	if len(reading.asked) != asked || len(rows) != 3 || rows[0].Cost == nil || rows[0].Cost.Micros == 0 {
		t.Fatalf("the check was asked %d times more; the journal = %+v", len(reading.asked)-asked, rows)
	}

	// A refusal of the provider is heard with its kind; its answer names the reason of the stop for the client of the app.
	stub.mu.Lock()
	stub.status = http.StatusUnauthorized
	stub.mu.Unlock()
	response, body = stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-of-the-cards", modelapi.LastAttemptHeader: "1"})
	if response.StatusCode != http.StatusUnauthorized || !strings.Contains(body, `"reason":"key"`) {
		t.Fatalf("a refusal of the key = %d %s", response.StatusCode, body)
	}
	if len(reading.failures) != 1 || reading.failures[0] != aisettings.FailureKey || !reading.lasts[0] || len(reading.settled) != 2 {
		t.Fatalf("the refusal was heard as %v %v, settled %d", reading.failures, reading.lasts, len(reading.settled))
	}
	for _, seen := range stub.requests() {
		if seen.header.Get(modelapi.LastAttemptHeader) != "" || seen.header.Get(modelapi.JobHeader) != "" {
			t.Fatalf("the provider got the headers of the client: %v", seen.header)
		}
	}
}
