package sagadraft

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"canonkeeper/backend/internal/domain/textface"
)

// A version of the text (specs/2026-11-06-writing-agent.md, WA0.4, WA7.5, WA10): the working text whole, kept before
// every action of the model that changes it and after every writing, with the plan and the pack it was written by.

// VersionReason says why a version was kept: written by the agent, or what followed it.
type VersionReason string

// Reasons of a version.
const (
	VersionWritten       VersionReason = "written"
	VersionBeforeWrite   VersionReason = "before_write"
	VersionBeforeApply   VersionReason = "before_apply"
	VersionBeforeTake    VersionReason = "before_take"
	VersionBeforeRestore VersionReason = "before_restore"
	VersionBeforeDelete  VersionReason = "before_delete"
	VersionSwitch        VersionReason = "switch"
	// The text before a file of the author replaced it, and the text a proofreading read: «как было» of all its
	// corrections (specs/2026-11-07-writing-proofread.md, PR2.2, PR3.2).
	VersionBeforeUpload    VersionReason = "before_upload"
	VersionBeforeProofread VersionReason = "before_proofread"
	// The chapter before the first change a conversation with the voice made in it, its idea too: «Вернуть как было до
	// разговора» gives both back (specs/2026-11-10-ux5-voice.md VO1.1, VO1.3).
	VersionBeforeVoice VersionReason = "before_voice"
	// The author's text the draft could not take — the chapter changed in another place where he typed: «Ваш вариант»
	// (specs/2026-11-11-ux5-writing-path.md WP5.4). It keeps his idea when that was the field.
	VersionAuthorVariant VersionReason = "author_variant"
)

// SceneSupport is where a scene of a written version stands and the facts it stood on.
type SceneSupport struct {
	Scene   int      `json:"scene"`
	From    string   `json:"from,omitempty"`
	To      string   `json:"to,omitempty"`
	Support []string `json:"support,omitempty"`
}

// Version is one version of the text of a draft.
type Version struct {
	Number int
	Reason VersionReason
	Text   string
	// Idea is the idea as it stood, kept only by a version before a conversation and by the version kept before it was
	// given back; nil — the version keeps the text alone.
	Idea        *string
	Words       int
	AuthorWords int
	PlanNumber  int
	PackID      string
	Support     []SceneSupport
	JobID       string
	CreatedAt   time.Time
	// Place is the first words of what the action after the version changed in the text (specs/2026-11-11-ux9-writing-b.md
	// WB3.1): the version is named by it; empty when the action took nothing away or the version is older.
	Place string
	// Scenes are the scenes a writing wrote into a version «written» (its support), counted for its name.
	Scenes int
}

// NewVersion is a version of a working text: its words and the author's counted by the rule of «Ваших слов».
func NewVersion(reason VersionReason, text string) Version {
	return Version{Reason: reason, Text: text, Words: len(WordsOf(WithoutMarks(text))), AuthorWords: AuthorWords(text)}
}

// maxPlaceWords bounds the words a name of a version quotes of its place.
const maxPlaceWords = 6

// ChangedPlace names the place an action changed in a working text (WB3.1): the first words of what stood there before —
// whole words, read without the marks of the helper and the faces of the author —, «…» after the sixth; empty when the
// action took nothing away.
func ChangedPlace(before, after string) string {
	was, now := placeTokens.FindAllString(textface.Plain(WithoutMarks(before)).Text, -1), placeTokens.FindAllString(textface.Plain(WithoutMarks(after)).Text, -1)
	start := 0
	for start < len(was) && start < len(now) && was[start] == now[start] {
		start++
	}
	end := 0
	for end < len(was)-start && end < len(now)-start && was[len(was)-1-end] == now[len(now)-1-end] {
		end++
	}
	words := strings.Fields(strings.Join(was[start:len(was)-end], ""))
	if len(words) == 0 || !strings.ContainsFunc(strings.Join(words, " "), unicode.IsLetter) {
		return ""
	}
	if len(words) > maxPlaceWords {
		return strings.TrimRight(strings.Join(words[:maxPlaceWords], " "), ",.;:!?—–-") + "…"
	}
	return strings.Join(words, " ")
}

