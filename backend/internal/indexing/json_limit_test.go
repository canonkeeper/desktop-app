package indexing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"

	"github.com/go-chi/chi/v5"
)

// SEC-15 (specs/2026-10-28-security-hardening.md, R2.1): starting preparation reads at most 64 KiB
// of its JSON body. The padding is legal JSON whitespace, so only the size decides.
func TestHandlersStartIndexingReadsAtMostTheJSONBodyLimit(t *testing.T) {
	service := &startIndexingServiceForTest{}
	currentUser := &currentUserForIndexingHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
	router := chi.NewRouter()
	NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

	body := `{` + strings.Repeat(" ", 64<<10) + `"sourceView":"draft"}`
	req := httptest.NewRequest(http.MethodPost, "/projects/project-1/indexing-runs", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	req.Header.Set("Idempotency-Key", "indexing-1")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if len(service.calls) != 0 {
		t.Fatalf("service was called for a body over the limit: %#v", service.calls)
	}
}
