// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/modelapi"
	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/appwork"
	"canonkeeper/backend/internal/domain/coresteps"
	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modeljournal"

	"github.com/canonkeeper/desktop-app/translate"
)

// witness is a provider that writes down how many records lay when each request reached it, before its first byte.
type witness struct {
	journal *keptJournal
	next    http.Handler
	mu      sync.Mutex
	at      []int
}

func (stub *witness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	stub.mu.Lock()
	stub.at = append(stub.at, len(stub.journal.sent()))
	stub.mu.Unlock()
	stub.next.ServeHTTP(w, r)
}

func (stub *witness) counts() []int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]int(nil), stub.at...)
}

func journalStand(t *testing.T) (*stand, *keptJournal) {
	t.Helper()
	stand := openStand(t)
	return stand, stand.journal
}

// specs/2026-11-14-desk-journal.md JR1.1: the record of a sending lies before the first byte of its body reaches the provider —
// a stream and a whole answer, a probe of «Проверить связь» and «Определить» —, and the row of the request carries its number.
func TestTheRecordLiesBeforeTheFirstByte(t *testing.T) {
	stand, journal := journalStand(t)
	chat := &witness{journal: journal, next: &provider{chat: true}}
	stand.settings.set(t, custom(startProvider(t, chat), "auto"), "key-test-journal-0001")
	if response, stream := stand.post(t, chatBody(""), nil); response.StatusCode != http.StatusOK || !completed(stream) {
		t.Fatalf("a stream = %d %s", response.StatusCode, stream)
	}
	// A request without a stream: the stub answers a stream all the same, so only its record and its row are read here.
	stand.post(t, strings.Replace(coreBody(t, coresteps.Card), `"stream":true`, `"stream":false`, 1), nil)
	if _, err := stand.gateway.Probe(context.Background(), "95000000-0000-4000-8000-0000000000c1", aisettings.Step{Probe: aisettings.ProbeShort, Role: aisettings.RoleChat}); err != nil {
		t.Fatal(err)
	}
	// «Определить», the stream, the whole answer, the probe: each reached the provider with its record already there.
	if got := chat.counts(); len(got) != 4 || got[0] != 1 || got[1] != 2 || got[2] != 3 || got[3] != 4 {
		t.Fatalf("records at each request = %v", got)
	}
	sent := journal.sent()
	if sent[0].Purpose != modeljournal.PurposeProbe || sent[0].CheckID == "" || !sent[0].Named.Protocol || sent[0].Bytes != int64(len("{}")) {
		t.Fatalf("the record of «Определить» = %+v", sent[0])
	}
	if sent[1].Purpose != modeljournal.PurposeChat || sent[1].ThreadID != threadOfTheTest || sent[1].JobID != jobOfTheTest ||
		sent[2].Purpose != string(coresteps.Card) || !reflect.DeepEqual(sent[2].Named, defaultNamed) || sent[3].CheckID != "95000000-0000-4000-8000-0000000000c1" {
		t.Fatalf("the records = %+v", sent)
	}
	for _, record := range sent {
		if record.ID == "" || record.Profile != "own" || record.Host == "" || record.Bytes <= 0 || record.At.IsZero() {
			t.Fatalf("a record = %+v", record)
		}
	}
	rows := stand.journal.all()
	if len(rows) != 4 {
		t.Fatalf("rows = %+v", rows)
	}
	for index, row := range rows {
		if row.ID != sent[index].ID || row.CheckID != sent[index].CheckID {
			t.Errorf("row %d carries %q of the check %q, its record %q", index, row.ID, row.CheckID, sent[index].ID)
		}
	}
	// A repeat of the client is a sending of its own.
	stand.post(t, chatBody(""), nil)
	if len(journal.sent()) != 5 {
		t.Fatalf("the repeat wrote %d records", len(journal.sent()))
	}
}

