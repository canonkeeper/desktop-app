package sagamemory

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// The story time line over chapter cards (specs/2026-10-29-chapter-cards-quality.md, UC5).

// spanDays converts the units of a Span to days.
var spanDays = map[string]float64{"минута": 1.0 / 1440, "час": 1.0 / 24, "день": 1, "неделя": 7, "месяц": 30, "год": 365}

// Days is the span in days; a nil span is no time.
func (span *Span) Days() float64 {
	if span == nil {
		return 0
	}
	return span.N * spanDays[span.Unit]
}

// StoryDay places one chapter on the story time line: the day its action starts and ends,
// counted from the start of the first chapter, and how many spans up to its start and up to its
// end were unknown (counted as no time) or estimated by the card model. A chapter off the main
// line takes no time of it.
type StoryDay struct {
	Start, End                    float64
	UnknownAtStart, ApproxAtStart int
	Unknown, Approx               int
	OffLine                       bool
}

// offLineNote is a time note that declares the whole chapter off the main line of the story: an
// afterword or a foreword of the author, another storyline, a recollection, a dream. A note that
// only mentions them («прерывается воспоминаниями», «чередуется игра и реальность») does not.
var offLineNote = regexp.MustCompile(`^(?:вся глава[^.;:]{0,20}|глава[^.;:]{0,15}|целиком[^.;:]{0,10}|это\s+)?\s*(?:—\s*)?` +
	`(?:друг\p{L}* (?:сюжетн\p{L}* )?лини|параллельн\p{L}* лини|воспоминани|сон(?:\P{L}|$)|сновидени|послеслови|предислови|` +
	`внесюжетн|авторск\p{L}* (?:послеслови|обращени|предислови|примечани))`)

// OffLine reports whether the chapter's time note declares the chapter off the main line: its
// spans are another time, and its facts are history.
func (when *ChapterTime) OffLine() bool {
	return when != nil && offLineNote.MatchString(Normalize(strings.TrimSpace(when.Note)))
}

// StoryDays adds up the chapters' times in reading order: a chapter starts when the previous one
// ends plus the time before it and ends after its length (R5.1). The first chapter has no time
// before it; any other chapter without a card or without a span adds an unknown span; a chapter
// off the main line adds nothing.
func StoryDays(chapters int, cards map[int]*Card) []StoryDay {
	days := make([]StoryDay, chapters)
	var at float64
	unknown, approx := 0, 0
	add := func(span *Span) {
		if span == nil {
			unknown++
			return
		}
		at += span.Days()
		if span.Approx {
			approx++
		}
	}
	for i := range days {
		var when ChapterTime
		if card := cards[i]; card != nil && card.When != nil {
			when = *card.When
		}
		if when.OffLine() {
			days[i] = StoryDay{Start: at, End: at, UnknownAtStart: unknown, ApproxAtStart: approx, Unknown: unknown, Approx: approx, OffLine: true}
			continue
		}
		if i > 0 {
			add(when.After)
		}
		start, unknownAtStart, approxAtStart := at, unknown, approx
		add(when.Lasts)
		days[i] = StoryDay{Start: start, End: at, UnknownAtStart: unknownAtStart, ApproxAtStart: approxAtStart, Unknown: unknown, Approx: approx}
	}
	return days
}

// FormatDays writes an amount of story time the way a reader estimates it.
func FormatDays(days float64) string {
	switch {
	case days >= 365:
		return "≈ " + decimal(days/365) + " года"
	case days >= 60:
		return fmt.Sprintf("≈ %.0f мес.", days/30)
	case days >= 2:
		return fmt.Sprintf("≈ %.0f дн.", days)
	case days >= 1.0/24:
		return fmt.Sprintf("≈ %.0f ч", math.Round(days*24))
	default:
		return fmt.Sprintf("≈ %.0f мин", math.Round(days*1440))
	}
}

func decimal(value float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", value), ".", ",", 1)
}
