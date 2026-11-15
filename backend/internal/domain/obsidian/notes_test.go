// SPDX-License-Identifier: AGPL-3.0-only

package obsidian_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/sagaarchive"
)

func noteRecord(path, name string, aliases ...string) obsidian.Record {
	return obsidian.Record{
		Path: path, Aliases: aliases,
		Draft: authormaterials.Draft{Name: name, Kind: authormaterials.KindCharacter, Description: "Текст заметки " + path},
	}
}

// linkedTo is a record of the saga a note loaded and its link: the revision of the record now, the revision the link keeps
// and the fingerprint of the record the note gave then.
func linkedTo(id, key string, revision, linked int64, note obsidian.Record) obsidian.HeldRecord {
	return obsidian.HeldRecord{
		ID: id, Key: key, Revision: revision, Note: note.Path, LinkRevision: linked,
		Fingerprint: obsidian.RecordsFingerprint([]obsidian.Record{note}),
	}
}

type wanted struct {
	state   string
	target  string
	changed bool
	book    string
	held    string
}

func statesOf(records []obsidian.Record, states []obsidian.NoteState) map[string]wanted {
	got := map[string]wanted{}
	for index, state := range states {
		got[records[index].Path] = wanted{state: state.State, target: state.Target, changed: state.Changed, book: state.BookName, held: state.HeldBy}
	}
	return got
}

