// SPDX-License-Identifier: AGPL-3.0-only

package appwork

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	domain "canonkeeper/backend/internal/domain/appwork"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

// siteOfTheTest is the site of the account as the count sees it: one permit by ref — a known ref answers it as it is now,
// spending nothing —, the units it spent and returned, and what it answers next.
type siteOfTheTest struct {
	mu      sync.Mutex
	permits map[string]*domain.Permit
	units   int
	calls   []string
	// device is the device of the link now: the site knows the refs of a device only.
	device string
	// fail answers the next calls in turn; nil — the site answers.
	fail []error
	// opened is called once a permit is opened, before the answer goes back.
	opened func()
}

// markOfTheTest is the mark of the first link of the site of the test.
const markOfTheTest = "mark-of-device-1"

func newSite() *siteOfTheTest {
	return &siteOfTheTest{permits: map[string]*domain.Permit{}, device: "device-1"}
}

func (site *siteOfTheTest) LinkMark([]byte) string {
	site.mu.Lock()
	defer site.mu.Unlock()
	return "mark-of-" + site.device
}

// relink is a new link of the app: the site closed the live permits of the old device as canceled, without a return, and
// knows no ref of the new one.
func (site *siteOfTheTest) relink(device string) {
	site.mu.Lock()
	defer site.mu.Unlock()
	for key, permit := range site.permits {
		if strings.HasPrefix(key, site.device+":") && permit.State == domain.StateOpen {
			permit.State, permit.Outcome = domain.StateClosed, domain.OutcomeCanceled
		}
	}
	site.device = device
}

func (site *siteOfTheTest) next(call string) error {
	site.mu.Lock()
	defer site.mu.Unlock()
	site.calls = append(site.calls, call)
	if len(site.fail) == 0 {
		return nil
	}
	err := site.fail[0]
	site.fail = site.fail[1:]
	return err
}

func (site *siteOfTheTest) OpenPermit(_ context.Context, kind domain.Kind, ref string) (domain.Permit, error) {
	site.mu.Lock()
	key := site.device + ":" + ref
	permit, known := site.permits[key]
	if !known {
		permit = &domain.Permit{ID: "permit-" + site.device + "-" + ref[:8], Kind: kind, State: domain.StateOpen}
		site.permits[key] = permit
		if kind != domain.KindReading {
			site.units++
		}
	}
	answer := *permit
	site.mu.Unlock()
	err := site.next("open " + ref[:8])
	if err != nil && !errors.Is(err, errLostAnswer) {
		if !known {
			site.mu.Lock()
			delete(site.permits, key)
			if kind != domain.KindReading {
				site.units--
			}
			site.mu.Unlock()
		}
		return domain.Permit{}, err
	}
	if errors.Is(err, errLostAnswer) {
		return domain.Permit{}, accountdomain.ErrUnreachable
	}
	return answer, nil
}

// errLostAnswer is an open the site made whose answer was lost.
var errLostAnswer = errors.New("the answer was lost")

func (site *siteOfTheTest) ChargeUnit(_ context.Context, permitID, ref string) error {
	if err := site.next("charge " + ref[:8]); err != nil {
		return err
	}
	site.mu.Lock()
	defer site.mu.Unlock()
	if site.permits["steer:"+ref] == nil {
		site.permits["steer:"+ref] = &domain.Permit{ID: permitID}
		site.units++
	}
	return nil
}

func (site *siteOfTheTest) ReturnUnit(_ context.Context, _, ref string) (bool, error) {
	if err := site.next("return " + ref[:8]); err != nil {
		return false, err
	}
	site.mu.Lock()
	defer site.mu.Unlock()
	if site.permits["steer:"+ref] != nil {
		delete(site.permits, "steer:"+ref)
		site.units--
	}
	return true, nil
}

func (site *siteOfTheTest) ClosePermit(_ context.Context, permitID string, outcome domain.Outcome) (domain.Permit, error) {
	if err := site.next("close " + string(outcome)); err != nil {
		return domain.Permit{}, err
	}
	site.mu.Lock()
	defer site.mu.Unlock()
	for _, permit := range site.permits {
		if permit.ID == permitID && permit.State == domain.StateOpen {
			permit.State, permit.Outcome = domain.StateClosed, outcome
			permit.Returned = outcome == domain.OutcomeFailed
			if permit.Returned {
				site.units--
			}
		}
		if permit.ID == permitID {
			return *permit, nil
		}
	}
	return domain.Permit{}, &domain.Refusal{Status: 404, Code: domain.CodePermitNotFound}
}

type rowsOfTheTest struct {
	mu   sync.Mutex
	rows map[string]*Row
	// over are the Jobs that ended: their rows stay.
	over map[string]bool
}

// Answering counts the turns of the account of the link whose Job is not over, as the store does.
func (rows *rowsOfTheTest) Answering(_ context.Context, link string) (int64, error) {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	var running int64
	for jobID, row := range rows.rows {
		if row.Kind == domain.KindTurn && row.Source == domain.SourceAccount && row.Link == link && !rows.over[jobID] {
			running++
		}
	}
	return running, nil
}

func (rows *rowsOfTheTest) Work(_ context.Context, jobID string) (Row, bool, error) {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	row, ok := rows.rows[jobID]
	if !ok {
		return Row{}, false, nil
	}
	return *row, true, nil
}

func (rows *rowsOfTheTest) KeepNumber(_ context.Context, jobID string, number int) error {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	rows.rows[jobID].Number = number
	return nil
}

func (rows *rowsOfTheTest) KeepReturned(_ context.Context, jobID string, returned domain.Returned) error {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	rows.rows[jobID].Returned = returned
	return nil
}

func (rows *rowsOfTheTest) Pending(context.Context) ([]string, error) {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	var pending []string
	for jobID, row := range rows.rows {
		if row.Returned == domain.ReturnedPending {
			pending = append(pending, jobID)
		}
	}
	return pending, nil
}

