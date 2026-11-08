package sagadraft

import (
	"strings"
	"time"
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
	Number      int
	Reason      VersionReason
	Text        string
	Words       int
	AuthorWords int
	PlanNumber  int
	PackID      string
	Support     []SceneSupport
	JobID       string
	CreatedAt   time.Time
}

// NewVersion is a version of a working text: its words and the author's counted by the rule of «Ваших слов».
func NewVersion(reason VersionReason, text string) Version {
	return Version{Reason: reason, Text: text, Words: len(WordsOf(WithoutMarks(text))), AuthorWords: AuthorWords(text)}
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
// and a piece put inside a block of the model's text closes the block before it and opens it again after.
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
	piece := version[start:end]
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
			left, right = working[:at]+"\n\n", working[at:]
		} else {
			left, right = working[:placeStart], working[placeEnd:]
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
	text := left + piece + right
	if !ValidMarks(text) {
		return "", nil, ErrInvalid
	}
	var seams []int
	for _, at := range []int{len(left), len(left) + len(piece)} {
		if strings.TrimSpace(WithoutMarks(text[:at])) != "" && strings.TrimSpace(WithoutMarks(text[at:])) != "" {
			seams = append(seams, at)
		}
	}
	return text, seams, nil
}

// insideBlock says a text ends inside a block of the model's text: its last open mark is not closed.
func insideBlock(text string) bool {
	return strings.LastIndex(text, string(MarkOpen)) > strings.LastIndex(text, string(MarkClose))
}
