// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func ownWith(t *testing.T, chat, cards string, window, limit int) Own {
	t.Helper()
	settings, err := Draft{Source: "own", Own: &OwnDraft{
		Preset: "custom", BaseURL: ptr("https://gateway.example.test/v1"), Protocol: ptr("chat"), ChatModel: ptr(chat), CardsModel: ptr(cards),
		Advanced: AdvancedDraft{ContextWindow: &window, MaxOutput: &limit},
	}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	return *settings.Own
}

func ptr[T any](value T) *T { return &value }

// specs/2026-11-13-desk-probes.md PB2.3: the chat is probed 1–3, the reading of the books 1, 3, 5 and 4; one model in both
// roles is probed once, the short probes first, the calibration before the long answer and the long answer last; without
// a calibration the long answer is read as far as 12 000 characters.
func TestTheChecksProbeEachRoleOnceAndAModelOnce(t *testing.T) {
	same := Plan(ownWith(t, "north", "north", 128_000, 16_000))
	want := []Step{
		{ProbeShort, RoleChat, "north", 0}, {ProbeTool, RoleChat, "north", 0}, {ProbeLongInput, RoleChat, "north", 0},
		{ProbeCalibrate, RoleBooks, "north", 0}, {ProbeLongOutput, RoleBooks, "north", 12_000},
	}
	if !reflect.DeepEqual(same, want) {
		t.Fatalf("one model = %+v", same)
	}
	two := Plan(ownWith(t, "north-chat", "north-cards", 128_000, 16_000))
	want = []Step{
		{ProbeShort, RoleChat, "north-chat", 0}, {ProbeShort, RoleBooks, "north-cards", 0}, {ProbeTool, RoleChat, "north-chat", 0},
		{ProbeLongInput, RoleChat, "north-chat", 0}, {ProbeLongInput, RoleBooks, "north-cards", 0},
		{ProbeCalibrate, RoleBooks, "north-cards", 0}, {ProbeLongOutput, RoleBooks, "north-cards", 12_000},
	}
	if !reflect.DeepEqual(two, want) {
		t.Fatalf("two models = %+v", two)
	}
}

// PB2.3 (4), (5): the long answer is read as far as 4 000 tokens by the calibration of its model — 300 tokens for 1 000
// characters make 13 334 characters; another probe and no calibration stay as they are.
func TestTheCalibrationSaysHowMuchOfALongAnswerIsEnough(t *testing.T) {
	long := Step{Probe: ProbeLongOutput, Role: RoleBooks, Model: "north", Enough: 12_000}
	if got := long.Calibrated(300).Enough; got != 13_334 {
		t.Fatalf("by 300 tokens = %d", got)
	}
	if got := long.Calibrated(420).Enough; got != 9_524 {
		t.Fatalf("by 420 tokens = %d", got)
	}
	if got := long.Calibrated(0); got != long {
		t.Fatalf("without a calibration = %+v", got)
	}
	if short := (Step{Probe: ProbeShort, Role: RoleChat, Model: "north"}); short.Calibrated(300) != short {
		t.Fatal("the calibration changed a short probe")
	}
}