// specs/2026-11-15-desk-obsidian-notes.md ON1.2: the table of the states by one function — a new name; a name of the book; a
// name by «Другие имена»; a name a record of the author keeps; the same note unchanged and changed; a record edited in the
// app; a title changed to a name another record keeps; two linked notes that swap their names; two notes of the batch of one
// name; a renamed file with a title and without one; a record that leaves no name for another note when its own move fails;
// a name a record of another note of the batch keeps; two notes of a name a record of the author keeps; a swap with a record
// edited in the app. Of other_source the state says which note of the batch has the name — gets it or keeps it by its record —,
// none when a record no note of the batch owns keeps it.
func TestTheStatesOfTheRecordsOfNotes(t *testing.T) {
	olga := noteRecord("Персонажи/Ольга.md", "Ольга")
	book := map[string]string{"мирон": "Мирон", "ольга": "Ольга"}
	pass := obsidian.HeldRecord{ID: "r-pass", Key: "перевал", Revision: 2}
	cases := []struct {
		name    string
		records []obsidian.Record
		facts   []obsidian.HeldRecord
		want    map[string]wanted
	}{{
		name:    "a new name, a name of the book and one by its other names",
		records: []obsidian.Record{noteRecord("Персонажи/Ива.md", "Ива"), noteRecord("Персонажи/Мирон.md", "Мирон"), noteRecord("Персонажи/Сватов.md", "Мирон Сватов", "Мирон")},
		want: map[string]wanted{
			"Персонажи/Ива.md": {state: obsidian.StateNew}, "Персонажи/Мирон.md": {state: obsidian.StateInBook, book: "Мирон"},
			"Персонажи/Сватов.md": {state: obsidian.StateInBookByAlias, book: "Мирон"},
		},
	}, {
		name:    "a name a record of the author keeps",
		records: []obsidian.Record{noteRecord("Места/Перевал.md", "Перевал")},
		facts:   []obsidian.HeldRecord{pass},
		want:    map[string]wanted{"Места/Перевал.md": {state: obsidian.StateOtherSource, target: "r-pass"}},
	}, {
		name:    "the same note unchanged",
		records: []obsidian.Record{olga},
		facts:   []obsidian.HeldRecord{linkedTo("r-olga", "ольга", 3, 3, olga)},
		want:    map[string]wanted{olga.Path: {state: obsidian.StateSameNote, target: "r-olga"}},
	}, {
		name:    "the same note changed",
		records: []obsidian.Record{noteRecord(olga.Path, "Ольга", "Ольга Сватова")},
		facts:   []obsidian.HeldRecord{linkedTo("r-olga", "ольга", 3, 3, olga)},
		want:    map[string]wanted{olga.Path: {state: obsidian.StateSameNote, target: "r-olga", changed: true}},
	}, {
		name:    "a record edited in the app",
		records: []obsidian.Record{olga},
		facts:   []obsidian.HeldRecord{linkedTo("r-olga", "ольга", 4, 3, olga)},
		want:    map[string]wanted{olga.Path: {state: obsidian.StateEditedInApp, target: "r-olga"}},
	}, {
		name:    "a title changed to a name a record of the author keeps",
		records: []obsidian.Record{noteRecord(olga.Path, "Перевал")},
		facts:   []obsidian.HeldRecord{linkedTo("r-olga", "ольга", 3, 3, olga), pass},
		want:    map[string]wanted{olga.Path: {state: obsidian.StateOtherSource, target: "r-pass"}},
	}, {
		name:    "two linked notes swap their names",
		records: []obsidian.Record{noteRecord("Персонажи/А.md", "Ярослава"), noteRecord("Персонажи/Б.md", "Ведана")},
		facts: []obsidian.HeldRecord{
			linkedTo("r-a", "ведана", 1, 1, noteRecord("Персонажи/А.md", "Ведана")),
			linkedTo("r-b", "ярослава", 1, 1, noteRecord("Персонажи/Б.md", "Ярослава")),
		},
		want: map[string]wanted{
			"Персонажи/А.md": {state: obsidian.StateSameNote, target: "r-a", changed: true},
			"Персонажи/Б.md": {state: obsidian.StateSameNote, target: "r-b", changed: true},
		},
	}, {
		name:    "two notes of the batch of one name: the first by the order of the paths",
		records: []obsidian.Record{noteRecord("Персонажи/Ива 10.md", "Ива"), noteRecord("Персонажи/Ива 2.md", "Ива")},
		want: map[string]wanted{
			"Персонажи/Ива 2.md": {state: obsidian.StateNew}, "Персонажи/Ива 10.md": {state: obsidian.StateOtherSource, held: "Персонажи/Ива 2.md"},
		},
	}, {
		name:    "a renamed file with the title of its record: the record of the note gone may be replaced",
		records: []obsidian.Record{noteRecord("Персонажи/Ольга Сватова.md", "Ольга")},
		facts:   []obsidian.HeldRecord{linkedTo("r-olga", "ольга", 3, 3, olga)},
		want:    map[string]wanted{"Персонажи/Ольга Сватова.md": {state: obsidian.StateOtherSource, target: "r-olga"}},
	}, {
		name:    "a renamed file without a title is a new name",
		records: []obsidian.Record{noteRecord("Персонажи/Ольга Сватова.md", "Ольга Сватова")},
		facts:   []obsidian.HeldRecord{linkedTo("r-olga", "ольга", 3, 3, olga)},
		want:    map[string]wanted{"Персонажи/Ольга Сватова.md": {state: obsidian.StateNew}},
	}, {
		name: "a note whose new name is kept stays where it was, and the note that took its old name loses it",
		records: []obsidian.Record{
			noteRecord("Персонажи/А.md", "Перевал"), noteRecord("Персонажи/В.md", "Ведана"),
		},
		facts: []obsidian.HeldRecord{linkedTo("r-a", "ведана", 1, 1, noteRecord("Персонажи/А.md", "Ведана")), pass},
		want: map[string]wanted{
			"Персонажи/А.md": {state: obsidian.StateOtherSource, target: "r-pass"},
			"Персонажи/В.md": {state: obsidian.StateOtherSource, held: "Персонажи/А.md"},
		},
	}, {
		name: "two linked notes renamed to one free name: the first by the order of the paths",
		records: []obsidian.Record{
			noteRecord("Персонажи/А.md", "Ярослава"), noteRecord("Персонажи/Б.md", "Ярослава"),
		},
		facts: []obsidian.HeldRecord{
			linkedTo("r-a", "ведана", 1, 1, noteRecord("Персонажи/А.md", "Ведана")),
			linkedTo("r-b", "злата", 1, 1, noteRecord("Персонажи/Б.md", "Злата")),
		},
		want: map[string]wanted{
			"Персонажи/А.md": {state: obsidian.StateSameNote, target: "r-a", changed: true},
			"Персонажи/Б.md": {state: obsidian.StateOtherSource, held: "Персонажи/А.md"},
		},
	}, {
		name:    "a name the record of another note of the batch keeps is not replaced",
		records: []obsidian.Record{noteRecord("Герои/Ольга.md", "Ольга"), olga},
		facts:   []obsidian.HeldRecord{linkedTo("r-olga", "ольга", 3, 3, olga)},
		want: map[string]wanted{
			"Герои/Ольга.md": {state: obsidian.StateOtherSource, held: olga.Path}, olga.Path: {state: obsidian.StateSameNote, target: "r-olga"},
		},
	}, {
		name:    "two notes of one name a record of the author keeps: only the first by the order of the paths may replace it",
		records: []obsidian.Record{noteRecord("Места/Перевал.md", "Перевал"), noteRecord("Места/Застава.md", "Перевал")},
		facts:   []obsidian.HeldRecord{pass},
		want: map[string]wanted{
			"Места/Застава.md": {state: obsidian.StateOtherSource, target: "r-pass"}, "Места/Перевал.md": {state: obsidian.StateOtherSource, held: "Места/Застава.md"},
		},
	}, {
		name:    "a swap with a record edited in the app: the name of each note the record of the other keeps",
		records: []obsidian.Record{noteRecord("Персонажи/А.md", "Ярослава"), noteRecord("Персонажи/Б.md", "Ведана")},
		facts: []obsidian.HeldRecord{
			linkedTo("r-a", "ведана", 2, 1, noteRecord("Персонажи/А.md", "Ведана")),
			linkedTo("r-b", "ярослава", 1, 1, noteRecord("Персонажи/Б.md", "Ярослава")),
		},
		want: map[string]wanted{
			"Персонажи/А.md": {state: obsidian.StateOtherSource, held: "Персонажи/Б.md"},
			"Персонажи/Б.md": {state: obsidian.StateOtherSource, held: "Персонажи/А.md"},
		},
	}}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			states := obsidian.NoteStates(test.records, obsidian.NoteFacts{Records: test.facts}, book, nil)
			if got := statesOf(test.records, states); fmt.Sprint(got) != fmt.Sprint(test.want) {
				t.Fatalf("the states = %v, want %v", got, test.want)
			}
		})
	}
}

