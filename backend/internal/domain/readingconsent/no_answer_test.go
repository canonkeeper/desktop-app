// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"net/http"
	"testing"

	"canonkeeper/backend/internal/domain/aisettings"
)

// specs/2026-11-15-desk-reading-no-answer.md NA1.1, NA1.2; specs/2026-11-13-desk-consent.md CN3.4: the one table of the
// refusals of the provider. The funds — an exhausted quota too —, the key and the model pause at once; no connection, a wait
// the provider asks for and a failure of the provider — a 5xx, a stream torn mid-answer — only on the last attempt of the
// client, each with its own reason; a refusal of the request stops nothing — it marks its chapter. The helper of the account
// knows no such pause.
func TestAProviderThatDoesNotAnswerPausesTheReadingOnTheLastAttempt(t *testing.T) {
	for _, row := range []struct {
		name    string
		failure aisettings.Failure
		last    bool
		pause   Pause
	}{
		{"402", aisettings.Classify(http.StatusPaymentRequired, "", "Insufficient Balance", false), false, PauseFunds},
		{"an exhausted quota", aisettings.Classify(http.StatusTooManyRequests, "insufficient_quota", "", false), false, PauseFunds},
		{"401", aisettings.Classify(http.StatusUnauthorized, "", "", false), false, PauseKey},
		{"no such model", aisettings.Classify(http.StatusNotFound, "model_not_found", "The model north does not exist", false), false, PauseModel},
		{"no connection before the last attempt", aisettings.Classify(0, "connection_error", "", true), false, ""},
		{"no connection on the last attempt", aisettings.Classify(0, "connection_error", "", true), true, PauseConnection},
		{"503 before the last attempt", aisettings.Classify(http.StatusServiceUnavailable, "upstream_error", "overloaded", false), false, ""},
		{"503 on the last attempt", aisettings.Classify(http.StatusServiceUnavailable, "upstream_error", "overloaded", false), true, PauseNoAnswer},
		{"a wait before the last attempt", aisettings.Classify(http.StatusTooManyRequests, "rate_limit_exceeded", "slow down", false), false, ""},
		{"a wait on the last attempt", aisettings.Classify(http.StatusTooManyRequests, "rate_limit_exceeded", "slow down", false), true, PauseNoAnswer},
		{"a torn stream on the last attempt", aisettings.Classify(0, "stream_incomplete", "upstream stream disconnected", false), true, PauseNoAnswer},
		{"a refusal of the request on the last attempt", aisettings.Classify(http.StatusBadRequest, "context_length_exceeded", "too long", false), true, ""},
	} {
		pause, stops := PauseOf(row.failure, row.last)
		if stops != (row.pause != "") || stops && pause != row.pause {
			t.Errorf("%s (%s, last %v) = %q, %v; want %q", row.name, row.failure, row.last, pause, stops, row.pause)
		}
	}
	if !PauseNoAnswer.Valid() {
		t.Fatal("the pause «провайдер не отвечает» is no reason of the list")
	}
	if pause, stops := PauseOfAccount(string(PauseNoAnswer)); stops {
		t.Fatalf("the helper of the account pauses at %q", pause)
	}
}
