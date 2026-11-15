// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"log"
	"sync"
	"time"

	domain "canonkeeper/backend/internal/domain/desktop"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	service "canonkeeper/backend/internal/services/desktop"
)

// updater is an update of the app as the backend holds it (specs/2026-11-13-desk-updates.md UB1, UB2.1): the schedule and
// the setting of settings.json, the state the shell reports in update-state, and the wait for the turns of the chat
// before an installation. It asks the shell only by the version the shell reported; the addresses, the keys and the files
// are the shell's.
type updater struct {
	send     func(ControlMessage) error
	settings *settings
	version  string
	kind     domain.UpdateKind
	gate     *turnGate
	now      func() time.Time
	logger   *log.Logger

	mu    sync.Mutex
	shell domain.ShellUpdate
	// install: the author asked to install; once the file is downloaded and checked the update waits for the turns. A file
	// is downloaded only to be installed, so a backend started again hears it from the phase the shell reports; canceled
	// holds the cancel until the shell answers it.
	install  bool
	canceled bool
	// turns counts the chat turns of the local author still in work, once compose built it.
	turns func(context.Context) (int, error)
	wake  chan struct{}
}

func newUpdater(send func(ControlMessage) error, saved *settings, version string, kind domain.UpdateKind, logger *log.Logger) *updater {
	return &updater{
		send: send, settings: saved, version: version, kind: kind, gate: &turnGate{}, now: time.Now, logger: logger,
		wake: make(chan struct{}, 1),
	}
}

// useTurns sets the count of the turns of the chat.
func (updates *updater) useTurns(turns func(context.Context) (int, error)) {
	updates.mu.Lock()
	defer updates.mu.Unlock()
	updates.turns = turns
}

// heard takes an update-state of the shell (UB1.1): after every change and after every ready.
func (updates *updater) heard(message ControlMessage) {
	updates.mu.Lock()
	if message.Phase == "install_failed" && updates.shell.Phase != "install_failed" {
		// UB2.7: why the installation did not happen, once in the log of the run.
		updates.logger.Printf(`{"event":"desktop_update_install_failed","cause":%q}`, message.Cause)
	}
	updates.shell = domain.ShellUpdate{
		Phase: message.Phase, Found: message.Found, Version: message.Version, Notes: message.Notes,
		Percent: message.Percent, Refusal: message.Refusal, Package: message.Package,
	}
	switch {
	case message.Phase == "downloaded" && updates.gate.isClosed():
		// UB2.1, UB2.9: asked to install, the shell answers with the file still downloaded — it did not take the
		// installation (an erasure is marked, the window is leaving): new turns are taken again, and the backend does not
		// ask once more by itself.
		updates.install = false
		updates.gate.open()
	case message.Phase == "downloading" || message.Phase == "downloaded":
		updates.install = !updates.canceled
	case message.Phase == "installing":
		updates.install = false
	default:
		// The download, the check or the installation ended without the program changing: new turns are taken again.
		updates.install, updates.canceled = false, false
		updates.gate.open()
	}
	updates.mu.Unlock()
	select {
	case updates.wake <- struct{}{}:
	default:
	}
	if smokeInstall && message.Phase == "idle" && message.Found == "available" {
		if err := updates.Install(context.Background()); err != nil {
			updates.logger.Print(`{"event":"desktop_update_smoke_install_not_asked"}`)
		}
	}
}

// smokeInstall: the smoke build of an update in CI (updates_smoke.go, the build tag smokeupdate) presses «Обновить и
// перезапустить» itself once the shell reports a later version — no page is driven there. Every other build leaves it
// false: an update is installed only by the author.
var smokeInstall bool

// Update is getAppUpdate.
func (updates *updater) Update() domain.AppUpdate {
	updates.mu.Lock()
	defer updates.mu.Unlock()
	return updates.state()
}

