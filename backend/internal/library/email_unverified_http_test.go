package library

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
)

// R1.4.2: an unverified writer's import or queue start is 403 auth.email_unverified.
func TestImportErrorsMapUnverifiedEmail(t *testing.T) {
	rec := httptest.NewRecorder()
	writeImportServiceError(rec, httptest.NewRequest(http.MethodPost, "/", nil), fmt.Errorf("start: %w", auth.ErrEmailNotVerified))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"auth.email_unverified"`) {
		t.Fatalf("status = %d body = %s, want 403 auth.email_unverified", rec.Code, rec.Body.String())
	}
}
