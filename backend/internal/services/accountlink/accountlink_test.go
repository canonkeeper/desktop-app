// SPDX-License-Identifier: AGPL-3.0-only

package accountlink

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/accountlink"
	desktopdomain "canonkeeper/backend/internal/domain/desktop"
)

const passOfTheTest = "ckd_pass-of-the-test-north-00000000000000000000"

// site is the site of a test: what it was asked and what it answers.
type site struct {
	mu        sync.Mutex
	exchanges []domain.Exchange
	answers   []error
	pass      string
	held      string
	accounts  int
	account   error
	unlinked  []string
	told      bool
	waits     []wait
	// before runs before a read of the account, as another request of the page.
	before func()
}

// wait is how a call to the site was bounded: the deadline of its context and when the site was asked.
type wait struct {
	deadline time.Time
	bounded  bool
	at       time.Time
}

func (site *site) waited(ctx context.Context) {
	deadline, bounded := ctx.Deadline()
	site.waits = append(site.waits, wait{deadline: deadline, bounded: bounded, at: time.Now()})
}

func (site *site) Exchange(_ context.Context, exchange domain.Exchange) (string, domain.Account, error) {
	site.mu.Lock()
	defer site.mu.Unlock()
	site.exchanges = append(site.exchanges, exchange)
	var err error
	if len(site.answers) > 0 {
		err, site.answers = site.answers[0], site.answers[1:]
	}
	if err != nil {
		return "", domain.Account{}, err
	}
	return site.pass, domain.Account{Name: "Северный", Email: "a…@example.test"}, nil
}

func (site *site) Hold(pass string) {
	site.mu.Lock()
	defer site.mu.Unlock()
	site.held = pass
}

func (site *site) Held() bool {
	site.mu.Lock()
	defer site.mu.Unlock()
	return site.held != ""
}

func (site *site) Account(ctx context.Context) (domain.Account, error) {
	if before := site.before; before != nil {
		site.before = nil
		before()
	}
	site.mu.Lock()
	defer site.mu.Unlock()
	site.waited(ctx)
	if site.held == "" {
		// As the client: without a pass the site is not asked.
		return domain.Account{}, domain.ErrNotHeld
	}
	site.accounts++
	if errors.Is(site.account, domain.ErrPassInvalid) {
		site.held = ""
	}
	if site.account != nil {
		return domain.Account{}, site.account
	}
	return domain.Account{Name: "Северный", Quota: true, Questions: domain.Counter{Used: 2, Limit: 20}}, nil
}

func (site *site) Unlink(ctx context.Context) bool {
	site.mu.Lock()
	defer site.mu.Unlock()
	site.waited(ctx)
	if site.held == "" {
		return false
	}
	site.unlinked = append(site.unlinked, site.held)
	site.held = ""
	return site.told
}

// records are the records of the shell of a test.
type records struct {
	mu      sync.Mutex
	values  map[string]string
	saves   int
	deletes int
	refuse  bool
}

func (records *records) Value(id string) (string, bool) {
	records.mu.Lock()
	defer records.mu.Unlock()
	value, ok := records.values[id]
	return value, ok
}

func (records *records) Save(_ context.Context, id, value string) error {
	records.mu.Lock()
	defer records.mu.Unlock()
	records.saves++
	if records.refuse {
		return desktopdomain.ErrSecretNotKept
	}
	records.values[id] = value
	return nil
}

func (records *records) Delete(_ context.Context, id string) error {
	records.mu.Lock()
	defer records.mu.Unlock()
	records.deletes++
	if records.refuse {
		return desktopdomain.ErrSecretNotKept
	}
	delete(records.values, id)
	return nil
}

func (records *records) Store() desktopdomain.SecretsStore { return desktopdomain.SecretsInFile }

type leftovers struct {
	mu      sync.Mutex
	marked  []string
	forgets int
}