func workOfTheTest(site *siteOfTheTest, rows *rowsOfTheTest) *Work {
	work := New(site, rows, []byte("salt-of-the-test"), nil, nil)
	work.Now = func() time.Time { return time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC) }
	return work
}

func turnOf(jobID string) quota.Work {
	return quota.Work{Kind: quota.WorkTurn, JobID: jobID, UserID: "author", ChatID: "chat-north"}
}

// specs/2026-11-13-desk-account-ai.md AI2.1, AI2.2: an open the site made whose answer was lost is asked once more with the
// same ref — one permit, one unit —; no answer twice is a counter that did not answer, whose unit the site may give back;
// a refusal of the tier is the refusal of the site; an unlinked app refuses at once.
func TestAnOpenIsAskedOnceMoreWithTheSameRef(t *testing.T) {
	site := newSite()
	work := workOfTheTest(site, &rowsOfTheTest{rows: map[string]*Row{}})
	site.fail = []error{errLostAnswer}
	if err := work.Open(context.Background(), turnOf("job-north")); err != nil {
		t.Fatalf("an open whose answer was lost = %v", err)
	}
	if site.units != 1 || len(site.permits) != 1 || len(site.calls) != 2 || site.calls[0] != site.calls[1] {
		t.Fatalf("units %d, permits %d, calls %v", site.units, len(site.permits), site.calls)
	}
	if ticket, ok := work.TicketOf("job-north"); !ok || ticket.Source != domain.SourceAccount || ticket.PermitID == "" {
		t.Fatalf("the ticket = %+v, %v", ticket, ok)
	}
	site.fail = []error{accountdomain.ErrUnreachable, accountdomain.ErrUnreachable}
	if unreachable, ok := quota.AsCounterUnreachable(work.Open(context.Background(), turnOf("job-south"))); !ok || !unreachable.MayReturn {
		t.Fatal("two opens without an answer are not a counter that did not answer")
	}
	site.fail = []error{&domain.Refusal{Status: 429, Code: "quota.chat_turns_daily", RetryAfter: time.Hour}}
	if exceeded, ok := quota.AsExceeded(work.Open(context.Background(), turnOf("job-east"))); !ok || exceeded.Key != quota.KeyChatTurnsPerDay || exceeded.ResetsAt == nil {
		t.Fatal("a refusal of the tier is not the refusal of the site")
	}
	site.fail = []error{&domain.Refusal{Status: 429, Code: "quota.active_turns"}}
	if exceeded, ok := quota.AsExceeded(work.Open(context.Background(), turnOf("job-east"))); !ok || exceeded.Key != quota.KeyActiveTurns {
		t.Fatal("«разом» is not the refusal of the site")
	}
	site.fail = []error{accountdomain.ErrPassInvalid}
	if err := work.Open(context.Background(), turnOf("job-west")); !errors.Is(err, quota.ErrCounterUnlinked) {
		t.Fatalf("an unlinked app = %v", err)
	}
}

// AI2.3: a turn taken again asks its permit by the same ref; one that went by unused while the turn waited — its unit came
// back — takes the next number through the fence of the site; one that ended after requests ends the turn.
func TestATurnTakenAgainAsksItsPermitByTheSameRef(t *testing.T) {
	site := newSite()
	rows := &rowsOfTheTest{rows: map[string]*Row{"job-north": {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest}}}
	first := workOfTheTest(site, rows)
	if err := first.Open(context.Background(), turnOf("job-north")); err != nil {
		t.Fatal(err)
	}
	// The process stopped hard: a new one knows nothing but the row.
	again := workOfTheTest(site, rows)
	run := researchdomain.TurnRun{JobID: "job-north", ChatID: "chat-north"}
	if code, err := again.Begin(context.Background(), run); code != "" || err != nil || site.units != 1 || len(site.permits) != 1 {
		t.Fatalf("a turn taken again: %q %v, %d units, %d permits", code, err, site.units, len(site.permits))
	}
	// The permit went by unused while the turn waited longer than its lease: the next number, one unit more, the first gave
	// its unit back.
	for _, permit := range site.permits {
		permit.State, permit.Outcome = domain.StateExpired, domain.OutcomeExpiredUnused
		site.units--
	}
	waited := workOfTheTest(site, rows)
	if code, err := waited.Begin(context.Background(), run); code != "" || err != nil || rows.rows["job-north"].Number != 1 || site.units != 1 {
		t.Fatalf("a turn that waited: %q %v, number %d, %d units", code, err, rows.rows["job-north"].Number, site.units)
	}
	for _, permit := range site.permits {
		if permit.State == domain.StateOpen {
			permit.State, permit.Outcome = domain.StateExpired, domain.OutcomeFailed
		}
	}
	if code, _ := workOfTheTest(site, rows).Begin(context.Background(), run); code != researchdomain.CodePermitEnded {
		t.Fatalf("a permit that ended after requests = %q", code)
	}
	// A turn of «Свой ИИ» asks nothing.
	rows.rows["job-own"] = &Row{Kind: domain.KindTurn, Source: domain.SourceOwn}
	calls := len(site.calls)
	if code, _ := waited.Begin(context.Background(), researchdomain.TurnRun{JobID: "job-own"}); code != "" || len(site.calls) != calls {
		t.Fatalf("a turn of «Свой ИИ» asked the site: %q", code)
	}
	if ticket, ok := waited.TicketOf("job-own"); !ok || ticket.Source != domain.SourceOwn {
		t.Fatalf("the ticket of «Свой ИИ» = %+v", ticket)
	}
}

