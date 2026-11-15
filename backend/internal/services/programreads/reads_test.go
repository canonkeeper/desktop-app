// SPDX-License-Identifier: AGPL-3.0-only

package programreads

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modeljournal"
	"canonkeeper/backend/internal/projects"
)

const author = "aaaaaaaa-0000-4000-8000-000000000001"

// shelf is the list of the sagas of the author, two to a page.
type shelf []projects.ProjectRecord

func (sagas shelf) ListProjects(_ context.Context, actor string, input projects.ListProjectsInput) (projects.ProjectPage, error) {
	if actor != author {
		return projects.ProjectPage{}, errors.New("another author")
	}
	start := 0
	if input.Cursor != "" {
		fmt.Sscan(input.Cursor, &start)
	}
	end := min(start+2, len(sagas))
	page := projects.ProjectPage{Data: sagas[start:end]}
	if end < len(sagas) {
		next := fmt.Sprint(end)
		page.Meta = projects.PageMeta{HasMore: true, NextCursor: &next}
	}
	return page, nil
}

type journal struct {
	reads []modeljournal.ProgramRead
	err   error
}

func (journal *journal) RecordRead(_ context.Context, read modeljournal.ProgramRead) error {
	if journal.err != nil {
		return journal.err
	}
	journal.reads = append(journal.reads, read)
	return nil
}

func saga(id, title string) projects.ProjectRecord {
	return projects.ProjectRecord{ID: id, Title: title, BookCount: 2, ChapterCount: 40}
}

// MC2.3: the saga of an argument — none for the one saga; a key or a longer beginning of an id before a title; a title one
// living saga has, in any case; every refusal of its own kind, naming no saga. Every page of the list is read.
func TestTheArgumentNamesTheSaga(t *testing.T) {
	ctx := context.Background()
	one := New(shelf{saga("11111111-1111-4111-8111-111111111111", "Северный предел")}, &journal{}, author)
	if found, err := one.Find(ctx, ""); err != nil || found.Key != "11111111" || found.Books != 2 || found.Chapters != 40 {
		t.Fatalf("one saga: %+v, %v", found, err)
	}
	if _, err := New(shelf{}, &journal{}, author).Find(ctx, "Северный предел"); !errors.Is(err, ErrNoSagas) {
		t.Fatalf("no sagas: %v", err)
	}
	many := New(shelf{
		saga("22222222-1111-4111-8111-111111111111", "Северный предел"),
		saga("33333333-1111-4111-8111-111111111111", "Южный предел"),
		saga("33333333-2222-4111-8111-111111111111", "Южный предел"),
		saga("44444444-1111-4111-8111-111111111111", "  Северный Предел 2 "),
		saga("55555555-1111-4111-8111-111111111111", "Ёлка"),
	}, &journal{}, author)
	for _, example := range []struct {
		argument, want string
		err            error
	}{
		{"", "", ErrSeveral},
		{"22222222", "22222222-1111-4111-8111-111111111111", nil},
		{"22222222-1111", "22222222-1111-4111-8111-111111111111", nil},
		{"33333333", "", ErrAmbiguous},
		{"33333333-2", "33333333-2222-4111-8111-111111111111", nil},
		{"  северный ПРЕДЕЛ ", "22222222-1111-4111-8111-111111111111", nil},
		{"северный предел 2", "44444444-1111-4111-8111-111111111111", nil},
		{"Ёлка", "55555555-1111-4111-8111-111111111111", nil},
		{"южный предел", "", ErrSeveral},
		{"Западный предел", "", ErrNoSuch},
		{"66666666", "", ErrNoSuch},
	} {
		found, err := many.Find(ctx, example.argument)
		if !errors.Is(err, example.err) || found.ID != example.want {
			t.Fatalf("%q: %+v, %v", example.argument, found, err)
		}
	}
	all, err := many.Sagas(ctx)
	if err != nil || len(all) != 5 || all[4].Key != "55555555" {
		t.Fatalf("the sagas of every page: %+v, %v", all, err)
	}
}

