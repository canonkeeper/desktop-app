// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
	domain "canonkeeper/backend/internal/domain/desktop"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

// ST4.3, specs/2026-11-13-desk-updates.md UB1.6: the open app goes only to the hosts of its updates, and a build that is
// updated by hand goes nowhere. specs/2026-11-14-desk-journal.md JR4.2: the hosts stand while the daily check is on and
// «Добро пожаловать» is closed, or while a check or a download goes.
func TestTheOpenAppGoesOnlyWhereItsUpdatesGo(t *testing.T) {
	saved := &settings{values: map[string]json.RawMessage{}, welcomed: true, checkUpdates: true}
	if uses := newUpdater(nil, saved, "1.2.0", domain.UpdateNone, nil).network(); len(uses) != 0 {
		t.Fatalf("the network of a build updated by hand = %+v", uses)
	}
	for _, quiet := range []*settings{
		{values: map[string]json.RawMessage{}, welcomed: false, checkUpdates: true},
		{values: map[string]json.RawMessage{}, welcomed: true, checkUpdates: false},
	} {
		updates := newUpdater(nil, quiet, "1.2.0", domain.UpdateDeb, nil)
		if uses := updates.network(); len(uses) != 0 {
			t.Fatalf("the network without the daily check = %+v", uses)
		}
		for _, phase := range []string{"checking", "downloading"} {
			updates.shell = domain.ShellUpdate{Phase: phase}
			if uses := updates.network(); len(uses) != 3 {
				t.Fatalf("the network while the author's %s goes = %+v", phase, uses)
			}
		}
		updates.shell = domain.ShellUpdate{Phase: "downloaded"}
		if uses := updates.network(); len(uses) != 0 {
			t.Fatalf("the network of a downloaded update = %+v", uses)
		}
	}
	var hosts []string
	for _, use := range newUpdater(nil, saved, "1.2.0", domain.UpdateDeb, nil).network() {
		if use.Purpose == "" {
			t.Fatalf("%s is named without what for", use.Host)
		}
		hosts = append(hosts, use.Host)
	}
	if !slices.Equal(hosts, []string{"canonkeeper.ru", "github.com", "release-assets.githubusercontent.com"}) {
		t.Fatalf("the hosts of the updates = %v", hosts)
	}
}

// updaterOfTheBuild is the update of a build the tests compose without a shell: updated by hand.
func updaterOfTheBuild() *updater {
	return newUpdater(func(ControlMessage) error { return nil }, nil, "", domain.UpdateNone, log.New(io.Discard, "", 0))
}

// channelOfTheTest records what the backend writes to the shell.
type channelOfTheTest struct {
	mu   sync.Mutex
	sent []ControlMessage
}

func (channel *channelOfTheTest) send(message ControlMessage) error {
	channel.mu.Lock()
	defer channel.mu.Unlock()
	channel.sent = append(channel.sent, message)
	return nil
}

func (channel *channelOfTheTest) kinds() []string {
	channel.mu.Lock()
	defer channel.mu.Unlock()
	var kinds []string
	for _, message := range channel.sent {
		kinds = append(kinds, message.Type+" "+message.Version)
	}
	return kinds
}

