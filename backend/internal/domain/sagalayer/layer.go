// Package sagalayer holds the thread layer of a saga (specs/2026-11-01-writer-assistant-index.md, UC2): the
// arcs and threads of each book, the registry of the open threads of the saga and the checkpoint of a
// build. A thread is navigation, not a fact: facts are checked in the text. It performs no I/O.
package sagalayer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// The importance of a registry record.
const (
	WeightMajor = "major"
	WeightMinor = "minor"
)

// Ref is one place a record names: the chapter, whose key may shift, and the paragraph, 0 when the
// model gave none.
type Ref struct {
	Chapter string `json:"c"`
	P       int    `json:"p,omitempty"`
}

// Thread is one thread of a book or one record of the registry (R2.5).
type Thread struct {
	Text      string `json:"thread"`
	Kind      string `json:"kind,omitempty"`
	First     []Ref  `json:"first,omitempty"`
	Last      []Ref  `json:"last,omitempty"`
	Weight    string `json:"weight,omitempty"`
	Closed    bool   `json:"closed,omitempty"`
	ClosedHow string `json:"closedHow,omitempty"`
}

// Arc is one story arc of a book.
type Arc struct {
	Title   string `json:"title"`
	From    Ref    `json:"from"`
	To      Ref    `json:"to"`
	Gist    string `json:"gist,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

// Book is what the map call extracted from one book, bound to the digest of its chapters (R2.3).
type Book struct {
	ID      string   `json:"id"`
	Order   int      `json:"order"`
	Digest  string   `json:"digest"`
	Arcs    []Arc    `json:"arcs,omitempty"`
	Threads []Thread `json:"threads,omitempty"`
}

// Stamp says what a card of a chapter was built from; CardVersion is 0 for a chapter without a card. Hash is
// the current text of the chapter; Card is the text the card was built from when it is another one: a card
// carried over a change of signs (specs/2026-10-27-update-book-from-file.md, R3.2, R3.3).
type Stamp struct {
	Chapter     string `json:"c"`
	Hash        string `json:"h"`
	CardVersion int    `json:"v"`
	Card        string `json:"k,omitempty"`
}

// built is the text the card of the stamp was built from.
func (stamp Stamp) built() string {
	if stamp.Card != "" {
		return stamp.Card
	}
	return stamp.Hash
}

// Layer is a finished build of a project.
type Layer struct {
	Chapters []Stamp
	Books    []Book
	Threads  []Thread
}

// Verdict is the answer of a check of one record (R2.4); Failed marks a record that kept its words
// because every attempt of the check failed.
type Verdict struct {
	Decision string `json:"v"`
	Text     string `json:"thread,omitempty"`
	Failed   bool   `json:"failed,omitempty"`
}

// Steps is the checkpoint of a build after the map step (R2.6): each list is there once its call
// answered, each verdict once its check ended. Digest names the chapters it belongs to.
type Steps struct {
	Digest      string          `json:"digest"`
	Reduced     bool            `json:"reduced,omitempty"`
	Registry    []Thread        `json:"registry,omitempty"`
	Verdicts    map[int]Verdict `json:"verdicts,omitempty"`
	GapDone     bool            `json:"gapDone,omitempty"`
	Gap         []Thread        `json:"gap,omitempty"`
	GapVerdicts map[int]Verdict `json:"gapVerdicts,omitempty"`
}

// Stored is the row of a project: the finished build, if any, named by Digest, and the checkpoint of
// a build under way (R2.6): the books it has asked the model about and its later steps.
type Stored struct {
	Digest          string
	Layer           Layer
	CheckpointBooks []Book
	Steps           *Steps
}

// ApplyVerdicts leaves what the checks kept: a fixed record in its new words, a dropped one gone.
func ApplyVerdicts(threads []Thread, verdicts map[int]Verdict) []Thread {
	kept := make([]Thread, 0, len(threads))
	for i, thread := range threads {
		verdict := verdicts[i]
		switch {
		case verdict.Decision == "drop":
			continue
		case verdict.Decision == "fix" && verdict.Text != "":
			thread.Text = verdict.Text
		}
		kept = append(kept, thread)
	}
	return kept
}

const (
	dueChapters    = 5
	duePercent     = 3
	dueAtLeastEver = 2
)

// Due says whether a build is worth paying for (R2.2): there is none yet, or at least two chapters have a new
// card since it and they are not less than 5 chapters or 3 % of the chapters: one changed chapter never rebuilds
// the threads, also in a small saga (specs/2026-10-27-update-book-from-file.md, R5.1). A chapter without a card
// waits for its card; a card carried over a change of signs is the card the build read (R3.3).
func Due(current, built []Stamp) bool {
	stored := make(map[string]Stamp, len(built))
	for _, stamp := range built {
		stored[stamp.Chapter] = stamp
	}
	changed := 0
	for _, stamp := range current {
		previous, ok := stored[stamp.Chapter]
		if stamp.CardVersion > 0 && (!ok || previous.built() != stamp.built() || previous.CardVersion != stamp.CardVersion) {
			changed++
		}
	}
	if changed == 0 {
		return false
	}
	return len(built) == 0 || changed >= dueAtLeastEver && (changed >= dueChapters || changed*100 >= duePercent*len(current))
}

// DueAfter is Due for a change the cards do not have yet (R6.2): changed chapters will get a new card, of total
// chapters of the saga, and built says whether a build exists.
func DueAfter(changed, total int, built bool) bool {
	if changed == 0 {
		return false
	}
	return !built || changed >= dueAtLeastEver && (changed >= dueChapters || changed*100 >= duePercent*total)
}

// Digest names a list of chapters in reading order with what their cards were built from (a carried card
// names the text it was built from) and the instructions of the layer: the digest of a book binds its map result (R2.3), the digest of all the
// chapters a build.
func Digest(stamps []Stamp) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%d\n", InstructionsVersion)
	for _, stamp := range stamps {
		fmt.Fprintf(hash, "%s %s %d\n", stamp.Chapter, stamp.built(), stamp.CardVersion)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// BookThread is a thread of a book the registry does not hold.
type BookThread struct {
	Thread
	Book int
}

// heldShare is how much of the words of a book thread one registry record must share to hold it.
const heldShare = 0.6

// BookOnly lists the open threads of the books the registry does not hold (R2.7): the reduce call
// writes its records in its own words and keeps no link to the book threads, so the words decide. A
// thread in doubt is listed: it is marked as possibly closed, and a lost detail costs more.
func (layer *Layer) BookOnly() []BookThread {
	held := make([]map[string]bool, len(layer.Threads))
	for i, thread := range layer.Threads {
		held[i] = stems(thread.Text)
	}
	var only []BookThread
	for _, book := range layer.Books {
		for _, thread := range book.Threads {
			if !thread.Closed && !heldBy(stems(thread.Text), held) {
				only = append(only, BookThread{Thread: thread, Book: book.Order})
			}
		}
	}
	return only
}

func heldBy(words map[string]bool, records []map[string]bool) bool {
	if len(words) == 0 {
		return false
	}
	for _, record := range records {
		if float64(common(words, record)) >= heldShare*float64(len(words)) {
			return true
		}
	}
	return false
}

// RenderRefs writes places the way the model reads and cites them; a chapter no longer in the version
// is left out.
func RenderRefs(refs []Ref, version *sagamemory.Version) string {
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		for i := range version.Chapters {
			if version.Chapters[i].ID == ref.Chapter {
				text := version.Chapters[i].Key
				if ref.P > 0 {
					text += fmt.Sprintf(" ¶%d", ref.P)
				}
				parts = append(parts, text)
				break
			}
		}
	}
	return strings.Join(parts, ", ")
}
