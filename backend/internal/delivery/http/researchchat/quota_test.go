package researchchat

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
)

// R1.6, R2.1–R2.2: a refused question is 429 with its quota code; the daily refusal says when
// the counter resets, the active one does not.
func TestChatErrorsMapQuotaRefusals(t *testing.T) {
	rec := httptest.NewRecorder()
	writeServiceError(rec, quota.DailyExceeded(quota.KeyChatTurnsPerDay, 30, time.Now()))
	retryAfter, _ := strconv.Atoi(rec.Header().Get("Retry-After"))
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), `"code":"quota.chat_turns_daily"`) ||
		retryAfter < 1 || retryAfter > 24*60*60 {
		t.Fatalf("daily: status = %d retry = %q body = %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
	rec = httptest.NewRecorder()
	writeServiceError(rec, &quota.Exceeded{Key: quota.KeyActiveTurns, Limit: 2})
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), `"code":"quota.active_turns"`) ||
		rec.Header().Get("Retry-After") != "" {
		t.Fatalf("active: status = %d retry = %q body = %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
}