func (leftovers *leftovers) MarkDropped(pass string) error {
	leftovers.mu.Lock()
	defer leftovers.mu.Unlock()
	leftovers.marked = append(leftovers.marked, pass)
	return nil
}

func (leftovers *leftovers) ForgetDropped() error {
	leftovers.mu.Lock()
	defer leftovers.mu.Unlock()
	leftovers.forgets++
	return nil
}

// helpersOfTheTest count the helpers heard and the links made, with the intent of the last one.
type helpersOfTheTest struct {
	heard, linked int
	chose         bool
}

func (helpers *helpersOfTheTest) Heard(context.Context, domain.Helper) { helpers.heard++ }
func (helpers *helpersOfTheTest) Linked(_ context.Context, _ domain.Helper, chose bool) {
	helpers.linked++
	helpers.chose = chose
}

type shell struct{ focused int }

func (shell *shell) Focus() error {
	shell.focused++
	return nil
}

// stand is the link of a test over its site, records and shell.
type stand struct {
	now       time.Time
	site      *site
	records   *records
	leftovers *leftovers
	shell     *shell
	helpers   *helpersOfTheTest
	link      Parts
}

func newStand() *stand {
	stand := &stand{
		now:  time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC),
		site: &site{pass: passOfTheTest, told: true}, records: &records{values: map[string]string{}}, leftovers: &leftovers{},
		shell: &shell{}, helpers: &helpersOfTheTest{},
	}
	stand.link = NewParts(Build{
		Site: stand.site, Records: stand.records, Leftovers: stand.leftovers, Shell: stand.shell, Helpers: stand.helpers,
		Random: bytes.NewReader(bytes.Repeat([]byte{7, 1, 9}, 4000)), Now: func() time.Time { return stand.now },
		SiteURL: "https://site.example.test", Port: 24567, Platform: domain.PlatformLinux, Version: "1.3.0", Computer: "Рабочий стол",
	})
	return stand
}

func (stand *stand) started(t *testing.T) url.Values {
	t.Helper()
	state, err := stand.link.Start.Execute(context.Background(), false)
	if err != nil || state.Phase != domain.PhaseWaiting || state.Waiting == nil {
		t.Fatalf("Start = %+v, %v", state, err)
	}
	address, err := url.Parse(state.Waiting.Address)
	if err != nil {
		t.Fatal(err)
	}
	return address.Query()
}

