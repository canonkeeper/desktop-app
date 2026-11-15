// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// The states of a record of a note against the saga (specs/2026-11-13-desk-obsidian.md OB4.3,
// specs/2026-11-15-desk-obsidian-notes.md ON1.2).
const (
	StateNew           = "new"
	StateInBook        = "in_book"
	StateInBookByAlias = "in_book_by_alias"
	StateSameNote      = "same_note"
	StateEditedInApp   = "edited_in_app"
	StateOtherSource   = "other_source"
)

// MaxRecords bounds the records of one load of notes and of a saga after it (ON1.7): every reader of all the records of a
// saga — the map, the tools of the chat, the preview — takes the first 5 000, and a record past them no one would see.
const MaxRecords = 5000

// HeldRecord is a record of the saga a state of a note depends on (ON1.2): one whose name a note of the batch has, or one a
// note of the batch is linked with. Note is the note it is linked with — "" for none —, LinkRevision the revision of the
// record the link keeps and Fingerprint the fingerprint of the record its note gave at the load.
type HeldRecord struct {
	ID           string
	Key          string
	Revision     int64
	Note         string
	LinkRevision int64
	Fingerprint  string
}

// NoteFacts are what the states of the notes of a batch depend on in the saga: the records that hold their names or that
// their notes are linked with, and how many records the saga keeps. The preview reads them, the load reads them again in
// its transaction with the rows locked.
type NoteFacts struct {
	Records []HeldRecord
	Total   int
}

// NoteState is what the record of a note is to the saga.
type NoteState struct {
	State string
	// Changed says, for same_note, that the note gives another record than at its load.
	Changed  bool
	BookName string
	// Target is the record a write of the note goes to — its own for same_note and edited_in_app, the one that holds its
	// name for other_source when a decision may replace it — and Revision is the revision of Target; "" for none.
	Target   string
	Revision int64
	// HeldBy is, for other_source, the note of the batch that has the name — gets it, or keeps it by its record —; "" when a
	// record no note of the batch owns keeps it.
	HeldBy string
	// Seen is what the author sees of the state — the state, its target and the revision of its target —: a load writes
	// a record only when it sees what its preview saw (ON1.3).
	Seen string
}

