// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	domain "canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/aisettings"
)

// A 500 of the window leaves the class of its error in the journal of the request — what broke, never the text of the
// error: without it a failure of an operation of the window cannot be found afterwards.
func TestAnUnexpectedErrorOfTheWindowIsNamedInTheJournalOfTheRequest(t *testing.T) {
	settings := &settingsOfTheTest{settings: domain.None()}
	keys := &keysOfTheTest{values: map[string]string{}}
	for cause, failure := range map[string]error{
		`"errorCause":"deadline_exceeded"`: context.DeadlineExceeded,
		`"errorClass":"internal"`:          errors.New("the text of an error with a secret-looking-word"),
	} {
		var journal bytes.Buffer
		broken := windowOfTheAIWith(service.Read{Settings: settings, Keys: keys, Stops: stopsOfTheTest{err: failure}}, settings, keys)
		window := httpapi.RequestLogger(&journal)(broken)
		if status, answer, _ := askTheWindow(window, http.MethodGet, "/desktop/ai", "", "cookie-of-the-window"); status != http.StatusInternalServerError || answer["code"] != "server.error" {
			t.Fatalf("a failed count = %d %v", status, answer)
		}
		if line := journal.String(); !strings.Contains(line, cause) || strings.Contains(line, "secret-looking-word") {
			t.Fatalf("the journal of the request of a 500: want %s and no text of the error, got %s", cause, line)
		}
	}
}
