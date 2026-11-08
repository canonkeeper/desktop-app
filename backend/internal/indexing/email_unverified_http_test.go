package indexing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
)

// R1.4.2: an unverified writer's reindexing is 403 auth.email_unverified.
func TestIndexingErrorsMapUnverifiedEmail(t *testing.T) {
	rec := httptest.NewRecorder()
	writeIndexingServiceError(rec, context.Background(), auth.ErrEmailNotVerified)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"auth.email_unverified"`) {
		t.Fatalf("status = %d body = %s, want 403 auth.email_unverified", rec.Code, rec.Body.String())
	}
}
