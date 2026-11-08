package sagamemory

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// Card is the saga memory of one chapter text (UC2): a detailed summary, in-story time
// markers, characters with aliases, named entities with types and state changes, each
// anchored to the paragraph where it first appears. Cards of the v3 instructions also place the
// chapter on the story time line and list its events and the facts it states
// (specs/2026-10-29-chapter-cards-quality.md); v4 cards also say who knows what and what the chapter
// leaves open (specs/2026-11-01-index-foundation.md, UC2).
type Card struct {
	Summary    string       `json:"summary"`
	When       *ChapterTime `json:"when,omitempty"`
	Events     []Event      `json:"events,omitempty"`
	Time       []string     `json:"time,omitempty"`
	Characters []Mention    `json:"characters,omitempty"`
	Entities   []Mention    `json:"entities,omitempty"`
	Facts      []Fact       `json:"facts,omitempty"`
	Changes    []Change     `json:"changes,omitempty"`
	Knows      []Knowledge  `json:"knows,omitempty"`
	Hooks      []Hook       `json:"hooks,omitempty"`
}

// Knowledge is what the chapter says about what a character knows: Kind is one of узнал, понял,
// предполагает, скрывает, не знает, ошибается.
type Knowledge struct {
	Who  string `json:"who"`
	Kind string `json:"kind,omitempty"`
	What string `json:"what"`
	P    int    `json:"p,omitempty"`
}

// Hook is something the chapter leaves open: an unexplained detail, a hint, a promise, a threat.
type Hook struct {
	What string `json:"what"`
	P    int    `json:"p,omitempty"`
}

// Mention is a character or a named entity of one chapter; P is the paragraph of its
// first mention, 0 when the model gave none.
type Mention struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	Type    string   `json:"type,omitempty"`
	Note    string   `json:"note,omitempty"`
	P       int      `json:"p,omitempty"`
}

// Change is one state change of a character, item or relation in the chapter.
type Change struct {
	Subject string `json:"subject"`
	Change  string `json:"change"`
	P       int    `json:"p,omitempty"`
}

// ChapterTime places a chapter on the story time line: the phrase that opens it in time, the
// time since the events before it and the time its events take when the text gives them, a date
// of the story world and what makes its time special (a recollection, another storyline).
type ChapterTime struct {
	Opens string `json:"opens,omitempty"`
	After *Span  `json:"after,omitempty"`
	Lasts *Span  `json:"lasts,omitempty"`
	Date  string `json:"date,omitempty"`
	Note  string `json:"note,omitempty"`
}

// Span is an amount of story time in one of the TimeUnits; Approx marks an amount the model
// judged from the text rather than read in it.
type Span struct {
	N      float64 `json:"n"`
	Unit   string  `json:"unit"`
	Approx bool    `json:"approx,omitempty"`
}

// TimeUnits are the units of a Span, shortest first.
var TimeUnits = []string{"минута", "час", "день", "неделя", "месяц", "год"}

// Event is one event of the chapter with its participants and the time the text gives it. Key marks
// one of the chapter's main events, which a long range shows; a v4 card lists all its events.
type Event struct {
	Event string   `json:"event"`
	Who   []string `json:"who,omitempty"`
	Time  string   `json:"time,omitempty"`
	P     int      `json:"p,omitempty"`
	Key   bool     `json:"key,omitempty"`
}

// Fact is one value of an attribute of a character or thing the chapter states: «Мирон, возраст,
// 24 года». Past marks a value of the time before the chapter's action (a recollection), which
// is history, not the value at the chapter.
type Fact struct {
	Subject   string `json:"subject"`
	Attribute string `json:"attribute"`
	Value     string `json:"value"`
	P         int    `json:"p,omitempty"`
	Past      bool   `json:"past,omitempty"`
}

