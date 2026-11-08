package researchchat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/authormemory"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
)

var memoryCall = ToolContext{Caller: agent.ToolCaller{ThreadID: "root", RootThreadID: "root"}, TurnID: "turn"}

type memoryRepositoryStub struct {
	authority researchdomain.ReadAuthority
	drafts    []authormemory.Draft
	filter    authormemory.Filter
	page      authormemory.Page
	err       error
}

func (stub *memoryRepositoryStub) ReadAuthority(context.Context, ToolContext) (researchdomain.ReadAuthority, error) {
	return stub.authority, nil
}

func (stub *memoryRepositoryStub) RememberForTurn(_ context.Context, _ ToolContext, _ researchdomain.ReadAuthority, draft authormemory.Draft) error {
	stub.drafts = append(stub.drafts, draft)
	return stub.err
}

func (stub *memoryRepositoryStub) AuthorMemoryForTurn(_ context.Context, _ ToolContext, _ researchdomain.ReadAuthority, filter authormemory.Filter) (authormemory.Page, error) {
	stub.filter = filter
	return stub.page, stub.err
}

type chapterVersionStub struct{ version *sagamemory.Version }

func (stub chapterVersionStub) ChapterVersion(context.Context, string, string) (*sagamemory.Version, error) {
	return stub.version, nil
}

func memoryVersion() *sagamemory.Version {
	return sagamemory.NewVersion("project", "published", "fingerprint", []sagamemory.Chapter{
		{ID: "chapter-1", BookID: "book-1", BookOrder: 1, Order: 1, Title: "Глава 12. Порт"},
		{ID: "chapter-2", BookID: "book-2", BookOrder: 2, Order: 1, Title: "Глава 13. Башня"},
	})
}

// R4.1, R4.2: a record keeps its kind, text, chapter and name, and the answer signs it with its kind.
func TestRememberKeepsTheRecordOfTheTurn(t *testing.T) {
	repository := &memoryRepositoryStub{authority: researchdomain.ReadAuthority{ProjectID: "project", SourceView: "published", Scope: researchdomain.ScopeProject, JobID: "turn-job"}}
	tools := NewAuthorMemoryTools(repository, chapterVersionStub{memoryVersion()})
	output, err := tools.Remember(context.Background(), memoryCall, RememberInput{Kind: "plan", Text: "  Кира узнает правду  ", Chapter: "13", Name: " Кира "})
	if err != nil {
		t.Fatal(err)
	}
	if len(repository.drafts) != 1 || repository.drafts[0] != (authormemory.Draft{Kind: authormemory.KindPlan, Text: "Кира узнает правду", ChapterID: "chapter-2", Name: "Кира", JobID: "turn-job"}) {
		t.Fatalf("drafts = %+v", repository.drafts)
	}
	for _, want := range []string{"план автора, в книге этого ещё нет", "глава 13", "имя: Кира", "отменить", "«Что помнит помощник»"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q: %s", want, output)
		}
	}
}

