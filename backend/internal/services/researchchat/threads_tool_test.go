package researchchat

import (
	"context"
	"reflect"
	"strings"
	"testing"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

// threadLayerStub is the stored build, what the tool asked for and how often.
type threadLayerStub struct {
	layer     *sagalayer.Layer
	withBooks []bool
}

func (stub *threadLayerStub) SagaThreads(_ context.Context, _ string, withBooks bool) (*sagalayer.Layer, error) {
	stub.withBooks = append(stub.withBooks, withBooks)
	return stub.layer, nil
}

// noThreadLayer is the layer of the tests that do not ask about it.
type noThreadLayer struct{}

func (noThreadLayer) SagaThreads(context.Context, string, bool) (*sagalayer.Layer, error) {
	return nil, nil
}

// builtLayer was built up to chapter 2 of testSaga: chapter 3 is newer.
func builtLayer() *sagalayer.Layer {
	return &sagalayer.Layer{
		Chapters: []sagalayer.Stamp{
			{Chapter: "c1", Hash: "hash-c1", CardVersion: 4}, {Chapter: "c2", Hash: "hash-c2", CardVersion: 4},
		},
		Threads: []sagalayer.Thread{
			{Text: "Мелкая нить про дорогу на север", Kind: "ружьё", Weight: sagalayer.WeightMinor, First: []sagalayer.Ref{{Chapter: "c2", P: 1}}, Last: []sagalayer.Ref{{Chapter: "c2", P: 1}}},
			{Text: "Сватов обещал вернуть долг Вьюжницы", Kind: "обещание", Weight: sagalayer.WeightMajor, First: []sagalayer.Ref{{Chapter: "c1", P: 2}}, Last: []sagalayer.Ref{{Chapter: "c2"}}},
		},
		Books: []sagalayer.Book{
			{ID: "b1", Order: 1, Arcs: []sagalayer.Arc{{Title: "Приход в город", From: sagalayer.Ref{Chapter: "c1"}, To: sagalayer.Ref{Chapter: "c2"}, Gist: "Мирон входит в город.", Outcome: "Остался."}},
				Threads: []sagalayer.Thread{
					{Text: "Странный ключ у ворот", Kind: "ружьё", First: []sagalayer.Ref{{Chapter: "c1", P: 1}}, Last: []sagalayer.Ref{{Chapter: "c1", P: 2}}},
					{Text: "Сватов обещал вернуть долг Вьюжницы", First: []sagalayer.Ref{{Chapter: "c1", P: 2}}},
					{Text: "Закрытая история про ярмарку", Closed: true, First: []sagalayer.Ref{{Chapter: "c1"}}},
				}},
			{ID: "b2", Order: 2, Arcs: []sagalayer.Arc{{Title: "Возвращение Вьюжницы", From: sagalayer.Ref{Chapter: "c3"}, To: sagalayer.Ref{Chapter: "c3"}}},
				Threads: []sagalayer.Thread{{Text: "Кто прислал Вьюжницу", Kind: "тайна", First: []sagalayer.Ref{{Chapter: "c3", P: 1}}, Last: []sagalayer.Ref{{Chapter: "c3", P: 1}}}}},
		},
	}
}

func newThreadTools(scope researchdomain.Scope, bookID string, layer *sagalayer.Layer) (*ReadTools, *readRepositoryStub, *threadLayerStub) {
	repository := &readRepositoryStub{authority: researchdomain.ReadAuthority{
		ProjectID: "project-1", SourceView: "draft", Scope: scope, BookID: bookID,
	}}
	stub := &threadLayerStub{layer: layer}
	return NewReadTools(repository, sagamemorytest.NewSaga(testSaga(), testStems, v3Cards()), &scanModelStub{}, nil, ScanSettings{}, stub), repository, stub
}

func retainedIDs(repository *readRepositoryStub) []string {
	var ids []string
	for _, call := range repository.retained {
		for _, chapter := range call {
			ids = append(ids, chapter.ChapterID)
		}
	}
	return ids
}

// R2.7: the registry, the important first, in chapter keys and paragraphs; up to which chapter it was
// built and how many are newer; what it shows goes to the manifests; a thread is navigation.
func TestThreadsShowsTheRegistry(t *testing.T) {
	tools, repository, stub := newThreadTools(researchdomain.ScopeProject, "", builtLayer())
	output, err := tools.Threads(context.Background(), rootCall, ThreadsInput{})
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(output, "\n", 2)[0]
	if first != "Нитей: 2 (важных 1); слой собран по гл. 2, глав новее: 1." {
		t.Fatalf("first line = %q in\n%s", first, output)
	}
	important := strings.Index(output, "Сватов обещал вернуть долг Вьюжницы")
	minor := strings.Index(output, "Мелкая нить про дорогу")
	if important < 0 || minor < important {
		t.Fatalf("the important thread goes first:\n%s", output)
	}
	for _, want := range []string{
		"[важная, обещание] Сватов обещал вернуть долг Вьюжницы — возникла гл. 1 (¶2), последний раз гл. 2",
		"[ружьё] Мелкая нить про дорогу на север — возникла гл. 2 (¶1), последний раз гл. 2 (¶1)",
		"read",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("no %q in\n%s", want, output)
		}
	}
	if strings.Contains(output, "Странный ключ") || !reflect.DeepEqual(stub.withBooks, []bool{false}) {
		t.Fatalf("the registry reads no books: %v\n%s", stub.withBooks, output)
	}
	if ids := retainedIDs(repository); !reflect.DeepEqual(ids, []string{"c1", "c2"}) {
		t.Fatalf("retained = %v", ids)
	}
}

