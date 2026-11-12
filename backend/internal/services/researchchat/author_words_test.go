package researchchat

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/authormemory"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

// authorWordsStub is the memory of the author the saga tools read, under the authority each call passes.
type authorWordsStub struct {
	records     []authormemory.Record
	authorities []researchdomain.ReadAuthority
	filters     []authormemory.Filter
}

func (stub *authorWordsStub) AuthorMemoryForTurn(_ context.Context, _ ToolContext, authority researchdomain.ReadAuthority, filter authormemory.Filter) (authormemory.Page, error) {
	stub.authorities = append(stub.authorities, authority)
	stub.filters = append(stub.filters, filter)
	return authormemory.Page{Records: stub.records, Total: int64(len(stub.records))}, nil
}

func record(id string, kind authormemory.Kind, name, text string) authormemory.Record {
	return authormemory.Record{ID: id, Kind: kind, Name: name, Text: text, CreatedAt: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}
}

const (
	correctionSigned = "[поправка автора: важнее карточек и пересказов; если текст книги говорит иначе — назови оба варианта"
	planSigned       = "[план автора: в книге этого ещё нет"
	authorWordsHead  = "Автор сказал об этом"
)

// specs/2026-11-11-chat-corrections.md CR1.1, CR1.3, CR1.4: entity of a hero brings the author's correction and plan about
// him, newest first, each signed with its kind, after the answer it had before; a rule of style, what the author rejected and
// a record about another hero do not come, and without records the answer is the one it was to the sign.
func TestEntityBringsTheCorrectionAndThePlanOfTheAuthorAboutTheHero(t *testing.T) {
	tools, repository := newLayerTools(researchdomain.ScopeProject, "")
	ctx := context.Background()
	plain, err := tools.Entity(ctx, rootCall, EntityInput{Name: "Мирон"})
	if err != nil {
		t.Fatal(err)
	}
	memory := &authorWordsStub{}
	tools.WithAuthorMemory(memory)
	if output, err := tools.Entity(ctx, rootCall, EntityInput{Name: "Мирон"}); err != nil || output != plain {
		t.Fatalf("without records: %q, %v; want %q", output, err, plain)
	}
	correction := record("1", authormemory.KindCorrection, "Мирон", "Мирону двадцать лет, а не двадцать четыре: так я решил.")
	correction.ChapterID = "c1"
	memory.records = []authormemory.Record{
		correction,
		record("2", authormemory.KindStyleRule, "Мирон", "Мирон говорит коротко."),
		record("3", authormemory.KindPlan, "", "В пятом томе Мирон уйдёт на север."),
		record("4", authormemory.KindRejected, "Мирон", "Мирон не погибнет."),
		record("5", authormemory.KindCorrection, "Вьюжница", "Вьюжница не ждала у ворот."),
	}
	output, err := tools.Entity(ctx, rootCall, EntityInput{Name: "Мирон"})
	if err != nil {
		t.Fatal(err)
	}
	block, ok := strings.CutPrefix(output, strings.TrimRight(plain, "\n")+"\n\n")
	if !ok {
		t.Fatalf("the answer before the block changed: %q", output)
	}
	want := authorWordsHead + " (записи памяти автора, не текст книги):\n" +
		correctionSigned + "; глава 1; имя: Мирон] Мирону двадцать лет, а не двадцать четыре: так я решил. (11.11.2026)\n" +
		planSigned + "] В пятом томе Мирон уйдёт на север. (11.11.2026)\n"
	if block != want {
		t.Fatalf("block = %q\nwant    %q", block, want)
	}
	if last := memory.filters[len(memory.filters)-1]; !slices.Equal(last.Kinds, authormemory.AboutKinds) || last.Query != "" ||
		memory.authorities[len(memory.authorities)-1] != repository.authority {
		t.Fatalf("the records are read by %+v under %+v", last, memory.authorities)
	}
	// CR1.4: a record deleted or undone does not come from the next call.
	memory.records = memory.records[1:]
	if output, err := tools.Entity(ctx, rootCall, EntityInput{Name: "Мирон"}); err != nil || strings.Contains(output, "двадцать лет") || !strings.Contains(output, "уйдёт на север") {
		t.Fatalf("after the correction went: %q, %v", output, err)
	}
}

