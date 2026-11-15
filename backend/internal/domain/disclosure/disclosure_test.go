// SPDX-License-Identifier: AGPL-3.0-only

package disclosure

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const (
	chapterA = "11111111-1111-1111-1111-111111111111"
	chapterB = "22222222-2222-2222-2222-222222222222"
	chatA    = "33333333-3333-3333-3333-333333333333"
	recordA  = "44444444-4444-4444-4444-444444444444"
)

// JR2.3: a place goes once, at the larger precision, with the paragraphs of both; a record once; the largest list of names.
func TestMergeKeepsAPlaceOnceAtItsLargerPrecision(t *testing.T) {
	var first Named
	first.Chapter(chapterA, Paragraphs, 3, 5)
	first.Chapter(chapterA, Paragraphs, 6, 9)
	first.Chapter(chapterB, Excerpts, 0, 0)
	second := Named{
		Question: true, Memory: []string{recordA}, Names: 3, Sections: []int{2},
		Chapters:    []Chapter{{ID: chapterB, Precision: Whole}, {ID: chapterA, Precision: Titles}},
		Attachments: []Attachment{{Chat: chatA, Number: 1, Paragraphs: []Range{{From: 1, To: 4}}}},
	}
	merged := Merge(first, Merge(second, Named{Memory: []string{recordA}, Names: 2, Attachments: []Attachment{{Chat: chatA, Number: 1, Paragraphs: []Range{{From: 5, To: 9}}}}}))
	want := Named{
		Question: true, Memory: []string{recordA}, Names: 3, Sections: []int{2},
		Chapters:    []Chapter{{ID: chapterA, Precision: Paragraphs, Paragraphs: []Range{{From: 3, To: 9}}}, {ID: chapterB, Precision: Whole}},
		Attachments: []Attachment{{Chat: chatA, Number: 1, Paragraphs: []Range{{From: 1, To: 9}}}},
	}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged = %+v", merged)
	}
	// The file of the journal merges all the namings of its rows in one pass: the same as one after another.
	third := Named{Memory: []string{recordA}, Names: 2, Attachments: []Attachment{{Chat: chatA, Number: 1, Paragraphs: []Range{{From: 5, To: 9}}}}}
	if all := Merge(first, second, third); !reflect.DeepEqual(all, want) {
		t.Fatalf("merged at once = %+v", all)
	}
}

// JR1.4: the value goes to the gateway in one line of ASCII; a value of another form is refused before any record.
func TestTheHeaderIsOneLineOfASCIIAndOfItsFormOnly(t *testing.T) {
	var named Named
	named.Chapter(chapterA, Whole, 0, 0)
	named.Names = 214
	encoded, err := named.Encode()
	if err != nil || strings.ContainsAny(encoded, "\n\r") {
		t.Fatalf("encoded = %q, %v", encoded, err)
	}
	parsed, err := Parse(encoded)
	if err != nil || !reflect.DeepEqual(parsed, named) {
		t.Fatalf("parsed = %+v, %v", parsed, err)
	}
	for _, bad := range []string{
		``, `[]`, `{"chapters":[{"id":"x","p":"whole"}]}`, `{"chapters":[{"id":"` + chapterA + `","p":"all"}]}`,
		`{"chapters":[{"id":"` + chapterA + `","p":"whole","r":[{"f":1,"t":2}]}]}`, `{"chapters":[{"id":"` + chapterA + `","p":"paragraphs"}]}`,
		`{"chapters":[{"id":"` + chapterA + `","p":"paragraphs","r":[{"f":5,"t":2}]}]}`, `{"names":-1}`, `{"title":"Белый порт"}`,
		`{"question":true}{"question":true}`, `{"sections":[0]}`, `{"attachments":[{"chat":"` + chatA + `","n":0,"r":[{"f":1,"t":1}]}]}`,
	} {
		if _, err := Parse(bad); !errors.Is(err, ErrShape) {
			t.Fatalf("%q = %v", bad, err)
		}
	}
	if (Named{Protocol: true}).Empty() != true || (Named{Names: 1}).Empty() {
		t.Fatal("empty is the author's text only")
	}
}

// JR1.6: the refusal of the journal is its own kind with its code, not a stop of the reading.
func TestTheRefusalOfTheJournalIsItsOwnKind(t *testing.T) {
	err := error(&JournalError{Code: CodeUnavailable})
	if !errors.Is(err, ErrJournal) || CodeOf(err) != CodeUnavailable || CodeOf(errors.New("other")) != "" {
		t.Fatalf("err = %v", err)
	}
	ctx := With(context.Background(), Named{Threads: true})
	if named, ok := Of(ctx); !ok || !named.Threads {
		t.Fatal("the context lost the value")
	}
	if _, ok := Of(context.Background()); ok {
		t.Fatal("a context without a value has one")
	}
}

// specs/2026-11-15-desk-mcp.md MC4.2: an answer of several pages names the text of every chapter at Pages, its paragraphs
// taken off — the titles and the cards of chapters keep their precision; in the merge of a day Pages stands between the
// beginning and the whole — given whole and by pages, a place went whole; by pages and by paragraphs, by pages. The titles of
// sagas are counted, the largest count kept, and checked with the rest.
func TestThePagesOfAnAnswerStandBetweenTheBeginningAndTheWhole(t *testing.T) {
	const titled, carded, begun, excerpted = "55555555-5555-5555-5555-555555555555", "66666666-6666-6666-6666-666666666666", "77777777-7777-7777-7777-777777777777", "88888888-8888-8888-8888-888888888888"
	var answer Named
	answer.Chapter(chapterA, Paragraphs, 3, 9)
	answer.Chapter(chapterB, Whole, 0, 0)
	answer.Chapter(titled, Titles, 0, 0)
	answer.Chapter(carded, Cards, 0, 0)
	answer.Chapter(begun, Beginning, 0, 0)
	answer.Chapter(excerpted, Excerpts, 0, 0)
	answer.Memory = []string{recordA}
	paged := answer.InPages()
	want := Named{Chapters: []Chapter{
		{ID: chapterA, Precision: Pages}, {ID: chapterB, Precision: Pages}, {ID: titled, Precision: Titles}, {ID: carded, Precision: Cards},
		{ID: begun, Precision: Pages}, {ID: excerpted, Precision: Pages},
	}, Memory: []string{recordA}}
	if !reflect.DeepEqual(paged, want) || answer.Chapters[0].Precision != Paragraphs {
		t.Fatalf("paged = %+v, the answer = %+v", paged, answer)
	}
	var whole, paragraphs, beginning Named
	whole.Chapter(chapterA, Whole, 0, 0)
	paragraphs.Chapter(chapterB, Paragraphs, 1, 2)
	beginning.Chapter(chapterB, Beginning, 0, 0)
	merged := Merge(paged, whole, paragraphs, beginning)
	if merged.Chapters[0].Precision != Whole || merged.Chapters[1].Precision != Pages || merged.Chapters[1].Paragraphs != nil {
		t.Fatalf("merged = %+v", merged)
	}
	if sagas := Merge(Named{Sagas: 2}, Named{Sagas: 5}, Named{Sagas: 3}); sagas.Sagas != 5 || sagas.Empty() {
		t.Fatalf("sagas = %+v", sagas)
	}
	if _, err := Parse(`{"sagas":-1}`); !errors.Is(err, ErrShape) {
		t.Fatal("a negative count of sagas is of the form")
	}
	if _, err := Parse(`{"chapters":[{"id":"` + chapterA + `","p":"pages"}],"sagas":2}`); err != nil {
		t.Fatal(err)
	}
}