// AI2.3: a turn that waited in the queue of the same process longer than the lease of its permit asks the site by the same
// ref when a worker takes it — not the state its start saw —: the permit went by unused, its unit came back, and the next
// number opens a new one; a permit that lives is asked too and goes on, spending nothing.
func TestATurnThatWaitedInTheSameProcessAsksItsPermitAgain(t *testing.T) {
	site := newSite()
	rows := &rowsOfTheTest{rows: map[string]*Row{"job-north": {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest}}}
	work := workOfTheTest(site, rows)
	if err := work.Open(context.Background(), turnOf("job-north")); err != nil {
		t.Fatal(err)
	}
	run := researchdomain.TurnRun{JobID: "job-north", ChatID: "chat-north"}
	calls := len(site.calls)
	if code, err := work.Begin(context.Background(), run); code != "" || err != nil || len(site.calls) != calls+1 || site.units != 1 {
		t.Fatalf("a turn taken at once: %q %v, calls %v, %d units", code, err, site.calls[calls:], site.units)
	}
	// Two turns ran longer than the lease: the permit of the third went by unused at the site while it waited.
	rows.rows["job-south"] = &Row{Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest}
	if err := work.Open(context.Background(), turnOf("job-south")); err != nil {
		t.Fatal(err)
	}
	opened, _ := work.TicketOf("job-south")
	for _, permit := range site.permits {
		if permit.ID == opened.PermitID {
			permit.State, permit.Outcome = domain.StateExpired, domain.OutcomeExpiredUnused
			site.units--
		}
	}
	code, err := work.Begin(context.Background(), researchdomain.TurnRun{JobID: "job-south", ChatID: "chat-north"})
	taken, _ := work.TicketOf("job-south")
	if code != "" || err != nil || rows.rows["job-south"].Number != 1 || site.units != 2 || taken.PermitID == opened.PermitID || taken.PermitID == "" {
		t.Fatalf("a turn that waited longer than the lease: %q %v, number %d, %d units, permit renewed %v",
			code, err, rows.rows["job-south"].Number, site.units, taken.PermitID != opened.PermitID)
	}
}

// AI2.7, AI2.9: the end of a turn closes its permit; a failed turn whose requests all came is returned — yes —, an answered
// one is not; a dropped request leaves it pending; a close that did not reach the site is pending and goes again before the
// next open; the stop of the app closes the turns as failed before they are canceled.
func TestTheEndOfATurnClosesItsPermitAndSaysWhetherItCameBack(t *testing.T) {
	site := newSite()
	rows := &rowsOfTheTest{rows: map[string]*Row{}}
	work := workOfTheTest(site, rows)
	for job, end := range map[string]researchdomain.TurnEnd{"job-1": researchdomain.TurnFailed, "job-2": researchdomain.TurnAnswered, "job-3": researchdomain.TurnFailed} {
		rows.rows[job] = &Row{Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest}
		if err := work.Open(context.Background(), turnOf(job)); err != nil {
			t.Fatal(err)
		}
		if job == "job-3" {
			work.Dropped(job)
		}
		work.End(context.Background(), job, end)
	}
	if rows.rows["job-1"].Returned != domain.ReturnedYes || rows.rows["job-2"].Returned != domain.ReturnedNo || rows.rows["job-3"].Returned != domain.ReturnedPending {
		t.Fatalf("returned: %s %s %s", rows.rows["job-1"].Returned, rows.rows["job-2"].Returned, rows.rows["job-3"].Returned)
	}
	rows.rows["job-4"] = &Row{Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest}
	if err := work.Open(context.Background(), turnOf("job-4")); err != nil {
		t.Fatal(err)
	}
	site.fail = []error{accountdomain.ErrUnreachable}
	work.End(context.Background(), "job-4", researchdomain.TurnCanceled)
	if rows.rows["job-4"].Returned != domain.ReturnedPending {
		t.Fatalf("a close that did not reach the site = %s", rows.rows["job-4"].Returned)
	}
	calls := len(site.calls)
	if err := work.Open(context.Background(), turnOf("job-5")); err != nil {
		t.Fatal(err)
	}
	if site.calls[calls] != "close canceled" || rows.rows["job-4"].Returned != domain.ReturnedNo {
		t.Fatalf("the next open did not close first: %v, %s", site.calls[calls:], rows.rows["job-4"].Returned)
	}
	rows.rows["job-5"] = &Row{Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest}
	// A turn no worker took asked no model: the stop leaves it to the lease of the site.
	rows.rows["job-6"] = &Row{Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest}
	if err := work.Open(context.Background(), turnOf("job-6")); err != nil {
		t.Fatal(err)
	}
	if code, err := work.Begin(context.Background(), researchdomain.TurnRun{JobID: "job-5", ChatID: "chat-north"}); code != "" || err != nil {
		t.Fatalf("the turn taken = %q %v", code, err)
	}
	stopped := len(site.calls)
	stop := work.Stopping(context.Background())
	work.StopRunning(stop)
	work.End(stop, "job-5", researchdomain.TurnCanceled)
	work.End(stop, "job-6", researchdomain.TurnCanceled)
	// The cancel after the close drops the streams of the turn that ran: its question is pending until the site's word.
	if calls := strings.Join(site.calls[stopped:], ","); calls != "close failed" ||
		rows.rows["job-5"].Returned != domain.ReturnedPending || rows.rows["job-6"].Returned != domain.ReturnedPending {
		t.Fatalf("the stop of the app: %v, %s %s", calls, rows.rows["job-5"].Returned, rows.rows["job-6"].Returned)
	}
}