// specs/2026-11-13-desk-account.md AC1.1–AC1.3: a link waits with the same address while it lives, a return with its state
// spends it once and exchanges the code with the verifier of its challenge, the name of the computer and the version;
// the pass is held once the shell kept it, and the window is raised.
func TestALinkWaitsReturnsOnceAndHoldsThePassTheShellKept(t *testing.T) {
	stand := newStand()
	query := stand.started(t)
	if query.Get("port") != "24567" || query.Get("platform") != "linux" || len(query.Get("state")) != 43 || len(query.Get("challenge")) != 43 {
		t.Fatalf("the address asks %v", query)
	}
	again := stand.link.Read.Execute(context.Background(), false)
	if again.Phase != domain.PhaseWaiting || again.Waiting == nil || !bytes.Contains([]byte(again.Waiting.Address), []byte(query.Get("state"))) ||
		!again.Waiting.Expires.Equal(stand.now.Add(domain.WaitLifetime)) {
		t.Fatalf("the waiting read again = %+v", again)
	}
	if _, err := stand.link.Start.Execute(context.Background(), false); !errors.Is(err, domain.ErrNotNow) {
		t.Fatalf("a second start = %v", err)
	}
	code := "code-of-the-test-north-00000000000000000000"
	if _, err := stand.link.Finish.Execute(context.Background(), code, "state-of-another"); !errors.Is(err, domain.ErrStateInvalid) {
		t.Fatalf("a wrong state = %v", err)
	}
	if _, err := stand.link.Finish.Execute(context.Background(), "short", query.Get("state")); !errors.Is(err, domain.ErrStateInvalid) {
		t.Fatalf("a code of another shape = %v", err)
	}
	if len(stand.site.exchanges) != 0 {
		t.Fatal("a refused return asked the site")
	}
	outcome, err := stand.link.Finish.Execute(context.Background(), code, query.Get("state"))
	if err != nil || outcome != Linked {
		t.Fatalf("Finish = %v, %v", outcome, err)
	}
	exchange := stand.site.exchanges[0]
	if domain.Challenge(exchange.Verifier) != query.Get("challenge") || exchange.Code != code || exchange.Name != "Рабочий стол" || exchange.AppVersion != "1.3.0" {
		t.Fatalf("the exchange = %+v", exchange)
	}
	if stand.records.values[desktopdomain.SecretAccountPass] != passOfTheTest || stand.site.held != passOfTheTest || stand.shell.focused != 1 ||
		stand.leftovers.forgets != 1 {
		t.Fatalf("after the link: records %d, held %v, focused %d", len(stand.records.values), stand.site.Held(), stand.shell.focused)
	}
	// specs/2026-11-13-desk-account-ai.md AI1.2: the build hears the link made once — the helper of the account is connected again.
	if stand.helpers.linked != 1 {
		t.Fatalf("the build heard %d links made", stand.helpers.linked)
	}
	if _, err := stand.link.Finish.Execute(context.Background(), code, query.Get("state")); !errors.Is(err, domain.ErrStateInvalid) {
		t.Fatalf("the state returned twice = %v", err)
	}
	linked := stand.link.Read.Execute(context.Background(), false)
	if linked.Phase != domain.PhaseLinked || linked.Account == nil || linked.Account.Questions.Limit != 20 || linked.Computer != "Рабочий стол" ||
		linked.Store != desktopdomain.SecretsInFile || linked.Pages.Apps != "https://site.example.test/account#apps" {
		t.Fatalf("linked = %+v", linked)
	}
	if _, err := stand.link.Start.Execute(context.Background(), false); !errors.Is(err, domain.ErrNotNow) {
		t.Fatalf("a start of a linked app = %v", err)
	}
	if stand.helpers.linked != 1 {
		t.Fatalf("a read of the account was heard as a link made: %d", stand.helpers.linked)
	}
}

// AC1.1: a build the site cannot return to starts nothing; a canceled waiting is not returned to.
func TestALinkTheSiteCannotReturnToDoesNotStart(t *testing.T) {
	for name, change := range map[string]func(*Build){
		"port":    func(build *Build) { build.Port = 19999 },
		"system":  func(build *Build) { build.Platform = "darwin" },
		"version": func(build *Build) { build.Version = "" },
	} {
		stand := newStand()
		build := stand.link.build
		change(&build)
		parts := NewParts(build)
		if state, err := parts.Start.Execute(context.Background(), false); !errors.Is(err, domain.ErrCannotStart) || state.Waiting != nil {
			t.Errorf("%s: %+v, %v", name, state, err)
		}
	}
	stand := newStand()
	query := stand.started(t)
	stand.link.Cancel.Execute(context.Background())
	if state := stand.link.Read.Execute(context.Background(), false); state.Phase != domain.PhaseNone {
		t.Fatalf("after the cancel = %+v", state)
	}
	if _, err := stand.link.Finish.Execute(context.Background(), "code-of-the-test-north-00000000000000000000", query.Get("state")); !errors.Is(err, domain.ErrStateInvalid) {
		t.Fatalf("a canceled state = %v", err)
	}
	stand.now = stand.now.Add(domain.WaitLifetime)
	if state := stand.link.Read.Execute(context.Background(), false); state.Phase != domain.PhaseNone {
		t.Fatalf("a late waiting = %+v", state)
	}
}

