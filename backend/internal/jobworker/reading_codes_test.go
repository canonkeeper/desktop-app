package jobworker

import (
	"testing"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// specs/2026-11-13-desk-consent.md CN3.4; specs/2026-11-15-desk-reading-no-answer.md NA1.1: every stop of the reading ends
// its Job with its own code, and the journal of the Jobs keeps it — «провайдер не отвечает» too.
func TestEveryStopOfTheReadingEndsWithASafeCode(t *testing.T) {
	for _, stop := range []sagamemory.Stop{
		sagamemory.StopFunds, sagamemory.StopKey, sagamemory.StopModel, sagamemory.StopConnection, sagamemory.StopNoAnswer,
		sagamemory.StopPaused, sagamemory.StopCharacters, sagamemory.StopReadingFence, sagamemory.StopUnfit,
		sagamemory.StopUnavailable,
	} {
		if code := stop.Code(); safeTerminalJobErrorCode(code) != code || code == "reading.paused" && stop != sagamemory.StopPaused {
			t.Errorf("the stop %s ends with %q, kept as %q", stop, code, safeTerminalJobErrorCode(code))
		}
	}
}