func updaterOfTheTest(t *testing.T, kind domain.UpdateKind) (*updater, *channelOfTheTest, *settings) {
	t.Helper()
	saved, err := loadSettings(filepath.Join(t.TempDir(), "settings.json"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	channel := &channelOfTheTest{}
	return newUpdater(channel.send, saved, "1.2.0", kind, log.New(io.Discard, "", 0)), channel, saved
}

// UB1.7: the page reads the phase the shell reported, and a phase allows only its operations.
func TestAnUpdateFollowsThePhaseTheShellReports(t *testing.T) {
	updates, channel, _ := updaterOfTheTest(t, domain.UpdateDeb)
	update := updates.Update()
	if update.Phase != domain.UpdateIdle || update.Found != domain.UpdateNeverChecked || update.Available != nil || !update.Daily {
		t.Fatalf("before any check = %+v", update)
	}
	if err := updates.Install(context.Background()); !errors.Is(err, domain.ErrUpdateState) {
		t.Fatalf("an installation without a version = %v", err)
	}
	if err := updates.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := updates.Check(context.Background()); !errors.Is(err, domain.ErrUpdateState) {
		t.Fatalf("a check while one goes on = %v", err)
	}
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "available", Version: "1.3.0", Notes: "Что нового"})
	update = updates.Update()
	if update.Found != domain.UpdateAvailable || update.Available == nil || update.Available.Version != "1.3.0" || update.CheckedAt.IsZero() {
		t.Fatalf("a version found = %+v", update)
	}
	if err := updates.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "downloading", Version: "1.3.0", Percent: 40})
	if update := updates.Update(); update.Phase != domain.UpdateDownloading || update.Percent != 40 {
		t.Fatalf("a download = %+v", update)
	}
	if err := updates.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The shell asks only by the version it reported; the page names none.
	if got := channel.kinds(); !slices.Equal(got, []string{"update-check ", "update-download 1.3.0", "update-cancel 1.3.0"}) {
		t.Fatalf("the backend wrote %v", got)
	}
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "install_refused", Version: "1.3.0", Refusal: "declined", Package: "/home/тест/Загрузки/CanonKeeper_1.3.0_amd64.deb"})
	if update := updates.Update(); update.Phase != domain.UpdateInstallRefused || update.Refusal != domain.UpdateDeclined || update.Package == "" {
		t.Fatalf("a refusal = %+v", update)
	}
	// «Обновить и перезапустить» again downloads anew.
	if err := updates.Install(context.Background()); err != nil {
		t.Fatal(err)
	}

	// A build updated by hand checks nothing.
	manual, channel, _ := updaterOfTheTest(t, domain.UpdateNone)
	if err := manual.Check(context.Background()); !errors.Is(err, domain.ErrUpdateState) || len(channel.kinds()) != 0 {
		t.Fatalf("a check of a build updated by hand = %v, %v", err, channel.kinds())
	}
}

// UB2.1: the file downloaded and checked waits for the turns; the step that finds none closes the gate of new turns, and
// only then the shell is asked to install. A cancel opens it again.
func TestAnInstallationWaitsForTheTurnsAndClosesTheirGateInOneStep(t *testing.T) {
	updates, channel, saved := updaterOfTheTest(t, domain.UpdateAppImage)
	var running atomic.Int32
	running.Store(1)
	updates.useTurns(func(context.Context) (int, error) { return int(running.Load()), nil })
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "available", Version: "1.3.0", Notes: "Что нового"})
	if err := updates.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "downloaded", Version: "1.3.0", Notes: "Что нового"})
	updates.whenNoTurns(context.Background())
	if update := updates.Update(); update.Phase != domain.UpdateWaitingTurn {
		t.Fatalf("with a turn in work = %+v", update)
	}
	done, err := updates.gate.Admit()
	if err != nil {
		t.Fatal("a new turn was refused while the update waited")
	}
	done()
	running.Store(0)
	updates.whenNoTurns(context.Background())
	if update := updates.Update(); update.Phase != domain.UpdateInstalling {
		t.Fatalf("without turns = %+v", update)
	}
	if _, err := updates.gate.Admit(); !errors.Is(err, researchdomain.ErrUpdating) {
		t.Fatalf("a new turn after the gate closed = %v", err)
	}
	if got := channel.kinds(); got[len(got)-1] != "update-install 1.3.0" {
		t.Fatalf("the backend wrote %v", got)
	}
	// UB2.8: the notes of the release are kept with its version for the start of that version.
	if saved.releaseNotesOf("1.3.0") != "Что нового" || saved.releaseNotesOf("1.2.0") != "" {
		t.Fatal("the notes of the release were not kept with its version")
	}
	if err := updates.Cancel(context.Background()); !errors.Is(err, domain.ErrUpdateState) {
		t.Fatalf("a cancel of an installation asked for = %v", err)
	}
	// The installation did not happen: the backend starts again — here the shell says so — and turns are taken again.
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "install_failed", Version: "1.3.0"})
	if done, err := updates.gate.Admit(); err != nil {
		t.Fatal("the gate stayed closed after the installation failed")
	} else {
		done()
	}
}

// UB2.1, UB2.9: the shell may not take the installation it was asked for — an erasure is marked, or the window is already
// leaving — and answers with the file still downloaded. New turns are then taken again, the backend does not ask once more
// by itself, and the author can cancel the update.
func TestAnInstallationTheShellDidNotTakeOpensTheGateAgain(t *testing.T) {
	updates, channel, _ := updaterOfTheTest(t, domain.UpdateAppImage)
	updates.useTurns(func(context.Context) (int, error) { return 0, nil })
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "available", Version: "1.3.0"})
	if err := updates.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "downloaded", Version: "1.3.0"})
	updates.whenNoTurns(context.Background())
	if _, err := updates.gate.Admit(); !errors.Is(err, researchdomain.ErrUpdating) {
		t.Fatalf("a new turn after the backend asked to install = %v", err)
	}
	// The answer of the shell: not installing.
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "downloaded", Version: "1.3.0"})
	done, err := updates.gate.Admit()
	if err != nil {
		t.Fatal("the gate stayed closed after the shell did not take the installation")
	}
	done()
	updates.whenNoTurns(context.Background())
	if got := channel.kinds(); !slices.Equal(got, []string{"update-download 1.3.0", "update-install 1.3.0"}) {
		t.Fatalf("the backend wrote %v", got)
	}
	if update := updates.Update(); update.Phase != domain.UpdateWaitingTurn {
		t.Fatalf("after the shell did not take the installation = %+v", update)
	}
	if err := updates.Cancel(context.Background()); err != nil {
		t.Fatalf("a cancel after the shell did not take the installation = %v", err)
	}
}