// AI2.5, AI2.6: the reason of a failed turn is the refusal of its last request, cleared by a request the site took; the fence
// of the day is remembered and a new turn is refused at once, before any permit.
func TestTheReasonOfATurnAndTheRefusalsThatRepeat(t *testing.T) {
	site := newSite()
	work := workOfTheTest(site, &rowsOfTheTest{rows: map[string]*Row{}})
	if err := work.Open(context.Background(), turnOf("job-north")); err != nil {
		t.Fatal(err)
	}
	work.Refused("job-north", "chat", domain.AnswerOf(domain.Refusal{Status: 429, Code: domain.CodeBusy}))
	if work.Failure("job-north") != "" {
		t.Fatal("ai.busy is a reason")
	}
	work.Refused("job-north", "chat", domain.AnswerOf(domain.Refusal{Status: 502, Code: domain.CodeProviderFailed}))
	work.Accepted("job-north")
	if work.Failure("job-north") != "" {
		t.Fatal("a refusal before a request the site took stays the reason")
	}
	work.Refused("job-north", "chat", domain.AnswerOf(domain.Refusal{Status: 403, Code: domain.CodeDayFence}))
	if work.Failure("job-north") != researchdomain.CodeDayFence {
		t.Fatalf("the reason = %q", work.Failure("job-north"))
	}
	calls := len(site.calls)
	if refusal, ok := quota.AsCounterRefusal(work.Open(context.Background(), turnOf("job-south"))); !ok || refusal.Code != domain.CodeDayFence || len(site.calls) != calls {
		t.Fatal("the fence of the day is not refused at once")
	}
}

// AI2.8: a correction is charged once by the key of its repeat — a repeat after a lost answer spends nothing more —, and
// one the turn did not take is returned.
func TestACorrectionIsChargedOnceByTheKeyOfItsRepeat(t *testing.T) {
	site := newSite()
	work := workOfTheTest(site, &rowsOfTheTest{rows: map[string]*Row{}})
	if err := work.Open(context.Background(), turnOf("job-north")); err != nil {
		t.Fatal(err)
	}
	steer := quota.Steer{JobID: "job-north", Key: "key-of-the-repeat-1"}
	site.fail = []error{accountdomain.ErrUnreachable}
	if err := work.Steer(context.Background(), steer); err != nil {
		t.Fatal(err)
	}
	if err := work.Steer(context.Background(), steer); err != nil || site.units != 2 {
		t.Fatalf("a repeat of a correction: %v, %d units", err, site.units)
	}
	if err := work.Unsteer(context.Background(), steer); err != nil || site.units != 1 {
		t.Fatalf("a correction not taken: %v, %d units", err, site.units)
	}
}

// AI3.2: the permit of a reading is opened at the first step of its Job, the first live number from 0 after a restart;
// one the site ended mid-Job is replaced by the next number; the end of the Job closes it as done.
func TestThePermitOfAReadingIsOpenedAtItsFirstStep(t *testing.T) {
	site := newSite()
	work := workOfTheTest(site, &rowsOfTheTest{rows: map[string]*Row{}})
	first, err := work.Reading(context.Background(), "job-cards", false)
	if err != nil || first == "" || site.units != 0 {
		t.Fatalf("the first step: %q %v, %d units", first, err, site.units)
	}
	if again, _ := work.Reading(context.Background(), "job-cards", false); again != first {
		t.Fatal("a second step opened another permit")
	}
	renewed, err := work.Reading(context.Background(), "job-cards", true)
	if err != nil || renewed == first {
		t.Fatalf("a permit the site ended was not replaced: %q %v", renewed, err)
	}
	// A restart: the permit of number 0 ended, the one of number 1 lives.
	for _, permit := range site.permits {
		if permit.ID == first {
			permit.State = domain.StateClosed
		}
	}
	restarted := workOfTheTest(site, &rowsOfTheTest{rows: map[string]*Row{}})
	if found, err := restarted.Reading(context.Background(), "job-cards", false); err != nil || found != renewed {
		t.Fatalf("after a restart: %q %v", found, err)
	}
	restarted.Done(context.Background(), "job-cards", domain.OutcomeDone)
	if site.calls[len(site.calls)-1] != "close done" {
		t.Fatalf("the end of the Job: %v", site.calls)
	}
	calls := len(site.calls)
	restarted.Done(context.Background(), "job-map", domain.OutcomeDone)
	if len(site.calls) != calls {
		t.Fatal("a Job without a step asked the site")
	}
}

// AI1.4, AI2.3, AI3.8: the gateway finds the permit of an upload of materials of the account by the ref of its start after a
// restart — the same permit, no unit —, never opens one for an upload of «Свой ИИ» or one whose permit ended, and reads the
// source of a Job the process does not hold from the row of its start.
func TestTheGatewayFindsTheWorkOfAJobWithoutOpeningAny(t *testing.T) {
	site := newSite()
	rows := &rowsOfTheTest{rows: map[string]*Row{
		"job-own": {Kind: domain.KindMaterials, Source: domain.SourceOwn},
	}}
	work := workOfTheTest(site, rows)
	if err := work.Open(context.Background(), quota.Work{Kind: quota.WorkMaterials, JobID: "job-upload", UserID: "author"}); err != nil {
		t.Fatal(err)
	}
	rows.rows["job-upload"] = &Row{Kind: domain.KindMaterials, Source: domain.SourceAccount, Link: markOfTheTest}
	units := site.units
	restarted := workOfTheTest(site, rows)
	if ticket, ok := restarted.WorkOf(context.Background(), "job-upload"); !ok || ticket.Source != domain.SourceAccount || ticket.PermitID != "" {
		t.Fatalf("the work of the upload = %+v %v", ticket, ok)
	}
	permit, err := restarted.Materials(context.Background(), "job-upload")
	if err != nil || permit == "" || site.units != units {
		t.Fatalf("the permit found = %q %v, units %d → %d", permit, err, units, site.units)
	}
	if permit, err := restarted.Materials(context.Background(), "job-own"); err != nil || permit != "" || site.units != units {
		t.Fatalf("an upload of «Свой ИИ» got a permit %q %v", permit, err)
	}
	if _, ok := restarted.WorkOf(context.Background(), "job-unknown"); ok {
		t.Fatal("a Job without a row has a work")
	}
	restarted.Done(context.Background(), "job-upload", domain.OutcomeDone)
	before := len(site.calls)
	if permit, err := restarted.Materials(context.Background(), "job-upload"); err != nil || permit != "" || site.units != units {
		t.Fatalf("an ended permit was found or opened again: %q %v", permit, err)
	}
	if len(site.calls) != before+1 {
		t.Fatalf("calls = %v", site.calls[before:])
	}
}

