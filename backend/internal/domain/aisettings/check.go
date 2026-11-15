// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// Probe is one probe of «Проверить связь» (specs/2026-11-13-desk-probes.md PB2.3; DA4.4): a short answer, a call of a
// tool, a long text read whole, a long answer given whole and the calibration of the tokens of Russian text.
type Probe string

const (
	ProbeShort      Probe = "short"
	ProbeTool       Probe = "tool"
	ProbeLongInput  Probe = "long_input"
	ProbeLongOutput Probe = "long_output"
	ProbeCalibrate  Probe = "calibrate"
)

// Known says the probe is one of the five.
func (probe Probe) Known() bool {
	switch probe {
	case ProbeShort, ProbeTool, ProbeLongInput, ProbeLongOutput, ProbeCalibrate:
		return true
	}
	return false
}

// Role is the role whose model a probe asks: the chat or the reading of the books.
type Role string

const (
	RoleChat  Role = "chat"
	RoleBooks Role = "books"
)

// What the settings lack for a verdict (PB2.4), beside the reasons of the probes, and what a check that ran out of its term
// says of the probes it did not end.
const (
	// ReasonSmallWindow — the window of the chat is under 64 000 (under 128 000 the chat works, poorer on long sagas).
	ReasonSmallWindow Reason = "small_window"
	// ReasonSmallLimit — the limit of an answer is under 16 000.
	ReasonSmallLimit Reason = "small_limit"
	// ReasonSlow — the provider answers slower than the check may last (PB2.1): the probe the term stopped and those after it
	// did not end.
	ReasonSlow Reason = "slow"
)

// Known says the role is the chat or the books.
func (role Role) Known() bool { return role == RoleChat || role == RoleBooks }

// Known says the reason is of the closed list (PB2.4).
func (reason Reason) Known() bool {
	switch reason {
	case ReasonUnreachable, ReasonKeyRefused, ReasonNoModel, ReasonNoTools, ReasonStartOnly, ReasonCutAnswer, ReasonRateLimited,
		ReasonOther, ReasonSmallWindow, ReasonSmallLimit, ReasonSlow:
		return true
	}
	return false
}

// The bounds of the verdicts (PB2.4) and of a long answer (PB2.3 (4)).
const (
	chatWindowLeast  = 64_000
	chatWindowFull   = 128_000
	booksLimitLeast  = 16_000
	longAnswerTokens = 4_000
	// longAnswerCharacters stands for 4 000 tokens of Russian text when the calibration of the model is not known.
	longAnswerCharacters = 12_000
)

// Stops says the check goes no further (PB2.4): the provider did not answer or refused the key, and every other probe
// would fail the same way; or the term of the check ran out.
func (reason Reason) Stops() bool {
	return reason == ReasonUnreachable || reason == ReasonKeyRefused || reason == ReasonSlow
}

// Step is one probe of a check: what is asked, of which role and of which model.
type Step struct {
	Probe Probe
	Role  Role
	Model string
	// Enough is, for the probe of a long answer, the characters of its text that hold 4 000 tokens (PB2.3 (4)): the probe
	// reads the answer so far and closes it — the rest would only cost the author time. Zero for the other probes.
	Enough int
}

// Plan is the probes of a check (PB2.3): the chat is probed 1–3, the reading of the books 1, 3, 5 and 4; one model in both
// roles is probed once. The short probes go first: the connection and the key stop the others. The calibration goes before
// the long answer, which is read only as far as 4 000 tokens by it (Calibrated), and the long answer goes last: it takes
// most of the time, and when the term runs out during it every other probe has ended.
func Plan(own Own) []Step {
	chat, books := own.ChatModel, own.CardsModel
	step := func(probe Probe, role Role, model string) Step {
		step := Step{Probe: probe, Role: role, Model: model}
		if probe == ProbeLongOutput {
			step.Enough = longAnswerCharacters
		}
		return step
	}
	if chat == books {
		return []Step{
			step(ProbeShort, RoleChat, chat), step(ProbeTool, RoleChat, chat), step(ProbeLongInput, RoleChat, chat),
			step(ProbeCalibrate, RoleBooks, books), step(ProbeLongOutput, RoleBooks, books),
		}
	}
	return []Step{
		step(ProbeShort, RoleChat, chat), step(ProbeShort, RoleBooks, books), step(ProbeTool, RoleChat, chat),
		step(ProbeLongInput, RoleChat, chat), step(ProbeLongInput, RoleBooks, books),
		step(ProbeCalibrate, RoleBooks, books), step(ProbeLongOutput, RoleBooks, books),
	}
}