// UnmarshalJSON reads a span leniently, since only the saga time line reads it: a number given as
// a string, or a string «3 дня», is read too; anything else — also a span without an amount — leaves
// the span empty, and ParseCard drops an empty span (an unknown time) rather than the card.
func (span *Span) UnmarshalJSON(data []byte) error {
	var text string
	if json.Unmarshal(data, &text) == nil {
		*span = parseSpan(text)
		return nil
	}
	var raw struct {
		N      json.RawMessage `json:"n"`
		Unit   string          `json:"unit"`
		Approx bool            `json:"approx"`
	}
	*span = Span{}
	if json.Unmarshal(data, &raw) != nil || len(raw.N) == 0 || string(raw.N) == "null" {
		return nil
	}
	var number float64
	if json.Unmarshal(raw.N, &number) != nil {
		var digits string
		if json.Unmarshal(raw.N, &digits) != nil {
			return nil
		}
		*span = parseSpan(digits + " " + raw.Unit)
		span.Approx = raw.Approx
		return nil
	}
	*span = Span{N: number, Unit: raw.Unit, Approx: raw.Approx}
	return nil
}

// CardParts names the parts of chapter cards a reader needs; the others are not read, so a call
// decodes only what it prints (specs/2026-10-28-saga-search-in-postgres.md, UC3).
type CardParts struct {
	Summary     bool // summary
	Time        bool // time markers
	Mentions    bool // characters and entities: the glossary
	Changes     bool // state changes
	ChapterTime bool // the chapter's place in story time (v3)
	Events      bool // the chapter's events (v3)
	Facts       bool // facts about characters and things (v3)
	Knows       bool // who knows what (v4)
	Hooks       bool // what the chapter leaves open (v4)
}

// AllCardParts reads whole cards.
var AllCardParts = CardParts{Summary: true, Time: true, Mentions: true, Changes: true, ChapterTime: true, Events: true, Facts: true, Knows: true, Hooks: true}

// Omitted lists the JSON keys of the card parts not read.
func (parts CardParts) Omitted() []string {
	omitted := []string{}
	for _, part := range []struct {
		read bool
		keys []string
	}{
		{parts.Summary, []string{"summary"}}, {parts.Time, []string{"time"}},
		{parts.Mentions, []string{"characters", "entities"}}, {parts.Changes, []string{"changes"}},
		{parts.ChapterTime, []string{"when"}}, {parts.Events, []string{"events"}}, {parts.Facts, []string{"facts"}},
		{parts.Knows, []string{"knows"}}, {parts.Hooks, []string{"hooks"}},
	} {
		if !part.read {
			omitted = append(omitted, part.keys...)
		}
	}
	return omitted
}

const (
	maxCardItems     = 400
	maxCardTimes     = 200
	maxCardAliases   = 30
	maxCardTextRunes = 1000
	maxSummaryRunes  = 12000
)

var ErrModelOutput = errors.New("saga memory: invalid model output")

// ChapterText renders a chapter with numbered paragraphs, the form models read and cite.
func ChapterText(chapter *Chapter) string {
	var builder strings.Builder
	builder.WriteString(chapter.Label())
	if chapter.Key != "" {
		fmt.Fprintf(&builder, " (гл. %s)", chapter.Key)
	}
	builder.WriteString("\n\n")
	for index, paragraph := range chapter.Paragraphs {
		fmt.Fprintf(&builder, "¶%d %s\n", index+1, paragraph.Text)
	}
	return builder.String()
}

// ParseCard extracts the card JSON from model output and normalizes it: blank entries are
// dropped, paragraph numbers outside the chapter become 0 and sizes are bounded. A card
// without a summary is rejected so the builder asks again.
func ParseCard(output string, paragraphs int) (Card, error) {
	card, err := ParseCardAdditions(output, paragraphs)
	if err != nil {
		return Card{}, err
	}
	if card.Summary == "" {
		return Card{}, fmt.Errorf("%w: empty summary", ErrModelOutput)
	}
	return card, nil
}