// ON1.3: what the author sees of a state — one for every state that creates a record, another for another target or
// revision of the record a write goes to.
func TestWhatTheAuthorSeesOfAState(t *testing.T) {
	olga := noteRecord("Персонажи/Ольга.md", "Ольга")
	seen := func(facts []obsidian.HeldRecord, book map[string]string, record obsidian.Record) string {
		return obsidian.NoteStates([]obsidian.Record{record}, obsidian.NoteFacts{Records: facts}, book, nil)[0].Seen
	}
	created := seen(nil, nil, olga)
	if created != seen(nil, map[string]string{"ольга": "Ольга"}, olga) || created == "" {
		t.Fatal("a new record and a record of a name of the book are seen apart")
	}
	same := seen([]obsidian.HeldRecord{linkedTo("r-olga", "ольга", 3, 3, olga)}, nil, olga)
	if same == created || same == seen([]obsidian.HeldRecord{linkedTo("r-olga", "ольга", 5, 5, olga)}, nil, olga) {
		t.Fatal("the revision of the record of the note is not seen")
	}
	if seen([]obsidian.HeldRecord{{ID: "r-1", Key: "ольга", Revision: 1}}, nil, olga) == seen([]obsidian.HeldRecord{{ID: "r-2", Key: "ольга", Revision: 1}}, nil, olga) {
		t.Fatal("the record that keeps the name is not seen")
	}
}

// ON1.6: a note of a name of no key and a note whose record the rules of a record refuse are skipped by their reasons, the
// same rule for the preview and the load; the record is what those rules keep.
func TestTheRulesOfARecordSkipANote(t *testing.T) {
	plain := func(text string) []string { return strings.Split(text, "\n") }
	if _, skip := obsidian.Material(obsidian.Note{Path: "Персонажи/!!!.md", Name: "!!!", Body: "Кто-то."}, authormaterials.KindCharacter, plain); skip != obsidian.SkipNoName {
		t.Fatalf("a name of no key = %q", skip)
	}
	if _, skip := obsidian.Material(obsidian.Note{Path: "Персонажи/Ива.md", Name: "Ива", Body: "Знак \x01 не держится."}, authormaterials.KindCharacter, plain); skip != obsidian.SkipEncoding {
		t.Fatalf("a record the rules refuse = %q", skip)
	}
	record, skip := obsidian.Material(obsidian.Note{Path: "Персонажи /Ива.md", Name: "Ива", Body: "Травница."}, authormaterials.KindCharacter, plain)
	if skip != "" || record.Draft.Section != "Персонажи" {
		t.Fatalf("the section of a folder with a space at its end = %q, %q", record.Draft.Section, skip)
	}
	if clean, ok := authormaterials.CleanDraft(record.Draft); !ok || clean.Section != record.Draft.Section {
		t.Fatal("the record is not what the rules of a record keep")
	}
}