// AI2.6: a refusal of the site is remembered for the work of its kind — the scan of a turn is the turn, a step of the reading
// the reading —, so the fence and the gateway refuse the same work at once and let other work go.
func TestARefusalIsRememberedForTheWorkOfItsKind(t *testing.T) {
	site := newSite()
	work := workOfTheTest(site, &rowsOfTheTest{rows: map[string]*Row{}})
	work.Refused("job-cards", "card", domain.AnswerOf(domain.Refusal{Status: 400, Code: domain.CodeStepInstruction}))
	if work.RefusedNow("names", "") != domain.CodeAppUnfit || work.RefusedNow("chat", "chat-north") != "" {
		t.Fatal("an app the site does not take for the reading is not the reading's alone")
	}
	if err := work.Open(context.Background(), turnOf("job-north")); err != nil {
		t.Fatalf("a turn after a refused step = %v", err)
	}
	work.Refused("job-north", "scan", domain.AnswerOf(domain.Refusal{Status: 400, Code: domain.CodeValidation}))
	if work.RefusedNow("chat", "chat-north") != domain.CodeAppUnfit {
		t.Fatal("the scan of a turn is not the turn")
	}
	if refusal, ok := quota.AsCounterRefusal(work.Open(context.Background(), turnOf("job-south"))); !ok || refusal.Code != quota.CounterUnfitCode {
		t.Fatal("a new turn of an app the site does not take is not refused at once")
	}
	work.Refused("job-north", "chat", domain.AnswerOf(domain.Refusal{Status: 413, Code: domain.CodeTooLarge}))
	if work.RefusedNow("chat", "chat-north") == "" {
		t.Fatal("the thread too large is not remembered for its chat")
	}
}

// AI2.3, AI3.8 (Decision log: the gateway only finds a permit of an upload, never opens one): the site knows the refs of one
// device only. A turn or an upload of materials started under a link the author since replaced is never asked by its ref —
// that would open and pay a new permit —: the turn ends «связь с аккаунтом прервана» and its question is not returned (the
// unlinking closed the permit canceled), the upload has no permit, and the close of a permit of the old link asks nothing.
func TestAWorkOfAnotherLinkIsNeverAskedByItsRef(t *testing.T) {
	site := newSite()
	rows := &rowsOfTheTest{rows: map[string]*Row{
		"job-turn":   {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest},
		"job-upload": {Kind: domain.KindMaterials, Source: domain.SourceAccount, Link: markOfTheTest},
		"job-held":   {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest},
	}}
	work := workOfTheTest(site, rows)
	for _, opening := range []quota.Work{turnOf("job-turn"), turnOf("job-held"), {Kind: quota.WorkMaterials, JobID: "job-upload", UserID: "author"}} {
		if err := work.Open(context.Background(), opening); err != nil {
			t.Fatal(err)
		}
	}
	if code, err := work.Begin(context.Background(), researchdomain.TurnRun{JobID: "job-held", ChatID: "chat-north"}); code != "" || err != nil {
		t.Fatalf("the turn taken = %q %v", code, err)
	}
	site.relink("device-2")
	units, calls := site.units, len(site.calls)
	work.End(context.Background(), "job-held", researchdomain.TurnFailed)
	restarted := workOfTheTest(site, rows)
	if permit, err := restarted.Materials(context.Background(), "job-upload"); permit != "" || err != nil {
		t.Fatalf("an upload of the old link got a permit %q %v", permit, err)
	}
	if code, err := restarted.Begin(context.Background(), researchdomain.TurnRun{JobID: "job-turn", ChatID: "chat-north"}); code != researchdomain.CodeAccountUnlinked || err != nil {
		t.Fatalf("a turn of the old link = %q %v", code, err)
	}
	if site.units != units || len(site.calls) != calls {
		t.Fatalf("the work of the old link asked the site: %d → %d units, calls %v", units, site.units, site.calls[calls:])
	}
	if rows.rows["job-turn"].Returned != domain.ReturnedNo || rows.rows["job-held"].Returned != domain.ReturnedNo {
		t.Fatalf("returned: %s %s", rows.rows["job-turn"].Returned, rows.rows["job-held"].Returned)
	}
}

// AI2.8: a correction the turn did not take is returned under the permit it was charged under, even when the turn ended in
// between — the site returns it as well —, and a return that got no answer is asked once more.
func TestACorrectionNotTakenComesBackAfterItsTurnEnded(t *testing.T) {
	site := newSite()
	rows := &rowsOfTheTest{rows: map[string]*Row{"job-north": {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest}}}
	work := workOfTheTest(site, rows)
	if err := work.Open(context.Background(), turnOf("job-north")); err != nil {
		t.Fatal(err)
	}
	if code, err := work.Begin(context.Background(), researchdomain.TurnRun{JobID: "job-north", ChatID: "chat-north"}); code != "" || err != nil {
		t.Fatalf("the turn taken = %q %v", code, err)
	}
	steer := quota.Steer{JobID: "job-north", Key: "key-of-the-repeat-late"}
	if err := work.Steer(context.Background(), steer); err != nil || site.units != 2 {
		t.Fatalf("the correction: %v, %d units", err, site.units)
	}
	work.End(context.Background(), "job-north", researchdomain.TurnAnswered)
	site.fail = []error{accountdomain.ErrUnreachable}
	if err := work.Unsteer(context.Background(), steer); err != nil || site.units != 1 {
		t.Fatalf("a correction refused by a turn that ended: %v, %d units, calls %v", err, site.units, site.calls)
	}
	calls := len(site.calls)
	if err := work.Unsteer(context.Background(), steer); err != nil || len(site.calls) != calls {
		t.Fatalf("a correction returned twice: %v, calls %v", err, site.calls[calls:])
	}
}