// ParseCardAdditions reads the answer of the pass over a written card: the same schema and the same
// normalization as a card, but only what the card missed, so no summary is required.
func ParseCardAdditions(output string, paragraphs int) (Card, error) {
	start, end := strings.Index(output, "{"), strings.LastIndex(output, "}")
	if start < 0 || end <= start {
		return Card{}, fmt.Errorf("%w: no JSON object", ErrModelOutput)
	}
	var card Card
	if err := json.Unmarshal([]byte(output[start:end+1]), &card); err != nil {
		return Card{}, fmt.Errorf("%w: %v", ErrModelOutput, err)
	}
	card.Summary = bounded(card.Summary, maxSummaryRunes)
	paragraph := func(p int) int {
		if p < 1 || p > paragraphs {
			return 0
		}
		return p
	}
	times := card.Time[:0]
	for _, value := range card.Time {
		if value = bounded(value, maxCardTextRunes); value != "" && len(times) < maxCardTimes {
			times = append(times, value)
		}
	}
	card.Time = times
	mentions := func(items []Mention) []Mention {
		kept := items[:0]
		for _, item := range items {
			item.Name = bounded(item.Name, maxCardTextRunes)
			if item.Name == "" || len(kept) == maxCardItems {
				continue
			}
			aliases := item.Aliases[:0]
			for _, alias := range item.Aliases {
				if alias = bounded(alias, maxCardTextRunes); alias != "" && alias != item.Name && len(aliases) < maxCardAliases {
					aliases = append(aliases, alias)
				}
			}
			item.Aliases = aliases
			item.Type = strings.ToLower(bounded(item.Type, maxCardTextRunes))
			item.Note = bounded(item.Note, maxCardTextRunes)
			item.P = paragraph(item.P)
			kept = append(kept, item)
		}
		return kept
	}
	card.Characters = mentions(card.Characters)
	card.Entities = mentions(card.Entities)
	changes := card.Changes[:0]
	for _, change := range card.Changes {
		change.Subject = bounded(change.Subject, maxCardTextRunes)
		change.Change = bounded(change.Change, maxCardTextRunes)
		if change.Subject == "" || change.Change == "" || len(changes) == maxCardItems {
			continue
		}
		change.P = paragraph(change.P)
		changes = append(changes, change)
	}
	card.Changes = changes
	card.When = boundedTime(card.When)
	events := card.Events[:0]
	for _, event := range card.Events {
		event.Event = bounded(event.Event, maxCardTextRunes)
		if event.Event == "" || len(events) == maxCardItems {
			continue
		}
		who := event.Who[:0]
		for _, name := range event.Who {
			if name = bounded(name, maxCardTextRunes); name != "" && len(who) < maxCardAliases {
				who = append(who, name)
			}
		}
		event.Who, event.Time, event.P = who, bounded(event.Time, maxCardTextRunes), paragraph(event.P)
		events = append(events, event)
	}
	card.Events = events
	facts := card.Facts[:0]
	for _, fact := range card.Facts {
		fact.Subject = bounded(fact.Subject, maxCardTextRunes)
		fact.Attribute = strings.ToLower(bounded(fact.Attribute, maxCardTextRunes))
		fact.Value = bounded(fact.Value, maxCardTextRunes)
		if fact.Subject == "" || fact.Attribute == "" || fact.Value == "" || len(facts) == maxCardItems {
			continue
		}
		fact.P = paragraph(fact.P)
		facts = append(facts, fact)
	}
	card.Facts = facts
	knows := card.Knows[:0]
	for _, item := range card.Knows {
		item.Who, item.What = bounded(item.Who, maxCardTextRunes), bounded(item.What, maxCardTextRunes)
		item.Kind = strings.ToLower(bounded(item.Kind, maxCardTextRunes))
		if item.Who == "" || item.What == "" || len(knows) == maxCardItems {
			continue
		}
		item.P = paragraph(item.P)
		knows = append(knows, item)
	}
	card.Knows = knows
	hooks := card.Hooks[:0]
	for _, hook := range card.Hooks {
		hook.What = bounded(hook.What, maxCardTextRunes)
		if hook.What == "" || len(hooks) == maxCardItems {
			continue
		}
		hook.P = paragraph(hook.P)
		hooks = append(hooks, hook)
	}
	card.Hooks = hooks
	return card, nil
}