// specs/2026-11-17-desk-update-linux.md UL2.1, UL2.4: another installation of programs holds a lock of Debian — the shell
// refuses the package it was asked to install before the backend stops. New turns are taken again, no package is named, and
// «Обновить и перезапустить» asks the shell again.
func TestAnotherInstallationOfProgramsRefusesThePackageAndTheTurnsGoOn(t *testing.T) {
	updates, channel, _ := updaterOfTheTest(t, domain.UpdateDeb)
	updates.useTurns(func(context.Context) (int, error) { return 0, nil })
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "available", Version: "1.3.0"})
	if err := updates.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "downloaded", Version: "1.3.0"})
	updates.whenNoTurns(context.Background())
	if _, err := updates.gate.Admit(); !errors.Is(err, researchdomain.ErrUpdating) {
		t.Fatalf("a new turn after the backend asked to install = %v", err)
	}
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "install_refused", Version: "1.3.0", Refusal: "busy"})
	done, err := updates.gate.Admit()
	if err != nil {
		t.Fatal("the gate stayed closed after another installation refused the package")
	}
	done()
	if update := updates.Update(); update.Phase != domain.UpdateInstallRefused || update.Refusal != domain.UpdateBusy || update.Package != "" || !domain.MayInstall(update) {
		t.Fatalf("a refusal by another installation = %+v", update)
	}
	if err := updates.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := channel.kinds(); !slices.Equal(got, []string{"update-download 1.3.0", "update-install 1.3.0", "update-download 1.3.0"}) {
		t.Fatalf("the backend wrote %v", got)
	}
}

// UB2.7: the cause of an installation that did not happen, which the shell reports with install_failed, is in the log of
// the run once.
func TestTheCauseOfAFailedInstallationIsInTheLogOfTheRun(t *testing.T) {
	updates, _, _ := updaterOfTheTest(t, domain.UpdateNSIS)
	var written desktoptest.Output
	updates.logger = log.New(&written, "", 0)
	failed := ControlMessage{Type: messageUpdateState, Phase: "install_failed", Version: "1.3.0", Cause: "not_launched"}
	updates.heard(failed)
	updates.heard(failed)
	if got := strings.Count(string(written.Bytes()), `{"event":"desktop_update_install_failed","cause":"not_launched"}`); got != 1 {
		t.Fatalf("the log of the run says the cause %d times: %s", got, written.Bytes())
	}
}

// UB2.1: a turn the gate let in is in work before the gate can close — the count and the closing wait for it.
func TestATurnTheGateLetInIsCountedBeforeTheGateCloses(t *testing.T) {
	gate := &turnGate{}
	done, err := gate.Admit()
	if err != nil {
		t.Fatal(err)
	}
	inWork := atomic.Int32{}
	closing := make(chan bool)
	go func() {
		closed, _ := gate.closeWhenIdle(context.Background(), func(context.Context) (int, error) { return int(inWork.Load()), nil })
		closing <- closed
	}()
	select {
	case <-closing:
		t.Fatal("the gate closed while a turn was being admitted")
	case <-time.After(100 * time.Millisecond):
	}
	inWork.Store(1) // the admitted turn became a Job
	done()
	if closed := <-closing; closed {
		t.Fatal("the gate closed over a turn in work")
	}
}