// AI2.9: whether the question of a turn came back is told: a turn whose permit went by unused and whose next one the tier
// refused — yes, nothing was spent —; a turn the process does not hold — taken before a restart, canceled by the author
// while it waits — asks its permit by the ref of its row, closes it canceled and keeps what the site answered; a close that
// went again keeps the return the site answered when no request was dropped.
func TestWhetherAQuestionCameBackIsTold(t *testing.T) {
	site := newSite()
	rows := &rowsOfTheTest{rows: map[string]*Row{
		"job-tier":   {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest},
		"job-queued": {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest},
		"job-late":   {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest},
	}}
	work := workOfTheTest(site, rows)
	for _, job := range []string{"job-tier", "job-queued", "job-late"} {
		if err := work.Open(context.Background(), turnOf(job)); err != nil {
			t.Fatal(err)
		}
	}
	tier, _ := work.TicketOf("job-tier")
	for _, permit := range site.permits {
		if permit.ID == tier.PermitID {
			permit.State, permit.Outcome = domain.StateExpired, domain.OutcomeExpiredUnused
			site.units--
		}
	}
	site.fail = []error{nil, &domain.Refusal{Status: 429, Code: researchdomain.CodeQuestionsOver}}
	if code, _ := work.Begin(context.Background(), researchdomain.TurnRun{JobID: "job-tier", ChatID: "chat-north"}); code != researchdomain.CodeQuestionsOver {
		t.Fatalf("a turn the tier refused = %q", code)
	}
	work.End(context.Background(), "job-tier", researchdomain.TurnFailed)
	if rows.rows["job-tier"].Returned != domain.ReturnedYes || rows.rows["job-tier"].Number != 0 {
		t.Fatalf("a turn the tier refused: returned %s, number %d", rows.rows["job-tier"].Returned, rows.rows["job-tier"].Number)
	}
	// A restart: the queued turn is canceled by the author before any worker took it — closed canceled, the site keeps the unit.
	restarted := workOfTheTest(site, rows)
	restarted.End(context.Background(), "job-queued", researchdomain.TurnCanceled)
	if rows.rows["job-queued"].Returned != domain.ReturnedNo || site.calls[len(site.calls)-1] != "close canceled" {
		t.Fatalf("a turn of the run before = %s, calls %v", rows.rows["job-queued"].Returned, site.calls)
	}
	site.fail = []error{accountdomain.ErrUnreachable}
	work.End(context.Background(), "job-late", researchdomain.TurnFailed)
	if rows.rows["job-late"].Returned != domain.ReturnedPending {
		t.Fatalf("a close that did not reach the site = %s", rows.rows["job-late"].Returned)
	}
	rows.rows["job-next"] = &Row{Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest}
	if err := work.Open(context.Background(), turnOf("job-next")); err != nil {
		t.Fatal(err)
	}
	if rows.rows["job-late"].Returned != domain.ReturnedYes {
		t.Fatalf("a close that went again = %s", rows.rows["job-late"].Returned)
	}
}

// AI2.7: the closes that did not reach the site go again before the next open all at once, and one the site answered — even
// with a refusal — goes no more.
func TestACloseTheSiteAnsweredGoesNoMore(t *testing.T) {
	site := newSite()
	rows := &rowsOfTheTest{rows: map[string]*Row{}}
	work := workOfTheTest(site, rows)
	for _, job := range []string{"job-1", "job-2"} {
		rows.rows[job] = &Row{Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest}
		if err := work.Open(context.Background(), turnOf(job)); err != nil {
			t.Fatal(err)
		}
	}
	for _, job := range []string{"job-1", "job-2"} {
		site.fail = []error{accountdomain.ErrUnreachable}
		work.End(context.Background(), job, researchdomain.TurnFailed)
	}
	site.fail = []error{&domain.Refusal{Status: 404, Code: domain.CodePermitNotFound}, &domain.Refusal{Status: 404, Code: domain.CodePermitNotFound}}
	if err := work.Open(context.Background(), turnOf("job-3")); err != nil {
		t.Fatal(err)
	}
	calls := len(site.calls)
	if err := work.Open(context.Background(), turnOf("job-4")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(site.calls[calls:], ","), "close") {
		t.Fatalf("a close the site answered went again: %v", site.calls[calls:])
	}
	if rows.rows["job-1"].Returned != domain.ReturnedNo || rows.rows["job-2"].Returned != domain.ReturnedNo {
		t.Fatalf("returned after a refused close: %s %s", rows.rows["job-1"].Returned, rows.rows["job-2"].Returned)
	}
}

// AI2.1, AI2.3: a turn a worker takes asks its permit with one repeat by the same ref, as the open of its start: one lost
// answer of the site does not end a turn whose permit lives, and the repeat spends no second unit.
func TestATurnTakenAsksItsPermitOnceMoreWhenAnAnswerIsLost(t *testing.T) {
	site := newSite()
	rows := &rowsOfTheTest{rows: map[string]*Row{"job-north": {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest}}}
	work := workOfTheTest(site, rows)
	if err := work.Open(context.Background(), turnOf("job-north")); err != nil {
		t.Fatal(err)
	}
	site.fail = []error{errLostAnswer}
	code, err := work.Begin(context.Background(), researchdomain.TurnRun{JobID: "job-north", ChatID: "chat-north"})
	if code != "" || err != nil || site.units != 1 || len(site.permits) != 1 {
		t.Fatalf("a turn taken with one lost answer = %q %v, %d units, calls %v", code, err, site.units, site.calls)
	}
	if ticket, ok := work.TicketOf("job-north"); !ok || ticket.PermitID == "" {
		t.Fatalf("the ticket = %+v %v", ticket, ok)
	}
}