// ON1.6, ON1.7: the source of a record is the path of its note by the rule of the bridge — a line the saga file keeps —, and
// a load past 5 000 records of the load or of the saga after it is refused with its numbers.
func TestTheSourceAndTheLimitOfTheRecordsOfNotes(t *testing.T) {
	if got := obsidian.NoteSource("Персонажи/Мирон Сватов.md"); got != "Персонажи/Мирон Сватов.md" {
		t.Fatalf("the source = %q", got)
	}
	long := strings.Repeat("Очень длинная папка/", 20) + "Мирон.md"
	got := obsidian.NoteSource(long)
	if utf8.RuneCountInString(got) != authormaterials.MaxSourceNameRunes || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "/Мирон.md") ||
		!sagaarchive.ValidLine(got, authormaterials.MaxSourceNameRunes) {
		t.Fatalf("the source of a long path = %q", got)
	}
	if got := obsidian.NoteSource("Персонажи/Ива\u0085�.md"); !sagaarchive.ValidLine(got, authormaterials.MaxSourceNameRunes) {
		t.Fatalf("the source with a control and a lost sign = %q", got)
	}
	if err := obsidian.CheckRecords(obsidian.MaxRecords, 0, obsidian.MaxRecords); err != nil {
		t.Fatalf("5 000 records = %v", err)
	}
	if err := obsidian.CheckRecords(obsidian.MaxRecords+1, 0, 0); err != (obsidian.TooManyRecords{Records: obsidian.MaxRecords + 1}) {
		t.Fatalf("5 001 records of a load = %v", err)
	}
	if err := obsidian.CheckRecords(10, obsidian.MaxRecords-5, 6); err != (obsidian.TooManyRecords{Records: 10, Saga: obsidian.MaxRecords + 1}) {
		t.Fatalf("5 001 records of the saga after the load = %v", err)
	}
}

// ON1.3, ON1.4: what a load does with each record — kept when its state is not what the preview saw, whatever the
// decision; a new record for the states that create one; the same note written only when it changed; a record edited in the
// app and the record that keeps the name of the note written only by a decision to replace, by the note or by its state; a
// name another note of the load gets is never replaced.
func TestWhatALoadDoesWithEachRecord(t *testing.T) {
	olga := noteRecord("Персонажи/Ольга.md", "Ольга")
	records := []obsidian.Record{
		noteRecord("Персонажи/Ива.md", "Ива"), noteRecord("Персонажи/Мирон.md", "Мирон"), olga,
		noteRecord("Персонажи/Злата.md", "Злата"), noteRecord("Места/Перевал.md", "Перевал"), noteRecord("Места/Застава.md", "Застава"),
		noteRecord("Персонажи/Ивы.md", "Ива"), noteRecord("Персонажи/Ведана.md", "Ведана"),
	}
	facts := obsidian.NoteFacts{Records: []obsidian.HeldRecord{
		linkedTo("r-olga", "ольга", 2, 2, olga),
		linkedTo("r-zlata", "злата", 3, 2, noteRecord("Персонажи/Злата.md", "Злата")),
		{ID: "r-pass", Key: "перевал", Revision: 1}, {ID: "r-post", Key: "застава", Revision: 1},
		linkedTo("r-vedana", "ведана", 4, 3, noteRecord("Персонажи/Ведана.md", "Ведана")),
	}}
	states := obsidian.NoteStates(records, obsidian.NoteFacts{Records: facts.Records}, map[string]string{"мирон": "Мирон"}, nil)
	seen := map[string]string{}
	for index, record := range records {
		seen[record.Path] = states[index].Seen
	}
	seen["Персонажи/Мирон.md"] = "something else"
	decisions := obsidian.Decisions{
		States: map[string]obsidian.Decision{obsidian.StateOtherSource: obsidian.DecisionReplace, obsidian.StateEditedInApp: obsidian.DecisionReplace},
		Notes:  map[string]obsidian.Decision{"Места/Застава.md": obsidian.DecisionKeep, "Персонажи/Ведана.md": obsidian.DecisionKeep},
	}
	got := map[string]obsidian.NoteOutcome{}
	for index, outcome := range obsidian.PlanNotes(records, states, seen, decisions) {
		got[records[index].Path] = outcome
	}
	want := map[string]obsidian.NoteOutcome{
		"Персонажи/Ива.md": obsidian.OutcomeCreated, "Персонажи/Мирон.md": obsidian.OutcomeKeptChanged,
		olga.Path: obsidian.OutcomeUnchanged, "Персонажи/Злата.md": obsidian.OutcomeUpdated, "Места/Перевал.md": obsidian.OutcomeReplaced,
		"Места/Застава.md": obsidian.OutcomeKeptTaken, "Персонажи/Ивы.md": obsidian.OutcomeKeptTaken,
		"Персонажи/Ведана.md": obsidian.OutcomeKeptEdited,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("the outcomes = %v, want %v", got, want)
	}
	changed := noteRecord(olga.Path, "Ольга", "Ольга Сватова")
	again := obsidian.NoteStates([]obsidian.Record{changed}, facts, nil, nil)
	if outcome := obsidian.PlanNotes([]obsidian.Record{changed}, again, map[string]string{olga.Path: again[0].Seen}, obsidian.Decisions{}); outcome[0] != obsidian.OutcomeUpdated {
		t.Fatalf("the same note changed = %s", outcome[0])
	}
}