// MC4.2: a reading goes to the journal with the number, the time, the author and the name of the program; a page of an
// answer of several pages names its places by pages; sagas counts the titles; a saga gone before the record is no such saga.
func TestTheRecordOfAReadingIsThePagesOfItsAnswer(t *testing.T) {
	ctx := context.Background()
	kept := &journal{}
	reads := New(shelf{}, kept, author)
	var named disclosure.Named
	named.Chapter("11111111-1111-4111-8111-111111111111", disclosure.Paragraphs, 1, 5)
	if err := reads.Record(ctx, Read{Client: "claude-code", ProjectID: "p", Bytes: 900, Page: 2, Pages: 6, Named: named}); err != nil {
		t.Fatal(err)
	}
	if err := reads.Record(ctx, Read{Client: "x", Title: "Codex", Bytes: 30, Page: 1, Pages: 1, Sagas: 2}); err != nil {
		t.Fatal(err)
	}
	first, second := kept.reads[0], kept.reads[1]
	if first.ID == "" || first.At.IsZero() || first.ActorUserID != author || first.Client != "Claude Code" || first.Page != 2 ||
		first.Pages != 6 || first.Named.Chapters[0].Precision != disclosure.Pages || first.Named.Chapters[0].Paragraphs != nil {
		t.Fatalf("the page: %+v", first)
	}
	if second.Client != "Codex" || second.Named.Sagas != 2 || second.ProjectID != "" {
		t.Fatalf("sagas: %+v", second)
	}
	kept.err = fmt.Errorf("gone: %w", modeljournal.ErrSagaGone)
	if err := reads.Record(ctx, Read{Client: "x", ProjectID: "p", Page: 1, Pages: 1}); !errors.Is(err, ErrNoSuch) {
		t.Fatalf("a saga gone: %v", err)
	}
	kept.err = errors.New("the journal is down")
	if err := reads.Record(ctx, Read{Client: "x", ProjectID: "p", Page: 1, Pages: 1}); err == nil || errors.Is(err, ErrNoSuch) {
		t.Fatalf("a failed record: %v", err)
	}
}

// MC4.3: the name of a program is its title, its known name, or its name cleaned — NFC, no Cc and Cf, its spaces one, up to
// 200 characters; none — «программа без имени».
func TestTheNameOfAProgramIsWhatItSaysOfItself(t *testing.T) {
	for _, example := range []struct{ name, title, want string }{
		{"claude-code", "Claude Code", "Claude Code"},
		{"claude-code", "", "Claude Code"},
		{"claude-ai", "", "Claude Desktop"},
		{"codex-mcp-client", "", "Codex"},
		{"my-writer", "", "my-writer"},
		{"", "", Unnamed},
		{"  \t ", "​", Unnamed},
		{"evil‮edoc", "", "evil edoc"},
		{"на​звание   с\nпереводом", "", "на звание с переводом"},
		{"é", "", "é"},
		{strings.Repeat("я", 500), "", strings.Repeat("я", 200)},
		{"claude-ai", "  Мой‮ Claude ", "Мой Claude"},
	} {
		if got := ProgramName(example.name, example.title); got != example.want {
			t.Fatalf("%q %q = %q", example.name, example.title, got)
		}
	}
}

// MC4.2: the naming of a call keeps in memory what its tools named, merged; it writes nothing.
func TestTheNamingOfACallKeepsItInMemory(t *testing.T) {
	naming := NewNaming()
	var first, second disclosure.Named
	first.Chapter("11111111-1111-4111-8111-111111111111", disclosure.Paragraphs, 1, 3)
	second.Chapter("11111111-1111-4111-8111-111111111111", disclosure.Paragraphs, 4, 6)
	second.Threads = true
	for _, named := range []disclosure.Named{first, second} {
		if id, err := naming.Name(context.Background(), "program", "p", named); err != nil || id != "" {
			t.Fatal(id, err)
		}
	}
	got := naming.Named()
	if !got.Threads || len(got.Chapters) != 1 || got.Chapters[0].Paragraphs[0] != (disclosure.Range{From: 1, To: 6}) {
		t.Fatalf("named = %+v", got)
	}
}