// Calibrated is the probe of a long answer read as far as 4 000 tokens by the calibration of its model (PB2.3 (4), (5)):
// so many characters of its text. Another probe, or no calibration, stays as it is.
func (step Step) Calibrated(calibration float64) Step {
	if step.Probe == ProbeLongOutput && calibration > 0 {
		step.Enough = int(math.Ceil(longAnswerTokens * 1000 / calibration))
	}
	return step
}

// Call is a call of a tool in an answer.
type Call struct {
	Name      string
	Arguments string
}

// Usage is the tokens of an answer as the provider reported them; Reasoning is the part of Output the model spent
// reasoning, when the provider tells it.
type Usage struct {
	Input, Output, Reasoning int64
}

// Answer is what the provider answered to a probe as the gateway read it. It lives only while the check judges it: it is
// never kept, logged or answered. A long answer the gateway closed once it had enough has its text and no usage.
type Answer struct {
	Failure Failure
	Text    string
	Calls   []Call
	// Cut says the answer ended at the limit of its length.
	Cut   bool
	Usage *Usage
}

// Judge says whether a probe passed and, when not, why (PB2.3, PB2.4). A refusal of the request itself means the tool for
// the probe of the tool and the length for the long text: the short probe went with the same profile. A long answer is its
// text: 4 000 tokens of the output without the reasoning by the usage, or as many characters as the step names without it.
func Judge(step Step, answer Answer) (bool, Reason) {
	probe := step.Probe
	if answer.Failure != FailureNone {
		switch {
		case answer.Failure == FailureRefused && probe == ProbeTool:
			return false, ReasonNoTools
		case answer.Failure == FailureRefused && probe == ProbeLongInput:
			return false, ReasonStartOnly
		}
		return false, answer.Failure.Reason()
	}
	switch probe {
	case ProbeTool:
		if called(answer.Calls) {
			return true, ""
		}
		return false, ReasonNoTools
	case ProbeLongInput:
		if strings.Contains(strings.ToLower(answer.Text), longInputFact) {
			return true, ""
		}
		return false, ReasonStartOnly
	case ProbeLongOutput:
		enough := step.Enough
		if enough <= 0 {
			enough = longAnswerCharacters
		}
		long := utf8.RuneCountInString(answer.Text) >= enough
		if answer.Usage != nil {
			long = answer.Usage.Output-answer.Usage.Reasoning >= longAnswerTokens
		}
		if answer.Cut || !long {
			return false, ReasonCutAnswer
		}
		return true, ""
	default:
		if answer.Cut || strings.TrimSpace(answer.Text) == "" {
			return false, ReasonOther
		}
		return true, ""
	}
}

// called is the call of the function of the probe with arguments by its schema: a word and a whole number.
func called(calls []Call) bool {
	for _, call := range calls {
		if call.Name != ProbeTool.Name() {
			continue
		}
		var arguments struct {
			Word  *string      `json:"word"`
			Count *json.Number `json:"count"`
		}
		decoder := json.NewDecoder(strings.NewReader(call.Arguments))
		decoder.UseNumber()
		if decoder.Decode(&arguments) != nil || arguments.Word == nil || strings.TrimSpace(*arguments.Word) == "" || arguments.Count == nil {
			continue
		}
		if _, err := arguments.Count.Int64(); err == nil {
			return true
		}
	}
	return false
}

// Calibration is the input tokens the provider counts for 1 000 characters of Russian text (PB2.3 (5)): what the probe of
// the calibration cost more than the short probe of the same model — the same words with the text before them — for the
// characters of the text, to a tenth. Without the usage of both there is none.
func Calibration(short, calibrate Answer) (float64, bool) {
	if short.Usage == nil || calibrate.Usage == nil {
		return 0, false
	}
	extra := calibrate.Usage.Input - short.Usage.Input
	if extra <= 0 {
		return 0, false
	}
	return math.Round(float64(extra)*1000/float64(CalibrationCharacters())*10) / 10, true
}