// NoteStates are the states of the records of the notes of a batch (ON1.2), one rule for the preview and the load: by the
// records of the saga, the links of the notes and the names of the batch after all its renames. A note is known by its path
// from the root of the vault — a renamed file is another note. The name of a note goes to the record that keeps it, if one
// does: the record of the note itself, and no other note gets it; a name no record keeps — a new one, or one a record of a
// note of the batch leaves for another — goes to the first note that has it by the order of the paths. A linked note whose
// name another record keeps or another note gets is other_source; so is the second of two notes of one name, and of two
// notes of one name a record no note of the batch owns keeps only the first by the order of the paths may replace it.
//
// A record of a linked note leaves its name only when the load writes it under the new one: its note is the same, its new
// name is free, and — for a load, which names what the author saw by the paths of the notes (seen; nil for the preview) —
// the author saw it leave. Every other record keeps its name: one edited in the app until the author replaces it, one whose
// note lost its new name, one whose move the author did not see. The rule goes to its fixed point, so a swap or a chain of
// renames stands or falls whole, and a load of what its preview showed sees what the preview showed.
func NoteStates(records []Record, facts NoteFacts, book map[string]string, seen map[string]string) []NoteState {
	byID := map[string]HeldRecord{}
	byNote := map[string]string{}
	for _, held := range facts.Records {
		byID[held.ID] = held
		if held.Note != "" {
			byNote[held.Note] = held.ID
		}
	}
	inBatch := map[string]bool{}
	keys := make([]string, len(records))
	linked := make([]string, len(records))
	order := make([]int, len(records))
	for index, record := range records {
		inBatch[record.Path] = true
		keys[index] = sagamemory.GlossaryKey(record.Draft.Name)
		linked[index] = byNote[record.Path]
		order[index] = index
	}
	slices.SortStableFunc(order, func(left, right int) int { return pathOrder(records[left].Path, records[right].Path) })
	edited := func(index int) bool {
		held := byID[linked[index]]
		return held.Revision != held.LinkRevision
	}
	movers := map[int]bool{}
	for index := range records {
		if linked[index] != "" && !edited(index) && keys[index] != byID[linked[index]].Key {
			movers[index] = true
		}
	}
	// moving is what the author sees of a note whose record leaves its name: the same note, written into its own record.
	moving := func(index int) string {
		own := byID[linked[index]]
		return seenOf(NoteState{State: StateSameNote, Target: own.ID, Revision: own.Revision})
	}
	var lost map[int]bool
	var kept map[string]string
	var winner map[string]int
	for {
		lost, kept, winner = map[int]bool{}, map[string]string{}, map[string]int{}
		moved := map[string]bool{}
		for index := range movers {
			moved[linked[index]] = true
		}
		for id, held := range byID {
			if !moved[id] {
				kept[held.Key] = id
			}
		}
		for _, index := range order {
			key := keys[index]
			if id, ok := kept[key]; ok {
				lost[index] = linked[index] != id
				continue
			}
			_, lost[index] = winner[key]
			if !lost[index] {
				winner[key] = index
			}
		}
		// A note that lost the name it moves to, or whose move the author did not see, leaves its record where it was:
		// whoever took the old name loses it again.
		again := false
		for index := range movers {
			if lost[index] || seen != nil && seen[records[index].Path] != moving(index) {
				delete(movers, index)
				again = true
			}
		}
		if !again {
			break
		}
	}
	states := make([]NoteState, len(records))
	offered := map[string]string{}
	for _, index := range order {
		record := records[index]
		state := NoteState{}
		own := byID[linked[index]]
		switch id, held := kept[keys[index]]; {
		case lost[index]:
			state.State = StateOtherSource
			// The record that keeps the name may be replaced, unless a note of the batch is its own; of the notes that have its
			// name, by the first by the order of the paths only — the others the name of another note of the batch.
			switch {
			case !held:
				state.HeldBy = records[winner[keys[index]]].Path
			case inBatch[byID[id].Note]:
				state.HeldBy = byID[id].Note
			case offered[keys[index]] != "":
				state.HeldBy = offered[keys[index]]
			default:
				offered[keys[index]] = record.Path
				state.Target, state.Revision = id, byID[id].Revision
			}
		case linked[index] != "" && edited(index):
			state.State, state.Target, state.Revision = StateEditedInApp, own.ID, own.Revision
		case linked[index] != "":
			state.State, state.Target, state.Revision = StateSameNote, own.ID, own.Revision
			state.Changed = own.Fingerprint != RecordsFingerprint([]Record{record})
		case book[keys[index]] != "":
			state.State, state.BookName = StateInBook, book[keys[index]]
		default:
			state.State = StateNew
			for _, alias := range record.Aliases {
				if name := book[sagamemory.GlossaryKey(alias)]; name != "" {
					state.State, state.BookName = StateInBookByAlias, name
					break
				}
			}
		}
		state.Seen = seenOf(state)
		states[index] = state
	}
	return states
}

// pathOrder is the natural order of the paths of notes, the bytes for paths it does not tell apart.
func pathOrder(left, right string) int {
	switch {
	case NaturalLess(left, right):
		return -1
	case NaturalLess(right, left):
		return 1
	}
	return strings.Compare(left, right)
}

// seenOf is what the author sees of a state: a new record for new, in_book and in_book_by_alias alike — a record of each is
// created —, otherwise the state, its target and the revision of its target.
func seenOf(state NoteState) string {
	what := "create"
	switch state.State {
	case StateSameNote, StateEditedInApp, StateOtherSource:
		what = state.State + "\x00" + state.Target + "\x00" + strconv.FormatInt(state.Revision, 10)
	}
	sum := sha256.Sum256([]byte(what))
	return hex.EncodeToString(sum[:8])
}

// TooManyRecords is a load of notes past MaxRecords (obsidian.too_large): Records is the records of the load, Saga the
// records of the saga after it.
type TooManyRecords struct{ Records, Saga int }

func (TooManyRecords) Error() string { return "obsidian: too many records" }

// CheckRecords refuses a load of records past MaxRecords — the records of the load, or of the saga after the records it
// creates (ON1.7).
func CheckRecords(records, total, creates int) error {
	if records > MaxRecords || total+creates > MaxRecords {
		return TooManyRecords{Records: records, Saga: total + creates}
	}
	return nil
}

// Creates says a state makes a new record.
func Creates(state string) bool {
	return state == StateNew || state == StateInBook || state == StateInBookByAlias
}

// NoteSource is the name of the source of a record from a note (ON1.6): the path of the note from the root of the vault, a
// control or a lost sign a space, a path longer than the source name of a record its tail with «…» before it — a line the
// saga file keeps as it is.
func NoteSource(notePath string) string {
	value := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(notePath, string(utf8.RuneError)))
	if utf8.RuneCountInString(value) <= authormaterials.MaxSourceNameRunes {
		return value
	}
	runes := []rune(value)
	return "…" + string(runes[len(runes)-authormaterials.MaxSourceNameRunes+1:])
}