// R2.7: level=all adds the book threads the registry does not hold, marked, without the closed ones.
func TestThreadsLevelAllMarksTheThreadsOfBooks(t *testing.T) {
	tools, repository, _ := newThreadTools(researchdomain.ScopeProject, "", builtLayer())
	output, err := tools.Threads(context.Background(), rootCall, ThreadsInput{Level: "all"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Нитей: 4 (важных 1)",
		"[по книге, могло закрыться; т. 1, ружьё] Странный ключ у ворот — возникла гл. 1 (¶1), последний раз гл. 1 (¶2)",
		"[по книге, могло закрыться; т. 2, тайна] Кто прислал Вьюжницу — возникла гл. 3 (¶1)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("no %q in\n%s", want, output)
		}
	}
	if strings.Contains(output, "ярмарку") || strings.Count(output, "Сватов обещал") != 1 {
		t.Fatalf("closed or held threads are not listed:\n%s", output)
	}
	if ids := retainedIDs(repository); !reflect.DeepEqual(ids, []string{"c1", "c2", "c3"}) {
		t.Fatalf("retained = %v", ids)
	}
}

// R2.7: who goes through the name registry: any name of the hero finds him in any inflection.
func TestThreadsFindsAHeroUnderAllHisNames(t *testing.T) {
	cards := v3Cards()
	repository := &readRepositoryStub{authority: researchdomain.ReadAuthority{ProjectID: "project-1", SourceView: "draft", Scope: researchdomain.ScopeProject}}
	layer := builtLayer()
	layer.Threads = append(layer.Threads, sagalayer.Thread{Text: "Мирону предстоит суд", Kind: "конфликт", Weight: sagalayer.WeightMinor})
	stub := &threadLayerStub{layer: layer}
	tools := NewReadTools(repository, sagamemorytest.NewSaga(testSaga(), testStems, cards), &scanModelStub{}, nil, ScanSettings{}, stub)
	for _, name := range []string{"Мирон", "Сватов"} {
		output, err := tools.Threads(context.Background(), rootCall, ThreadsInput{Who: name})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output, "Мирону предстоит суд") || !strings.Contains(output, "Сватов обещал") || strings.Contains(output, "дорогу на север") {
			t.Fatalf("who=%s:\n%s", name, output)
		}
		if !strings.Contains(output, "Мирон, также: Сватов") {
			t.Fatalf("who=%s names him:\n%s", name, output)
		}
	}
	output, err := tools.Threads(context.Background(), rootCall, ThreadsInput{Who: "Незнакомец"})
	if err != nil || !strings.Contains(output, "«Незнакомец» нет в словаре") || !strings.HasPrefix(output, "Нитей: 0") {
		t.Fatalf("an unknown name: %q, %v", output, err)
	}
	layer.Threads = append(layer.Threads, sagalayer.Thread{Text: "Янтарь пропал", Weight: sagalayer.WeightMinor}, sagalayer.Thread{Text: "Ян ушёл", Weight: sagalayer.WeightMinor})
	output, err = tools.Threads(context.Background(), rootCall, ThreadsInput{Who: "Ян"})
	if err != nil || !strings.Contains(output, "Ян ушёл") || strings.Contains(output, "Янтарь") {
		t.Fatalf("a short name is a whole word: %q, %v", output, err)
	}
}

func TestThreadsFilters(t *testing.T) {
	tools, _, _ := newThreadTools(researchdomain.ScopeProject, "", builtLayer())
	byQuery, err := tools.Threads(context.Background(), rootCall, ThreadsInput{Query: "дороге север"})
	if err != nil || !strings.Contains(byQuery, "дорогу на север") || strings.Contains(byQuery, "долг") {
		t.Fatalf("query: %q, %v", byQuery, err)
	}
	byKind, err := tools.Threads(context.Background(), rootCall, ThreadsInput{Kind: "Обещание", Level: "all"})
	if err != nil || !strings.Contains(byKind, "Сватов обещал") || strings.Contains(byKind, "дорогу") || strings.Contains(byKind, "Странный ключ") {
		t.Fatalf("kind: %q, %v", byKind, err)
	}
	for _, input := range []ThreadsInput{{Level: "часть"}, {Part: "другое"}, {Query: strings.Repeat("я", maxQueryRunes+1)}, {Who: strings.Repeat("я", maxQueryRunes+1)}, {Kind: strings.Repeat("я", maxQueryRunes+1)}} {
		if _, err := tools.Threads(context.Background(), rootCall, input); err == nil {
			t.Fatalf("%+v is refused", input)
		} else if _, ok := IsArgumentError(err); !ok {
			t.Fatalf("%+v: %v is not a correctable error", input, err)
		}
	}
}

