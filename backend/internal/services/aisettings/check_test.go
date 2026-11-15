// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/aisettings"
)

// fakeProber answers each step as answer says, or waits for the end of its context when block is set — for every probe,
// or for the probe blockOn names.
type fakeProber struct {
	mu      sync.Mutex
	asked   []domain.Step
	checks  []string
	answer  func(domain.Step) domain.Answer
	block   bool
	blockOn domain.Probe
	ended   chan struct{}
}

func (prober *fakeProber) Probe(ctx context.Context, check string, step domain.Step) (domain.Answer, error) {
	prober.mu.Lock()
	prober.asked = append(prober.asked, step)
	prober.checks = append(prober.checks, check)
	block := prober.block || prober.blockOn == step.Probe
	prober.mu.Unlock()
	if block {
		<-ctx.Done()
		if prober.ended != nil {
			close(prober.ended)
		}
		return domain.Answer{}, ctx.Err()
	}
	return prober.answer(step), nil
}

func (prober *fakeProber) steps() []domain.Step {
	prober.mu.Lock()
	defer prober.mu.Unlock()
	return append([]domain.Step(nil), prober.asked...)
}

// passing answers every probe as a model that fits both roles does, with usage.
func passing(step domain.Step) domain.Answer {
	switch step.Probe {
	case domain.ProbeTool:
		return domain.Answer{Calls: []domain.Call{{Name: domain.ProbeTool.Name(), Arguments: `{"word":"маяк","count":3}`}}, Usage: &domain.Usage{Input: 150, Output: 12}}
	case domain.ProbeLongInput:
		return domain.Answer{Text: "Сорок семь бакенов", Usage: &domain.Usage{Input: 20_000, Output: 6}}
	case domain.ProbeLongOutput:
		return domain.Answer{Text: strings.Repeat("Глава. ", 3000), Usage: &domain.Usage{Input: 140, Output: 7_000}}
	case domain.ProbeCalibrate:
		return domain.Answer{Text: "Готово", Usage: &domain.Usage{Input: 100 + int64(domain.CalibrationCharacters())*3/10, Output: 2}}
	default:
		return domain.Answer{Text: "Готово", Usage: &domain.Usage{Input: 100, Output: 2}}
	}
}

type keptReports struct {
	mu      sync.Mutex
	report  *domain.Report
	written int
}

func (kept *keptReports) Report() (domain.Report, bool) {
	kept.mu.Lock()
	defer kept.mu.Unlock()
	if kept.report == nil {
		return domain.Report{}, false
	}
	return *kept.report, true
}

func (kept *keptReports) KeepReport(report domain.Report) error {
	kept.mu.Lock()
	defer kept.mu.Unlock()
	kept.report = &report
	kept.written++
	return nil
}