// A mistaken call is corrected in the same run and nothing is written; a reader is told to say so.
func TestRememberRefusals(t *testing.T) {
	authority := researchdomain.ReadAuthority{ProjectID: "project", SourceView: "published", Scope: researchdomain.ScopeBook, BookID: "book-1"}
	for name, test := range map[string]struct {
		input RememberInput
		path  string
	}{
		"kind":       {RememberInput{Kind: "fact", Text: "т"}, "$.kind"},
		"empty text": {RememberInput{Kind: "plan", Text: "  "}, "$.text"},
		"long text":  {RememberInput{Kind: "plan", Text: strings.Repeat("т", authormemory.MaxTextRunes+1)}, "$.text"},
		"long name":  {RememberInput{Kind: "plan", Text: "т", Name: strings.Repeat("и", authormemory.MaxNameRunes+1)}, "$.name"},
		"no chapter": {RememberInput{Kind: "plan", Text: "т", Chapter: "99"}, "$.chapter"},
		"other book": {RememberInput{Kind: "plan", Text: "т", Chapter: "13"}, "$.chapter"},
	} {
		repository := &memoryRepositoryStub{authority: authority}
		_, err := NewAuthorMemoryTools(repository, chapterVersionStub{memoryVersion()}).Remember(context.Background(), memoryCall, test.input)
		argument, ok := IsArgumentError(err)
		if !ok || argument.Path != test.path || len(repository.drafts) != 0 {
			t.Fatalf("%s: err = %v, drafts = %+v", name, err, repository.drafts)
		}
	}
	repository := &memoryRepositoryStub{authority: authority, err: researchdomain.ErrNotEditor}
	_, err := NewAuthorMemoryTools(repository, chapterVersionStub{memoryVersion()}).Remember(context.Background(), memoryCall, RememberInput{Kind: "plan", Text: "т"})
	outcome, ok := AsToolOutcome(err)
	if !ok || outcome.Type != "memory_forbidden" {
		t.Fatalf("a reader = %v", err)
	}
	foreign := errors.New("database")
	repository = &memoryRepositoryStub{authority: authority, err: foreign}
	if _, err := NewAuthorMemoryTools(repository, chapterVersionStub{memoryVersion()}).Remember(context.Background(), memoryCall, RememberInput{Kind: "plan", Text: "т"}); !errors.Is(err, foreign) {
		t.Fatalf("another failure = %v", err)
	}
}

// R4.3, R4.4: every record is signed with its kind, a plan is never told as what happened, a correction
// stands over the cards and not over the text; a record of a chapter that is gone says so.
func TestMemorySignsEveryRecordWithItsKind(t *testing.T) {
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	repository := &memoryRepositoryStub{authority: researchdomain.ReadAuthority{ProjectID: "project", SourceView: "published"}, page: authormemory.Page{Total: 3, Records: []authormemory.Record{
		{Kind: authormemory.KindPlan, Text: "Кира узнает правду", ChapterID: "chapter-1", Name: "Кира", AuthorName: "Лена", CreatedAt: at},
		{Kind: authormemory.KindCorrection, Text: "Ей двадцать лет", ChapterID: "gone", AuthorName: "Лена", CreatedAt: at},
		{Kind: authormemory.KindRejected, Text: "Брат не возвращается", AuthorName: "Лена", CreatedAt: at},
	}}}
	tools := NewAuthorMemoryTools(repository, chapterVersionStub{memoryVersion()})
	output, err := tools.Memory(context.Background(), memoryCall, MemoryInput{Query: " Кира ", Kind: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	if repository.filter.Query != "Кира" || len(repository.filter.Kinds) != 1 || repository.filter.Kinds[0] != authormemory.KindPlan {
		t.Fatalf("filter = %+v", repository.filter)
	}
	for _, want := range []string{
		"Записей: 3.", "план ещё не случился в книге", "важнее карточек, но не текста",
		"[план автора, в книге этого ещё нет; глава 12; имя: Кира] Кира узнает правду (Лена, 02.11.2026)",
		"[поправка автора, важнее карточек, но не текста; глава удалена] Ей двадцать лет",
		"[отвергнуто автором] Брат не возвращается",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
}

func TestMemoryNamesWhatIsNotShown(t *testing.T) {
	long := strings.Repeat("т", authormemory.MaxTextRunes)
	page := authormemory.Page{Total: 40}
	for range 40 {
		page.Records = append(page.Records, authormemory.Record{Kind: authormemory.KindPlan, Text: long, AuthorName: "Лена"})
	}
	output := renderMemory(page, nil)
	if !strings.Contains(output, "показаны 18 самых новых") {
		t.Fatalf("output = %s", output[:200])
	}
	if _, err := NewAuthorMemoryTools(&memoryRepositoryStub{}, chapterVersionStub{memoryVersion()}).Memory(context.Background(), memoryCall, MemoryInput{Kind: "fact"}); err == nil {
		t.Fatal("an unknown kind was read")
	}
	empty, err := NewAuthorMemoryTools(&memoryRepositoryStub{}, chapterVersionStub{memoryVersion()}).Memory(context.Background(), memoryCall, MemoryInput{})
	if err != nil || !strings.HasPrefix(empty, "Записей: 0.") {
		t.Fatalf("empty memory = %q, %v", empty, err)
	}
}
