// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	domain "canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/desktop"
	"canonkeeper/backend/internal/domain/modeljournal"
)

// Prober asks the provider of the author one probe the way the work goes: the local gateway. Its error is the end of the
// context — the check stopped; a failure of the provider is in the answer.
type Prober interface {
	// Probe asks one probe of the check whose number it carries (specs/2026-11-14-desk-journal.md JR1.7).
	Probe(ctx context.Context, check string, step domain.Step) (domain.Answer, error)
}

// ReportReader reads the last check of «Свой ИИ» settings.json keeps.
type ReportReader interface {
	Report() (domain.Report, bool)
}

// ReportKeeper keeps the last check in the section ai of settings.json, whole.
type ReportKeeper interface {
	ReportReader
	KeepReport(domain.Report) error
}

// CheckState is where the check of this run of the app is.
type CheckState string

const (
	// CheckIdle: no check since the app started.
	CheckIdle     CheckState = "idle"
	CheckRunning  CheckState = "running"
	CheckDone     CheckState = "done"
	CheckCanceled CheckState = "canceled"
	// CheckTimedOut: the check ran longer than its term and stopped; its report says slow for the probes it did not end.
	CheckTimedOut CheckState = "timed_out"
)

// CheckTime is the term of a check (specs/2026-11-13-desk-probes.md PB2.1).
const CheckTime = 3 * time.Minute

// stopTime bounds the wait for the probe that runs once the check is canceled.
const stopTime = 5 * time.Second

var (
	// ErrNotConnected is a check of a helper that is not connected: no model or no key it needs (PB2.1).
	ErrNotConnected = errors.New("aisettings: the helper is not connected")
	// ErrCheckRunning is a check while another runs.
	ErrCheckRunning = errors.New("aisettings: a check runs")
	// ErrStopped is a check after the app began to stop.
	ErrStopped = errors.New("aisettings: the app stops")
)

// Verdicts is a report with what it says now (PB2.4, PB2.5): its verdicts by the window and the limit as they are, and
// whether what it checked changed since.
type Verdicts struct {
	Report domain.Report
	Chat   domain.Verdict
	Books  domain.Verdict
	Stale  bool
}

// verdictsOf is what a report says with the settings now; nothing without a profile.
func verdictsOf(report domain.Report, settings domain.Settings) *Verdicts {
	if settings.Own == nil {
		return nil
	}
	advanced := settings.Own.Advanced
	return &Verdicts{Report: report, Chat: report.Chat(advanced), Books: report.Books(advanced), Stale: report.Stale(settings)}
}

// Check is what the window reads of the check of this run (getAiCheck): where it is, when it started, the probe that runs,
// the probes that went and, once done, its report.
type Check struct {
	State     CheckState
	StartedAt time.Time
	Current   *domain.Step
	Results   []domain.Result
	Report    *Verdicts
}

// Checks is «Проверить связь» (PB2): one check at a time in the process, by the saved settings and key, no longer than its
// term; it starts at once and runs on its own, the window reads how it goes and may stop it. A check that ends keeps its
// report in settings.json, and so does a late one — the probe the term stopped and those after it failed as slow; a
// canceled one keeps nothing. The probes go through Prober; nothing of their texts or answers is kept or logged.
type Checks struct {
	Settings SettingsReader
	Keys     KeyHolder
	Prober   Prober
	Reports  ReportKeeper
	Limit    time.Duration
	Now      func() time.Time
	// Logger hears that a report could not be kept, never what it holds.
	Logger *log.Logger

	mu      sync.Mutex
	current *checkRun
	stopped bool
}

// checkRun is one check while it runs and after.
type checkRun struct {
	// id is the number of the check: every probe and «Определить» inside it carry it in the journal (JR1.7).
	id         string
	state      CheckState
	startedAt  time.Time
	step       *domain.Step
	results    []domain.Result
	report     *domain.Report
	keyChanged bool
	cancel     context.CancelFunc
	done       chan struct{}
}

// Start is startAiCheck: the helper must be connected and no other check run.
func (checks *Checks) Start(context.Context) (Check, error) {
	settings := checks.Settings.AI()
	if settings.Source == domain.SourceAccount {
		return Check{}, ErrNotForSource
	}
	if !settings.Connected(domain.Held{Key: checks.Keys.Has(desktop.SecretOwnAI)}) {
		return Check{}, ErrNotConnected
	}
	checks.mu.Lock()
	defer checks.mu.Unlock()
	switch {
	case checks.stopped:
		return Check{}, ErrStopped
	case checks.current != nil && checks.current.state == CheckRunning:
		return Check{}, ErrCheckRunning
	}
	own := *settings.Own
	ctx, cancel := context.WithTimeout(context.Background(), checks.Limit)
	run := &checkRun{id: modeljournal.NewID(), state: CheckRunning, startedAt: checks.Now(), cancel: cancel, done: make(chan struct{})}
	checks.current = run
	go checks.execute(ctx, run, own)
	return checks.viewLocked(run, settings), nil
}

