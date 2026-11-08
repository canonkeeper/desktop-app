package httpapi

import (
	"io"
	"net/http"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/observability"
)

func RequestLogger(out io.Writer) func(http.Handler) http.Handler {
	return observability.RequestLogger(out, observability.RequestLoggerOptions{
		RouteInfo:     RequestRouteInfo,
		PanicResponse: writePanicProblem,
	})
}

// writePanicProblem отвечает на панику обработчика тем же problem, что и на любую неожиданную ошибку.
func writePanicProblem(w http.ResponseWriter, requestID governance.RequestID) {
	WriteProblem(w, string(requestID), http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
}