// connected are the settings of a connected helper: one model, or two.
func connected(t *testing.T, chat, cards string) *keptSettings {
	t.Helper()
	settings, err := domain.Draft{Source: "own", Own: &domain.OwnDraft{
		Preset: "custom", BaseURL: ptr("https://gateway.example.test/v1"), Protocol: ptr("chat"), ChatModel: ptr(chat), CardsModel: ptr(cards),
	}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	return &keptSettings{settings: settings}
}

func ptr[T any](value T) *T { return &value }

// waitFor reads the check until it leaves running.
func waitFor(t *testing.T, checks *Checks) Check {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if check := checks.Read(); check.State != CheckRunning {
			return check
		}
		if time.Now().After(deadline) {
			t.Fatal("the check did not end")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func checksOf(settings *keptSettings, prober *fakeProber, reports *keptReports) *Checks {
	return &Checks{
		Settings: settings, Keys: &heldKeys{values: map[string]string{}}, Prober: prober, Reports: reports, Limit: time.Minute, Now: time.Now,
	}
}

// specs/2026-11-13-desk-probes.md PB2.1, PB2.3–PB2.5: the check starts and answers at once, probes each step once, ends
// «годится для чата и для чтения книг» with the calibration, and keeps its report with the models it checked.
func TestACheckThatPassesKeepsItsReport(t *testing.T) {
	settings := connected(t, "north", "north")
	prober := &fakeProber{answer: passing}
	reports := &keptReports{}
	checks := checksOf(settings, prober, reports)
	if check := checks.Read(); check.State != CheckIdle {
		t.Fatalf("before a check = %+v", check)
	}
	started, err := checks.Start(context.Background())
	if err != nil || started.State != CheckRunning || started.StartedAt.IsZero() {
		t.Fatalf("start = %+v, %v", started, err)
	}
	check := waitFor(t, checks)
	if check.State != CheckDone || check.Report == nil || len(check.Results) != 5 {
		t.Fatalf("the check = %+v", check)
	}
	if steps := prober.steps(); len(steps) != 5 {
		t.Fatalf("the probes = %+v", steps)
	}
	view := check.Report
	if view.Chat != (domain.Verdict{Fit: domain.FitYes}) || view.Books != (domain.Verdict{Fit: domain.FitYes}) || view.Stale ||
		view.Report.Calibration == nil || *view.Report.Calibration < 299.9 || *view.Report.Calibration > 300.1 {
		t.Fatalf("the report = %+v", view)
	}
	kept, ok := reports.Report()
	if !ok || reports.written != 1 || kept.Checked.ChatModel != "north" || kept.At.IsZero() {
		t.Fatalf("the kept report = %+v", kept)
	}
	read := Read{Settings: settings, Keys: &heldKeys{values: map[string]string{}}, Reports: reports}
	if state := stateOf(t, read); state.Check == nil || state.Check.Chat.Fit != domain.FitYes || state.Check.Stale {
		t.Fatalf("getAiSettings = %+v", state.Check)
	}
	settings.settings.Own.ChatModel = "north-2"
	if state := stateOf(t, read); state.Check == nil || !state.Check.Stale {
		t.Fatalf("getAiSettings after a new model = %+v", state.Check)
	}
}

// PB2.3 (4), (5): two models — the calibration is the calibrate probe of the model of the books against the short probe of
// that model, not of the chat, and the long answer is read as far as 4 000 tokens by it; every probe goes once, the
// calibration before the long answer and the long answer last.
func TestTwoModelsAreCalibratedByTheModelOfTheBooks(t *testing.T) {
	extra := int64(domain.CalibrationCharacters()) * 3 / 10
	prober := &fakeProber{answer: func(step domain.Step) domain.Answer {
		switch {
		case step.Probe == domain.ProbeShort && step.Model == "north-chat":
			return domain.Answer{Text: "Готово", Usage: &domain.Usage{Input: 140, Output: 2}}
		case step.Probe == domain.ProbeShort:
			return domain.Answer{Text: "Готово", Usage: &domain.Usage{Input: 90, Output: 2}}
		case step.Probe == domain.ProbeCalibrate:
			return domain.Answer{Text: "Готово", Usage: &domain.Usage{Input: 90 + extra, Output: 2}}
		}
		return passing(step)
	}}
	reports := &keptReports{}
	checks := checksOf(connected(t, "north-chat", "north-cards"), prober, reports)
	if _, err := checks.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	check := waitFor(t, checks)
	if check.State != CheckDone || check.Report == nil || check.Report.Chat.Fit != domain.FitYes || check.Report.Books.Fit != domain.FitYes {
		t.Fatalf("the check = %+v", check)
	}
	if calibration := check.Report.Report.Calibration; calibration == nil || *calibration < 299.9 || *calibration > 300.1 {
		t.Fatalf("the calibration = %v", calibration)
	}
	steps := prober.steps()
	want := []domain.Step{
		{Probe: domain.ProbeShort, Role: domain.RoleChat, Model: "north-chat"}, {Probe: domain.ProbeShort, Role: domain.RoleBooks, Model: "north-cards"},
		{Probe: domain.ProbeTool, Role: domain.RoleChat, Model: "north-chat"}, {Probe: domain.ProbeLongInput, Role: domain.RoleChat, Model: "north-chat"},
		{Probe: domain.ProbeLongInput, Role: domain.RoleBooks, Model: "north-cards"}, {Probe: domain.ProbeCalibrate, Role: domain.RoleBooks, Model: "north-cards"},
		{Probe: domain.ProbeLongOutput, Role: domain.RoleBooks, Model: "north-cards", Enough: 13_334},
	}
	if len(steps) != len(want) {
		t.Fatalf("the probes = %+v", steps)
	}
	for index := range want {
		if steps[index] != want[index] {
			t.Errorf("probe %d = %+v, want %+v", index, steps[index], want[index])
		}
	}
}

// PB2.1, PB2.4: without the helper there is no check; a refused key stops the probes after the first and is kept.
func TestACheckNeedsTheHelperAndStopsOnTheKey(t *testing.T) {
	prober := &fakeProber{answer: func(domain.Step) domain.Answer { return domain.Answer{Failure: domain.FailureKey} }}
	reports := &keptReports{}
	none := &keptSettings{settings: domain.None()}
	if _, err := checksOf(none, prober, reports).Start(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("without the helper = %v", err)
	}
	deepseek, err := domain.Draft{Source: "own", Own: &domain.OwnDraft{Preset: "deepseek", ChatModel: ptr("a"), CardsModel: ptr("b")}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checksOf(&keptSettings{settings: deepseek}, prober, reports).Start(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("DeepSeek without its key = %v", err)
	}
	if len(prober.steps()) != 0 {
		t.Fatal("a probe went without the helper")
	}

	checks := checksOf(connected(t, "north-chat", "north-cards"), prober, reports)
	if _, err := checks.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	check := waitFor(t, checks)
	if check.State != CheckDone || len(check.Results) != 1 || len(prober.steps()) != 1 || check.Report == nil ||
		check.Report.Chat != (domain.Verdict{Fit: domain.FitNo, Reason: domain.ReasonKeyRefused}) ||
		check.Report.Books != (domain.Verdict{Fit: domain.FitNo, Reason: domain.ReasonKeyRefused}) || check.Report.Report.Calibration != nil {
		t.Fatalf("a refused key = %+v", check)
	}
}

// PB2.1: one check at a time; cancelAiCheck stops the probe that runs and keeps nothing; a check longer than its term
// stops with that reason and keeps what it learned: the probe the term stopped is slow, the rest are known.
func TestOneCheckAtATimeThatStopsWhenAskedOrLate(t *testing.T) {
	prober := &fakeProber{block: true, ended: make(chan struct{})}
	reports := &keptReports{}
	checks := checksOf(connected(t, "north", "north"), prober, reports)
	if _, err := checks.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := checks.Start(context.Background()); !errors.Is(err, ErrCheckRunning) {
		t.Fatalf("a second check = %v", err)
	}
	for len(prober.steps()) == 0 {
		time.Sleep(time.Millisecond)
	}
	if check := checks.Read(); check.Current == nil || check.Current.Probe != domain.ProbeShort {
		t.Fatalf("the check that runs = %+v", check)
	}
	checks.Cancel()
	select {
	case <-prober.ended:
	case <-time.After(time.Second):
		t.Fatal("the probe did not stop")
	}
	if check := checks.Read(); check.State != CheckCanceled || check.Report != nil || check.Current != nil {
		t.Fatalf("a canceled check = %+v", check)
	}
	if _, kept := reports.Report(); kept {
		t.Fatal("a canceled check kept a report")
	}

	late := &fakeProber{answer: passing, blockOn: domain.ProbeLongOutput}
	checks = checksOf(connected(t, "north", "north"), late, reports)
	checks.Limit = 50 * time.Millisecond
	if _, err := checks.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	check := waitFor(t, checks)
	if check.State != CheckTimedOut || check.Report == nil || check.Report.Chat != (domain.Verdict{Fit: domain.FitYes}) ||
		check.Report.Books != (domain.Verdict{Fit: domain.FitNo, Reason: domain.ReasonSlow}) || len(check.Results) != 5 {
		t.Fatalf("a check late in the long answer = %+v", check)
	}
	if last := check.Results[4]; last.Probe != domain.ProbeLongOutput || last.Passed || last.Reason != domain.ReasonSlow {
		t.Fatalf("the probe the term stopped = %+v", last)
	}
	if kept, ok := reports.Report(); !ok || kept.Calibration == nil || len(kept.Results) != 5 {
		t.Fatalf("a late check kept %+v", kept)
	}
	checks.Stop()
	if _, err := checks.Start(context.Background()); err == nil {
		t.Fatal("a check started after the stop of the process")
	}
}

// PB2.5: a new key makes the kept report stale, and a check during which the key changed is stale at once.
func TestANewKeyMakesTheCheckStale(t *testing.T) {
	settings := connected(t, "north", "north")
	reports := &keptReports{}
	release := make(chan struct{})
	prober := &fakeProber{answer: func(step domain.Step) domain.Answer {
		if step.Probe == domain.ProbeShort {
			<-release
		}
		return passing(step)
	}}
	checks := checksOf(settings, prober, reports)
	keys := &heldKeys{values: map[string]string{}}
	if _, err := checks.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for len(prober.steps()) == 0 {
		time.Sleep(time.Millisecond)
	}
	if err := (SetKey{Keys: keys, Checks: checks}).Execute(context.Background(), "key-test-new-0001"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if check := waitFor(t, checks); check.Report == nil || !check.Report.Stale {
		t.Fatalf("a check during a new key = %+v", check.Report)
	}

	prober.answer = passing
	if _, err := checks.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if check := waitFor(t, checks); check.Report == nil || check.Report.Stale {
		t.Fatalf("a check after the key = %+v", check.Report)
	}
	if err := (DeleteKey{Keys: keys, Checks: checks}).Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if kept, ok := reports.Report(); !ok || !kept.KeyChanged {
		t.Fatalf("the report after the key was removed = %+v", kept)
	}
	if check := checks.Read(); check.Report == nil || !check.Report.Stale {
		t.Fatalf("the check after the key was removed = %+v", check.Report)
	}
}

// specs/2026-11-14-desk-journal.md JR1.7: one press of «Проверить связь» is one number — every probe of the check carries it
// and the journal says its row goes on while it runs —, the next check has another.
func TestEveryProbeOfACheckCarriesItsNumber(t *testing.T) {
	settings := connected(t, "north", "north")
	prober := &fakeProber{answer: passing, blockOn: domain.ProbeLongOutput}
	checks := checksOf(settings, prober, &keptReports{})
	if checks.Running() != "" {
		t.Fatal("a number before any check")
	}
	if _, err := checks.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(prober.steps()) < 4 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	running := checks.Running()
	checks.Cancel()
	waitFor(t, checks)
	prober.mu.Lock()
	prober.blockOn = ""
	prober.mu.Unlock()
	if _, err := checks.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, checks)
	prober.mu.Lock()
	numbers := append([]string(nil), prober.checks...)
	prober.mu.Unlock()
	if running == "" || len(numbers) < 5 || numbers[0] != running {
		t.Fatalf("running %q, numbers %v", running, numbers)
	}
	first, second := 0, 0
	for _, number := range numbers {
		switch number {
		case running:
			first++
		default:
			second++
			if numbers[len(numbers)-1] != number {
				t.Fatalf("the second check has two numbers: %v", numbers)
			}
		}
	}
	if first == 0 || second == 0 || checks.Running() != "" {
		t.Fatalf("first %d, second %d, running %q", first, second, checks.Running())
	}
}