// JR1.1: a record that does not lie, and a gateway without the journal, send nothing — journal.unavailable, which neither
// Codex nor the client of the core repeats; a refusal before the sending leaves no record.
func TestWithoutItsRecordNothingIsSent(t *testing.T) {
	stand, journal := journalStand(t)
	stub := &provider{}
	server := startProvider(t, stub)
	stand.post(t, chatBody(""), nil) // the helper is not connected yet
	stand.settings.set(t, custom(server, "responses"), "key-test-journal-0002")
	journal.refuse.Store(true)
	for _, body := range []string{chatBody(""), coreBody(t, coresteps.Card)} {
		response, answer := stand.post(t, body, nil)
		if response.StatusCode != http.StatusBadRequest || !strings.Contains(answer, disclosure.CodeUnavailable) {
			t.Fatalf("a record that did not lie = %d %s", response.StatusCode, answer)
		}
	}
	if _, err := stand.gateway.Probe(context.Background(), "95000000-0000-4000-8000-0000000000c2", aisettings.Step{Probe: aisettings.ProbeShort, Role: aisettings.RoleChat}); err != nil {
		t.Fatal(err)
	}
	journal.refuse.Store(false)
	reading := &readingOfTheTest{refuse: "sum"}
	stand.gateway.UseReading(reading)
	if response, _ := stand.post(t, coreBody(t, coresteps.Card), nil); response.StatusCode != http.StatusConflict {
		t.Fatalf("a paused reading = %d", response.StatusCode)
	}
	if len(stub.requests()) != 0 || len(journal.sent()) != 0 || len(stand.journal.all()) != 0 {
		t.Fatalf("the provider got %d, records %d, rows %d", len(stub.requests()), len(journal.sent()), len(stand.journal.all()))
	}

	bare, err := Open(stand.settings, stand.gateway.logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bare.Close() })
	t.Cleanup(bare.Threads().Bind(threadOfTheTest, jobOfTheTest))
	other := *stand
	other.gateway = bare
	if response, answer := other.post(t, chatBody(""), nil); response.StatusCode != http.StatusBadRequest || !strings.Contains(answer, disclosure.CodeUnavailable) {
		t.Fatalf("a gateway without the journal = %d %s", response.StatusCode, answer)
	}
	if len(stub.requests()) != 0 {
		t.Fatal("a gateway without the journal asked the provider")
	}
}

// JR1.1, JR1.6: a record of a request of the reading that did not lie releases its reserve — no row, no count — and pauses
// nothing; the provider is not asked.
func TestARecordThatDidNotLieReleasesTheReserve(t *testing.T) {
	stand, journal := journalStand(t)
	stub := &provider{}
	stand.settings.set(t, custom(startProvider(t, stub), "responses"), "key-test-journal-0003")
	reading := &readingOfTheTest{}
	stand.gateway.UseReading(reading)
	journal.refuse.Store(true)
	if response, _ := stand.post(t, coreBody(t, coresteps.Card), nil); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("the refused record = %d", response.StatusCode)
	}
	reading.mu.Lock()
	settled, failures, stops, released := len(reading.settled), len(reading.failures), len(reading.stops), reading.released
	reading.mu.Unlock()
	if settled != 0 || failures != 0 || stops != 0 || released != 1 || len(stub.requests()) != 0 {
		t.Fatalf("settled %d, failures %d, stops %d, released %d, provider %d", settled, failures, stops, released, len(stub.requests()))
	}
}