// execute runs the probes of the plan one after another (PB2.3): a refused key or no connection stops the rest. The
// calibration is the calibrate probe of the model of the books against its own short probe, and the long answer is read as
// far as 4 000 tokens by it. When the term runs out, the probe that ran and the rest fail as slow and the report is kept with
// what went; a canceled check keeps nothing. The report names what was checked when the check started.
func (checks *Checks) execute(ctx context.Context, run *checkRun, own domain.Own) {
	defer close(run.done)
	defer run.cancel()
	var calibration *float64
	shorts := map[string]domain.Answer{}
	late := false
	for _, step := range domain.Plan(own) {
		if calibration != nil {
			step = step.Calibrated(*calibration)
		}
		checks.mu.Lock()
		run.step = &step
		checks.mu.Unlock()
		answer, err := checks.Prober.Probe(ctx, run.id, step)
		if err != nil || ctx.Err() != nil {
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				checks.end(run)
				return
			}
			late = true
			checks.mu.Lock()
			run.results = append(run.results, domain.Result{Probe: step.Probe, Role: step.Role, Model: step.Model, Reason: domain.ReasonSlow})
			checks.mu.Unlock()
			break
		}
		switch step.Probe {
		case domain.ProbeShort:
			shorts[step.Model] = answer
		case domain.ProbeCalibrate:
			if value, ok := domain.Calibration(shorts[step.Model], answer); ok {
				calibration = &value
			}
		}
		passed, reason := domain.Judge(step, answer)
		checks.mu.Lock()
		run.results = append(run.results, domain.Result{Probe: step.Probe, Role: step.Role, Model: step.Model, Passed: passed, Reason: reason})
		checks.mu.Unlock()
		if !passed && reason.Stops() {
			break
		}
	}
	report := domain.Report{At: checks.Now(), Checked: domain.CheckedOf(own), Calibration: calibration}
	checks.mu.Lock()
	defer checks.mu.Unlock()
	report.Results, report.KeyChanged = append([]domain.Result(nil), run.results...), run.keyChanged
	if err := checks.Reports.KeepReport(report); err != nil && checks.Logger != nil {
		checks.Logger.Print(`{"event":"desktop_ai_check_not_kept"}`)
	}
	run.report, run.state, run.step = &report, CheckDone, nil
	if late {
		run.state = CheckTimedOut
	}
}

// end is a check canceled before its report: by the author or as the app stops.
func (checks *Checks) end(run *checkRun) {
	checks.mu.Lock()
	defer checks.mu.Unlock()
	run.state, run.step = CheckCanceled, nil
}

// Running is the number of the check that runs now, "" when none does: the journal says its row goes on (JR2.1).
func (checks *Checks) Running() string {
	checks.mu.Lock()
	defer checks.mu.Unlock()
	if checks.current == nil || checks.current.state != CheckRunning {
		return ""
	}
	return checks.current.id
}

// Read is getAiCheck.
func (checks *Checks) Read() Check {
	settings := checks.Settings.AI()
	checks.mu.Lock()
	defer checks.mu.Unlock()
	if checks.current == nil {
		return Check{State: CheckIdle}
	}
	return checks.viewLocked(checks.current, settings)
}

func (checks *Checks) viewLocked(run *checkRun, settings domain.Settings) Check {
	check := Check{State: run.state, StartedAt: run.startedAt, Results: append([]domain.Result(nil), run.results...)}
	if run.step != nil {
		step := *run.step
		check.Current = &step
	}
	if run.report != nil {
		check.Report = verdictsOf(*run.report, settings)
	}
	return check
}

// Cancel is cancelAiCheck: the probe that runs stops, and the check keeps nothing.
func (checks *Checks) Cancel() {
	checks.mu.Lock()
	run := checks.current
	checks.mu.Unlock()
	if run == nil {
		return
	}
	run.cancel()
	select {
	case <-run.done:
	case <-time.After(stopTime):
	}
}

// Stop cancels the check that runs as the app stops — before the gateway closes — and takes no new one.
func (checks *Checks) Stop() {
	checks.mu.Lock()
	checks.stopped = true
	checks.mu.Unlock()
	checks.Cancel()
}

// KeyChanged hears that the key changed (PB2.5): the kept report and the check that runs are stale.
func (checks *Checks) KeyChanged(context.Context) {
	checks.mu.Lock()
	defer checks.mu.Unlock()
	if run := checks.current; run != nil {
		run.keyChanged = true
		if run.report != nil {
			run.report.KeyChanged = true
		}
	}
	if report, ok := checks.Reports.Report(); ok && !report.KeyChanged {
		report.KeyChanged = true
		if err := checks.Reports.KeepReport(report); err != nil && checks.Logger != nil {
			checks.Logger.Print(`{"event":"desktop_ai_check_not_kept"}`)
		}
	}
}