// Result is one probe as a check keeps it: what was asked of which model and how it went — no text of the question or of
// the answer.
type Result struct {
	Probe  Probe
	Role   Role
	Model  string
	Passed bool
	Reason Reason
}

// Checked is what a check checked: the address, the protocol and the models of the roles.
type Checked struct {
	BaseURL    string
	Protocol   Protocol
	ChatModel  string
	CardsModel string
}

// CheckedOf is what a check of the profile checks.
func CheckedOf(own Own) Checked {
	return Checked{BaseURL: own.BaseURL, Protocol: own.Protocol, ChatModel: own.ChatModel, CardsModel: own.CardsModel}
}

// Report is the last check of «Свой ИИ» as settings.json keeps it (PB2.5): when, what it checked, the probes, the
// calibration and whether the key changed since.
type Report struct {
	At          time.Time
	Checked     Checked
	Results     []Result
	Calibration *float64
	KeyChanged  bool
}

// Stale says the check was made before a change of what it checked (PB2.5): the address, the protocol, the model of a role
// or the key. The window, the limit and the prices do not make it stale: its verdicts read them as they are now.
func (report Report) Stale(settings Settings) bool {
	return report.KeyChanged || settings.Own == nil || CheckedOf(*settings.Own) != report.Checked
}

// Fit is a verdict for a role.
type Fit string

const (
	FitYes Fit = "fit"
	// FitThin — the chat works, its answers are poorer on long sagas: the window is under 128 000.
	FitThin Fit = "thin"
	FitNo   Fit = "unfit"
)

// Verdict is how a role fits and why it does not fully.
type Verdict struct {
	Fit    Fit
	Reason Reason
}

// Chat is the verdict of the chat (PB2.4): probes 1–3 of its model passed and its window is at least 64 000, under
// 128 000 thin.
func (report Report) Chat(advanced Advanced) Verdict {
	if reason, ok := report.passed(report.Checked.ChatModel, ProbeShort, ProbeTool, ProbeLongInput); !ok {
		return Verdict{Fit: FitNo, Reason: reason}
	}
	switch {
	case advanced.ContextWindow < chatWindowLeast:
		return Verdict{Fit: FitNo, Reason: ReasonSmallWindow}
	case advanced.ContextWindow < chatWindowFull:
		return Verdict{Fit: FitThin, Reason: ReasonSmallWindow}
	}
	return Verdict{Fit: FitYes}
}

// Books is the verdict of the reading of the books (PB2.4): probes 1, 3 and 4 of its model passed and its limit of an
// answer is at least 16 000.
func (report Report) Books(advanced Advanced) Verdict {
	if reason, ok := report.passed(report.Checked.CardsModel, ProbeShort, ProbeLongInput, ProbeLongOutput); !ok {
		return Verdict{Fit: FitNo, Reason: reason}
	}
	if advanced.MaxOutput < booksLimitLeast {
		return Verdict{Fit: FitNo, Reason: ReasonSmallLimit}
	}
	return Verdict{Fit: FitYes}
}

// passed says the probes of a model passed, or the reason of the first that did not; a probe the check did not reach
// failed for the reason that stopped it: the connection, the key or the term.
func (report Report) passed(model string, probes ...Probe) (Reason, bool) {
	for _, probe := range probes {
		found := false
		for _, result := range report.Results {
			if result.Probe != probe || result.Model != model {
				continue
			}
			found = true
			if !result.Passed {
				return result.Reason, false
			}
		}
		if !found {
			return report.stopper(), false
		}
	}
	return "", true
}

// stopper is the reason that stopped the check, other when nothing did.
func (report Report) stopper() Reason {
	for _, result := range report.Results {
		if !result.Passed && result.Reason.Stops() {
			return result.Reason
		}
	}
	return ReasonOther
}

// ReadingCalibration is the calibration the estimate of the reading may take (specs/2026-11-13-desk-consent.md CN1.4): only
// that of a check that is not stale — it measured the model of the reading the settings name now.
func (report Report) ReadingCalibration(settings Settings) (float64, bool) {
	if report.Calibration == nil || report.Stale(settings) || *report.Calibration <= 0 {
		return 0, false
	}
	return *report.Calibration, true
}
