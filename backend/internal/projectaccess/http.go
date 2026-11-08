package projectaccess

import (
	"net/http"

	"canonkeeper/backend/internal/httpapi"
)

func WriteProblem(w http.ResponseWriter, decision Decision, requestID string) {
	switch decision {
	case DecisionAllowed:
		return
	case DecisionUnauthenticated:
		httpapi.WriteProblem(w, requestID, http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case DecisionNotFound:
		httpapi.WriteProblem(w, requestID, http.StatusNotFound, "Not Found", "resource not found", "not_found")
	case DecisionForbidden:
		httpapi.WriteProblem(w, requestID, http.StatusForbidden, "Forbidden", "access denied", "forbidden")
	default:
		httpapi.WriteProblem(w, requestID, http.StatusInternalServerError, "Internal Server Error", "unexpected authorization decision", "server.error")
	}
}