// JR1.2: a request of Codex whose thread is in no turn goes nowhere whatever the source: a status Codex does not repeat with
// turn.unbound, no request to the provider or the site, no record.
func TestARequestOfCodexOutsideATurnIsRefusedForEverySource(t *testing.T) {
	stand, journal := journalStand(t)
	stub := &provider{}
	stand.settings.set(t, custom(startProvider(t, stub), "responses"), "key-test-journal-0004")
	for _, thread := range []string{"", "thread-unknown"} {
		response, body := stand.post(t, chatBody(""), map[string]string{"thread-id": thread})
		if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, codeUnbound) {
			t.Fatalf("%q = %d %s", thread, response.StatusCode, body)
		}
	}
	accountStandOf, account := accountStand(t)
	accountJournal := accountStandOf.journal
	if response, body := accountStandOf.post(t, chatBody(""), map[string]string{"thread-id": "thread-unknown"}); response.StatusCode != http.StatusBadRequest || !strings.Contains(body, codeUnbound) {
		t.Fatalf("the account = %d %s", response.StatusCode, body)
	}
	if asked, _, _ := account.heard(); len(asked) != 0 || len(stub.requests()) != 0 || len(journal.sent()) != 0 || len(accountJournal.sent()) != 0 {
		t.Fatalf("the site heard %v, the provider %d, records %d", asked, len(stub.requests()), len(journal.sent()))
	}
}

// JR1.4: a call of the core names what it gives in its header, which the gateway takes off before the provider; one without
// it, or with one of another form, is refused with journal.unnamed before any record; a request of Codex with it is refused.
func TestACallOfTheCoreNamesWhatItGives(t *testing.T) {
	stand, journal := journalStand(t)
	stub := &provider{}
	stand.settings.set(t, custom(startProvider(t, stub), "responses"), "key-test-journal-0005")
	for _, value := range []string{"", `{}`, `{"title":"Белый порт"}`, `{"chapters":[{"id":"x","p":"whole"}]}`} {
		response, body := stand.post(t, coreBody(t, coresteps.Card), map[string]string{disclosure.Header: value})
		if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, disclosure.CodeUnnamed) {
			t.Fatalf("%q = %d %s", value, response.StatusCode, body)
		}
	}
	if response, _ := stand.post(t, chatBody(""), map[string]string{disclosure.Header: `{"names":1}`}); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("a request of Codex with the header = %d", response.StatusCode)
	}
	if len(stub.requests()) != 0 || len(journal.sent()) != 0 {
		t.Fatal("a call that named nothing went or lay")
	}
	if response, _ := stand.post(t, coreBody(t, coresteps.Names), map[string]string{disclosure.Header: `{"names":214}`, modelapi.JobHeader: "job-of-the-names"}); response.StatusCode != http.StatusOK {
		t.Fatalf("a named call = %d", response.StatusCode)
	}
	if got := stub.requests(); len(got) != 1 || got[0].header.Get(disclosure.Header) != "" {
		t.Fatal("the provider got the header of the journal")
	}
	if sent := journal.sent(); len(sent) != 1 || sent[0].Named.Names != 214 || sent[0].JobID != "job-of-the-names" {
		t.Fatalf("the record = %+v", sent)
	}
}