// R2.7: part=arcs lists the arcs by book.
func TestThreadsShowsArcsByBook(t *testing.T) {
	tools, repository, stub := newThreadTools(researchdomain.ScopeProject, "", builtLayer())
	output, err := tools.Threads(context.Background(), rootCall, ThreadsInput{Part: "arcs"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Арок: 2; слой собран по гл. 2, глав новее: 1.",
		"Том 1:",
		"- Приход в город (гл. 1 – гл. 2): Мирон входит в город. Итог: Остался.",
		"Том 2:",
		"- Возвращение Вьюжницы (гл. 3 – гл. 3)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("no %q in\n%s", want, output)
		}
	}
	if !reflect.DeepEqual(stub.withBooks, []bool{true}) || !reflect.DeepEqual(retainedIDs(repository), []string{"c1", "c2", "c3"}) {
		t.Fatalf("books read %v, retained %v", stub.withBooks, retainedIDs(repository))
	}
}

// The registry is about the whole saga: a book-scoped chat sees only what its book's call extracted.
func TestThreadsInABookChatShowsOnlyItsBook(t *testing.T) {
	tools, repository, stub := newThreadTools(researchdomain.ScopeBook, "b2", builtLayer())
	output, err := tools.Threads(context.Background(), rootCall, ThreadsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Кто прислал Вьюжницу") || strings.Contains(output, "Сватов обещал") || strings.Contains(output, "Странный ключ") || !strings.Contains(output, "только этой книги") {
		t.Fatalf("book chat:\n%s", output)
	}
	if !reflect.DeepEqual(retainedIDs(repository), []string{"c3"}) || !reflect.DeepEqual(stub.withBooks, []bool{true}) {
		t.Fatalf("retained %v, books %v", retainedIDs(repository), stub.withBooks)
	}
	arcs, err := tools.Threads(context.Background(), rootCall, ThreadsInput{Part: "arcs"})
	if err != nil || !strings.Contains(arcs, "Возвращение Вьюжницы") || strings.Contains(arcs, "Приход в город") {
		t.Fatalf("book chat arcs: %q, %v", arcs, err)
	}
}

func TestThreadsWithoutALayerPointsToTheText(t *testing.T) {
	tools, repository, _ := newThreadTools(researchdomain.ScopeProject, "", nil)
	output, err := tools.Threads(context.Background(), rootCall, ThreadsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "не собран") || !strings.Contains(output, "chapters") || !strings.Contains(output, "search") {
		t.Fatalf("no layer: %q", output)
	}
	if ids := retainedIDs(repository); len(ids) != 0 {
		t.Fatalf("retained = %v", ids)
	}
}

// A chapter edited since the build counts as newer; a chapter gone from this text version leaves its
// record readable without the place, and the output stays bounded.
func TestThreadsOutputKeepsItsBounds(t *testing.T) {
	version := testSaga()
	version.Chapters[1].ContentHash = "edited"
	repository := &readRepositoryStub{authority: researchdomain.ReadAuthority{ProjectID: "project-1", SourceView: "draft", Scope: researchdomain.ScopeProject}}
	layer := builtLayer()
	layer.Threads = []sagalayer.Thread{{Text: "Нить вне версии", First: []sagalayer.Ref{{Chapter: "gone", P: 1}}, Weight: sagalayer.WeightMajor}}
	for range 3000 {
		layer.Threads = append(layer.Threads, sagalayer.Thread{Text: strings.Repeat("длинная запись о нити ", 10), Weight: sagalayer.WeightMinor})
	}
	stub := &threadLayerStub{layer: layer}
	tools := NewReadTools(repository, sagamemorytest.NewSaga(version, testStems, v3Cards()), &scanModelStub{}, nil, ScanSettings{}, stub)
	output, err := tools.Threads(context.Background(), rootCall, ThreadsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.SplitN(output, "\n", 2)[0], "глав новее: 2") {
		t.Fatalf("first line: %s", output[:200])
	}
	if !strings.Contains(output, "[важная] Нить вне версии\n") {
		t.Fatalf("a record without a place in this version:\n%s", output[:300])
	}
	if len([]rune(output)) > maxThreadsOutputRunes+500 || !strings.Contains(output, "… и ещё ") || !strings.Contains(output, "сузьте") {
		t.Fatalf("output of %d runes ends %q", len([]rune(output)), output[len(output)-100:])
	}
}
