// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

// Decision is what the author decided of a record a load does not write by itself
// (specs/2026-11-15-desk-obsidian-notes.md ON1.4): to replace it with what its note says, or to keep it.
type Decision string

const (
	DecisionReplace Decision = "replace"
	DecisionKeep    Decision = "keep"
)

// Decisions are the decisions of a load: by the path of a note, else one rule a state.
type Decisions struct {
	Notes  map[string]Decision
	States map[string]Decision
}

func (decisions Decisions) of(notePath, state string) Decision {
	if decision, ok := decisions.Notes[notePath]; ok {
		return decision
	}
	return decisions.States[state]
}

// NoteOutcome is what a load did with the record of a note (ON1.8).
type NoteOutcome string

const (
	OutcomeCreated   NoteOutcome = "created"
	OutcomeUpdated   NoteOutcome = "updated"
	OutcomeReplaced  NoteOutcome = "replaced"
	OutcomeUnchanged NoteOutcome = "unchanged"
	// OutcomeKeptTaken is a name another record keeps or another note of the load gets; OutcomeKeptEdited a record edited
	// in the app; OutcomeKeptChanged a record whose state is not the one its preview saw — the author made or changed a
	// record since (ON1.3).
	OutcomeKeptTaken   NoteOutcome = "kept_taken"
	OutcomeKeptEdited  NoteOutcome = "kept_edited"
	OutcomeKeptChanged NoteOutcome = "kept_changed"
)

// PlanNotes is what a load does with the record of each note by its state now (ON1.3, ON1.4): a record whose state is not
// the one the author saw in the preview is kept, whatever he decided; new, in_book and in_book_by_alias are new records;
// the same note is written when it gives another record than at its load; a record edited in the app is written only by a
// decision to replace it, and so is the record that keeps the name of the note when no note of the load is its own.
func PlanNotes(records []Record, states []NoteState, seen map[string]string, decisions Decisions) []NoteOutcome {
	outcomes := make([]NoteOutcome, len(records))
	for index, record := range records {
		state := states[index]
		replace := decisions.of(record.Path, state.State) == DecisionReplace
		switch {
		case seen[record.Path] != state.Seen:
			outcomes[index] = OutcomeKeptChanged
		case Creates(state.State):
			outcomes[index] = OutcomeCreated
		case state.State == StateSameNote && state.Changed:
			outcomes[index] = OutcomeUpdated
		case state.State == StateSameNote:
			outcomes[index] = OutcomeUnchanged
		case state.State == StateEditedInApp && replace:
			outcomes[index] = OutcomeUpdated
		case state.State == StateEditedInApp:
			outcomes[index] = OutcomeKeptEdited
		case replace && state.Target != "":
			outcomes[index] = OutcomeReplaced
		default:
			outcomes[index] = OutcomeKeptTaken
		}
	}
	return outcomes
}