// JR1.1: the gateway passes the body to the site of the account again while it waits for its place and once under a new
// permit of the reading — every passing lies its record before the site gets it, the size of the body as it goes, and every
// passing the site answered has its row with the code of the answer.
func TestEveryPassingToTheSiteLiesItsRecord(t *testing.T) {
	stand, account := accountStand(t)
	busy := &appwork.Refusal{Status: http.StatusTooManyRequests, Code: appwork.CodeBusy}
	account.answers = []siteAnswer{{refusal: busy}, {refusal: busy}, {text: "Ответ"}}
	if response, stream := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"}); response.StatusCode != http.StatusOK || !completed(stream) {
		t.Fatalf("the turn = %d %s", response.StatusCode, stream)
	}
	_, _, bodies := account.heard()
	sent := stand.journal.sent()
	if len(sent) != 3 || len(bodies) != 3 {
		t.Fatalf("records %d, passings %d", len(sent), len(bodies))
	}
	for index, record := range sent {
		if record.Profile != "account" || record.Host != siteHostOfTheTest || record.Bytes != int64(len(bodies[index])) || record.ThreadID != "thread-1" {
			t.Fatalf("record %d = %+v", index, record)
		}
	}
	rows := stand.journal.all()
	if len(rows) != 3 {
		t.Fatalf("rows %+v", rows)
	}
	for index, row := range rows {
		wantCode := appwork.CodeBusy
		if index == 2 {
			wantCode = ""
		}
		if row.ID != sent[index].ID || row.RefusalCode != wantCode || (index < 2) != (row.Failure == modeljournal.FailureRefused) {
			t.Fatalf("row %d = %+v, its record %s", index, row, sent[index].ID)
		}
	}

	reading := &readingOfTheTest{}
	stand.gateway.UseReading(reading)
	account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: 409, Code: appwork.CodePermitClosed}}, {text: "карточка"}}
	if response, stream := stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-cards"}); response.StatusCode != http.StatusOK || !completed(stream) {
		t.Fatalf("the step = %d %s", response.StatusCode, stream)
	}
	if got := stand.journal.sent(); len(got) != 5 || got[3].Purpose != string(coresteps.Card) || got[4].Purpose != string(coresteps.Card) || got[4].JobID != "job-cards" {
		t.Fatalf("the records of the step = %+v", got[3:])
	}
	// Each passing reached the site with its record already there.
	account.mu.Lock()
	recordsAt := append([]int(nil), account.recordsAt...)
	account.mu.Unlock()
	if !reflect.DeepEqual(recordsAt, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("records when the site was asked = %v", recordsAt)
	}
	// The passing the site closed the permit of has its row by itself; the step that answered is settled once.
	rows = stand.journal.all()
	reading.mu.Lock()
	settled := append([]modeljournal.Request(nil), reading.settled...)
	reading.mu.Unlock()
	if len(rows) != 4 || rows[3].ID != stand.journal.sent()[3].ID || rows[3].RefusalCode != appwork.CodePermitClosed ||
		len(settled) != 1 || settled[0].ID != stand.journal.sent()[4].ID || settled[0].Outcome != modeljournal.OutcomeCompleted {
		t.Fatalf("rows %+v, settled %+v", rows, settled)
	}
	stand.gateway.UseReading(nil)

	stand.journal.refuse.Store(true)
	account.answers = []siteAnswer{{text: "Ответ"}}
	if response, body := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"}); response.StatusCode != http.StatusBadRequest || !strings.Contains(body, disclosure.CodeUnavailable) {
		t.Fatalf("a turn whose record did not lie = %d %s", response.StatusCode, body)
	}
	if asked, _, _ := account.heard(); len(asked) != 5 {
		t.Fatalf("the site heard %d passings", len(asked))
	}
	account.mu.Lock()
	refused := append([]appwork.Answer(nil), account.refused...)
	account.mu.Unlock()
	if len(refused) == 0 || refused[len(refused)-1].Reason != appwork.ReasonJournal || refused[len(refused)-1].Status != appwork.Lasting {
		t.Fatalf("the reason of the turn = %+v", refused)
	}
}

// JR1.1, JR1.6 (the second check, M6, M10): a step of the reading through the account whose record did not lie goes nowhere —
// the site is not asked —, its reserve goes without a row or a count, and the reading does not pause.
func TestAStepOfTheAccountWithoutItsRecordGoesNowhere(t *testing.T) {
	stand, account := accountStand(t)
	reading := &readingOfTheTest{}
	stand.gateway.UseReading(reading)
	stand.journal.refuse.Store(true)
	response, body := stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-cards"})
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, disclosure.CodeUnavailable) {
		t.Fatalf("the step = %d %s", response.StatusCode, body)
	}
	asked, _, _ := account.heard()
	reading.mu.Lock()
	defer reading.mu.Unlock()
	if len(asked) != 0 || reading.released != 1 || len(reading.settled) != 0 || len(reading.stops) != 0 || len(reading.failures) != 0 || len(stand.journal.all()) != 0 {
		t.Fatalf("the site %v, released %d, settled %d, stops %v, failures %v, rows %d", asked, reading.released, len(reading.settled), reading.stops, reading.failures, len(stand.journal.all()))
	}
}