// PB2.3, PB2.4: what each probe reads in an answer — a word, the call of the probe by its schema, the fact of the start, a
// long answer that did not end at its limit — and what a failure of the provider means for it. A long answer is its text:
// the tokens of the reasoning do not make it long, and one closed once it had enough characters is long.
func TestEachProbeJudgesItsAnswer(t *testing.T) {
	long := strings.Repeat("Летопись острова. ", 800)
	enough := Step{Probe: ProbeLongOutput, Enough: 13_334}
	for _, check := range []struct {
		what   string
		probe  Probe
		answer Answer
		passed bool
		reason Reason
	}{
		{"a word", ProbeShort, Answer{Text: "Готово."}, true, ""},
		{"nothing", ProbeShort, Answer{Text: "  "}, false, ReasonOther},
		{"no connection", ProbeShort, Answer{Failure: FailureUnreachable}, false, ReasonUnreachable},
		{"a refused key", ProbeShort, Answer{Failure: FailureKey}, false, ReasonKeyRefused},
		{"no such model", ProbeShort, Answer{Failure: FailureModel}, false, ReasonNoModel},
		{"a wait", ProbeLongOutput, Answer{Failure: FailureRate}, false, ReasonRateLimited},
		{"a refused request", ProbeShort, Answer{Failure: FailureRefused}, false, ReasonOther},
		{"the call", ProbeTool, Answer{Calls: []Call{{Name: ProbeTool.Name(), Arguments: `{"word":"маяк","count":3}`}}}, true, ""},
		{"a call of another word", ProbeTool, Answer{Calls: []Call{{Name: ProbeTool.Name(), Arguments: `{"word":"берег","count":5}`}}}, true, ""},
		{"no call", ProbeTool, Answer{Text: `{"word":"маяк","count":3}`}, false, ReasonNoTools},
		{"arguments not by the schema", ProbeTool, Answer{Calls: []Call{{Name: ProbeTool.Name(), Arguments: `{"word":"маяк","count":"три"}`}}}, false, ReasonNoTools},
		{"a fraction", ProbeTool, Answer{Calls: []Call{{Name: ProbeTool.Name(), Arguments: `{"word":"маяк","count":3.5}`}}}, false, ReasonNoTools},
		{"another function", ProbeTool, Answer{Calls: []Call{{Name: "other", Arguments: `{"word":"маяк","count":3}`}}}, false, ReasonNoTools},
		{"tools refused", ProbeTool, Answer{Failure: FailureRefused}, false, ReasonNoTools},
		{"the fact", ProbeLongInput, Answer{Text: "Сорок семь бакенов"}, true, ""},
		{"the end only", ProbeLongInput, Answer{Text: "Не знаю, в тексте про рыбаков."}, false, ReasonStartOnly},
		{"a text too long for it", ProbeLongInput, Answer{Failure: FailureRefused}, false, ReasonStartOnly},
		{"a long answer", ProbeLongOutput, Answer{Text: "…", Usage: &Usage{Input: 200, Output: 6_200}}, true, ""},
		{"a long answer without usage", ProbeLongOutput, Answer{Text: long}, true, ""},
		{"cut at the limit", ProbeLongOutput, Answer{Text: long, Cut: true, Usage: &Usage{Output: 6_000}}, false, ReasonCutAnswer},
		{"a short answer", ProbeLongOutput, Answer{Text: "Глава 1.", Usage: &Usage{Input: 200, Output: 900}}, false, ReasonCutAnswer},
		{"an answer mostly reasoned", ProbeLongOutput, Answer{Text: "Глава 1.", Usage: &Usage{Output: 4_500, Reasoning: 3_000}}, false, ReasonCutAnswer},
		{"a long answer after reasoning", ProbeLongOutput, Answer{Text: long, Usage: &Usage{Output: 7_500, Reasoning: 3_000}}, true, ""},
		{"the calibration", ProbeCalibrate, Answer{Text: "Готово"}, true, ""},
	} {
		passed, reason := Judge(Step{Probe: check.probe}, check.answer)
		if passed != check.passed || reason != check.reason {
			t.Errorf("%s: %v %q, want %v %q", check.what, passed, reason, check.passed, check.reason)
		}
	}
	if passed, reason := Judge(enough, Answer{Text: strings.Repeat("ж", 13_334)}); !passed {
		t.Errorf("a long answer closed once it had enough = %v %q", passed, reason)
	}
	if passed, reason := Judge(enough, Answer{Text: strings.Repeat("ж", 13_333)}); passed || reason != ReasonCutAnswer {
		t.Errorf("an answer one character short of enough = %v %q", passed, reason)
	}
}