// AC1.3: the refusal of the site is asked once; a lost answer once more with the same code and verifier; a pass the
// shell did not keep is unlinked with itself, its record removed, and the link failed for the keyring.
func TestTheOutcomesOfTheExchange(t *testing.T) {
	stand := newStand()
	stand.site.answers = []error{domain.ErrLinkInvalid}
	query := stand.started(t)
	if outcome, _ := stand.link.Finish.Execute(context.Background(), "code-of-the-test-north-00000000000000000000", query.Get("state")); outcome != ExchangeFailed || len(stand.site.exchanges) != 1 {
		t.Fatalf("a refusal = %v after %d", outcome, len(stand.site.exchanges))
	}
	if state := stand.link.Read.Execute(context.Background(), false); state.Phase != domain.PhaseFailed || state.Failure != domain.FailedExchange {
		t.Fatalf("after a refusal = %+v", state)
	}

	stand = newStand()
	stand.site.answers = []error{domain.ErrAnswerLost}
	query = stand.started(t)
	if outcome, _ := stand.link.Finish.Execute(context.Background(), "code-of-the-test-north-00000000000000000000", query.Get("state")); outcome != Linked ||
		len(stand.site.exchanges) != 2 || stand.site.exchanges[0] != stand.site.exchanges[1] {
		t.Fatalf("a lost answer = %v after %+v", outcome, stand.site.exchanges)
	}

	stand = newStand()
	stand.site.answers = []error{domain.ErrAnswerLost, domain.ErrLinkInvalid}
	query = stand.started(t)
	if outcome, _ := stand.link.Finish.Execute(context.Background(), "code-of-the-test-north-00000000000000000000", query.Get("state")); outcome != ExchangeFailed || len(stand.site.exchanges) != 2 {
		t.Fatalf("a lost answer refused again = %v after %d", outcome, len(stand.site.exchanges))
	}

	stand = newStand()
	stand.records.refuse = true
	query = stand.started(t)
	if outcome, _ := stand.link.Finish.Execute(context.Background(), "code-of-the-test-north-00000000000000000000", query.Get("state")); outcome != NotSaved {
		t.Fatalf("a pass not kept = %v", outcome)
	}
	if len(stand.site.unlinked) != 1 || stand.site.unlinked[0] != passOfTheTest || stand.site.Held() || stand.records.deletes != 1 ||
		len(stand.leftovers.marked) != 1 || stand.leftovers.marked[0] != passOfTheTest || stand.shell.focused != 0 {
		t.Fatalf("a pass not kept: unlinked %v, deletes %d, marked %d", stand.site.unlinked, stand.records.deletes, len(stand.leftovers.marked))
	}
	if state := stand.link.Read.Execute(context.Background(), false); state.Phase != domain.PhaseFailed || state.Failure != domain.FailedKeyring {
		t.Fatalf("after a pass not kept = %+v", state)
	}
	if _, err := stand.link.Start.Execute(context.Background(), false); err != nil {
		t.Fatalf("a start after a failure = %v", err)
	}
}

