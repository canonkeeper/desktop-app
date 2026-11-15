package httpapi

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/observability"
)

// WriteQuotaProblem answers a quota refusal (specs/2026-10-28-writer-quotas-and-fair-queue.md
// R1.6): 429 for questions and uploads — with Retry-After until 00:00 Moscow time for a daily
// limit — and 409 for projects, books, chapters and text. It reports false for any other error.
func WriteQuotaProblem(w http.ResponseWriter, err error, now time.Time) bool {
	if errors.Is(err, quota.ErrDemoSaga) {
		// The demo saga is out of the limits and takes no text (specs/2026-11-09-demo-saga.md DS3.3).
		w.Header().Set("Cache-Control", "no-store")
		WriteProblem(w, "", http.StatusConflict, "Conflict", "the demo saga takes no text", quota.DemoSagaCode)
		return true
	}
	if writeCounterProblem(w, err, now) {
		return true
	}
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

// writeCounterProblem answers a refusal of the counter of a site — the desktop app with the helper of its account
// (specs/2026-11-13-desk-account-ai.md AI2.1, AI2.6) — at the start of a turn, a correction or an upload of materials: the
// counter not linked is «помощник не подключён»; a counter that did not answer twice is ai.account_unreachable with whether
// the site may have counted the unit and gives it back itself once the lease of the permit ends; a refusal the app remembers
// keeps the code of the site — the fence of the day until the Moscow midnight. The site has no counter: nothing changes there.
func writeCounterProblem(w http.ResponseWriter, err error, now time.Time) bool {
	if errors.Is(err, quota.ErrCounterUnlinked) {
		w.Header().Set("Cache-Control", "no-store")
		WriteProblem(w, "", http.StatusConflict, "Conflict", "the helper is not connected", "helper.not_connected")
		return true
	}
	if unreachable, ok := quota.AsCounterUnreachable(err); ok {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(struct {
			Problem
			MayReturn bool `json:"mayReturn"`
		}{Problem{
			Type: "about:blank", Title: "Service Unavailable", Status: http.StatusServiceUnavailable,
			Detail: "the site of the account did not answer", Code: quota.CounterUnreachableCode,
			RequestID: w.Header().Get(observability.RequestIDHeader),
		}, unreachable.MayReturn})
		return true
	}
	refusal, ok := quota.AsCounterRefusal(err)
	if !ok {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if refusal.Code == quota.CounterDayFenceCode {
		seconds := int64(math.Ceil(quota.NextMoscowMidnight(now).Sub(now).Seconds()))
		w.Header().Set("Retry-After", strconv.FormatInt(max(seconds, 1), 10))
		WriteProblem(w, "", http.StatusTooManyRequests, "Too Many Requests", "the work of the day of the app is over", refusal.Code)
		return true
	}
	WriteProblem(w, "", http.StatusConflict, "Conflict", "the site of the account does not take this work", refusal.Code)
	return true
}