// JR1.1, JR1.7 (the second check, M3, M15): «Определить» before a request of the reading whose record did not lie asks the
// provider nothing — journal.unavailable —, the reserve of the check goes, and nothing is heard as a refusal of the provider.
func TestAProbeOfTheProtocolWithoutItsRecordGoesNowhere(t *testing.T) {
	stand, journal := journalStand(t)
	stub := &provider{}
	stand.settings.set(t, custom(startProvider(t, stub), "auto"), "key-test-journal-0007")
	reading := &readingOfTheTest{}
	stand.gateway.UseReading(reading)
	journal.refuse.Store(true)
	response, body := stand.post(t, coreBody(t, coresteps.Card), nil)
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, disclosure.CodeUnavailable) {
		t.Fatalf("the request = %d %s", response.StatusCode, body)
	}
	reading.mu.Lock()
	defer reading.mu.Unlock()
	if len(stub.requests()) != 0 || reading.released != 1 || len(reading.failures) != 0 || len(reading.settled) != 0 || len(stand.journal.all()) != 0 {
		t.Fatalf("the provider %d, released %d, failures %v, settled %d, rows %d", len(stub.requests()), reading.released, reading.failures, len(reading.settled), len(stand.journal.all()))
	}
}

// JR1.6 (the second check, M12): a turn of «Свой ИИ» whose request the journal refused fails with its own reason — the port of
// the reason of a turn hears journal, which Codex does not repeat.
func TestATurnOfTheOwnAIFailsWithTheReasonOfTheJournal(t *testing.T) {
	stand, journal := journalStand(t)
	stand.settings.set(t, custom(startProvider(t, &provider{}), "responses"), "key-test-journal-0008")
	account := newAccountOfTheTest() // the port of the reason of a turn; the Job of the test is «Свой ИИ»'s
	stand.gateway.UseAccount(account)
	journal.refuse.Store(true)
	if response, body := stand.post(t, chatBody(""), nil); response.StatusCode != http.StatusBadRequest || !strings.Contains(body, disclosure.CodeUnavailable) {
		t.Fatalf("the turn = %d %s", response.StatusCode, body)
	}
	account.mu.Lock()
	defer account.mu.Unlock()
	if len(account.refused) != 1 || account.refused[0] != (appwork.Answer{Status: appwork.Lasting, Reason: appwork.ReasonJournal}) || len(account.asked) != 0 {
		t.Fatalf("the reason heard = %+v, the site %v", account.refused, account.asked)
	}
}

// JR1.1 (the second check, finding 3): a permit of the reading the site did not open goes between the check and the sending —
// nothing went: no record, no row, no count; the reserve goes, and a stop of the site still pauses the reading.
func TestAPermitThatDidNotOpenWritesNoRow(t *testing.T) {
	for _, failure := range []error{
		&appwork.Refusal{Status: http.StatusTooManyRequests, Code: appwork.CodeAccountCharacters},
		accountdomain.ErrUnreachable,
	} {
		stand, account := accountStand(t)
		stand.gateway.UseAccount(refusingPermit{accountOfTheTest: account, err: failure})
		reading := &readingOfTheTest{}
		stand.gateway.UseReading(reading)
		stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-of-the-cards"})
		asked, _, _ := account.heard()
		reading.mu.Lock()
		if len(asked) != 0 || len(stand.journal.sent()) != 0 || len(stand.journal.all()) != 0 || len(reading.settled) != 0 || reading.released != 1 {
			t.Errorf("%v: the site %v, records %d, rows %d, settled %d, released %d", failure, asked, len(stand.journal.sent()), len(stand.journal.all()), len(reading.settled), reading.released)
		}
		if refusal, ok := failure.(*appwork.Refusal); ok && (len(reading.stops) != 1 || reading.stops[0] != stopOfAccount(*refusal)) {
			t.Errorf("the stop of the site = %v", reading.stops)
		}
		reading.mu.Unlock()
	}
}

// refusingPermit is an account whose permit of the reading does not open.
type refusingPermit struct {
	*accountOfTheTest
	err error
}