func (updates *updater) state() domain.AppUpdate {
	phase, found := domain.UpdateOf(updates.shell, updates.kind, updates.gate.isClosed())
	update := domain.AppUpdate{
		Version: updates.version, Daily: updates.settings.Daily(), Kind: updates.kind, Phase: phase, Found: found,
		Percent: updates.shell.Percent, Refusal: domain.UpdateRefusal(updates.shell.Refusal), Package: updates.shell.Package,
		CheckedAt: updates.settings.Checked(),
	}
	if updates.kind != domain.UpdateNone && updates.shell.Version != "" {
		update.Available = &domain.UpdateRelease{Version: updates.shell.Version, Notes: updates.shell.Notes}
	}
	return update
}

// Check asks the shell for a new version now: «Проверить сейчас», and the daily check.
func (updates *updater) Check(context.Context) error {
	updates.mu.Lock()
	defer updates.mu.Unlock()
	if !domain.MayCheck(updates.state().Phase, updates.kind) {
		return domain.ErrUpdateState
	}
	return updates.check()
}

func (updates *updater) check() error {
	if err := updates.send(ControlMessage{Type: messageUpdateCheck}); err != nil {
		return err
	}
	updates.shell.Phase = "checking"
	if err := updates.settings.keepChecked(updates.now()); err != nil {
		updates.logger.Printf(`{"event":"desktop_update_check_not_kept","error":%q}`, err.Error())
	}
	return nil
}

// Install is «Обновить и перезапустить» (UB2.1): the shell downloads and checks the file first; the turns of the chat are
// taken meanwhile.
func (updates *updater) Install(context.Context) error {
	updates.mu.Lock()
	defer updates.mu.Unlock()
	if !domain.MayInstall(updates.state()) {
		return domain.ErrUpdateState
	}
	if err := updates.send(ControlMessage{Type: messageUpdateDownload, Version: updates.shell.Version}); err != nil {
		return err
	}
	updates.install, updates.canceled = true, false
	updates.shell.Phase, updates.shell.Percent = "downloading", 0
	return nil
}

// Cancel stops the download or the wait for the turns; an installation the backend already asked for goes on.
func (updates *updater) Cancel(context.Context) error {
	updates.mu.Lock()
	defer updates.mu.Unlock()
	if !domain.MayCancel(updates.state().Phase) {
		return domain.ErrUpdateState
	}
	if err := updates.send(ControlMessage{Type: messageUpdateCancel, Version: updates.shell.Version}); err != nil {
		return err
	}
	updates.install, updates.canceled = false, true
	return nil
}

// KeepDaily turns the daily check on or off.
func (updates *updater) KeepDaily(daily bool) error {
	return updates.settings.KeepDaily(daily)
}

// run is the schedule (UB1.2) and the wait for the turns (UB2.1) until ctx ends: the first check half a minute after
// ready, then once a day from the last one — only while the daily check is on and the author closed «Добро пожаловать»;
// a check that is off asks nothing.
func (updates *updater) run(ctx context.Context) {
	if updates.kind == domain.UpdateNone {
		return
	}
	first := time.NewTimer(updateFirstCheck)
	defer first.Stop()
	look := time.NewTicker(updateLook)
	defer look.Stop()
	turns := time.NewTicker(updateTurnsLook)
	defer turns.Stop()
	started := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			started = true
			updates.daily()
		case <-look.C:
			if started {
				updates.daily()
			}
		case <-turns.C:
			updates.whenNoTurns(ctx)
		case <-updates.wake:
			updates.whenNoTurns(ctx)
		}
	}
}

// daily asks for a new version when a day passed since the last check, and at once while the shell knows nothing yet in
// this run (specs/2026-11-15-desk-updates-web.md UW1.5): the shell keeps a found version only until it stops, so a start,
// «Начать» and the check turned on ask again. It asks only from idle: a check over a phase of a failure would wipe its
// words and the path of its package — the failure stays until the author acts or the app starts again.
func (updates *updater) daily() {
	welcome, daily := updates.settings.Welcome()
	if welcome || !daily {
		return
	}
	updates.mu.Lock()
	defer updates.mu.Unlock()
	state := updates.state()
	// A build updated by hand has no schedule: run does not start for it.
	if state.Phase != domain.UpdateIdle {
		return
	}
	unknown := state.Found == domain.UpdateNeverChecked
	if checked := updates.settings.Checked(); !unknown && !checked.IsZero() && updates.now().Sub(checked) < updateDay {
		return
	}
	if err := updates.check(); err != nil {
		updates.logger.Print(`{"event":"desktop_update_check_not_sent"}`)
	}
}