// CR1.2: a record about another name of the hero by the name registry, one about a role of his and one that names him in the
// text in an oblique case come; one about another hero does not.
func TestTheBlockFindsTheHeroUnderEveryNameAndInAnyCase(t *testing.T) {
	text := sagamemorytest.NewText(testSaga(), testStems, v3Cards())
	text.SetNames(&sagamemory.NameRegistry{Persons: []sagamemory.NamePerson{
		{Name: "мирон", Names: []string{"сватов"}, Roles: []string{"первый король"}},
	}})
	tools, _ := newLayerTools(researchdomain.ScopeProject, "")
	tools.saga = sagamemoryservice.NewSaga(text, text)
	tools.WithAuthorMemory(&authorWordsStub{records: []authormemory.Record{
		record("1", authormemory.KindCorrection, "Сватов", "Ему двадцать лет."),
		record("2", authormemory.KindPlan, "первый король", "Корону он отдаст."),
		record("3", authormemory.KindPlan, "", "Ворота откроют Мирону ночью."),
		record("4", authormemory.KindCorrection, "Вьюжница", "Она не ждала у ворот."),
	}})
	output, err := tools.Entity(context.Background(), rootCall, EntityInput{Name: "Мирон"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Ему двадцать лет.", "Корону он отдаст.", "Ворота откроют Мирону ночью."} {
		if !strings.Contains(output, want) {
			t.Errorf("no %q in %q", want, output)
		}
	}
	if strings.Contains(output, "не ждала у ворот") {
		t.Errorf("a record about another hero came: %q", output)
	}
}

// CR1.1, CR1.3: search by a query with a name brings the records about it; of seven five come, newest first, and a line
// says how many more there are and where to read them.
func TestSearchBringsFiveRecordsAndSaysHowManyMore(t *testing.T) {
	tools, _ := newTestTools(researchdomain.ScopeProject, "")
	memory := &authorWordsStub{}
	for index := range 7 {
		memory.records = append(memory.records, record(fmt.Sprint(index), authormemory.KindCorrection, "Мирон", fmt.Sprintf("Поправка %d.", index+1)))
	}
	tools.WithAuthorMemory(memory)
	output, err := tools.Search(context.Background(), rootCall, SearchInput{Queries: []string{"мирон вошёл"}})
	if err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(output, authorWordsHead)
	if !ok {
		t.Fatalf("no block: %q", output)
	}
	for index := range 5 {
		if !strings.Contains(block, fmt.Sprintf("Поправка %d.", index+1)) {
			t.Errorf("record %d of the newest five is not shown: %q", index+1, block)
		}
	}
	if strings.Contains(block, "Поправка 6.") || !strings.HasSuffix(block, "\n… и ещё 2: прочитай memory\n") {
		t.Fatalf("block = %q", block)
	}
}

// CR1.5: the block is the author's words, not a chapter: a call with it retains the chapters the same call retains without it.
func TestTheBlockRetainsNoChapter(t *testing.T) {
	calls := map[string]func(*ReadTools) (string, error){
		"entity": func(tools *ReadTools) (string, error) {
			return tools.Entity(context.Background(), rootCall, EntityInput{Name: "Мирон"})
		},
		"search": func(tools *ReadTools) (string, error) {
			return tools.Search(context.Background(), rootCall, SearchInput{Queries: []string{"мирон"}})
		},
	}
	for name, call := range calls {
		plain, plainRepository := newLayerTools(researchdomain.ScopeProject, "")
		if _, err := call(plain); err != nil {
			t.Fatal(err)
		}
		with, repository := newLayerTools(researchdomain.ScopeProject, "")
		chaptered := record("1", authormemory.KindCorrection, "Мирон", "Ему двадцать лет.")
		chaptered.ChapterID = "c2"
		with.WithAuthorMemory(&authorWordsStub{records: []authormemory.Record{chaptered}})
		output, err := call(with)
		if err != nil || !strings.Contains(output, authorWordsHead) {
			t.Fatalf("%s: %q, %v", name, output, err)
		}
		if !reflect.DeepEqual(repository.retained, plainRepository.retained) {
			t.Fatalf("%s retained %v with the block, %v without", name, repository.retained, plainRepository.retained)
		}
	}
}

// The table of specs/2026-11-11-chat-corrections.md: the tools asked about a name or words bring the block; the list of types,
// a range of chapters, the reading of a chapter and the scan do not.
func TestTheToolsAskedAboutANameBringTheBlock(t *testing.T) {
	ctx := context.Background()
	for _, item := range []struct {
		name string
		call func(*ReadTools) (string, error)
		want bool
	}{
		{"glossary by a name", func(tools *ReadTools) (string, error) {
			return tools.Glossary(ctx, rootCall, GlossaryInput{Query: "Сватов"})
		}, true},
		{"glossary of the types", func(tools *ReadTools) (string, error) { return tools.Glossary(ctx, rootCall, GlossaryInput{}) }, false},
		{"changes", func(tools *ReadTools) (string, error) {
			return tools.Changes(ctx, rootCall, ChangesInput{Subject: "Мирон"})
		}, true},
		{"timeline by words", func(tools *ReadTools) (string, error) {
			return tools.Timeline(ctx, rootCall, TimelineInput{Query: "Мирон"})
		}, true},
		{"timeline of a range", func(tools *ReadTools) (string, error) { return tools.Timeline(ctx, rootCall, TimelineInput{}) }, false},
		{"threads of a hero", func(tools *ReadTools) (string, error) {
			return tools.Threads(ctx, rootCall, ThreadsInput{Who: "Мирон"})
		}, true},
		{"read", func(tools *ReadTools) (string, error) { return tools.Read(ctx, rootCall, ReadInput{Chapter: "1"}) }, false},
		{"chapters", func(tools *ReadTools) (string, error) {
			return tools.Chapters(ctx, rootCall, ChaptersInput{Title: "Мирон"})
		}, false},
		{"scan", func(tools *ReadTools) (string, error) {
			return tools.Scan(ctx, rootCall, ScanInput{Task: "все места, где Мирон у ворот"})
		}, false},
	} {
		tools, _ := newLayerTools(researchdomain.ScopeProject, "")
		tools.WithAuthorMemory(&authorWordsStub{records: []authormemory.Record{record("1", authormemory.KindCorrection, "Мирон", "Ему двадцать лет.")}})
		output, err := item.call(tools)
		if err != nil {
			t.Fatalf("%s: %v", item.name, err)
		}
		if got := strings.Contains(output, authorWordsHead); got != item.want {
			t.Errorf("%s: block %v, want %v: %q", item.name, got, item.want, output)
		}
	}
}
