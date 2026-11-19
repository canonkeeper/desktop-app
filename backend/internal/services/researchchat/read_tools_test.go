package researchchat

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/modelthread"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

type readRepositoryStub struct {
	authority    researchdomain.ReadAuthority
	authorityErr error
	retainErr    error
	retained     [][]ChapterRevision
}

func (stub *readRepositoryStub) ReadAuthority(context.Context, ToolContext) (researchdomain.ReadAuthority, error) {
	return stub.authority, stub.authorityErr
}

func (stub *readRepositoryStub) RetainChapters(_ context.Context, _ ToolContext, _ researchdomain.ReadAuthority, chapters []ChapterRevision) error {
	stub.retained = append(stub.retained, chapters)
	return stub.retainErr
}

// testCards are the cards of chapters 1 and 3; chapter 2 has none yet.
func testCards() map[int]*sagamemory.Card {
	return map[int]*sagamemory.Card{
		0: {
			Summary: "Мирон приходит в город.", Time: []string{"утро (¶1)"},
			Characters: []sagamemory.Mention{{Name: "Мирон", Note: "входит в город", P: 1}},
			Entities:   []sagamemory.Mention{{Name: "Вьюжница", Type: "раса", Note: "ждёт у ворот", P: 2}},
			Changes:    []sagamemory.Change{{Subject: "Мирон", Change: "получил пропуск", P: 1}},
		},
		2: {
			Summary:    "Вьюжница возвращается.",
			Characters: []sagamemory.Mention{{Name: "Сватов", Aliases: []string{"Мирон"}, P: 2}},
			Entities:   []sagamemory.Mention{{Name: "вьюжница", Type: "раса", P: 1}},
			Changes:    []sagamemory.Change{{Subject: "Мирон", Change: "потерял пропуск", P: 2}},
		},
	}
}

type scanModelStub struct {
	mu     sync.Mutex
	delay  time.Duration
	inputs []string
	jobs   []string
	// threads are the threads of the calls, by the order of inputs (specs/2026-11-17-model-cache.md MC1.3).
	threads [][]string
	// atOnce is how many calls ran at once at most.
	running, atOnce int
}

func (stub *scanModelStub) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	if instructions != sagamemory.ScanInstructions {
		return "", quota.ModelUsage{}, errors.New("unexpected instructions")
	}
	stub.mu.Lock()
	stub.running++
	stub.atOnce = max(stub.atOnce, stub.running)
	stub.mu.Unlock()
	select {
	case <-time.After(stub.delay):
	case <-ctx.Done():
		stub.mu.Lock()
		stub.running--
		stub.mu.Unlock()
		return "", quota.ModelUsage{}, ctx.Err()
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.running--
	thread, _ := modelthread.Of(ctx)
	stub.inputs = append(stub.inputs, input)
	stub.jobs = append(stub.jobs, jobs.JobIDOf(ctx))
	stub.threads = append(stub.threads, thread)
	if strings.Contains(input, "Вьюжница снова появилась") {
		return `{"found":[{"item":"Вьюжница","chapter":"3","p":1,"quote":"Вьюжница снова появилась.","note":"раса"}]}`, scanCallUsage, nil
	}
	return `{"found":[{"item":"вьюжница","chapter":"гл. 1","p":2,"quote":"Вьюжница ждала у ворот.","note":""}]}`, scanCallUsage, nil
}

// scanCallUsage is what one stubbed scan call costs.
var scanCallUsage = quota.ModelUsage{Calls: 1, InputTokens: 100, OutputTokens: 10}

type usageStub struct {
	mu      sync.Mutex
	records []quota.UsageRecord
}

func (stub *usageStub) RecordModelUsage(_ context.Context, record quota.UsageRecord) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.records = append(stub.records, record)
}