// AI2.9 (pending does not stay): a turn a kill left mid-stream is taken by the next run, which does not know a request of it
// was dropped — the site may read the dropped stream for a minute and take a returned unit back —, so its failure tells
// pending, not yes; once the lease of the site and that minute passed, the word of the site is asked by the ref of the row,
// once, and the question tells it. Nothing is asked before.
func TestAKilledTurnTellsPendingUntilTheWordOfTheSite(t *testing.T) {
	site := newSite()
	rows := &rowsOfTheTest{rows: map[string]*Row{"job-north": {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest}}}
	first := workOfTheTest(site, rows)
	if err := first.Open(context.Background(), turnOf("job-north")); err != nil {
		t.Fatal(err)
	}
	if code, err := first.Begin(context.Background(), researchdomain.TurnRun{JobID: "job-north", ChatID: "chat-north"}); code != "" || err != nil {
		t.Fatal(code, err)
	}
	first.Dropped("job-north") // the kill drops the stream of the request
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	again := workOfTheTest(site, rows)
	again.Now = func() time.Time { return now }
	again.Recall(context.Background())
	if code, err := again.Begin(context.Background(), researchdomain.TurnRun{JobID: "job-north", ChatID: "chat-north"}); code != "" || err != nil {
		t.Fatal(code, err)
	}
	again.End(context.Background(), "job-north", researchdomain.TurnFailed) // runtime.root_bind_failed
	if rows.rows["job-north"].Returned != domain.ReturnedPending {
		t.Fatalf("a turn the kill left tells %q", rows.rows["job-north"].Returned)
	}
	calls := len(site.calls)
	now = now.Add(SettleAfter - time.Second)
	again.Settle(context.Background())
	if len(site.calls) != calls {
		t.Fatalf("the site was asked before its word is final: %v", site.calls[calls:])
	}
	now = now.Add(time.Second)
	again.Settle(context.Background())
	if rows.rows["job-north"].Returned != domain.ReturnedYes || len(site.calls) != calls+1 {
		t.Fatalf("after the word of the site: %q, calls %v", rows.rows["job-north"].Returned, site.calls[calls:])
	}
	again.Settle(context.Background())
	if len(site.calls) != calls+1 {
		t.Fatal("a question settled was asked again")
	}
}

// specs/2026-11-15-desk-interrupted-turn.md IT1.2: a turn a crash left, whose session the next start revoked, ends before
// Begin — the end of its failure is the only call of the count. At any age of the turn no new permit is asked: its permit,
// gone by unused while the app was down or still open after requests, is asked by the ref of its row — spending nothing —
// and closed failed; its question is pending until the word of the site, asked once the lease and the minute of a dropped
// stream passed. A turn of «Свой ИИ» asks the site nothing.
func TestAnInterruptedTurnAsksNoNewPermitAtAnyAge(t *testing.T) {
	for name, age := range map[string]func(*domain.Permit){
		"started long after the kill": func(permit *domain.Permit) {
			permit.State, permit.Outcome, permit.Returned = domain.StateExpired, domain.OutcomeExpiredUnused, true
		},
		"started at once": func(*domain.Permit) {},
	} {
		t.Run(name, func(t *testing.T) {
			site := newSite()
			rows := &rowsOfTheTest{rows: map[string]*Row{
				"job-north": {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest},
				"job-own":   {Kind: domain.KindTurn, Source: domain.SourceOwn},
			}}
			if err := workOfTheTest(site, rows).Open(context.Background(), turnOf("job-north")); err != nil {
				t.Fatal(err)
			}
			for _, permit := range site.permits {
				age(permit)
			}
			units, permits, calls := site.units, len(site.permits), len(site.calls)
			now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
			again := workOfTheTest(site, rows)
			again.Now = func() time.Time { return now }
			again.Recall(context.Background())
			again.End(context.Background(), "job-north", researchdomain.TurnFailed) // model.session_ended, before Begin
			again.End(context.Background(), "job-own", researchdomain.TurnFailed)
			asked := site.calls[calls:]
			if len(site.permits) != permits || site.units > units || len(asked) != 2 || !strings.HasPrefix(asked[0], "open ") || asked[1] != "close failed" {
				t.Fatalf("an interrupted turn: %d permits (was %d), %d units (was %d), asked %v", len(site.permits), permits, site.units, units, asked)
			}
			if returned := rows.rows["job-north"].Returned; returned != domain.ReturnedPending {
				t.Fatalf("an interrupted turn tells %q", returned)
			}
			if returned := again.Returned(context.Background(), "job-own"); returned != domain.ReturnedNone {
				t.Fatalf("an interrupted turn of «Свой ИИ» tells %q", returned)
			}
			now = now.Add(SettleAfter)
			again.Settle(context.Background())
			if returned := rows.rows["job-north"].Returned; returned != domain.ReturnedYes || len(site.permits) != permits {
				t.Fatalf("after the word of the site: %q, %d permits", returned, len(site.permits))
			}
		})
	}
}

