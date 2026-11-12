package sagadraft

import (
	"time"
)

// The notes on a draft and its chapter (specs/2026-11-02-writer-workshop.md, W6, W7.1): what the writing Job and
// the helper found, each at its exact place. A note is a table of its own, so it never moves the revision of the
// draft the author types in; only his click applies one.

// The limits of a note, as the table holds them.
const (
	// A quote of a place. The place of a variant is as long as its scene (specs/2026-11-04-writing-plain-path.md P5.5).
	MaxQuoteRunes       = 2000
	MaxNoteBodyRunes    = 2000
	MaxReplacementRunes = 4000
)

// NoteKind says what a note is: of the helper, of the Job of the writing agent, or one the earlier writing left
// (brief, meaning, repeat, machine): those stay the author's data until he closes them, and none is written any more
// (specs/2026-11-06-writing-agent.md, WA10.5).
type NoteKind string

const (
	NoteBrief    NoteKind = "brief"
	NoteCanon    NoteKind = "canon"
	NoteQuestion NoteKind = "question"
	NoteVariant  NoteKind = "variant"
	// NoteReplaced is what the helper replaced on the author's direct request: Quote is what stands there now, Replacement
	// what stood there, for «Вернуть как было» (specs/2026-11-04-writing-plain-path.md P5.6).
	NoteReplaced NoteKind = "replaced"
	// NoteMeaning is a sentence the check of meaning fixed while writing (W3.4), or a piece it could not check.
	NoteMeaning NoteKind = "meaning"
	// NoteRepeat is a sentence that still repeats the book (W3.3).
	NoteRepeat NoteKind = "repeat"
	// NoteMachine is a place the working judge quoted (W3.5).
	NoteMachine NoteKind = "machine"
	// The notes of the writing agent's Job (specs/2026-11-06-writing-agent.md, WA5.6, WA6, WA7): what a scene invented,
	// not canon until the chapter is in the book; a finding of style of layer A or B; a yes-or-no question of layer C;
	// a seam; a beat of the plan the text did not follow.
	NoteNew        NoteKind = "new"
	NoteStyle      NoteKind = "style"
	NoteCraft      NoteKind = "craft"
	NoteSeam       NoteKind = "seam"
	NotePlanFollow NoteKind = "plan_follow"
	// NoteProofread is a correction of the proofreading of a chapter (specs/2026-11-07-writing-proofread.md, PR3): a
	// fragment and what stands there instead, by its category; «Принять все» takes typos, spelling and punctuation.
	NoteProofread NoteKind = "proofread"
)

// Finding says the note is a finding of «Сверка» the author decides on: what the check found and what the earlier
// writing left (specs/2026-11-07-writing-screen.md WS7.1). The corrections of the proofreading are counted apart, the
// questions, variants and replaced places are not findings.
func (kind NoteKind) Finding() bool {
	switch kind {
	case NoteCanon, NoteNew, NoteStyle, NoteCraft, NoteSeam, NotePlanFollow, NoteMeaning, NoteRepeat, NoteMachine:
		return true
	}
	return false
}

// FindingKinds are the kinds of the findings of «Сверка», as a store counts them.
func FindingKinds() []string {
	kinds := []string{}
	for _, kind := range []NoteKind{NoteCanon, NoteNew, NoteStyle, NoteCraft, NoteSeam, NotePlanFollow, NoteMeaning, NoteRepeat, NoteMachine} {
		if kind.Finding() {
			kinds = append(kinds, string(kind))
		}
	}
	return kinds
}

// NoteTarget is the text a note stands in: the one working text, or none (a brief of the helper before
// specs/2026-11-06-writing-agent.md S8). The notes on the chapter text of the earlier writing stand in the working text
// since the migration of S8 (WA10.4).
type NoteTarget string

const (
	TargetDraft NoteTarget = "draft"
	TargetNone  NoteTarget = "none"
)

// NoteStatus is what the author did with a note.
type NoteStatus string

const (
	NoteOpen      NoteStatus = "open"
	NoteAnswered  NoteStatus = "answered"
	NoteApplied   NoteStatus = "applied"
	NoteDismissed NoteStatus = "dismissed"
)

// The origins of a note.
const (
	OriginHelper = "helper"
	OriginJob    = "job"
)

// Note is one note. Quote is its place, as the text stood when it was written; Replacement is what «Применить»
// puts there; a sentence the writing fixed keeps what it was in Replacement and is applied already.
type Note struct {
	ID          string
	Number      int
	Kind        NoteKind
	Target      NoteTarget
	Origin      string
	Section     string
	Quote       string
	Body        string
	Replacement string
	Options     []string
	Refs        []string
	Answer      string
	Status      NoteStatus
	// VariantGroup joins the variants of one scene: «Заменить» by one dismisses the others (W6.4).
	VariantGroup string
	// AnchorBefore and AnchorAfter are what stood right around a replaced place: they find it again at any time, however
	// the author changed the new text in it (P5.6).
	AnchorBefore string
	AnchorAfter  string
	// A question of the writing agent is a card (specs/2026-11-06-writing-agent.md, WA3.5): what it blocks («план» or
	// «сцена N»), what is known with quotes, what each option means for the scene, the recommended option and the one
	// taken when the author skips it.
	Blocks    string
	Known     string
	Effects   []string
	Recommend string
	Fallback  string
	// A finding of the writing agent's Job: its level by the hierarchy of trust (blocking only against a quote of the
	// book), its category, the book's quote, the scene of the plan, the pack it was found or written by (WA6.4).
	Level     string
	Category  string
	BookQuote string
	Scene     int
	PackID    string
	// TurnID is the native turn of the helper that wrote the note, as the feed names it (providerTurnId): the feed
	// shows the note under that turn's answer (W4.5). The row keeps the chat turn and reads its native one.
	TurnID    string
	CreatedAt time.Time
	UpdatedAt time.Time
}
