package modelapi

import (
	"encoding/json"
	"net/http"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// The refusals of the local gateway of the app (specs/2026-11-13-desk-consent.md CN3.4; specs/2026-11-14-desk-journal.md
// JR1.6): the client of the app reads them by their codes and reasons; the client of the site never meets them.

// gatewayRefusal is the refusal of the local gateway of the app: its code and the reason of the reading it names.
type gatewayRefusal struct {
	Error struct {
		Code   string `json:"code"`
		Reason string `json:"reason"`
	} `json:"error"`
}

// stopOf is the typed stop of a refusal of the gateway (CN3.4): the funds, the key, the model and a refusal of the check
// of the reading are not repeated; no connection is repeated and stops after the last attempt. A refusal the gateway named
// refused is not repeated either (specs/2026-11-15-desk-reading-rest.md RR1.1): the caller gets sagamemory.ErrRefused.
func stopOf(raw []byte) (stop *sagamemory.Stopped, unreachable, refused bool) {
	var refusal gatewayRefusal
	if json.Unmarshal(raw, &refusal) != nil {
		return nil, false, false
	}
	switch {
	case refusal.Error.Code == codeReadingPaused:
		return &sagamemory.Stopped{Stop: sagamemory.StopPaused}, false, false
	case sagamemory.Stop(refusal.Error.Reason).Typed():
		return &sagamemory.Stopped{Stop: sagamemory.Stop(refusal.Error.Reason)}, false, false
	default:
		return nil, refusal.Error.Reason == string(sagamemory.StopConnection), refusal.Error.Reason == reasonRefused
	}
}

// journalCode is the code of a refusal of the journal of the gateway of the app (journal.unavailable, journal.unnamed), or "".
func journalCode(raw []byte) string {
	var refusal gatewayRefusal
	if json.Unmarshal(raw, &refusal) != nil {
		return ""
	}
	switch refusal.Error.Code {
	case disclosure.CodeUnavailable, disclosure.CodeUnnamed:
		return refusal.Error.Code
	default:
		return ""
	}
}

// repeatsInApp says the client of the app repeats a refusal of the gateway of the status: a wait (429), a failure (5xx) and a
// refusal of a state that passes — 408, 409, 423, 425 —, which the table of the gateway counts as no answer, not as a refusal
// of the request (aisettings.Classify; specs/2026-11-15-desk-reading-no-answer.md NA2.1). The statuses that pass are those of
// the one rule aisettings.Passes, which this core adapter of both builds does not import: a test holds the two together
// (specs/2026-11-15-desk-reading-queue.md RQ3.1). The client of the site repeats a wait and a failure alone.
func repeatsInApp(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusLocked, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	default:
		return status >= 500
	}
}

// codeReadingPaused is the refusal of the check of the reading at the gateway of the app; reasonRefused the reason the gateway
// gives a refusal that repeating does not cure (RR1.1).
const (
	codeReadingPaused = "reading.paused"
	reasonRefused     = "refused"
)
