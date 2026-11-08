package codex

import (
	"encoding/json"
	"testing"

	"canonkeeper/backend/internal/domain/agent"

	"github.com/openai/codex/sdk/go/protocol"
)

// R4.1.1: every codexErrorInfo variant of the UC4.1 table gets its failure class; the rest has none.
func TestResultFromTurnClassifiesModelFailures(t *testing.T) {
	for _, test := range []struct {
		info string
		want agent.RunFailure
	}{
		{`"usageLimitExceeded"`, agent.RunFailureUsageLimitExceeded},
		{`"serverOverloaded"`, agent.RunFailureServerOverloaded},
		{`"internalServerError"`, agent.RunFailureServerOverloaded},
		{`"rateLimitExceeded"`, agent.RunFailureServerOverloaded},
		// Codex 0.165.0: no capacity on the route (specs/2026-10-28-codex-0165.md UC4).
		{`"policyCapacityUnavailable"`, agent.RunFailureServerOverloaded},
		{`"flexUnavailable"`, agent.RunFailureServerOverloaded},
		{`"modelNotFound"`, ""},
		{`"tooManyDenials"`, ""},
		{`{"httpConnectionFailed":{"httpStatusCode":502}}`, agent.RunFailureConnectionFailed},
		{`{"httpConnectionFailed":{"httpStatusCode":null}}`, agent.RunFailureConnectionFailed},
		{`{"responseStreamConnectionFailed":{"httpStatusCode":503}}`, agent.RunFailureConnectionFailed},
		{`{"responseStreamDisconnected":{"httpStatusCode":null}}`, agent.RunFailureConnectionFailed},
		{`{"responseTooManyFailedAttempts":{"httpStatusCode":429}}`, agent.RunFailureConnectionFailed},
		{`"contextWindowExceeded"`, agent.RunFailureContextWindowExceeded},
		{`"sessionBudgetExceeded"`, agent.RunFailureContextWindowExceeded},
		{`"badRequest"`, ""},
		{`"other"`, ""},
		{`{"activeTurnNotSteerable":{"turnKind":"review"}}`, ""},
		{`"futureProviderClass"`, ""},
		{``, ""},
	} {
		t.Run(test.info, func(t *testing.T) {
			turnError := protocol.TurnError{Message: "localized provider message that must not be parsed"}
			if test.info != "" {
				var info protocol.CodexErrorInfo
				if err := json.Unmarshal([]byte(test.info), &info); err != nil {
					t.Fatal(err)
				}
				turnError.CodexErrorInfo = protocol.Some(info)
			}
			turn := testTurn("turn-failed", "job-turn", protocol.TurnStatusFailed)
			turn.Error = protocol.Some(turnError)

			if result := resultFromTurn(turn); result.Failure != test.want {
				t.Fatalf("failure = %q, want %q", result.Failure, test.want)
			}
		})
	}
}
