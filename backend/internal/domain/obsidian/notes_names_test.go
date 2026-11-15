// SPDX-License-Identifier: AGPL-3.0-only

package obsidian_test

import (
	"fmt"
	"testing"

	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// stale is what a request says the author saw of a record when the preview said otherwise.
const stale = "0000000000000000"

// batch is a load of notes against the records of a saga.
type batch struct {
	name    string
	records []obsidian.Record
	facts   []obsidian.HeldRecord
}

// batches are the loads whose final names depend on one another: two notes that swap their names, a chain of three, two
// notes of one name a record of the author keeps, a swap with a record edited in the app, a note that leaves its name to a
// new note, a record edited in the app whose note takes a new name and leaves its old one to a new note, a renamed file and
// a new note of the title of its record.
func batches() []batch {
	linked := func(id, key string, revision int64, path, name string) obsidian.HeldRecord {
		return linkedTo(id, key, revision, 1, noteRecord(path, name))
	}
	return []batch{{
		name:    "a swap",
		records: []obsidian.Record{noteRecord("Персонажи/А.md", "Ярослава"), noteRecord("Персонажи/Б.md", "Ведана")},
		facts:   []obsidian.HeldRecord{linked("r-a", "ведана", 1, "Персонажи/А.md", "Ведана"), linked("r-b", "ярослава", 1, "Персонажи/Б.md", "Ярослава")},
	}, {
		name: "a chain of three",
		records: []obsidian.Record{
			noteRecord("Персонажи/А.md", "Ярослава"), noteRecord("Персонажи/Б.md", "Злата"), noteRecord("Персонажи/В.md", "Ива"),
		},
		facts: []obsidian.HeldRecord{
			linked("r-a", "ведана", 1, "Персонажи/А.md", "Ведана"), linked("r-b", "ярослава", 1, "Персонажи/Б.md", "Ярослава"),
			linked("r-c", "злата", 1, "Персонажи/В.md", "Злата"),
		},
	}, {
		name:    "two notes of one name a record of the author keeps",
		records: []obsidian.Record{noteRecord("Места/Перевал.md", "Перевал"), noteRecord("Места/Застава.md", "Перевал")},
		facts:   []obsidian.HeldRecord{{ID: "r-pass", Key: "перевал", Revision: 2}},
	}, {
		name:    "a swap with a record edited in the app",
		records: []obsidian.Record{noteRecord("Персонажи/А.md", "Ярослава"), noteRecord("Персонажи/Б.md", "Ведана")},
		facts:   []obsidian.HeldRecord{linked("r-a", "ведана", 2, "Персонажи/А.md", "Ведана"), linked("r-b", "ярослава", 1, "Персонажи/Б.md", "Ярослава")},
	}, {
		name:    "a note leaves its name to a new note",
		records: []obsidian.Record{noteRecord("Персонажи/А.md", "Злата"), noteRecord("Персонажи/Н.md", "Ведана")},
		facts:   []obsidian.HeldRecord{linked("r-a", "ведана", 1, "Персонажи/А.md", "Ведана")},
	}, {
		name:    "a record edited in the app, its note renamed, its old name a new note's",
		records: []obsidian.Record{noteRecord("Персонажи/А.md", "Злата"), noteRecord("Персонажи/Н.md", "Ведана")},
		facts:   []obsidian.HeldRecord{linked("r-a", "ведана", 3, "Персонажи/А.md", "Ведана")},
	}, {
		name:    "a renamed file and a new note of the title of its record",
		records: []obsidian.Record{noteRecord("Персонажи/Ольга Сватова.md", "Ольга"), noteRecord("Герои/Ольга.md", "Ольга")},
		facts:   []obsidian.HeldRecord{linked("r-olga", "ольга", 1, "Персонажи/Ольга.md", "Ольга")},
	}}
}

// writes plays the plan of a load on the names of the records as the transaction of the load writes them: the records that
// change their names leave them first, then each write in turn, one name one record at every step. It says which write would
// meet another record on a name, write a record twice or write none.
func writes(test batch, states []obsidian.NoteState, outcomes []obsidian.NoteOutcome) error {
	names := map[string]string{}
	for _, held := range test.facts {
		names[held.ID] = held.Key
	}
	holder := func(key string) string {
		for id, name := range names {
			if name == key {
				return id
			}
		}
		return ""
	}
	over := func(outcome obsidian.NoteOutcome) bool {
		return outcome == obsidian.OutcomeUpdated || outcome == obsidian.OutcomeReplaced
	}
	for index, outcome := range outcomes {
		if target := states[index].Target; over(outcome) && names[target] != sagamemory.GlossaryKey(test.records[index].Draft.Name) {
			names[target] = "\x01" + target
		}
	}
	written := map[string]bool{}
	for index, outcome := range outcomes {
		note, key := test.records[index].Path, sagamemory.GlossaryKey(test.records[index].Draft.Name)
		switch {
		case over(outcome):
			target := states[index].Target
			if target == "" || written[target] {
				return fmt.Errorf("%s writes the record %q twice or none", note, target)
			}
			if id := holder(key); id != "" && id != target {
				return fmt.Errorf("%s writes %s over the name of %s", note, target, id)
			}
			written[target], names[target] = true, key
		case outcome == obsidian.OutcomeCreated:
			if id := holder(key); id != "" {
				return fmt.Errorf("%s makes a record of the name of %s", note, id)
			}
			names["new:"+note] = key
		}
	}
	return nil
}

// specs/2026-11-15-desk-obsidian-notes.md ON1.2–ON1.4, one rule of the final names: a load of what a preview showed gives the
// states of the preview; and whatever the author saw — every record seen as the preview showed it or any of them not — and
// whatever he decided, the plan of a load meets no record on a name and writes no record twice: a record a load leaves keeps
// its name, and a note whose new name such a record or another note keeps is kept itself. Only a race with the author is a
// conflict.
func TestTheNamesOfALoadNeverMeet(t *testing.T) {
	rules := []map[string]obsidian.Decision{
		{}, {obsidian.StateOtherSource: obsidian.DecisionReplace}, {obsidian.StateEditedInApp: obsidian.DecisionReplace},
		{obsidian.StateOtherSource: obsidian.DecisionReplace, obsidian.StateEditedInApp: obsidian.DecisionReplace},
	}
	for _, test := range batches() {
		facts := obsidian.NoteFacts{Records: test.facts}
		preview := obsidian.NoteStates(test.records, facts, nil, nil)
		honest := map[string]string{}
		for index, record := range test.records {
			honest[record.Path] = preview[index].Seen
		}
		if load := obsidian.NoteStates(test.records, facts, nil, honest); fmt.Sprint(load) != fmt.Sprint(preview) {
			t.Errorf("%s: the load sees %v, the preview showed %v", test.name, load, preview)
		}
		for mask := 0; mask < 1<<len(test.records); mask++ {
			seen := map[string]string{}
			for index, record := range test.records {
				seen[record.Path] = honest[record.Path]
				if mask&(1<<index) != 0 {
					seen[record.Path] = stale
				}
			}
			states := obsidian.NoteStates(test.records, facts, nil, seen)
			for _, rule := range rules {
				outcomes := obsidian.PlanNotes(test.records, states, seen, obsidian.Decisions{States: rule})
				if err := writes(test, states, outcomes); err != nil {
					t.Errorf("%s, stale %b, rule %v: %v (outcomes %v)", test.name, mask, rule, err, outcomes)
				}
			}
		}
	}
}

// ON1.3: a record a load leaves because the author did not see what the preview showed keeps its name, and so the notes that
// were to take it are kept and named — a swap with one side stale, a chain of three whose last link is stale.
func TestAStaleRecordKeepsTheNamesThatDependOnIt(t *testing.T) {
	for _, test := range []struct {
		batch batch
		stale string
	}{{batches()[0], "Персонажи/Б.md"}, {batches()[1], "Персонажи/В.md"}} {
		facts := obsidian.NoteFacts{Records: test.batch.facts}
		seen := map[string]string{}
		for index, state := range obsidian.NoteStates(test.batch.records, facts, nil, nil) {
			seen[test.batch.records[index].Path] = state.Seen
		}
		seen[test.stale] = stale
		states := obsidian.NoteStates(test.batch.records, facts, nil, seen)
		for index, outcome := range obsidian.PlanNotes(test.batch.records, states, seen, obsidian.Decisions{}) {
			if outcome != obsidian.OutcomeKeptChanged {
				t.Errorf("%s: %s = %s, %+v; want it kept as changed", test.batch.name, test.batch.records[index].Path, outcome, states[index])
			}
		}
	}
}