// whenNoTurns asks the shell to install once the file is downloaded and checked and no turn of the chat is in work: the
// count and the closing of the gate of new turns are one step under the lock of the gate (UB2.1). The notes of the
// release are kept with its version first (UB2.8).
func (updates *updater) whenNoTurns(ctx context.Context) {
	updates.mu.Lock()
	defer updates.mu.Unlock()
	if !updates.install || updates.shell.Phase != "downloaded" || updates.gate.isClosed() || updates.turns == nil {
		return
	}
	count := updates.turns
	countCtx, cancel := context.WithTimeout(ctx, updateTurnsWithin)
	defer cancel()
	closed, err := updates.gate.closeWhenIdle(countCtx, count)
	if err != nil {
		updates.logger.Print(`{"event":"desktop_update_turns_not_counted"}`)
		return
	}
	if !closed {
		return
	}
	if err := updates.settings.keepNotes(updates.shell.Version, updates.shell.Notes); err != nil {
		updates.logger.Printf(`{"event":"desktop_update_notes_not_kept","error":%q}`, err.Error())
	}
	if err := updates.send(ControlMessage{Type: messageUpdateInstall, Version: updates.shell.Version}); err != nil {
		updates.gate.open()
		updates.logger.Print(`{"event":"desktop_update_install_not_sent"}`)
		return
	}
	updates.logger.Print(`{"event":"desktop_update_install_asked"}`)
}

// network is where the updates take the app (ST4.3, UB1.6): the hosts of the shell's list, for a build that updates itself.
//
// The hosts stand while the daily check is on and «Добро пожаловать» is closed, or while a check or a download goes — one the
// author started himself included (specs/2026-11-14-desk-journal.md JR4.2).
func (updates *updater) network() []service.NetworkUse {
	if updates.kind == domain.UpdateNone {
		return nil
	}
	welcome, daily := updates.settings.Welcome()
	updates.mu.Lock()
	phase := updates.state().Phase
	updates.mu.Unlock()
	if (welcome || !daily) && phase != domain.UpdateChecking && phase != domain.UpdateDownloading {
		return nil
	}
	return []service.NetworkUse{
		{Host: "canonkeeper.ru", Purpose: "узнать о новой версии приложения и скачать её"},
		{Host: "github.com", Purpose: "узнать о новой версии и скачать её, если сайт не ответил"},
		{Host: "release-assets.githubusercontent.com", Purpose: "скачать файл новой версии из выпусков на GitHub"},
	}
}

// turnGate admits the new turns of the chat (UB2.1): an update closes it by the same step that finds no turn in work.
type turnGate struct {
	mu     sync.RWMutex
	closed bool
}

// Admit lets a new turn in until done; a closed gate refuses with researchdomain.ErrUpdating.
func (gate *turnGate) Admit() (func(), error) {
	gate.mu.RLock()
	if gate.closed {
		gate.mu.RUnlock()
		return nil, researchdomain.ErrUpdating
	}
	return gate.mu.RUnlock, nil
}

// closeWhenIdle counts the turns in work while no new one can come in and closes the gate when there is none.
func (gate *turnGate) closeWhenIdle(ctx context.Context, count func(context.Context) (int, error)) (bool, error) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	running, err := count(ctx)
	if err != nil {
		return false, err
	}
	if running > 0 {
		return false, nil
	}
	gate.closed = true
	return true, nil
}

func (gate *turnGate) open() {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.closed = false
}

func (gate *turnGate) isClosed() bool {
	gate.mu.RLock()
	defer gate.mu.RUnlock()
	return gate.closed
}
