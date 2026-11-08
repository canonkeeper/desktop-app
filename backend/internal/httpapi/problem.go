package httpapi

import (
	"encoding/json"
	"net/http"

	"canonkeeper/backend/internal/observability"
)

type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Code      string `json:"code"`
	RequestID string `json:"requestId"`
}

func WriteProblem(w http.ResponseWriter, requestID string, status int, title, detail, code string) {
	if requestID == "" {
		requestID = w.Header().Get(observability.RequestIDHeader)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{
		Type:      "about:blank",
		Title:     title,
		Status:    status,
		Detail:    detail,
		Code:      code,
		RequestID: requestID,
	})
}
