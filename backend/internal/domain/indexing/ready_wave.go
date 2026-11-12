package indexing

import (
	"math"
	"time"
)

// The wave of the preparation of a saga (specs/2026-11-09-first-session.md, FS1): it opens with the import of a new
// book and closes once, when the text and the cards of every book are ready and nothing of the saga is still in work.
const (
	// ReadyLetterAfter is how long a wave lasts before its end is worth a letter (FS2.1) and a promise of one (FS5.6).
	ReadyLetterAfter = 2 * time.Minute
	// ReadyShownFor is how long the site tells of a closed wave (FS4.1).
	ReadyShownFor = 7 * 24 * time.Hour
	// PaceWindow is the stretch of the latest cards the estimate of what is left is drawn from (FS5.2).
	PaceWindow = 5 * time.Minute
	// paceCards is the least number of cards in the window that makes a pace.
	paceCards = 3
	// fewChapters is how many chapters without cards are few enough to say their time without a pace — a chapter added
	// from «Письмо», an edited one — and usualCardTime how long one takes with the wait for a worker, on the long side.
	fewChapters   = 3
	usualCardTime = time.Minute
)

// ReadyWave is a wave that has just closed: whose saga, whom to tell and what was read.
type ReadyWave struct {
	ProjectID       string
	ProjectName     string
	RecipientUserID string
	StartedAt       time.Time
	ReadyAt         time.Time
	Books           int
	Chapters        int
}

// Long reports whether the preparation took long enough for the author to have left the page.
func (wave ReadyWave) Long() bool {
	return wave.ReadyAt.Sub(wave.StartedAt) > ReadyLetterAfter
}

// PaceSpan is the stretch the pace of the cards is measured over: the wave so far, at most its last PaceWindow. The
// whole stretch counts, its idle time too, so the estimate errs on the long side.
func PaceSpan(startedAt, now time.Time) time.Duration {
	return min(max(now.Sub(startedAt), 0), PaceWindow)
}

// RemainingSeconds estimates the time of the chapters still without cards from the pace of the latest ones: recent
// cards were written over span. Without a pace — fewer than three cards or no time — a few chapters take their usual
// time each, and more than a few have no number: a book is read as long as it is read.
func RemainingSeconds(recent int, span time.Duration, remaining int) (int, bool) {
	if remaining <= 0 {
		return 0, false
	}
	if recent >= paceCards && span > 0 {
		return int(math.Ceil(span.Seconds() / float64(recent) * float64(remaining))), true
	}
	if remaining <= fewChapters {
		return remaining * int(usualCardTime.Seconds()), true
	}
	return 0, false
}
