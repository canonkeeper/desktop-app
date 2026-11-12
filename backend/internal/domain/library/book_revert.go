package library

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The return of a book to the version before its last update from a file (specs/2026-11-12-ux10-book-revert.md BR1,
// BR2): the update keeps 7 days what it did to each chapter and what the chapter was before it — the snapshot; the
// preview tells and the return writes one plan, read from the snapshot and from how the book stands now. The words of
// the chapters are not in the snapshot: they stay in the earlier revisions of their paragraphs.

var (
	// ErrBookBusy is a book an update from a file writes now.
	ErrBookBusy = errors.New("library: the book is being updated from a file")
	// ErrUpdateGone is a book without a live snapshot: never updated, returned already, or its 7 days are over.
	ErrUpdateGone = errors.New("library: the book has no update to return from")
	// ErrUpdateChanged is a book that changed between the preview of the return and the return.
	ErrUpdateChanged = errors.New("library: the book changed after the preview of the return")
)

// UpdateChange is what an update from a file did to a chapter of the book (BR1.1).
type UpdateChange string

const (
	// UpdateWords gave the chapter other words: a new revision, a new card later.
	UpdateWords UpdateChange = "words"
	// UpdatePunctuation gave it other signs only: a new revision that kept its card.
	UpdatePunctuation UpdateChange = "punctuation"
	// UpdatePlaced kept its words and changed its title, its place or its italic and bold.
	UpdatePlaced UpdateChange = "placed"
	// UpdateSame left it as it was; the snapshot keeps its place, so the book comes back in its order.
	UpdateSame UpdateChange = "same"
	// UpdateAdded is a chapter the file added.
	UpdateAdded UpdateChange = "added"
	// UpdateRemoved is a chapter the file no longer had: it went to «Недавно удалённые».
	UpdateRemoved UpdateChange = "removed"
)

// UpdateChangeOf names what an update does to a chapter of the book by its pairing: moved and restyled say whether it
// takes another place or another italic and bold.
func UpdateChangeOf(pairing ChapterPairing, moved, restyled bool) UpdateChange {
	switch {
	case pairing.Book < 0:
		return UpdateAdded
	case pairing.Change == TextWords:
		return UpdateWords
	case pairing.Change == TextPunctuation:
		return UpdatePunctuation
	case pairing.Renamed || moved || restyled:
		return UpdatePlaced
	default:
		return UpdateSame
	}
}

// SnapshotChapter is a chapter of the snapshot: what the update did to it, what it left — its revision and lifecycle
// generation — and, but for a chapter the file added, its revision, title and place before the update.
type SnapshotChapter struct {
	ID               string
	Change           UpdateChange
	Revision         int64
	Generation       int64
	PreviousRevision int64
	PreviousTitle    string
	PreviousOrder    int
}

// ChapterNow is how a chapter of the book or of the snapshot stands now. Restorable is a deleted chapter whose 7 days
// in «Недавно удалённые» still go.
type ChapterNow struct {
	ID         string
	Title      string
	Order      int
	Revision   int64
	Generation int64
	Live       bool
	Restorable bool
}

// RevertAction is what the return does to a chapter.
type RevertAction string

const (
	// RevertText writes the words of the revision before the update, with its title and place, as a new revision.
	RevertText RevertAction = "text"
	// RevertPlace gives back the title, the place and the italic and bold before the update.
	RevertPlace RevertAction = "place"
	// RevertReturn brings a chapter the update removed back from «Недавно удалённые».
	RevertReturn RevertAction = "return"
	// RevertRemove takes a chapter the file added to «Недавно удалённые» for 7 days.
	RevertRemove RevertAction = "remove"
)

// RevertStay is why the return leaves a chapter as it is now (BR2.1).
type RevertStay string

const (
	// StayEdited is a chapter the author changed after the update: «правили после обновления».
	StayEdited RevertStay = "edited"
	// StayDeleted is a chapter deleted after the update: «удалена после обновления».
	StayDeleted RevertStay = "deleted"
	// StayReturned is a chapter the update removed that the author returned himself: «уже возвращена».
	StayReturned RevertStay = "returned"
)

// RevertStep is one chapter the return changes.
type RevertStep struct {
	Chapter SnapshotChapter
	Now     ChapterNow
	Action  RevertAction
}

// RevertKept is a chapter the return would change and leaves as it is, with why.
type RevertKept struct {
	Chapter SnapshotChapter
	Now     ChapterNow
	Stay    RevertStay
}

// BookRevertPlan is the whole return: the chapters it changes, those it leaves and why, the live chapters of the book
// after it in their order — Order[i] stands at place i+1 — and the fingerprint of the book it was planned on.
type BookRevertPlan struct {
	Steps       []RevertStep
	Kept        []RevertKept
	Order       []string
	Fingerprint string
}

// PlanBookRevert plans the return of a book by its snapshot (BR2.1, BR2.2). book is how the book stands now: every live
// chapter of it and every deleted chapter of the snapshot; a chapter purged for good is not in it. A chapter the author
// changed after the update stays as it is, and so does one he deleted or returned himself; the rest comes back as it
// was before the update, and the chapters the file added go. The previous chapters take their previous order; a chapter
// the previous book had not — kept as the author changed it, or added after the update — stands after the chapter it
// follows now.
func PlanBookRevert(updatedAt time.Time, snapshot []SnapshotChapter, book []ChapterNow) BookRevertPlan {
	now := make(map[string]ChapterNow, len(book))
	for _, chapter := range book {
		now[chapter.ID] = chapter
	}
	plan := BookRevertPlan{Steps: []RevertStep{}, Kept: []RevertKept{}, Fingerprint: revertFingerprint(updatedAt, snapshot, book)}
	liveAfter := map[string]bool{}
	removed := map[string]bool{}
	for _, chapter := range book {
		liveAfter[chapter.ID] = chapter.Live
	}
	step := func(chapter SnapshotChapter, current ChapterNow, action RevertAction) {
		plan.Steps = append(plan.Steps, RevertStep{Chapter: chapter, Now: current, Action: action})
	}
	keep := func(chapter SnapshotChapter, current ChapterNow, stay RevertStay) {
		plan.Kept = append(plan.Kept, RevertKept{Chapter: chapter, Now: current, Stay: stay})
	}
	for _, chapter := range snapshot {
		current, known := now[chapter.ID]
		switch chapter.Change {
		case UpdateRemoved:
			switch {
			case known && current.Live:
				keep(chapter, current, StayReturned)
			case known && current.Restorable && current.Generation == chapter.Generation:
				step(chapter, current, RevertReturn)
				liveAfter[chapter.ID] = true
			default:
				keep(chapter, current, StayDeleted)
			}
		case UpdateAdded:
			switch {
			case !known || !current.Live:
			case current.Revision != chapter.Revision:
				keep(chapter, current, StayEdited)
			default:
				step(chapter, current, RevertRemove)
				liveAfter[chapter.ID], removed[chapter.ID] = false, true
			}
		case UpdateSame:
		default:
			switch {
			case !known || !current.Live:
				keep(chapter, current, StayDeleted)
			case current.Revision != chapter.Revision:
				keep(chapter, current, StayEdited)
			case chapter.Change == UpdatePlaced:
				step(chapter, current, RevertPlace)
			default:
				step(chapter, current, RevertText)
			}
		}
	}
	previous := make([]SnapshotChapter, 0, len(snapshot))
	for _, chapter := range snapshot {
		if chapter.Change != UpdateAdded && liveAfter[chapter.ID] {
			previous = append(previous, chapter)
		}
	}
	slices.SortStableFunc(previous, func(a, b SnapshotChapter) int {
		if a.PreviousOrder != b.PreviousOrder {
			return a.PreviousOrder - b.PreviousOrder
		}
		return strings.Compare(a.ID, b.ID)
	})
	plan.Order = make([]string, 0, len(book))
	placed := map[string]bool{}
	for _, chapter := range previous {
		plan.Order = append(plan.Order, chapter.ID)
		placed[chapter.ID] = true
	}
	live := make([]ChapterNow, 0, len(book))
	for _, chapter := range book {
		if chapter.Live {
			live = append(live, chapter)
		}
	}
	slices.SortStableFunc(live, func(a, b ChapterNow) int {
		if a.Order != b.Order {
			return a.Order - b.Order
		}
		return strings.Compare(a.ID, b.ID)
	})
	for index, chapter := range live {
		if placed[chapter.ID] || removed[chapter.ID] {
			continue
		}
		at := 0
		for before := index - 1; before >= 0; before-- {
			if position := slices.Index(plan.Order, live[before].ID); position >= 0 {
				at = position + 1
				break
			}
		}
		plan.Order = slices.Insert(plan.Order, at, chapter.ID)
		placed[chapter.ID] = true
	}
	return plan
}

// RevertChapter is a chapter the preview names: its title now and, when the return gives it another one, the title it
// gets back; a kept chapter says why it stays.
type RevertChapter struct {
	ID            string
	Title         string
	PreviousTitle string
	Stay          RevertStay
}

// BookRevertPreview is what a return of the book would do (BR2.1): the chapters whose text comes back, those that get
// back their title, place and italic and bold, the chapters the update removed that come back, the chapters the file
// added that go to «Недавно удалённые», and those that stay as they are; with the update and the fingerprint the return
// carries.
type BookRevertPreview struct {
	BookID      string
	BookTitle   string
	UpdatedAt   time.Time
	RevertUntil time.Time
	Fingerprint string
	Text        []RevertChapter
	Place       []RevertChapter
	Returned    []RevertChapter
	Removed     []RevertChapter
	Kept        []RevertChapter
}

// PreviewOf tells the plan in the words of the preview, the chapters in the order of the book after the return and the
// chapters that go in their order now.
func (plan BookRevertPlan) PreviewOf(bookID, bookTitle string, updatedAt, revertUntil time.Time) BookRevertPreview {
	preview := BookRevertPreview{
		BookID: bookID, BookTitle: bookTitle, UpdatedAt: updatedAt, RevertUntil: revertUntil, Fingerprint: plan.Fingerprint,
		Text: []RevertChapter{}, Place: []RevertChapter{}, Returned: []RevertChapter{}, Removed: []RevertChapter{}, Kept: []RevertChapter{},
	}
	rank := make(map[string]int, len(plan.Order))
	for index, id := range plan.Order {
		rank[id] = index
	}
	named := func(chapter SnapshotChapter, now ChapterNow, stay RevertStay) RevertChapter {
		title := now.Title
		if title == "" {
			title = chapter.PreviousTitle
		}
		named := RevertChapter{ID: chapter.ID, Title: title, Stay: stay}
		if stay == "" && chapter.Change != UpdateAdded && chapter.PreviousTitle != title {
			named.PreviousTitle = chapter.PreviousTitle
		}
		return named
	}
	steps := slices.Clone(plan.Steps)
	position := func(step RevertStep) int {
		if step.Action == RevertRemove {
			return step.Now.Order
		}
		return rank[step.Chapter.ID]
	}
	slices.SortStableFunc(steps, func(a, b RevertStep) int { return position(a) - position(b) })
	for _, step := range steps {
		chapter := named(step.Chapter, step.Now, "")
		switch step.Action {
		case RevertText:
			preview.Text = append(preview.Text, chapter)
		case RevertPlace:
			preview.Place = append(preview.Place, chapter)
		case RevertReturn:
			preview.Returned = append(preview.Returned, chapter)
		case RevertRemove:
			preview.Removed = append(preview.Removed, chapter)
		}
	}
	for _, kept := range plan.Kept {
		preview.Kept = append(preview.Kept, named(kept.Chapter, kept.Now, kept.Stay))
	}
	return preview
}

// revertFingerprint names the book a return was planned on: the snapshot and every chapter as it stands. A preview
// and a return that see the same fingerprint write the same plan.
func revertFingerprint(updatedAt time.Time, snapshot []SnapshotChapter, book []ChapterNow) string {
	lines := make([]string, 0, len(snapshot)+len(book)+1)
	lines = append(lines, "at "+strconv.FormatInt(updatedAt.UnixMicro(), 10))
	for _, chapter := range snapshot {
		lines = append(lines, strings.Join([]string{"was", chapter.ID, string(chapter.Change),
			strconv.FormatInt(chapter.Revision, 10), strconv.FormatInt(chapter.Generation, 10)}, " "))
	}
	for _, chapter := range book {
		lines = append(lines, strings.Join([]string{"now", chapter.ID, strconv.FormatBool(chapter.Live),
			strconv.FormatBool(chapter.Restorable), strconv.FormatInt(chapter.Revision, 10),
			strconv.FormatInt(chapter.Generation, 10), strconv.Itoa(chapter.Order), chapter.Title}, " "))
	}
	slices.Sort(lines[1:])
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:16])
}
