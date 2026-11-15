// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"net/http"

	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/observability"
)

// unexpected answers 500 to an error no operation of the window names and reports its class to the journal of the
// request — the context, the state of the database or the stage of a failure, never the text of the error: a 500 the
// journal says nothing about cannot be found afterwards.
func unexpected(w http.ResponseWriter, r *http.Request, err error) {
	observability.ReportUnexpectedError(r.Context(), err)
	httpapi.WriteProblem(w, "", http.StatusInternalServerError, "Internal Server Error", "unexpected error", "server.error")
}