// AC2.1–AC2.3: the account is read once a minute or when the page asks; no answer keeps what the site told with «нет
// связи»; a refused pass breaks the link and its record goes; a refusal of a pass held before changes nothing.
func TestTheAccountIsReadAtMostOnceAMinuteAndARefusedPassBreaksTheLink(t *testing.T) {
	stand := newStand()
	stand.site.held = passOfTheTest
	stand.records.values[desktopdomain.SecretAccountPass] = passOfTheTest
	first := stand.link.Read.Execute(context.Background(), false)
	if first.Phase != domain.PhaseLinked || first.Account == nil || stand.site.accounts != 1 {
		t.Fatalf("first = %+v after %d", first, stand.site.accounts)
	}
	stand.now = stand.now.Add(30 * time.Second)
	if stand.link.Read.Execute(context.Background(), false); stand.site.accounts != 1 {
		t.Fatal("a second read in the minute asked the site")
	}
	if stand.link.Read.Execute(context.Background(), true); stand.site.accounts != 2 {
		t.Fatal("a fresh read did not ask the site")
	}
	stand.site.account = domain.ErrUnreachable
	stand.now = stand.now.Add(time.Minute)
	silent := stand.link.Read.Execute(context.Background(), false)
	if silent.Phase != domain.PhaseLinked || !silent.Unreachable || silent.Account == nil || silent.Account.Name != "Северный" {
		t.Fatalf("a silent site = %+v", silent)
	}
	stand.site.account = domain.ErrPassReplaced
	if replaced := stand.link.Read.Execute(context.Background(), true); replaced.Phase != domain.PhaseLinked || stand.records.deletes != 0 {
		t.Fatalf("a refusal of a pass held before = %+v", replaced)
	}
	stand.site.account = domain.ErrPassInvalid
	broken := stand.link.Read.Execute(context.Background(), true)
	if broken.Phase != domain.PhaseBroken || broken.Account != nil {
		t.Fatalf("a refused pass = %+v", broken)
	}
	stand.link.wait()
	if stand.records.deletes != 1 || len(stand.records.values) != 0 {
		t.Fatalf("the record of a refused pass: %d deletes", stand.records.deletes)
	}
	if _, err := stand.link.Start.Execute(context.Background(), false); err != nil {
		t.Fatalf("a start after a broken link = %v", err)
	}
}

// AC2.3, AC2.4: unlinking tells the site when it answers, stops using the pass in any case and removes its record; a
// record the shell did not remove is marked for the next start.
func TestUnlinkingStopsUsingThePassWhateverTheShellSays(t *testing.T) {
	stand := newStand()
	stand.site.held = passOfTheTest
	stand.records.values[desktopdomain.SecretAccountPass] = passOfTheTest
	if unlinked := stand.link.Unlink.Execute(context.Background()); !unlinked.Site || !unlinked.Computer || stand.site.Held() {
		t.Fatalf("Unlink = %+v", unlinked)
	}
	if state := stand.link.Read.Execute(context.Background(), false); state.Phase != domain.PhaseNone {
		t.Fatalf("after unlinking = %+v", state)
	}
	stand.site.held, stand.site.told = passOfTheTest, false
	stand.records.values[desktopdomain.SecretAccountPass] = passOfTheTest
	stand.records.refuse = true
	if unlinked := stand.link.Unlink.Execute(context.Background()); unlinked.Site || unlinked.Computer || stand.site.Held() ||
		len(stand.leftovers.marked) != 1 {
		t.Fatalf("Unlink of a silent site and shell = %+v, marked %v", unlinked, stand.leftovers.marked)
	}
	stand.site.held = passOfTheTest
	stand.link.Unlink.BeforeErase(context.Background())
	if stand.site.Held() || len(stand.site.unlinked) != 3 {
		t.Fatalf("the erasure did not unlink: %v", stand.site.unlinked)
	}
	if !NewParts(Build{Site: &site{held: "x"}, SiteURL: "https://site.example.test"}).Reaches() {
		t.Fatal("a linked app goes nowhere")
	}
	if stand.link.Reaches() {
		t.Fatal("an unlinked app goes to the site")
	}
}

// AC2.3, AC2.4: a read of the account that meets the unlinking of the author — the app was linked when the read began,
// the pass went before it reached the site — is no refusal: the app is not linked, not broken, and the record goes once.
func TestAReadThatMeetsUnlinkingDoesNotBreakTheLink(t *testing.T) {
	stand := newStand()
	stand.site.Hold(passOfTheTest)
	stand.records.values[desktopdomain.SecretAccountPass] = passOfTheTest
	stand.site.before = func() { stand.link.Unlink.Execute(context.Background()) }
	if state := stand.link.Read.Execute(context.Background(), true); state.Phase != domain.PhaseNone {
		t.Fatalf("a read that met unlinking = %+v", state)
	}
	stand.link.wait()
	if stand.site.accounts != 0 || stand.records.deletes != 1 || len(stand.leftovers.marked) != 0 {
		t.Fatalf("a read that met unlinking: %d reads, %d deletes", stand.site.accounts, stand.records.deletes)
	}
	if state := stand.link.Read.Execute(context.Background(), true); state.Phase != domain.PhaseNone || state.Unreachable {
		t.Fatalf("after unlinking = %+v", state)
	}
}