// PB2.4: the first probe that fails on the connection or the key stops the others, and so does the term (PB2.1).
func TestAConnectionOrAKeyStopsTheCheck(t *testing.T) {
	for reason, stops := range map[Reason]bool{
		ReasonUnreachable: true, ReasonKeyRefused: true, ReasonSlow: true, ReasonNoModel: false, ReasonNoTools: false, ReasonRateLimited: false,
		ReasonOther: false,
	} {
		if reason.Stops() != stops {
			t.Errorf("%s stops = %v", reason, reason.Stops())
		}
	}
}

// PB2.3 (5): the calibration is what the text of the calibration cost more than the short probe of the same model, for
// 1 000 characters; without usage there is none.
func TestTheCalibrationCountsTheTokensOfTheText(t *testing.T) {
	short := Answer{Text: "Готово", Usage: &Usage{Input: 120, Output: 2}}
	extra := int64(CalibrationCharacters()) * 3 / 10
	calibrate := Answer{Text: "Готово", Usage: &Usage{Input: 120 + extra, Output: 2}}
	value, ok := Calibration(short, calibrate)
	if !ok || value < 299.9 || value > 300.1 {
		t.Fatalf("the calibration = %v %v", value, ok)
	}
	if _, ok := Calibration(Answer{Text: "Готово"}, calibrate); ok {
		t.Fatal("a calibration without the usage of the short probe")
	}
	if _, ok := Calibration(short, Answer{Text: "Готово", Usage: &Usage{Input: 100}}); ok {
		t.Fatal("a calibration of less than nothing")
	}
}

// PB2.4: the chat fits when probes 1–3 of its model passed and its window is at least 64 000 — thin under 128 000; the
// reading of the books fits when probes 1, 3 and 4 of its model passed and its limit of an answer is at least 16 000. A
// failed probe names the reason, a probe the check did not reach the reason that stopped it.
func TestTheVerdictsReadTheProbesAndTheSettings(t *testing.T) {
	own := ownWith(t, "north", "north", 128_000, 16_000)
	passed := func(probe Probe, role Role) Result {
		return Result{Probe: probe, Role: role, Model: "north", Passed: true}
	}
	all := Report{At: time.Now(), Checked: CheckedOf(own), Results: []Result{
		passed(ProbeShort, RoleChat), passed(ProbeTool, RoleChat), passed(ProbeLongInput, RoleChat),
		passed(ProbeCalibrate, RoleBooks), passed(ProbeLongOutput, RoleBooks),
	}}
	if chat, books := all.Chat(own.Advanced), all.Books(own.Advanced); chat != (Verdict{Fit: FitYes}) || books != (Verdict{Fit: FitYes}) {
		t.Fatalf("all passed = %+v %+v", chat, books)
	}
	for window, want := range map[int]Verdict{
		32_000: {FitNo, ReasonSmallWindow}, 100_000: {FitThin, ReasonSmallWindow}, 64_000: {FitThin, ReasonSmallWindow}, 200_000: {Fit: FitYes},
	} {
		if got := all.Chat(ownWith(t, "north", "north", window, 16_000).Advanced); got != want {
			t.Errorf("the window %d = %+v", window, got)
		}
	}
	if got := all.Books(ownWith(t, "north", "north", 128_000, 8_000).Advanced); got != (Verdict{FitNo, ReasonSmallLimit}) {
		t.Errorf("the limit 8 000 = %+v", got)
	}

	noTools := all
	noTools.Results = append([]Result(nil), all.Results...)
	noTools.Results[1] = Result{Probe: ProbeTool, Role: RoleChat, Model: "north", Reason: ReasonNoTools}
	if chat, books := noTools.Chat(own.Advanced), noTools.Books(own.Advanced); chat != (Verdict{FitNo, ReasonNoTools}) || books != (Verdict{Fit: FitYes}) {
		t.Fatalf("no tools = %+v %+v", chat, books)
	}

	stopped := Report{Checked: CheckedOf(own), Results: []Result{{Probe: ProbeShort, Role: RoleChat, Model: "north", Reason: ReasonKeyRefused}}}
	if chat, books := stopped.Chat(own.Advanced), stopped.Books(own.Advanced); chat != (Verdict{FitNo, ReasonKeyRefused}) || books != (Verdict{FitNo, ReasonKeyRefused}) {
		t.Fatalf("a refused key = %+v %+v", chat, books)
	}

	two := ownWith(t, "north-chat", "north-cards", 128_000, 16_000)
	cut := Report{Checked: CheckedOf(two), Results: []Result{
		{Probe: ProbeShort, Role: RoleChat, Model: "north-chat", Passed: true}, {Probe: ProbeShort, Role: RoleBooks, Model: "north-cards", Passed: true},
		{Probe: ProbeTool, Role: RoleChat, Model: "north-chat", Passed: true}, {Probe: ProbeLongInput, Role: RoleChat, Model: "north-chat", Passed: true},
		{Probe: ProbeLongInput, Role: RoleBooks, Model: "north-cards", Reason: ReasonStartOnly},
		{Probe: ProbeLongOutput, Role: RoleBooks, Model: "north-cards", Reason: ReasonCutAnswer},
	}}
	if chat, books := cut.Chat(two.Advanced), cut.Books(two.Advanced); chat != (Verdict{Fit: FitYes}) || books != (Verdict{FitNo, ReasonStartOnly}) {
		t.Fatalf("two models = %+v %+v", chat, books)
	}

	// PB2.1: the term ran out during the long answer — the chat is known, the reading of the books is slow; and during the
	// long text of the chat — what it did not reach is slow too.
	late := Report{Checked: CheckedOf(own), Results: append(append([]Result(nil), all.Results[:4]...),
		Result{Probe: ProbeLongOutput, Role: RoleBooks, Model: "north", Reason: ReasonSlow})}
	if chat, books := late.Chat(own.Advanced), late.Books(own.Advanced); chat != (Verdict{Fit: FitYes}) || books != (Verdict{FitNo, ReasonSlow}) {
		t.Fatalf("late in the long answer = %+v %+v", chat, books)
	}
	earlier := Report{Checked: CheckedOf(own), Results: append(append([]Result(nil), all.Results[:2]...),
		Result{Probe: ProbeLongInput, Role: RoleChat, Model: "north", Reason: ReasonSlow})}
	if chat, books := earlier.Chat(own.Advanced), earlier.Books(own.Advanced); chat != (Verdict{FitNo, ReasonSlow}) || books != (Verdict{FitNo, ReasonSlow}) {
		t.Fatalf("late in the long text = %+v %+v", chat, books)
	}
}

