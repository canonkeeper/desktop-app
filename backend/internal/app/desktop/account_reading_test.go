// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"sync"
	"testing"
	"time"

	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	"canonkeeper/backend/internal/domain/aisettings"
	readingdomain "canonkeeper/backend/internal/domain/readingconsent"
	accountservice "canonkeeper/backend/internal/services/accountlink"
	readingservice "canonkeeper/backend/internal/services/readingconsent"
)

// countedSite is the client of the site of a linked app whose reads of the account are counted; silent ones wait for their
// term.
type countedSite struct {
	heldSite
	mu     sync.Mutex
	reads  int
	silent bool
}

func (*countedSite) Held() bool { return true }
func (site *countedSite) Account(ctx context.Context) (accountdomain.Account, error) {
	site.mu.Lock()
	site.reads++
	silent := site.silent
	site.mu.Unlock()
	if silent {
		<-ctx.Done()
		return accountdomain.Account{}, accountdomain.ErrUnreachable
	}
	return accountdomain.Account{Quota: true, Reading: accountdomain.Counter{Used: 10, Limit: 40}, Work: accountdomain.Counter{Used: 1, Limit: 8}}, nil
}

func (site *countedSite) count() int {
	site.mu.Lock()
	defer site.mu.Unlock()
	return site.reads
}

// pausedSagas are the consents of the account the store finds paused with the reasons asked.
type pausedSagas struct {
	paused map[string]readingdomain.Pause
}

func (pausedSagas) Consent(context.Context, string) (*readingdomain.Consent, error) { return nil, nil }
func (pausedSagas) ChangeConsent(context.Context, string, time.Time, func(*readingdomain.Consent) (readingdomain.Consent, error), bool) (readingdomain.Consent, error) {
	return readingdomain.Consent{}, readingservice.ErrNotFound
}
func (sagas pausedSagas) Paused(_ context.Context, _ readingdomain.Source, pauses []readingdomain.Pause) ([]string, error) {
	var found []string
	for project, pause := range sagas.paused {
		for _, asked := range pauses {
			if asked == pause {
				found = append(found, project)
			}
		}
	}
	return found, nil
}

type noReading struct{}

func (noReading) Queue(context.Context, string) error          { return nil }
func (noReading) Cancel(context.Context, string, string) error { return nil }

// theReadingOfTheAccount is a build linked with the site of the test, the helper of the account chosen, and the resume of its
// reading over the sagas.
func theReadingOfTheAccount(t *testing.T, site *countedSite, sagas pausedSagas) *build {
	t.Helper()
	saved, err := loadSettings(DataDir{Root: t.TempDir()}.Settings(), settingsNow)
	if err != nil {
		t.Fatal(err)
	}
	told := helperOf(helperOfTheSite)
	if err := saved.KeepAI(aisettings.Settings{Source: aisettings.SourceAccount, Account: &told}); err != nil {
		t.Fatal(err)
	}
	link := accountLink{parts: accountservice.NewParts(accountservice.Build{Site: site})}
	settings := readingSettings{ownAI: ownAI{settings: saved, secrets: newShellSecrets(nil, "", nil, 0), link: link.parts}, site: "https://site.example.test", account: &link}
	return &build{settings: saved, account: link, resume: &readingservice.Resume{Settings: settings, Store: sagas, Work: noReading{}, Now: time.Now}}
}

// specs/2026-11-13-desk-account-ai.md AI3.6 (the checks of the pauses): the schedule of the reading asks the site only while a
// saga stands at a pause of the account that goes on by itself at this check — an hour of checks with no such pause, the
// limits checked among them, asks the site nothing; a pause of the author does not either; a pause of no connection is checked
// with a fresh read of the account at every check.
func TestTheChecksOfThePausesAskTheSiteOnlyForAPauseThatGoesOn(t *testing.T) {
	site := &countedSite{}
	quiet := theReadingOfTheAccount(t, site, pausedSagas{paused: map[string]readingdomain.Pause{"saga-north": readingdomain.PauseAuthor}})
	quiet.resumeReading(context.Background(), false, true)
	for round := 1; round <= limitsEveryN; round++ {
		quiet.resumeReading(context.Background(), false, round%limitsEveryN == 0)
	}
	if reads := site.count(); reads != 0 {
		t.Fatalf("an hour without a pause that goes on asked the site %d times", reads)
	}
	paused := theReadingOfTheAccount(t, site, pausedSagas{paused: map[string]readingdomain.Pause{"saga-north": readingdomain.PauseConnection}})
	paused.resumeReading(context.Background(), false, false)
	paused.resumeReading(context.Background(), false, false)
	if reads := site.count(); reads != 2 {
		t.Fatalf("two checks of a pause of no connection asked the site %d times", reads)
	}
}

// AI3.7 (the poll of the status of a reading of the account): the limits of the tier come from what the app holds — a silent
// site does not hold the status —, and the read of the account goes aside, so a later poll tells what the site answered.
func TestTheLimitsOfTheStatusNeverWaitForTheSite(t *testing.T) {
	site := &countedSite{silent: true}
	desktop := theReadingOfTheAccount(t, site, pausedSagas{})
	settings := desktop.resume.Settings.(readingSettings)
	began := time.Now()
	if _, told := settings.AccountLimits(context.Background()); told || time.Since(began) > time.Second {
		t.Fatalf("the limits of a silent site: told %v after %s", told, time.Since(began))
	}
	awaitAside := func(reads int) {
		t.Helper()
		for deadline := time.Now().Add(time.Minute); site.count() < reads; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("the read aside did not come: %d", site.count())
			}
		}
	}
	awaitAside(1)
	site.mu.Lock()
	site.silent = false
	site.mu.Unlock()
	desktop.account.parts.Refresh()
	settings.AccountLimits(context.Background())
	awaitAside(2)
	var limits readingservice.AccountLimits
	var told bool
	for deadline := time.Now().Add(time.Minute); !told; time.Sleep(10 * time.Millisecond) {
		if limits, told = settings.AccountLimits(context.Background()); time.Now().After(deadline) {
			t.Fatal("the limits the site told aside never came")
		}
	}
	if limits.Characters.Limit != 40 || limits.Work.Limit != 8 {
		t.Fatalf("the limits = %+v", limits)
	}
}