// UB1.2: the daily check asks once a day from the last check, only with the check on and «Добро пожаловать» answered.
func TestTheDailyCheckAsksOnceADayOnlyWhenTheAuthorKeptIt(t *testing.T) {
	updates, channel, saved := updaterOfTheTest(t, domain.UpdateNSIS)
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	updates.now = func() time.Time { return now }
	updates.daily()
	if len(channel.kinds()) != 0 {
		t.Fatal("the welcome is not answered, and the app asked")
	}
	if err := saved.DismissWelcome(false); err != nil {
		t.Fatal(err)
	}
	now = now.Add(48 * time.Hour)
	updates.daily()
	if len(channel.kinds()) != 0 {
		t.Fatal("the check is off, and the app asked")
	}
	if err := saved.KeepDaily(true); err != nil {
		t.Fatal(err)
	}
	updates.daily()
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "latest"})
	now = now.Add(23 * time.Hour)
	updates.daily()
	now = now.Add(time.Hour)
	updates.daily()
	if got := channel.kinds(); !slices.Equal(got, []string{"update-check ", "update-check "}) {
		t.Fatalf("in a day and a day the backend wrote %v", got)
	}
}

// UB1.2, UB2.8: settings.json keeps when the app last checked and the notes of the release being installed with its
// version; a new start reads both, the notes are said only by the start of their version and once, and the choice of the
// welcome keeps the other keys of the section.
func TestSettingsKeepTheLastCheckAndTheNotesOfTheReleaseWithItsVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	saved, err := loadSettings(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	checked := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	if err := saved.keepChecked(checked); err != nil {
		t.Fatal(err)
	}
	if err := saved.keepNotes("1.3.0", "Что нового в 1.3.0"); err != nil {
		t.Fatal(err)
	}
	if err := saved.DismissWelcome(false); err != nil {
		t.Fatal(err)
	}
	again, err := loadSettings(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !again.Checked().Equal(checked) || again.Daily() {
		t.Fatalf("the last check %v, daily %v", again.Checked(), again.Daily())
	}
	// The installation did not happen: 1.2.0 says nothing and keeps the notes of 1.3.0.
	if again.releaseNotesOf("1.2.0") != "" {
		t.Fatal("the notes of 1.3.0 were said by 1.2.0")
	}
	if err := again.forgetNotes("1.2.0"); err != nil || again.releaseNotesOf("1.3.0") == "" {
		t.Fatalf("1.2.0 took the notes of 1.3.0 away: %v", err)
	}
	if again.releaseNotesOf("1.3.0") != "Что нового в 1.3.0" {
		t.Fatal("1.3.0 does not find its notes")
	}
	if err := again.forgetNotes("1.3.0"); err != nil || again.releaseNotesOf("1.3.0") != "" {
		t.Fatalf("the notes were said twice: %v", err)
	}
	if err := again.KeepDaily(true); err != nil || !again.Daily() || !again.Checked().Equal(checked) {
		t.Fatalf("the daily check: %v, %v, %v", err, again.Daily(), again.Checked())
	}
}

// UB3.7: the smoke build of an update presses «Обновить и перезапустить» itself once the shell reports a later version;
// a build without the tag smokeupdate never installs without the author.
func TestOnlyTheSmokeBuildInstallsWithoutTheAuthor(t *testing.T) {
	if smokeInstall {
		t.Fatal("a build without the tag smokeupdate installs by itself")
	}
	t.Cleanup(func() { smokeInstall = false })
	for _, smoke := range []bool{false, true} {
		smokeInstall = smoke
		updates, channel, _ := updaterOfTheTest(t, domain.UpdateAppImage)
		updates.heard(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "latest"})
		updates.heard(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "available", Version: "1.3.0"})
		want := []string(nil)
		if smoke {
			want = []string{"update-download 1.3.0"}
		}
		if got := channel.kinds(); !slices.Equal(got, want) {
			t.Fatalf("smoke %v: the backend wrote %v", smoke, got)
		}
	}
}