// PB2.5: a check is stale once the address, the protocol, the model of a role or the key changed after it — not the window,
// the limit or the prices, which its verdict reads as they are now.
func TestACheckIsStaleOnceWhatItCheckedChanged(t *testing.T) {
	own := ownWith(t, "north-chat", "north-cards", 128_000, 16_000)
	report := Report{Checked: CheckedOf(own)}
	settings := Settings{Source: SourceOwn, Own: &own}
	if report.Stale(settings) {
		t.Fatal("a fresh check is stale")
	}
	changed := func(change func(*Own)) Settings {
		copied := own
		change(&copied)
		return Settings{Source: SourceOwn, Own: &copied}
	}
	for what, settings := range map[string]Settings{
		"the address":         changed(func(own *Own) { own.BaseURL = "https://other.example.test/v1" }),
		"the protocol":        changed(func(own *Own) { own.Protocol = ProtocolResponses }),
		"the model of a chat": changed(func(own *Own) { own.ChatModel = "north-chat-2" }),
		"the model of books":  changed(func(own *Own) { own.CardsModel = "north-cards-2" }),
		"no profile":          {Source: SourceNone},
	} {
		if !report.Stale(settings) {
			t.Errorf("%s did not make the check stale", what)
		}
	}
	if report.Stale(changed(func(own *Own) { own.Advanced.ContextWindow = 32_000; own.Advanced.MaxOutput = 2_000 })) {
		t.Fatal("the window made the check stale")
	}
	keyed := report
	keyed.KeyChanged = true
	if !keyed.Stale(settings) {
		t.Fatal("a new key did not make the check stale")
	}
}
