package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"canonkeeper/backend/internal/domain/quota"
)

// WriteQuotaProblem answers a quota refusal (specs/2026-10-28-writer-quotas-and-fair-queue.md
// R1.6): 429 for questions and uploads — with Retry-After until 00:00 Moscow time for a daily
// limit — and 409 for projects, books, chapters and text. It reports false for any other error.
func WriteQuotaProblem(w http.ResponseWriter, err error, now time.Time) bool {
	exceeded, ok := quota.AsExceeded(err)
	if !ok {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	status, title := http.StatusConflict, "Conflict"
	switch exceeded.Key {
	case quota.KeyChatTurnsPerDay, quota.KeyImportsPerDay, quota.KeyImagesPerDay, quota.KeyImageUploadsPerDay, quota.KeyChapterDraftsPerDay, quota.KeyDrawingTurnsPerDay, quota.KeyDraftTurnsPerDay, quota.KeyDraftRewritesPerDay, quota.KeyActiveTurns:
		status, title = http.StatusTooManyRequests, "Too Many Requests"
	}
	if exceeded.ResetsAt != nil {
		seconds := int64(math.Ceil(exceeded.ResetsAt.Sub(now).Seconds()))
		w.Header().Set("Retry-After", strconv.FormatInt(max(seconds, 1), 10))
	}
	WriteProblem(w, "", status, title, "writer quota exceeded", exceeded.Code())
	return true
}