// AI2.9: a refusal of Begin by a lost answer tells pending — the site may have the permit open —; the lease of the site ends
// it unused, and the word asked after it tells yes. A question a run before left pending is recalled at the start and
// settled the same way; a refusal of the site to the ask keeps it pending.
func TestAPendingQuestionIsSettledByTheWordOfTheSite(t *testing.T) {
	site := newSite()
	rows := &rowsOfTheTest{rows: map[string]*Row{
		"job-lost":   {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest},
		"job-before": {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest},
	}}
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	work := workOfTheTest(site, rows)
	work.Now = func() time.Time { return now }
	for _, job := range []string{"job-lost", "job-before"} {
		if err := work.Open(context.Background(), turnOf(job)); err != nil {
			t.Fatal(err)
		}
	}
	site.fail = []error{accountdomain.ErrUnreachable, accountdomain.ErrUnreachable}
	if code, _ := work.Begin(context.Background(), researchdomain.TurnRun{JobID: "job-lost", ChatID: "chat-north"}); code != researchdomain.CodeSiteUnreachable ||
		rows.rows["job-lost"].Returned != domain.ReturnedPending {
		t.Fatalf("a refusal by a lost answer = %q, %q", code, rows.rows["job-lost"].Returned)
	}
	// The run before left job-before pending; this run recalls it at its start.
	rows.rows["job-before"].Returned = domain.ReturnedPending
	restarted := workOfTheTest(site, rows)
	restarted.Now = func() time.Time { return now }
	restarted.Recall(context.Background())
	for _, permit := range site.permits {
		permit.State, permit.Outcome, permit.Returned = domain.StateExpired, domain.OutcomeExpiredUnused, true
	}
	now = now.Add(SettleAfter)
	work.Settle(context.Background())
	site.fail = []error{&domain.Refusal{Status: 404, Code: domain.CodePermitNotFound}}
	restarted.Settle(context.Background())
	if rows.rows["job-lost"].Returned != domain.ReturnedYes || rows.rows["job-before"].Returned != domain.ReturnedPending {
		t.Fatalf("settled: lost %q, before %q", rows.rows["job-lost"].Returned, rows.rows["job-before"].Returned)
	}
}

// specs/2026-11-14-desk-journal.md JR1.6: a request of a turn the journal of the app did not send is the reason of the turn —
// of «Свой ИИ» and of the account alike — and the app remembers nothing of it.
func TestARefusalOfTheJournalIsTheReasonOfTheTurn(t *testing.T) {
	work := workOfTheTest(newSite(), &rowsOfTheTest{rows: map[string]*Row{}})
	work.Refused("job-own", "chat", domain.Answer{Status: domain.Lasting, Reason: domain.ReasonJournal})
	if work.Failure("job-own") != researchdomain.CodeJournalUnavailable || work.RefusedNow("chat", "") != "" {
		t.Fatalf("the reason = %q", work.Failure("job-own"))
	}
}

// linkOfTheTest counts how often the work made what the site told of the account stale.
type linkOfTheTest struct {
	mu      sync.Mutex
	refresh int
}

func (link *linkOfTheTest) Refresh() {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.refresh++
}

func (link *linkOfTheTest) refreshed() int {
	link.mu.Lock()
	defer link.mu.Unlock()
	return link.refresh
}

// specs/2026-11-15-desk-turns-here.md TH2.1: the turns of the account still answered here are those of the link now whose Job
// is not over, of any saga — a turn of «Свой ИИ», one of another link, one that ended are not —; without a link, none.
func TestTheTurnsAnsweredHereAreThoseOfTheLinkNow(t *testing.T) {
	site := newSite()
	rows := &rowsOfTheTest{rows: map[string]*Row{
		"job-north": {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest},
		"job-south": {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest},
		"job-ended": {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest},
		"job-own":   {Kind: domain.KindTurn, Source: domain.SourceOwn},
		"job-old":   {Kind: domain.KindTurn, Source: domain.SourceAccount, Link: "mark-of-device-0"},
		"job-books": {Kind: domain.KindReading, Source: domain.SourceAccount, Link: markOfTheTest},
	}, over: map[string]bool{"job-ended": true}}
	work := workOfTheTest(site, rows)
	if running, err := work.Answering(context.Background()); err != nil || running != 2 {
		t.Fatalf("the turns answered here = %d, %v; want 2", running, err)
	}
	site.relink("device-2")
	if running, err := work.Answering(context.Background()); err != nil || running != 0 {
		t.Fatalf("the turns answered here after a new link = %d, %v; want none", running, err)
	}
	unlinked := New(unlinkedSite{site}, rows, []byte("salt-of-the-test"), nil, nil)
	if running, err := unlinked.Answering(context.Background()); err != nil || running != 0 {
		t.Fatalf("the turns answered here without a link = %d, %v", running, err)
	}
}

// unlinkedSite is the site of an app that holds no pass: no mark of a link.
type unlinkedSite struct{ *siteOfTheTest }

func (unlinkedSite) LinkMark([]byte) string { return "" }

// TH2.3: «разом» refused by the site at the start of a question or when a worker takes a turn makes the next read of the
// account ask the site: the numbers the block of the refusal shows are not older than the refusal.
func TestARefusalAtOnceMakesWhatTheSiteToldStale(t *testing.T) {
	site := newSite()
	rows := &rowsOfTheTest{rows: map[string]*Row{}}
	link := &linkOfTheTest{}
	work := New(site, rows, []byte("salt-of-the-test"), link, nil)
	work.Now = func() time.Time { return time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC) }
	site.fail = []error{&domain.Refusal{Status: 429, Code: "quota.active_turns"}}
	if exceeded, ok := quota.AsExceeded(work.Open(context.Background(), turnOf("job-north"))); !ok || exceeded.Key != quota.KeyActiveTurns {
		t.Fatal("«разом» is not the refusal of the site")
	}
	if link.refreshed() != 1 {
		t.Fatalf("a refusal «разом» at the start made the account stale %d times", link.refreshed())
	}

	rows.rows["job-south"] = &Row{Kind: domain.KindTurn, Source: domain.SourceAccount, Link: markOfTheTest}
	if err := work.Open(context.Background(), turnOf("job-south")); err != nil {
		t.Fatal(err)
	}
	for _, permit := range site.permits {
		permit.State, permit.Outcome = domain.StateExpired, domain.OutcomeExpiredUnused
	}
	before := link.refreshed()
	site.fail = []error{nil, &domain.Refusal{Status: 429, Code: "quota.active_turns"}}
	code, err := work.Begin(context.Background(), researchdomain.TurnRun{JobID: "job-south", ChatID: "chat-north"})
	if code != researchdomain.CodeTurnsAtOnce || err != nil || link.refreshed() != before+1 {
		t.Fatalf("a turn refused «разом» when taken: %q %v, stale %d times after %d", code, err, link.refreshed(), before)
	}
}
