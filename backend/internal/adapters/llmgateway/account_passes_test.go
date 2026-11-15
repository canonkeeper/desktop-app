// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/appwork"
	"canonkeeper/backend/internal/domain/coresteps"
)

// stepOfTheAccount sends a step of the reading to the gateway whose site refuses it with the provider's status, and answers
// the reason the client of the core hears and the stops the consent heard.
func stepOfTheAccount(t *testing.T, step coresteps.Name, status int) (int, string, []string) {
	t.Helper()
	stand, account := accountStand(t)
	reading := &readingOfTheTest{}
	stand.gateway.UseReading(reading)
	account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: status, Code: appwork.CodeProviderRefused}}}
	response, body := stand.post(t, coreBody(t, step), map[string]string{modelapi.JobHeader: "job-cards"})
	reason := ""
	if at := strings.Index(body, `"reason":"`); at >= 0 {
		reason = strings.SplitN(body[at+len(`"reason":"`):], `"`, 2)[0]
	}
	reading.mu.Lock()
	defer reading.mu.Unlock()
	return response.StatusCode, reason, append([]string(nil), reading.stops...)
}

// specs/2026-11-15-desk-reading-queue.md RQ3.1: the provider of the site answered 408, 409, 423 or 425 — a state that passes,
// which a repeat cures: on a step of the reading it is the failure of the reading on the site the helper of the account knows —
// the pause «недоступно», which goes on by itself —, not a refusal of the request: no reason «refused», so no mark of the names
// and no words «запрос не принят»; on a request of a turn it goes to Codex with its status, which Codex repeats, and the reason
// heard is the failure of the helper, not a lasting one. 400, 413 and 422 stay refusals of the request.
func TestARefusalOfAStateThatPassesIsAFailureOfTheAccount(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusConflict, http.StatusLocked, http.StatusTooEarly} {
		for _, step := range []coresteps.Name{coresteps.Card, coresteps.Names} {
			if code, reason, stops := stepOfTheAccount(t, step, status); code != appwork.Lasting || reason != "unavailable" || len(stops) != 1 || stops[0] != "unavailable" {
				t.Errorf("%d on the step %s = %d, the reason %q, the stops %v; want the pause «недоступно»", status, step, code, reason, stops)
			}
		}
		stand, account := accountStand(t)
		account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: status, Code: appwork.CodeProviderRefused}}}
		response, body := stand.post(t, chatBody(""), map[string]string{"thread-id": "thread-1"})
		account.mu.Lock()
		heard := append([]appwork.Answer(nil), account.refused...)
		account.mu.Unlock()
		if response.StatusCode != status || !strings.Contains(body, appwork.CodeProviderRefused) || len(heard) != 1 || heard[0].Reason != appwork.ReasonFailed || heard[0].Remember != appwork.RememberNothing {
			t.Errorf("%d on a turn = %d %s, heard %+v; want its status for Codex to repeat and the failure of the helper", status, response.StatusCode, body, heard)
		}
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity} {
		if code, reason, stops := stepOfTheAccount(t, coresteps.Names, status); code != status || reason != "refused" || len(stops) != 0 {
			t.Errorf("%d on the step of the names = %d, the reason %q, the stops %v; want the refusal of the request", status, code, reason, stops)
		}
	}
}

// specs/2026-11-15-desk-reading-queue.md RQ3.1: what a repeat cures is one rule of both helpers, aisettings.Passes — a 4xx of a
// state that passes is no refusal of the request of «Свой ИИ» and a failure of the reading of the helper of the account; a 4xx
// «Свой ИИ» counts a refusal of the request is a refusal of the request of the helper of the account too.
func TestWhatARepeatCuresIsOneRuleOfBothHelpers(t *testing.T) {
	passing := 0
	for status := 400; status < 500; status++ {
		own := aisettings.Classify(status, "", "", false)
		switch {
		case aisettings.Passes(status):
			passing++
			if own != aisettings.FailureOther {
				t.Errorf("%d: «Свой ИИ» counts %q; want a failure, not a refusal", status, own)
			}
			if _, reason, _ := stepOfTheAccount(t, coresteps.Card, status); reason != "unavailable" {
				t.Errorf("%d: the helper of the account says %q; want the pause «недоступно»", status, reason)
			}
		case own == aisettings.FailureRefused:
			if _, reason, _ := stepOfTheAccount(t, coresteps.Card, status); reason != "refused" {
				t.Errorf("%d: «Свой ИИ» refuses the request, the helper of the account says %q", status, reason)
			}
		}
	}
	if passing != 4 {
		t.Fatalf("%d statuses pass; want 408, 409, 423 and 425", passing)
	}
}

// specs/2026-11-15-desk-reading-queue.md RQ3.1 on the binary of the set: a refusal of the provider of the site of a state that
// passes, on a request of a turn, is repeated by Codex — the turn answers once the site takes the repeat.
func TestCodexRepeatsATurnRefusalOfAStateThatPasses(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusConflict, http.StatusLocked, http.StatusTooEarly} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			stand, account := accountStand(t)
			account.works["job-of-the-turn"] = JobWork{Source: appwork.SourceAccount, Kind: appwork.KindTurn, PermitID: turnPermitMark, ChatID: "chat-1"}
			account.answers = []siteAnswer{{refusal: &appwork.Refusal{Status: status, Code: appwork.CodeProviderRefused}}, {text: turnAnswer}}
			client := runtimeChat(t, stand, 400_000, fewerRepeats...)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			calls := 0
			result, err := client.Run(ctx, turnRequest(stand, &calls))
			if asked, _, _ := account.heard(); err != nil || result.Status != agent.TurnCompleted || result.FinalResponse != turnAnswer || len(asked) != 2 {
				t.Fatalf("%d on a turn = %+v %v after %d requests; want the repeat answered", status, result.Status, err, len(asked))
			}
		})
	}
}