// specs/2026-11-15-desk-updates-web.md UW1.5: the shell knows a found version only while it runs, so each start asks
// again — once, half a minute after ready, when the shell knows nothing yet (idle with found never), even if the last
// check was an hour ago. A known outcome waits for its day; a phase of a failure asks nothing, and its words and the path
// of its package stay.
func TestTheCheckAtTheStartAsksOnlyWhenTheShellKnowsNothingYet(t *testing.T) {
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	started := func(t *testing.T, shell ControlMessage) (*updater, *channelOfTheTest) {
		t.Helper()
		updates, channel, saved := updaterOfTheTest(t, domain.UpdateDeb)
		updates.now = func() time.Time { return now }
		if err := saved.DismissWelcome(true); err != nil {
			t.Fatal(err)
		}
		if err := saved.keepChecked(now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		updates.heard(shell)
		return updates, channel
	}

	updates, channel := started(t, ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "never"})
	updates.daily()
	updates.daily()
	if got := channel.kinds(); !slices.Equal(got, []string{"update-check "}) {
		t.Fatalf("a start an hour after the last check: the backend wrote %v", got)
	}
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "latest"})
	updates.daily()
	if got := channel.kinds(); len(got) != 1 {
		t.Fatalf("an outcome known within its day: the backend wrote %v", got)
	}

	// The backend started again while the shell keeps what it found: the day of the last check counts.
	for _, found := range []string{"available", "latest", "offline"} {
		updates, channel = started(t, ControlMessage{Type: messageUpdateState, Phase: "idle", Found: found, Version: "1.3.0"})
		updates.daily()
		if got := channel.kinds(); len(got) != 0 {
			t.Fatalf("a known outcome %s an hour old: the backend wrote %v", found, got)
		}
	}

	// After an installation that did not happen the backend starts again in a phase of a failure: nothing is asked, and the
	// page still reads the failure and the package kept.
	kept := "/home/тест/Загрузки/CanonKeeper_1.3.0_amd64.deb"
	for _, failure := range []ControlMessage{
		{Type: messageUpdateState, Phase: "install_failed", Version: "1.3.0", Package: kept},
		{Type: messageUpdateState, Phase: "install_refused", Version: "1.3.0", Refusal: "unavailable", Package: kept},
		{Type: messageUpdateState, Phase: "download_failed", Version: "1.3.0"},
		{Type: messageUpdateState, Phase: "verify_failed", Version: "1.3.0"},
	} {
		updates, channel = started(t, failure)
		now = now.Add(48 * time.Hour)
		updates.daily()
		update := updates.Update()
		if got := channel.kinds(); len(got) != 0 || string(update.Phase) != failure.Phase || update.Package != failure.Package {
			t.Fatalf("a start in %s: the backend wrote %v, the page reads %+v", failure.Phase, got, update)
		}
	}
}

// UW1.5: the schedule asks within a minute after «Начать» with the check on and after the check is turned on, while the
// shell knows nothing yet — one check, however many looks pass; a check that is off and a build updated by hand ask
// nothing. The times of the schedule are the test's.
func TestTheScheduleAsksWithinAMinuteOfTheChoiceOnce(t *testing.T) {
	first, look := updateFirstCheck, updateLook
	updateFirstCheck, updateLook = 5*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { updateFirstCheck, updateLook = first, look })
	running := func(t *testing.T, kind domain.UpdateKind) (*updater, *channelOfTheTest, *settings) {
		t.Helper()
		updates, channel, saved := updaterOfTheTest(t, kind)
		if err := saved.keepChecked(time.Now().Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		updates.heard(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "never"})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			updates.run(ctx)
		}()
		t.Cleanup(func() {
			cancel()
			<-done
		})
		return updates, channel, saved
	}
	// A window of nothing is of the clock of the test, and a slow machine makes it only quieter; a check is waited for.
	quiet := func(channel *channelOfTheTest) []string {
		time.Sleep(100 * time.Millisecond)
		return channel.kinds()
	}
	asked := func(t *testing.T, channel *channelOfTheTest) []string {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for len(channel.kinds()) == 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		return quiet(channel)
	}

	// The welcome answered after the first timer: the next look asks, once.
	updates, channel, saved := running(t, domain.UpdateNSIS)
	if got := quiet(channel); len(got) != 0 {
		t.Fatalf("before «Начать» the backend wrote %v", got)
	}
	if err := saved.DismissWelcome(true); err != nil {
		t.Fatal(err)
	}
	if got := asked(t, channel); !slices.Equal(got, []string{"update-check "}) {
		t.Fatalf("after «Начать» the backend wrote %v", got)
	}
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "offline"})
	if got := quiet(channel); len(got) != 1 {
		t.Fatalf("after the outcome the backend wrote %v", got)
	}

	// «Начать» with the check off asks nothing; the switch turned on asks within a minute, once.
	updates, channel, saved = running(t, domain.UpdateAppImage)
	if err := saved.DismissWelcome(false); err != nil {
		t.Fatal(err)
	}
	if got := quiet(channel); len(got) != 0 {
		t.Fatalf("with the check off the backend wrote %v", got)
	}
	if err := updates.KeepDaily(true); err != nil {
		t.Fatal(err)
	}
	if got := asked(t, channel); !slices.Equal(got, []string{"update-check "}) {
		t.Fatalf("after the check was turned on the backend wrote %v", got)
	}

	// A build updated by hand has no schedule.
	_, channel, saved = running(t, domain.UpdateNone)
	if err := saved.DismissWelcome(true); err != nil {
		t.Fatal(err)
	}
	if got := quiet(channel); len(got) != 0 {
		t.Fatalf("a build updated by hand: the backend wrote %v", got)
	}
}