func (account refusingPermit) Reading(context.Context, string, bool) (string, error) {
	return "", account.err
}

// JR2.4 (the second check, finding 4): a record lay and the body did not leave the app — the client of the site holds no pass
// or waits out the site's Retry-After: the row says «не отправлено», not a refusal or no answer.
func TestABodyThatDidNotLeaveIsUnsent(t *testing.T) {
	for _, failure := range []error{
		errors.Join(accountdomain.ErrUnreachable, accountdomain.ErrNotSent),
		errors.Join(accountdomain.ErrNotHeld, accountdomain.ErrNotSent),
	} {
		stand, account := accountStand(t)
		stand.gateway.UseAccount(unsentSite{accountOfTheTest: account, err: failure})
		stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"})
		rows := stand.journal.all()
		if len(stand.journal.sent()) != 1 || len(rows) != 1 || rows[0].Failure != modeljournal.FailureUnsent || rows[0].RefusalCode != "" ||
			modeljournal.OutcomeWords(rows[0].Outcome, rows[0].Failure, true) != "не отправлено" {
			t.Errorf("%v: rows %+v", failure, rows)
		}
	}
}

// unsentSite is an account whose client of the site sends nothing.
type unsentSite struct {
	*accountOfTheTest
	err error
}

func (account unsentSite) Responses(context.Context, string, []byte) (io.ReadCloser, error) {
	return nil, account.err
}

// JR1.1, JR2.4 (the second check, findings 4 and 8): the size of a sending is the body that goes to the provider — the body of
// Chat Completions after the translation, the body of Responses as it goes —; a request the module refuses before its body
// went has no record and no row.
func TestTheSizeIsTheBodyThatGoesToTheProvider(t *testing.T) {
	for _, chat := range []bool{false, true} {
		stand, journal := journalStand(t)
		stub := &provider{chat: chat}
		protocol := map[bool]string{false: "responses", true: "chat"}[chat]
		stand.settings.set(t, custom(startProvider(t, stub), protocol), "key-test-journal-0009")
		if response, _ := stand.post(t, chatBody(""), nil); response.StatusCode != http.StatusOK {
			t.Fatalf("%s: the turn = %d", protocol, response.StatusCode)
		}
		got, sent := stub.requests(), journal.sent()
		if len(got) != 1 || len(sent) != 1 || sent[0].Bytes != int64(len(got[0].raw)) {
			t.Fatalf("%s: the record says %d bytes, the provider got %d", protocol, sent[0].Bytes, len(got[0].raw))
		}
		refused := strings.Replace(chatBody(""), `"stream":true`, `"stream":true,"tools":[{"type":"web_search"}]`, 1)
		response, body := stand.post(t, refused, nil)
		if response.StatusCode == http.StatusOK || !strings.Contains(body, translate.ErrorCodeUnsupportedCapability) || len(stub.requests()) != 1 ||
			len(journal.sent()) != 1 || len(stand.journal.all()) != 1 {
			t.Fatalf("%s: a request the module refused = %d %s, the provider %d, records %d, rows %d", protocol, response.StatusCode, body,
				len(stub.requests()), len(journal.sent()), len(stand.journal.all()))
		}
	}
}

// JR1.7: one check of «Проверить связь» is one number — its five probes and «Определить» inside it carry it, six records of
// one number; «Определить» inside a work has a number of its own.
func TestTheProbesOfOneCheckAreOneNumber(t *testing.T) {
	stand, journal := journalStand(t)
	stand.settings.set(t, custom(startProvider(t, &provider{}), "auto"), "key-test-journal-0006")
	const check = "95000000-0000-4000-8000-0000000000c3"
	for _, probe := range []aisettings.Probe{aisettings.ProbeShort, aisettings.ProbeTool, aisettings.ProbeShort, aisettings.ProbeCalibrate, aisettings.ProbeShort} {
		if _, err := stand.gateway.Probe(context.Background(), check, aisettings.Step{Probe: probe, Role: aisettings.RoleChat}); err != nil {
			t.Fatal(err)
		}
	}
	sent := journal.sent()
	if len(sent) != 6 || !sent[0].Named.Protocol {
		t.Fatalf("records = %+v", sent)
	}
	for _, record := range sent {
		if record.CheckID != check || record.Purpose != modeljournal.PurposeProbe || record.JobID != "" || !record.Named.Empty() {
			t.Fatalf("a record of the check = %+v", record)
		}
	}
	for _, row := range stand.journal.all() {
		if row.CheckID != check {
			t.Fatalf("a row of the check = %+v", row)
		}
	}
}