func testSaga() *sagamemory.Version {
	paragraphs := func(texts ...string) []sagamemory.Paragraph {
		out := make([]sagamemory.Paragraph, len(texts))
		for i, text := range texts {
			out[i] = sagamemory.Paragraph{Text: text}
		}
		return out
	}
	version := sagamemory.NewVersion("project-1", "draft", "fp", []sagamemory.Chapter{
		{ID: "c1", Revision: 3, BookID: "b1", BookOrder: 1, Order: 1, Title: "Глава 1. Начало",
			Paragraphs: paragraphs("Мирон вошёл в город.", "Вьюжница ждала у ворот.")},
		{ID: "c2", Revision: 5, BookID: "b1", BookOrder: 1, Order: 2, Title: "Глава 2. Дорога",
			Paragraphs: paragraphs("Дорога вела на север.")},
		{ID: "c3", Revision: 7, BookID: "b2", BookOrder: 2, Order: 1, Title: "Глава 3. Вьюжница",
			Paragraphs: paragraphs("Вьюжница снова появилась.", "Мирон узнал её.")},
	})
	return version
}

var testStems = map[string]string{"вьюжница": "вьюжниц", "мирон": "мирон"}

func newTestTools(scope researchdomain.Scope, bookID string) (*ReadTools, *readRepositoryStub) {
	tools, repository, _ := newTestToolsWithScan(scope, bookID, ScanSettings{})
	return tools, repository
}

func newTestToolsWithScan(scope researchdomain.Scope, bookID string, scan ScanSettings) (*ReadTools, *readRepositoryStub, *scanModelStub) {
	repository := &readRepositoryStub{authority: researchdomain.ReadAuthority{
		ProjectID: "project-1", SourceView: "draft", Scope: scope, BookID: bookID,
	}}
	model := &scanModelStub{}
	return NewReadTools(repository, sagamemorytest.NewSaga(testSaga(), testStems, testCards()), model, nil, scan, noThreadLayer{}), repository, model
}

var rootCall = ToolContext{Caller: agent.ToolCaller{ThreadID: "root", RootThreadID: "root"}, TurnID: "turn"}