// AC2.1, AC2.4, AC2.5: a read of the account, «Отвязать» and the unlinking before «Стереть все данные» wait for the site
// 5 seconds at most, and not less: the deadline the site gets is 5 seconds after the call began. The bounds hold whatever
// the load of the machine — the deadline is set between the two moments the test reads.
func TestTheSiteIsWaitedForFiveSeconds(t *testing.T) {
	const term = 5 * time.Second
	stand := newStand()
	for _, call := range []struct {
		name string
		run  func()
	}{
		{"getAccount", func() { stand.link.Read.Execute(context.Background(), true) }},
		{"unlinkAccount", func() { stand.link.Unlink.Execute(context.Background()) }},
		{"eraseDesktopData", func() { stand.link.Unlink.BeforeErase(context.Background()) }},
	} {
		stand.site.Hold(passOfTheTest)
		stand.site.waits = nil
		began := time.Now()
		call.run()
		if len(stand.site.waits) != 1 {
			t.Fatalf("%s asked the site %d times", call.name, len(stand.site.waits))
		}
		asked := stand.site.waits[0]
		if !asked.bounded || asked.deadline.Sub(asked.at) > term || asked.deadline.Sub(began) < term {
			t.Errorf("%s waits for the site %s (bounded %v)", call.name, asked.deadline.Sub(began), asked.bounded)
		}
	}
}

// specs/2026-11-13-desk-account-ai.md AI3.7 (the status of the reading never waits for the site): Held answers at once what the
// link holds — the account the site told last and when it told it — and asks the site aside, at most once a minute; a site
// that does not answer holds nothing, and once it answers the next call holds what it told.
func TestTheHeldAccountNeverWaitsForTheSite(t *testing.T) {
	stand := newStand()
	stand.site.Hold(passOfTheTest)
	first := stand.link.Read.Execute(context.Background(), true)
	silent := make(chan struct{})
	stand.site.before = func() { <-silent }
	stand.now = stand.now.Add(domain.FreshEvery)
	began := time.Now()
	held := stand.link.Read.Held()
	if time.Since(began) > time.Second || held.Account == nil || !held.At.Equal(first.At) {
		t.Fatalf("the held account waited %s: %+v", time.Since(began), held)
	}
	if again := stand.link.Read.Held(); !again.At.Equal(first.At) {
		t.Fatalf("a second call within the minute = %+v", again)
	}
	close(silent)
	stand.link.wait()
	if stand.site.accounts != 2 {
		t.Fatalf("the site was asked %d times; want the first read and one aside", stand.site.accounts)
	}
	if told := stand.link.Read.Held(); told.Account == nil || !told.At.Equal(stand.now) || told.Unreachable {
		t.Fatalf("after the site answered aside = %+v", told)
	}
}