// JR1.1 (the second check, finding 7): a request the client canceled while it waited for a place among «Запросов одновременно»
// went nowhere and leaves no record.
func TestARequestCanceledInTheQueueLeavesNoRecord(t *testing.T) {
	stand, journal := journalStand(t)
	stub := &provider{hold: 400 * time.Millisecond}
	draft := custom(startProvider(t, stub), "responses")
	one := 1
	draft.Advanced.Parallel = &one
	stand.settings.set(t, draft, "key-test-journal-0010")
	done := make(chan struct{})
	go func() {
		defer close(done)
		stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-first"})
	}()
	for deadline := time.Now().Add(2 * time.Second); len(stub.requests()) == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the first request did not reach the provider")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, stand.gateway.BaseURL()+"/responses", strings.NewReader(coreBody(t, coresteps.Card)))
	if err != nil {
		t.Fatal(err)
	}
	named, _ := defaultNamed.Encode()
	request.Header.Set("Authorization", "Bearer "+stand.gateway.Token())
	request.Header.Set(disclosure.Header, named)
	request.Header.Set(modelapi.JobHeader, "job-second")
	if response, err := http.DefaultClient.Do(request); err == nil {
		response.Body.Close()
		t.Fatalf("the canceled request was answered %d", response.StatusCode)
	}
	<-done
	time.Sleep(50 * time.Millisecond) // the handler of the canceled request ends after its client left
	if sent := journal.sent(); len(sent) != 1 || sent[0].JobID != "job-first" || len(stub.requests()) != 1 {
		t.Fatalf("records %+v, the provider got %d", sent, len(stub.requests()))
	}
}

// halfReading lets the first two requests of the reading go and pauses the rest at the sum.
type halfReading struct {
	readingOfTheTest
	admitted atomic.Int64
}

func (reading *halfReading) Admit(ctx context.Context, request ReadingRequest) (Ticket, string, error) {
	if reading.admitted.Add(1) > 2 {
		return nil, "sum", nil
	}
	return reading.readingOfTheTest.Admit(ctx, request)
}

// JR1.1 (the second check, finding 7): four chapters at once and a pause of the reading at the sum after two — the paused ones
// go nowhere, only the two that went have their records and rows.
func TestAPauseAmongFourChaptersSendsNoMore(t *testing.T) {
	stand, journal := journalStand(t)
	stub := &provider{hold: 100 * time.Millisecond}
	draft := custom(startProvider(t, stub), "responses")
	four := 4
	draft.Advanced.Parallel = &four
	stand.settings.set(t, draft, "key-test-journal-0011")
	reading := &halfReading{}
	stand.gateway.UseReading(reading)
	var wait sync.WaitGroup
	statuses := make(chan int, 4)
	for chapter := range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, _ := stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-of-the-cards-" + string(rune('a'+chapter))})
			statuses <- response.StatusCode
		}()
	}
	wait.Wait()
	close(statuses)
	paused := 0
	for status := range statuses {
		if status == http.StatusConflict {
			paused++
		}
	}
	reading.mu.Lock()
	settled := len(reading.settled)
	reading.mu.Unlock()
	if paused != 2 || len(stub.requests()) != 2 || len(journal.sent()) != 2 || settled != 2 {
		t.Fatalf("paused %d, the provider got %d, records %d, settled %d", paused, len(stub.requests()), len(journal.sent()), settled)
	}
}
