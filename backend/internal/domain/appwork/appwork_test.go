// SPDX-License-Identifier: AGPL-3.0-only

package appwork

import (
	"strings"
	"testing"
	"time"
)

// specs/2026-11-13-desk-account-ai.md AI2.2, AI4: the ref of a permit is derived from the Job and its number and the salt
// of the app — the same after a restart, never the Job or any word; a correction's from the key of its repeat.
func TestARefIsTheSameAfterARestartAndHoldsNothingOfTheJob(t *testing.T) {
	salt := []byte("salt-of-the-test")
	job := "8a1f6c1e-0000-4000-8000-00000000north"
	first, again := Ref(salt, job, 0), Ref(append([]byte(nil), salt...), job, 0)
	if first != again || len(first) != 64 || strings.Contains(first, job) || strings.Contains(first, "north") {
		t.Fatalf("Ref = %q, again %q", first, again)
	}
	for _, other := range []string{Ref(salt, job, 1), Ref([]byte("other-salt"), job, 0), SteerRef(salt, job), Ref(salt, job+"x", 0)} {
		if other == first {
			t.Fatalf("a ref of other work is %q", other)
		}
	}
	if SteerRef(salt, "record-north") != SteerRef(salt, "record-north") {
		t.Fatal("a repeat of a correction has another ref")
	}
}

// AI2.9: the unit of a question came back only when the site said so and nothing can take it back; a close that did not
// reach the site or a dropped request leave it open.
func TestWhetherTheUnitOfAQuestionCameBack(t *testing.T) {
	returned, kept := &Permit{State: StateClosed, Returned: true}, &Permit{State: StateClosed}
	for _, check := range []struct {
		what    string
		closed  *Permit
		dropped bool
		want    Returned
	}{
		{"returned", returned, false, ReturnedYes},
		{"answered", kept, false, ReturnedNo},
		{"returned, a request dropped", returned, true, ReturnedPending},
		{"the close did not reach the site", nil, false, ReturnedPending},
	} {
		if got := ReturnedOf(check.closed, check.dropped); got != check.want {
			t.Errorf("%s: %s, want %s", check.what, got, check.want)
		}
	}
}

// AI2.5 (Data): every lasting refusal is a status Codex does not repeat with its own reason; ai.busy is waited for; a
// refusal of the provider goes with the site's status; anything else is no connection.
func TestTheTableOfTheGateway(t *testing.T) {
	for code, want := range map[string]Answer{
		CodeDayFence:       {Status: 400, Reason: ReasonDayFence, Remember: RememberDay},
		CodePassInvalid:    {Status: 400, Reason: ReasonUnlinked},
		CodePermitClosed:   {Status: 400, Reason: ReasonPermitEnded},
		CodePermitNotFound: {Status: 400, Reason: ReasonPermitEnded},
		CodeUnavailable:    {Status: 400, Reason: ReasonUnavailable},
		CodeValidation:     {Status: 400, Reason: ReasonUnfit, Remember: RememberRun},
		CodeUnsupported:    {Status: 400, Reason: ReasonUnfit, Remember: RememberRun},
		CodeTooLarge:       {Status: 400, Reason: ReasonTooLarge, Remember: RememberChat},
		CodeBusy:           {Wait: true},
		CodeProviderFailed: {Status: 502, Reason: ReasonFailed},
		"server.error":     {Status: 502, Reason: ReasonUnreachable},
	} {
		status := 0
		if code == CodeProviderFailed {
			status = 502
		}
		if got := AnswerOf(Refusal{Status: status, Code: code}); got != want {
			t.Errorf("%s: %+v, want %+v", code, got, want)
		}
	}
	if got := AnswerOf(Refusal{Status: 429, Code: CodeProviderRefused, RetryAfter: time.Second}); got.Status != 429 || got.Reason != ReasonFailed {
		t.Fatalf("a refusal of the provider: %+v", got)
	}
	if Unreachable().Status != 502 || Unreachable().Reason != ReasonUnreachable {
		t.Fatal("no answer of the site is no connection")
	}
}

// AI2.6: the fence of the day is remembered until the Moscow midnight of the clock, an app that does not fit the site until
// the restart for the work of its kind, a thread too large for its chat only.
func TestTheRefusalsThatRepeatAreRemembered(t *testing.T) {
	refusals := NewRefusals()
	evening := time.Date(2026, 11, 13, 20, 30, 0, 0, time.UTC) // 23:30 in Moscow
	refusals.Heard(RememberDay, string(KindTurn), "chat-north", evening)
	if refusals.Of(string(KindTurn), "chat-south", evening.Add(29*time.Minute)) != CodeDayFence {
		t.Fatal("the fence of the day is not remembered before the midnight")
	}
	if refusals.Of(string(KindTurn), "chat-south", evening.Add(30*time.Minute)) != "" {
		t.Fatal("the fence of the day is remembered after the midnight")
	}
	refusals.Heard(RememberChat, string(KindTurn), "chat-north", evening)
	if refusals.Of(string(KindTurn), "chat-north", evening.Add(time.Hour)) != CodeTooLarge || refusals.Of(string(KindTurn), "chat-south", evening.Add(time.Hour)) != "" {
		t.Fatal("a thread too large is not the refusal of its chat only")
	}
	refusals.Heard(RememberRun, "materials", "", evening)
	if refusals.Of("materials", "", evening.Add(48*time.Hour)) != CodeValidation || refusals.Of(string(KindTurn), "chat-south", evening.Add(time.Hour)) != "" {
		t.Fatal("an app that does not fit is not remembered for its kind only")
	}
}