// Add appends what the pass over the card found, leaving out what a list already says in the same
// words and staying inside the size bounds of a card; the summary and the chapter time stay
// (specs/2026-11-01-index-foundation.md, R2.4).
func (card *Card) Add(more Card) {
	card.Time = addNew(card.Time, more.Time, func(value string) string { return value })
	mention := func(item Mention) string { return item.Name + "\x00" + item.Note }
	card.Characters = addNew(card.Characters, more.Characters, mention)
	card.Entities = addNew(card.Entities, more.Entities, mention)
	card.Events = addNew(card.Events, more.Events, func(item Event) string { return item.Event })
	card.Facts = addNew(card.Facts, more.Facts, func(item Fact) string {
		return item.Subject + "\x00" + item.Attribute + "\x00" + item.Value
	})
	card.Changes = addNew(card.Changes, more.Changes, func(item Change) string { return item.Subject + "\x00" + item.Change })
	card.Knows = addNew(card.Knows, more.Knows, func(item Knowledge) string { return item.Who + "\x00" + item.What })
	card.Hooks = addNew(card.Hooks, more.Hooks, func(item Hook) string { return item.What })
}

func addNew[T any](items, more []T, key func(T) string) []T {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[Normalize(key(item))] = true
	}
	for _, item := range more {
		if text := Normalize(key(item)); !seen[text] && len(items) < maxCardItems {
			seen[text] = true
			items = append(items, item)
		}
	}
	return items
}

// boundedTime bounds the texts of a chapter time, drops spans without a known unit or with a
// negative amount, and drops a chapter time left empty.
func boundedTime(when *ChapterTime) *ChapterTime {
	if when == nil {
		return nil
	}
	span := func(span *Span) *Span {
		if span == nil {
			return nil
		}
		unit := timeUnit(span.Unit)
		if unit == "" || math.IsNaN(span.N) || span.N < 0 || span.N > 1e6 {
			return nil
		}
		return &Span{N: span.N, Unit: unit, Approx: span.Approx}
	}
	when.Opens, when.Date, when.Note = bounded(when.Opens, maxCardTextRunes), bounded(when.Date, maxCardTextRunes), bounded(when.Note, maxCardTextRunes)
	when.After, when.Lasts = span(when.After), span(when.Lasts)
	if *when == (ChapterTime{}) {
		return nil
	}
	return when
}

// timeUnit names a unit of TimeUnits by any form of its word: «дня», «Недели», «лет».
func timeUnit(word string) string {
	word = strings.ToLower(strings.TrimSpace(word))
	for _, unit := range []struct{ prefix, unit string }{
		{"мин", "минута"}, {"час", "час"}, {"сут", "день"}, {"ден", "день"}, {"дн", "день"}, {"нед", "неделя"},
		{"мес", "месяц"}, {"год", "год"}, {"лет", "год"},
	} {
		if strings.HasPrefix(word, unit.prefix) {
			return unit.unit
		}
	}
	return ""
}

// parseSpan reads «3 дня» or «0.5 часа»; anything else is an empty span.
func parseSpan(text string) Span {
	fields := strings.Fields(strings.ReplaceAll(text, ",", "."))
	if len(fields) != 2 {
		return Span{}
	}
	var number float64
	if _, err := fmt.Sscanf(fields[0], "%g", &number); err != nil {
		return Span{}
	}
	return Span{N: number, Unit: fields[1]}
}

func bounded(value string, limit int) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return strings.TrimSpace(string([]rune(value)[:limit]))
}