// specs/2026-11-15-desk-account-link-web.md AW1.6: a waiting that ran out leaves the link failed for its term until a new
// link starts; a waiting the author canceled leaves none.
func TestAWaitingThatRanOutFailsTheLinkUntilANewOne(t *testing.T) {
	stand := newStand()
	stand.started(t)
	stand.now = stand.now.Add(domain.WaitLifetime)
	for range 2 {
		if state := stand.link.Read.Execute(context.Background(), false); state.Phase != domain.PhaseFailed || state.Failure != domain.FailedExpired || state.Waiting != nil {
			t.Fatalf("a waiting that ran out = %+v", state)
		}
	}
	if stand.link.Reaches() {
		t.Fatal("a link that ran out still goes to the site")
	}
	query := stand.started(t)
	stand.link.Cancel.Execute(context.Background())
	stand.now = stand.now.Add(domain.WaitLifetime)
	if state := stand.link.Read.Execute(context.Background(), false); state.Phase != domain.PhaseNone || state.Failure != "" {
		t.Fatalf("a canceled waiting = %+v", state)
	}
	// A return that comes late is refused and leaves the term that ran out.
	query = stand.started(t)
	stand.now = stand.now.Add(domain.WaitLifetime)
	if _, err := stand.link.Finish.Execute(context.Background(), "code-of-the-test-north-00000000000000000000", query.Get("state")); !errors.Is(err, domain.ErrStateInvalid) {
		t.Fatalf("a late return = %v", err)
	}
	if state := stand.link.Read.Execute(context.Background(), false); state.Phase != domain.PhaseFailed || state.Failure != domain.FailedExpired {
		t.Fatalf("after a late return = %+v", state)
	}
}

// AW1.6: a read whose clock passes the term in its middle says the waiting or «время вышло», never that nothing waits —
// the page stops asking at none, and the end would not be told. Whichever call of the clock the term passes at.
func TestAReadAtTheEdgeOfTheTermSaysTheWaitingOrItsEnd(t *testing.T) {
	for before := range 8 {
		stand := newStand()
		base := stand.now
		calls, edge := 0, false
		stand.link = NewParts(Build{
			Site: stand.site, Records: stand.records, Leftovers: stand.leftovers, Shell: stand.shell, Helpers: stand.helpers,
			Random: bytes.NewReader(bytes.Repeat([]byte{7, 1, 9}, 4000)),
			Now: func() time.Time {
				if !edge {
					return base
				}
				calls++
				if calls <= before {
					return base.Add(domain.WaitLifetime - time.Nanosecond)
				}
				return base.Add(domain.WaitLifetime)
			},
			SiteURL: "https://site.example.test", Port: 24567, Platform: domain.PlatformLinux, Version: "1.3.0", Computer: "Рабочий стол",
		})
		stand.started(t)
		edge = true
		first := stand.link.Read.Execute(context.Background(), false)
		if first.Phase != domain.PhaseWaiting && (first.Phase != domain.PhaseFailed || first.Failure != domain.FailedExpired) {
			t.Fatalf("the term passing at the call %d of the clock: the read = %s %q", before+1, first.Phase, first.Failure)
		}
		// The next read comes after the term.
		calls = before
		if next := stand.link.Read.Execute(context.Background(), false); next.Phase != domain.PhaseFailed || next.Failure != domain.FailedExpired {
			t.Fatalf("the term passing at the call %d of the clock: the next read = %s %q", before+1, next.Phase, next.Failure)
		}
	}
}

// AW3.2: the intent of the start lives with the waiting, and the build hears it with the link made.
func TestALinkMadeCarriesTheIntentOfItsStart(t *testing.T) {
	for _, chose := range []bool{true, false} {
		stand := newStand()
		state, err := stand.link.Start.Execute(context.Background(), chose)
		if err != nil {
			t.Fatal(err)
		}
		address, _ := url.Parse(state.Waiting.Address)
		if strings.Contains(address.RawQuery, "choose") {
			t.Fatalf("the address carries the intent: %s", address.RawQuery)
		}
		if outcome, err := stand.link.Finish.Execute(context.Background(), "code-of-the-test-north-00000000000000000000", address.Query().Get("state")); err != nil || outcome != Linked {
			t.Fatalf("Finish = %v, %v", outcome, err)
		}
		if stand.helpers.linked != 1 || stand.helpers.chose != chose {
			t.Fatalf("intent %v: the build heard %d links, the intent %v", chose, stand.helpers.linked, stand.helpers.chose)
		}
	}
}