// placeTokens cut a text into words and what stands between them: a place changes by whole words.
var placeTokens = regexp.MustCompile(`[\p{L}\p{M}\p{N}]+|[^\p{L}\p{M}\p{N}]+`)

// WithIdea is the version keeping the idea as it stood too.
func (version Version) WithIdea(idea string) Version {
	version.Idea = &idea
	return version
}

// Take is what «Взять в главу» takes of a version (WA7.5): the whole version, or a piece of it named by its first and
// last phrases, put in place of a place of the working text, after it, or at the end of the text without a place.
type Take struct {
	Whole bool
	From  string
	To    string
	Place *Scene
	After bool
}

// TakeInto is the working text after a take and the seams of the piece in it as byte offsets: a seam has text on both
// sides. The piece keeps what of it the author wrote and what not: the marks of a block it cuts are closed at its edges,
// and a piece put inside a block of the model's text closes the block before it and opens it again after. Its faces stay
// whole at its edges; in place of a place it takes the face around the place, and a face of the working text it cuts
// keeps what is left (specs/2026-11-08-italic-bold-in-text.md, TF2.3).
func TakeInto(working, version string, take Take) (string, []int, error) {
	if take.Whole {
		if take.From != "" || take.To != "" || take.Place != nil || take.After {
			return "", nil, ErrInvalid
		}
		return version, nil, nil
	}
	if strings.TrimSpace(take.From) == "" || strings.TrimSpace(take.To) == "" {
		return "", nil, ErrInvalid
	}
	// A place that does not stand once, or whose ends cut a block of the model's text apart, is a request to refuse.
	start, end, err := Span(version, "", take.From, take.To)
	if err != nil {
		return "", nil, ErrInvalid
	}
	open, closing := string(MarkOpen), string(MarkClose)
	piece := textface.Piece(version, start, end)
	if insideBlock(version[:start]) {
		piece = open + piece
	}
	if insideBlock(version[:end]) {
		piece += closing
	}
	left, right := working, ""
	switch {
	case take.Place == nil:
		if strings.TrimSpace(left) != "" {
			left = strings.TrimRight(left, " \t\n") + "\n\n"
		}
	default:
		placeStart, placeEnd, err := Span(working, take.Place.Place, take.Place.From, take.Place.To)
		if err != nil {
			return "", nil, ErrInvalid
		}
		if take.After {
			at := len(working)
			if next := strings.IndexByte(working[placeEnd:], '\n'); next >= 0 {
				at = placeEnd + next
			}
			left, right = textface.Whole(working[:at])+"\n\n", textface.Whole(working[at:])
		} else {
			closes, opens := textface.Around(working[placeStart:placeEnd])
			left, right, piece = working[:placeStart], working[placeEnd:], closes+piece+opens
		}
	}
	if insideBlock(left) {
		piece = closing + piece + open
		if strings.HasSuffix(left, open) {
			left, piece = strings.TrimSuffix(left, open), strings.TrimPrefix(piece, closing)
		}
		if strings.HasPrefix(right, closing) {
			right, piece = strings.TrimPrefix(right, closing), strings.TrimSuffix(piece, open)
		}
	}
	text, cuts := normalized(left+piece+right, len(left), len(left)+len(piece))
	if !ValidMarks(text) {
		return "", nil, ErrInvalid
	}
	var seams []int
	for _, at := range cuts {
		if strings.TrimSpace(WithoutMarks(text[:at])) != "" && strings.TrimSpace(WithoutMarks(text[at:])) != "" {
			seams = append(seams, at)
		}
	}
	return text, seams, nil
}

// normalized is a text with its faces made whole and offsets of it moved with its words.
func normalized(text string, offsets ...int) (string, []int) {
	whole := textface.Normalize(text)
	if whole == text {
		return text, offsets
	}
	plain := textface.Plain(whole)
	moved := make([]int, len(offsets))
	for i, at := range offsets {
		moved[i] = plain.At(len(textface.Plain(text[:at]).Text))
	}
	return whole, moved
}

// insideBlock says a text ends inside a block of the model's text: its last open mark is not closed.
func insideBlock(text string) bool {
	return strings.LastIndex(text, string(MarkOpen)) > strings.LastIndex(text, string(MarkClose))
}