func TestSearchRetainsEveryChapterInTheAnswer(t *testing.T) {
	tools, repository := newTestTools(researchdomain.ScopeProject, "")
	output, err := tools.Search(context.Background(), rootCall, SearchInput{Queries: []string{"вьюжница"}, Order: "story"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "2 абзацев в 2 главах") || !strings.Contains(output, "Главы с совпадениями: 1(1), 3(1)") ||
		!strings.Contains(output, "- [гл. 1 ¶2](source:1/2) Том 1, Глава 1. Начало: Вьюжница ждала у ворот.") {
		t.Fatalf("search output = %q", output)
	}
	want := [][]ChapterRevision{{{ChapterID: "c1", Revision: 3}, {ChapterID: "c3", Revision: 7}}}
	if !reflect.DeepEqual(repository.retained, want) {
		t.Fatalf("retained = %#v, want %#v", repository.retained, want)
	}
}

// A relevance-ordered search still names the earliest and the latest place in book order, so
// "впервые" and "к какому моменту" questions do not stop at the most relevant hit.
func TestRelevanceSearchNamesEarliestAndLatestPlaces(t *testing.T) {
	tools, _ := newTestTools(researchdomain.ScopeProject, "")
	output, err := tools.Search(context.Background(), rootCall, SearchInput{Queries: []string{"вьюжница"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Раньше всего по ходу книги: [гл. 1 ¶2](source:1/2)") ||
		!strings.Contains(output, "Позже всего: [гл. 3 ¶1](source:3/1)") {
		t.Fatalf("search output = %q", output)
	}
	story, err := tools.Search(context.Background(), rootCall, SearchInput{Queries: []string{"вьюжница"}, Order: "story"})
	if err != nil || strings.Contains(story, "Раньше всего") {
		t.Fatalf("story-ordered search repeats the earliest place: %q, %v", story, err)
	}
}

func TestBookScopedChatSeesOnlyItsBook(t *testing.T) {
	tools, repository := newTestTools(researchdomain.ScopeBook, "b2")
	output, err := tools.Search(context.Background(), rootCall, SearchInput{Queries: []string{"мирон"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "1 абзацев в 1 главах") || strings.Contains(output, "гл. 1 ") {
		t.Fatalf("book-scoped search leaked another book: %q", output)
	}
	_, err = tools.Read(context.Background(), rootCall, ReadInput{Chapter: "1"})
	if argument, ok := IsArgumentError(err); !ok || argument.Path != "$.chapter" {
		t.Fatalf("read outside the book = %v, want a correctable chapter error", err)
	}
	listed, err := tools.Chapters(context.Background(), rootCall, ChaptersInput{})
	if err != nil || !strings.HasPrefix(listed, "Глав: 1\nгл. 3 | Том 2, Глава 3. Вьюжница | 2 ¶") {
		t.Fatalf("book-scoped chapters = %q, %v", listed, err)
	}
	if len(repository.retained) != 2 {
		t.Fatalf("retained calls = %d, want only successful results", len(repository.retained))
	}
}

func TestReadReturnsNumberedRangeAroundAParagraph(t *testing.T) {
	tools, repository := newTestTools(researchdomain.ScopeProject, "")
	radius := 0
	output, err := tools.Read(context.Background(), rootCall, ReadInput{Chapter: "гл. 3", Around: 2, Radius: &radius})
	if err != nil {
		t.Fatal(err)
	}
	if output != "Том 2, Глава 3. Вьюжница — гл. 3, абзацы ¶2–¶2 из 2; ссылка на абзац: [гл. 3 ¶2](source:3/2)\n\n¶2 Мирон узнал её.\n" {
		t.Fatalf("read output = %q", output)
	}
	if want := [][]ChapterRevision{{{ChapterID: "c3", Revision: 7}}}; !reflect.DeepEqual(repository.retained, want) {
		t.Fatalf("retained = %#v", repository.retained)
	}
	if _, err := tools.Read(context.Background(), rootCall, ReadInput{Chapter: "3", From: 5}); err == nil {
		t.Fatal("read past the last paragraph succeeded")
	}
}

func TestChaptersShowCardSummariesTimeAndChanges(t *testing.T) {
	tools, repository := newTestTools(researchdomain.ScopeProject, "")
	output, err := tools.Chapters(context.Background(), rootCall, ChaptersInput{Detail: ChaptersSummary, To: "2"})
	if err != nil {
		t.Fatal(err)
	}
	if output != "Глав: 2\nКарточки ещё не построены для 1 из них: у этих глав только название.\n"+
		"гл. 1 | Том 1, Глава 1. Начало | 2 ¶\n    Мирон приходит в город.\nгл. 2 | Том 1, Глава 2. Дорога | 1 ¶\n" {
		t.Fatalf("chapters summary output = %q", output)
	}
	output, err = tools.Chapters(context.Background(), rootCall, ChaptersInput{Detail: ChaptersCard, From: "3"})
	if err != nil {
		t.Fatal(err)
	}
	if output != "Глав: 1\nгл. 3 | Том 2, Глава 3. Вьюжница | 2 ¶\n    Вьюжница возвращается.\n    Изменение (¶2): Мирон — потерял пропуск\n" {
		t.Fatalf("chapters card output = %q", output)
	}
	if _, err := tools.Chapters(context.Background(), rootCall, ChaptersInput{Detail: "full"}); err == nil {
		t.Fatal("unknown detail accepted")
	}
	if want := [][]ChapterRevision{{{ChapterID: "c1", Revision: 3}, {ChapterID: "c2", Revision: 5}}, {{ChapterID: "c3", Revision: 7}}}; !reflect.DeepEqual(repository.retained, want) {
		t.Fatalf("retained = %#v, want %#v", repository.retained, want)
	}
}

// R3.1, R3.3: the glossary merges names and aliases across cards and retains the chapters
// of every entry it shows; the type overview shows no chapter data.
func TestGlossaryMergesAliasesAndRetainsEntryChapters(t *testing.T) {
	tools, repository := newTestTools(researchdomain.ScopeProject, "")
	output, err := tools.Glossary(context.Background(), rootCall, GlossaryInput{})
	if err != nil || !strings.Contains(output, "Словарь неполон: карточки ещё не построены для 1 глав.") ||
		!strings.Contains(output, "Записей в словаре: 2. Типы:\n- персонаж: 1\n- раса: 1\n") {
		t.Fatalf("glossary types = %q, %v", output, err)
	}
	output, err = tools.Glossary(context.Background(), rootCall, GlossaryInput{Query: "сватов"})
	if err != nil || !strings.Contains(output, "- Мирон (персонаж; также: Сватов) — входит в город. Главы (2): 1, 3\n") {
		t.Fatalf("glossary entry = %q, %v", output, err)
	}
	if want := [][]ChapterRevision{{}, {{ChapterID: "c1", Revision: 3}, {ChapterID: "c3", Revision: 7}}}; !reflect.DeepEqual(repository.retained, want) {
		t.Fatalf("retained = %#v, want %#v", repository.retained, want)
	}
}

func TestChangesListSubjectChangesInStoryOrder(t *testing.T) {
	tools, repository := newTestTools(researchdomain.ScopeProject, "")
	output, err := tools.Changes(context.Background(), rootCall, ChangesInput{Subject: "мирон", To: "2"})
	if err != nil || output != "Изменений: 1 (по порядку книги; искал и под именами: Сватов)\n- гл. 1 (¶1): Мирон — получил пропуск\n" {
		t.Fatalf("changes = %q, %v", output, err)
	}
	// Chapter 3 named Мирон also Сватов, so its card entered the answer too.
	if want := [][]ChapterRevision{{{ChapterID: "c1", Revision: 3}, {ChapterID: "c3", Revision: 7}}}; !reflect.DeepEqual(repository.retained, want) {
		t.Fatalf("retained = %#v, want %#v", repository.retained, want)
	}
	if _, err := tools.Changes(context.Background(), rootCall, ChangesInput{}); err == nil {
		t.Fatal("changes without a subject succeeded")
	}
}

// specs/2026-10-29-chapter-cards-quality.md R6.1: a subject that is a name of a glossary entry
// finds the changes recorded under any of its names; another word keeps the substring search.
func TestChangesFindTheSubjectUnderAllItsNames(t *testing.T) {
	tools, _ := newTestTools(researchdomain.ScopeProject, "")
	output, err := tools.Changes(context.Background(), rootCall, ChangesInput{Subject: "Сватов"})
	want := "Изменений: 2 (по порядку книги; искал и под именами: Мирон)\n- гл. 1 (¶1): Мирон — получил пропуск\n- гл. 3 (¶2): Мирон — потерял пропуск\n"
	if err != nil || output != want {
		t.Fatalf("changes of an alias = %q, %v; want %q", output, err, want)
	}
	if output, err := tools.Changes(context.Background(), rootCall, ChangesInput{Subject: "пропуск"}); err != nil || !strings.HasPrefix(output, "Изменений: 2 (по порядку книги)\n") {
		t.Fatalf("changes of a word = %q, %v", output, err)
	}
}

// R3.1: the scan reads the range in bounded chunks, groups places by item in story order
// and retains only chapters with findings.
func TestScanGroupsFindingsByItemAcrossChunks(t *testing.T) {
	tools, repository, model := newTestToolsWithScan(researchdomain.ScopeProject, "", ScanSettings{Parallel: 2, ChunkRunes: 30})
	output, err := tools.Scan(context.Background(), rootCall, ScanInput{Task: "все упоминания вьюжниц"})
	if err != nil {
		t.Fatal(err)
	}
	if len(model.inputs) != 3 {
		t.Fatalf("scan inputs = %q, want one chunk per chapter", model.inputs)
	}
	want := "Просмотрено глав: 3. Найдено: 1 (мест: 3). Главы: гл. 1–3. Порядок — по первому появлению.\n" +
		"- вьюжница — мест: 3; впервые гл. 1 (¶2): «Вьюжница ждала у ворот.»; также: гл. 2, гл. 3 (¶1)\n"
	if output != want {
		t.Fatalf("scan output = %q, want %q", output, want)
	}
	if retained := repository.retained[0]; len(retained) != 3 {
		t.Fatalf("retained = %#v, want the chapters with findings", retained)
	}
	if _, err := tools.Scan(context.Background(), rootCall, ScanInput{Task: "ы"}); err == nil {
		t.Fatal("scan with a too short task succeeded")
	}
}

// specs/2026-11-17-model-cache.md MC1.3: each chunk of a scan goes under the thread of the saga and the chapters of the
// chunk in their order, so one chunk in two scans of other tasks is one thread, and the chunks of a scan go apart.
func TestTheChunksOfAScanGoUnderTheThreadsOfTheirChapters(t *testing.T) {
	tools, _, model := newTestToolsWithScan(researchdomain.ScopeProject, "", ScanSettings{Parallel: 2, ChunkRunes: 30})
	for _, task := range []string{"все упоминания вьюжниц", "кто стоит у ворот"} {
		if _, err := tools.Scan(context.Background(), rootCall, ScanInput{Task: task}); err != nil {
			t.Fatal(err)
		}
	}
	byInput := map[string][]string{}
	for index, input := range model.inputs {
		chapter := strings.SplitN(input, " (гл. ", 2)
		if len(chapter) < 2 {
			t.Fatalf("an input names no chapter: %q", input)
		}
		key := strings.SplitN(chapter[1], ")", 2)[0]
		want := []string{"scan", "project-1", "c" + key}
		if !reflect.DeepEqual(model.threads[index], want) {
			t.Fatalf("the chunk of chapter %s went under %q, want %q", key, model.threads[index], want)
		}
		byInput[key] = model.threads[index]
	}
	if len(model.inputs) != 6 || len(byInput) != 3 {
		t.Fatalf("%d calls over %d chunks", len(model.inputs), len(byInput))
	}
	chunk := &scanChunk{chapters: []*sagamemory.Chapter{{ID: "c1"}, {ID: "c2"}}}
	if parts, _ := modelthread.Of(scanThread(context.Background(), "project-1", chunk.chapters)); !reflect.DeepEqual(parts, []string{"scan", "project-1", "c1", "c2"}) {
		t.Fatalf("a chunk of two chapters goes under %q", parts)
	}
}

// specs/2026-11-17-model-cache.md MC2.1: the input of a chunk is the text of its chapters, as it was, then an empty line and
// the task — two scans of one chunk with other tasks share their beginning up to the task, which the cache of the provider
// gives.
func TestTheInputOfAChunkIsItsTextThenTheTask(t *testing.T) {
	tools, _, model := newTestToolsWithScan(researchdomain.ScopeProject, "", ScanSettings{Parallel: 1, ChunkRunes: 1000})
	for _, task := range []string{"все упоминания вьюжниц", "кто стоит у ворот"} {
		if _, err := tools.Scan(context.Background(), rootCall, ScanInput{Task: task, From: "1", To: "2"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(model.inputs) != 2 {
		t.Fatalf("scan inputs = %q, want one chunk a scan", model.inputs)
	}
	first, second := model.inputs[0], model.inputs[1]
	text := strings.TrimSuffix(first, "ЗАДАЧА: все упоминания вьюжниц")
	if !strings.HasPrefix(first, "Том 1, Глава 1. Начало (гл. 1)\n\n¶1 Мирон вошёл в город.\n") || !strings.HasSuffix(text, "¶1 Дорога вела на север.\n\n") ||
		text == first || second != text+"ЗАДАЧА: кто стоит у ворот" {
		t.Fatalf("the inputs of one chunk:\n%q\n%q", first, second)
	}
}

// MC2.2: one scan reads no more than 32 chunks at once, whatever the places of the process; the rest goes on as it does.
func TestAScanReadsNoMoreThan32ChunksAtOnce(t *testing.T) {
	chapters := make([]sagamemory.Chapter, 40)
	for index := range chapters {
		chapters[index] = sagamemory.Chapter{ID: fmt.Sprintf("c%d", index+1), BookID: "b1", BookOrder: 1, Order: index + 1,
			Title: fmt.Sprintf("Глава %d. Застава", index+1), Paragraphs: []sagamemory.Paragraph{{Text: "Вьюжница ждала у ворот."}}}
	}
	repository := &readRepositoryStub{authority: researchdomain.ReadAuthority{ProjectID: "project-1", SourceView: "draft", Scope: researchdomain.ScopeProject}}
	model := &scanModelStub{delay: 50 * time.Millisecond}
	tools := NewReadTools(repository, sagamemorytest.NewSaga(sagamemory.NewVersion("project-1", "draft", "fp", chapters), testStems, nil), model, nil,
		ScanSettings{Parallel: 96, ChunkRunes: 30}, noThreadLayer{})
	if _, err := tools.Scan(context.Background(), rootCall, ScanInput{Task: "все упоминания вьюжниц"}); err != nil {
		t.Fatal(err)
	}
	if len(model.inputs) != 40 || model.atOnce > 32 || model.atOnce < 2 {
		t.Fatalf("%d chunks read, %d at once", len(model.inputs), model.atOnce)
	}
}

// The app-server drops a tool call after 60 seconds: a scan stops starting chunks it cannot
// finish in its budget and tells the agent where to continue.
func TestScanStopsAtItsBudgetAndNamesTheNextChapter(t *testing.T) {
	tools, _, model := newTestToolsWithScan(researchdomain.ScopeProject, "", ScanSettings{Parallel: 1, ChunkRunes: 30, Budget: 300 * time.Millisecond})
	model.delay = 120 * time.Millisecond
	output, err := tools.Scan(context.Background(), rootCall, ScanInput{Task: "все упоминания вьюжниц"})
	if err != nil {
		t.Fatal(err)
	}
	if len(model.inputs) != 2 || !strings.HasPrefix(output, "Просмотрено глав: 2. Найдено: 1 (мест: 2). Главы: гл. 1–2.") ||
		!strings.Contains(output, "Не успел за один вызов: гл. 3. Продолжите scan с from=3 и тем же task.\n") {
		t.Fatalf("scan output = %q after %d model calls", output, len(model.inputs))
	}
}

func TestToolsReturnNothingWithoutAuthorityOrRetention(t *testing.T) {
	tools, repository := newTestTools(researchdomain.ScopeProject, "")
	repository.authorityErr = researchdomain.ErrForbidden
	if output, err := tools.Read(context.Background(), rootCall, ReadInput{Chapter: "1"}); !errors.Is(err, researchdomain.ErrForbidden) || output != "" {
		t.Fatalf("read without authority = %q, %v", output, err)
	}
	repository.authorityErr, repository.retainErr = nil, researchdomain.ErrStaleSource
	if output, err := tools.Read(context.Background(), rootCall, ReadInput{Chapter: "1"}); !errors.Is(err, researchdomain.ErrStaleSource) || output != "" {
		t.Fatalf("read whose chapter changed = %q, %v", output, err)
	}
	child := rootCall
	child.Caller.ThreadID, child.Caller.AgentRole = "child", "explorer"
	if _, err := tools.Read(context.Background(), child, ReadInput{Chapter: "1"}); !errors.Is(err, researchdomain.ErrForbidden) {
		t.Fatalf("subagent read = %v, want forbidden", err)
	}
}

func TestSearchArgumentErrorsAreCorrectable(t *testing.T) {
	tools, _ := newTestTools(researchdomain.ScopeProject, "")
	for name, input := range map[string]SearchInput{
		"no queries":    {},
		"bad mode":      {Queries: []string{"вьюжница"}, Match: "fuzzy"},
		"bad order":     {Queries: []string{"вьюжница"}, Order: "date"},
		"unknown from":  {Queries: []string{"вьюжница"}, From: "999"},
		"reverse range": {Queries: []string{"вьюжница"}, From: "3", To: "1"},
		"unknown book":  {Queries: []string{"вьюжница"}, Books: []int{9}},
	} {
		if _, err := tools.Search(context.Background(), rootCall, input); err == nil {
			t.Fatalf("%s: search succeeded", name)
		} else if _, ok := IsArgumentError(err); !ok {
			t.Fatalf("%s: error %v is not correctable", name, err)
		}
	}
}

// R7.2: every scan chunk sent to the model counts for the author of the turn's Job.
func TestScanRecordsUsageForTheTurn(t *testing.T) {
	tools, repository, _ := newTestToolsWithScan(researchdomain.ScopeProject, "", ScanSettings{Parallel: 2, ChunkRunes: 30, Model: "scan-model"})
	repository.authority.JobID = "job-1"
	usage := &usageStub{}
	tools.usage = usage
	if _, err := tools.Scan(context.Background(), rootCall, ScanInput{Task: "все упоминания вьюжниц"}); err != nil {
		t.Fatal(err)
	}
	if len(usage.records) != 3 {
		t.Fatalf("usage records = %+v, want one per chunk", usage.records)
	}
	for _, record := range usage.records {
		if record.Source != quota.UsageScan || record.JobID != "job-1" || record.ProjectID != repository.authority.ProjectID ||
			record.Model != "scan-model" || record.Usage != scanCallUsage {
			t.Fatalf("usage record = %+v", record)
		}
	}
	// specs/2026-11-13-desk-gateway.md GW3.2: a call of the scan names the Job of its turn to the client of the model —
	// the tool runs in the context of Codex, not of the turn.
	model := tools.scanModel.(*scanModelStub)
	if len(model.jobs) != 3 || model.jobs[0] != "job-1" || model.jobs[1] != "job-1" || model.jobs[2] != "job-1" {
		t.Fatalf("the calls of the scan named the Jobs %v", model.jobs)
	}
}

// countingSaga counts the chapters whose whole text a call reads.
type countingSaga struct {
	SagaText
	mu       sync.Mutex
	chapters []int
}

func (saga *countingSaga) Chapters(ctx context.Context, outline *sagamemory.Outline, chapters []int) error {
	saga.mu.Lock()
	saga.chapters = append(saga.chapters, chapters...)
	saga.mu.Unlock()
	return saga.SagaText.Chapters(ctx, outline, chapters)
}

// R3.2 of specs/2026-10-28-saga-search-in-postgres.md: a scan reads the text of a chunk only for
// its model call; a chunk the budget cuts is never read.
func TestScanReadsOnlyTheChunksItSends(t *testing.T) {
	repository := &readRepositoryStub{authority: researchdomain.ReadAuthority{ProjectID: "project-1", SourceView: "draft", Scope: researchdomain.ScopeProject}}
	saga := &countingSaga{SagaText: sagamemorytest.NewSaga(testSaga(), testStems, testCards())}
	model := &scanModelStub{delay: 120 * time.Millisecond}
	tools := NewReadTools(repository, saga, model, nil, ScanSettings{Parallel: 1, ChunkRunes: 30, Budget: 300 * time.Millisecond}, noThreadLayer{})
	if _, err := tools.Scan(context.Background(), rootCall, ScanInput{Task: "все упоминания вьюжниц"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saga.chapters, []int{0, 1}) || len(model.inputs) != 2 {
		t.Fatalf("scan read chapters %v for %d model calls, want only the two chunks it sent", saga.chapters, len(model.inputs))
	}
}

// A text version whose search rows are not built yet (as after a restore) is refused by every
// tool with ErrSearchNotReady and nothing is retained.
func TestToolsRefuseASagaWithoutSearchRows(t *testing.T) {
	repository := &readRepositoryStub{authority: researchdomain.ReadAuthority{ProjectID: "project-1", SourceView: "draft", Scope: researchdomain.ScopeProject}}
	tools := NewReadTools(repository, sagamemorytest.NewSaga(testSaga(), testStems, testCards(), 1), &scanModelStub{}, nil, ScanSettings{}, noThreadLayer{})
	for name, call := range map[string]func() (string, error){
		"search": func() (string, error) {
			return tools.Search(context.Background(), rootCall, SearchInput{Queries: []string{"вьюжница"}})
		},
		"read":     func() (string, error) { return tools.Read(context.Background(), rootCall, ReadInput{Chapter: "1"}) },
		"chapters": func() (string, error) { return tools.Chapters(context.Background(), rootCall, ChaptersInput{}) },
		"glossary": func() (string, error) { return tools.Glossary(context.Background(), rootCall, GlossaryInput{}) },
	} {
		if output, err := call(); !errors.Is(err, sagamemory.ErrSearchNotReady) || output != "" {
			t.Fatalf("%s = %q, %v; want ErrSearchNotReady", name, output, err)
		}
	}
	if len(repository.retained) != 0 {
		t.Fatalf("retained %v for refused calls", repository.retained)
	}
}

// A chapter deleted after the outline of a call is read has no paragraphs left to read: every
// tool that reads its text fails with ErrTextChanged, which the agent retries, instead of
// panicking on the missing text or answering with empty paragraphs; nothing is retained.
func TestToolsRetryAChapterDeletedDuringTheCall(t *testing.T) {
	repository := &readRepositoryStub{authority: researchdomain.ReadAuthority{ProjectID: "project-1", SourceView: "draft", Scope: researchdomain.ScopeProject}}
	text := sagamemorytest.NewText(testSaga(), testStems, testCards())
	text.LoseParagraphs(0)
	tools := NewReadTools(repository, sagamemoryservice.NewSaga(text, text), &scanModelStub{}, nil, ScanSettings{}, noThreadLayer{})
	for name, call := range map[string]func() (string, error){
		"search": func() (string, error) {
			return tools.Search(context.Background(), rootCall, SearchInput{Queries: []string{"вьюжница"}})
		},
		"substring": func() (string, error) {
			return tools.Search(context.Background(), rootCall, SearchInput{Queries: []string{"южниц"}, Match: string(sagamemory.ModeSubstring)})
		},
		"read": func() (string, error) { return tools.Read(context.Background(), rootCall, ReadInput{Chapter: "1"}) },
	} {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("%s panicked: %v", name, recovered)
				}
			}()
			if output, err := call(); !errors.Is(err, sagamemory.ErrTextChanged) || output != "" {
				t.Errorf("%s = %q, %v; want ErrTextChanged", name, output, err)
			}
		}()
	}
	if len(repository.retained) != 0 {
		t.Fatalf("retained %v for failed calls", repository.retained)
	}
}

// specs/2026-11-17-long-chapters.md LC3.1: a chapter longer than four chunks is not given to the model: its chunk stands
// skipped between its neighbours, which keep their inputs and their own ranges, and the answer names it with its length and
// what reads it.
func TestScanSkipsAChapterLongerThanFourChunks(t *testing.T) {
	version := testSaga()
	chapters := append([]sagamemory.Chapter(nil), version.Chapters...)
	for index := range chapters {
		chapters[index].Key, chapters[index].Index = "", 0
	}
	chapters[1].Paragraphs = []sagamemory.Paragraph{{Text: strings.TrimSpace(strings.Repeat("Дорога вела на север. ", 10))}}
	long := sagamemory.NewVersion("project-1", "draft", "fp", chapters)
	repository := &readRepositoryStub{authority: researchdomain.ReadAuthority{ProjectID: "project-1", SourceView: "draft", Scope: researchdomain.ScopeProject}}
	model := &scanModelStub{}
	usage := &usageStub{}
	tools := NewReadTools(repository, sagamemorytest.NewSaga(long, testStems, testCards()), model, usage, ScanSettings{Parallel: 2, ChunkRunes: 30}, noThreadLayer{})
	output, err := tools.Scan(context.Background(), rootCall, ScanInput{Task: "все упоминания вьюжниц"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Просмотрено глав: 2. Найдено: 1 (мест: 2). Главы: гл. 1, гл. 3. Порядок — по первому появлению.\n" +
		"Глава 2 слишком длинная для scan (219 знаков): ищите в ней через search или читайте read с from/to по абзацам.\n" +
		"- вьюжница — мест: 2; впервые гл. 1 (¶2): «Вьюжница ждала у ворот.»; также: гл. 3 (¶1)\n"
	if output != want {
		t.Fatalf("scan output = %q, want %q", output, want)
	}
	sort.Strings(model.inputs)
	inputs := []string{
		sagamemory.ChapterText(&testSaga().Chapters[0]) + "\nЗАДАЧА: все упоминания вьюжниц",
		sagamemory.ChapterText(&testSaga().Chapters[2]) + "\nЗАДАЧА: все упоминания вьюжниц",
	}
	sort.Strings(inputs)
	if !reflect.DeepEqual(model.inputs, inputs) || len(usage.records) != 2 {
		t.Fatalf("the model read %q, %d spends", model.inputs, len(usage.records))
	}
}
