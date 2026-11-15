package researchchat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/authormemory"
	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

// namingRecorder is the port of the naming of the tests: it keeps what each call named by its thread and saga.
type namingRecorder struct {
	mu      sync.Mutex
	named   []disclosure.Named
	threads []string
	taken   []string
}

func (recorder *namingRecorder) Name(_ context.Context, threadID, projectID string, named disclosure.Named) (string, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.named = append(recorder.named, named)
	recorder.threads = append(recorder.threads, threadID+"|"+projectID)
	return "naming-" + string(rune('a'+len(recorder.named))), nil
}

func (recorder *namingRecorder) Withdraw(_ context.Context, id string) error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.taken = append(recorder.taken, id)
	return nil
}

// all is what every call named, as one value.
func (recorder *namingRecorder) all() disclosure.Named {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	var all disclosure.Named
	for _, named := range recorder.named {
		all = disclosure.Merge(all, named)
	}
	return all
}

type materialsStub []authormaterials.Record

func (stub materialsStub) AuthorMaterials(context.Context, string) ([]authormaterials.Record, error) {
	return stub, nil
}

// namedMaterials are two records of the author: one the book knows the name of, one only the materials have.
var namedMaterials = materialsStub{
	{ID: "m-blizzard", Draft: authormaterials.Draft{Name: "Вьюжница", Kind: authormaterials.KindCharacter, Description: "Вьюжница ходит по снегу."}},
	{ID: "m-key", Draft: authormaterials.Draft{Name: "Северный ключ", Kind: authormaterials.KindItem, Description: "Ключ от заставы."}},
}

func whole(ids ...string) disclosure.Named {
	var named disclosure.Named
	for _, id := range ids {
		named.Chapter(id, disclosure.Whole, 0, 0)
	}
	return named
}

func of(precision disclosure.Precision, ids ...string) []disclosure.Chapter {
	chapters := make([]disclosure.Chapter, len(ids))
	for i, id := range ids {
		chapters[i] = disclosure.Chapter{ID: id, Precision: precision}
	}
	return chapters
}

// specs/2026-11-14-desk-journal.md JR1.3, JR1.4: every tool of the chat names what it showed — in the thread of its turn,
// before its answer goes — by the table of testdata/named-tools.json; a tool the table lists and no example runs fails here.
func TestEveryToolOfTheChatNamesWhatItShowed(t *testing.T) {
	raw, err := os.ReadFile("testdata/named-tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Tools map[string]string `json:"tools"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tools := func(t *testing.T) (*ReadTools, *readRepositoryStub, *namingRecorder) {
		read, repository := newTestTools(researchdomain.ScopeProject, "")
		repository.authority.RootThreadID, repository.authority.ChatID = "root", "chat-1"
		recorder := &namingRecorder{}
		return read.WithNaming(recorder).WithMaterials(namedMaterials), repository, recorder
	}
	examples := map[string]func(t *testing.T) (got, want disclosure.Named){
		"saga.read": func(t *testing.T) (disclosure.Named, disclosure.Named) {
			read, _, recorder := tools(t)
			if _, err := read.Read(ctx, rootCall, ReadInput{Chapter: "3", From: 1, To: 2}); err != nil {
				t.Fatal(err)
			}
			var want disclosure.Named
			want.Chapter("c3", disclosure.Paragraphs, 1, 2)
			return recorder.all(), want
		},
		"saga.search": func(t *testing.T) (disclosure.Named, disclosure.Named) {
			read, _, recorder := tools(t)
			if _, err := read.Search(ctx, rootCall, SearchInput{Queries: []string{"вьюжница"}, Order: "story"}); err != nil {
				t.Fatal(err)
			}
			return recorder.all(), disclosure.Named{Chapters: of(disclosure.Excerpts, "c1", "c3"), Materials: []string{"m-blizzard"}}
		},
		"saga.chapters": func(t *testing.T) (disclosure.Named, disclosure.Named) {
			read, _, recorder := tools(t)
			if _, err := read.Chapters(ctx, rootCall, ChaptersInput{}); err != nil {
				t.Fatal(err)
			}
			titles := recorder.all()
			if !reflect.DeepEqual(titles, disclosure.Named{Chapters: of(disclosure.Titles, "c1", "c2", "c3")}) {
				t.Fatalf("the titles named %+v", titles)
			}
			read, _, recorder = tools(t)
			if _, err := read.Chapters(ctx, rootCall, ChaptersInput{Detail: ChaptersSummary}); err != nil {
				t.Fatal(err)
			}
			return recorder.all(), disclosure.Named{Chapters: of(disclosure.Cards, "c1", "c2", "c3")}
		},
		"saga.glossary": func(t *testing.T) (disclosure.Named, disclosure.Named) {
			read, _, recorder := tools(t)
			if _, err := read.Glossary(ctx, rootCall, GlossaryInput{Query: "вьюжница"}); err != nil {
				t.Fatal(err)
			}
			return recorder.all(), disclosure.Named{Chapters: of(disclosure.Cards, "c1", "c3"), Materials: []string{"m-blizzard"}}
		},
		"saga.entity": func(t *testing.T) (disclosure.Named, disclosure.Named) {
			read, _, recorder := tools(t)
			// A name only the materials have: the record goes whole, and no chapter.
			if _, err := read.Entity(ctx, rootCall, EntityInput{Name: "Северный ключ"}); err != nil {
				t.Fatal(err)
			}
			return recorder.all(), disclosure.Named{Materials: []string{"m-key"}}
		},
		"saga.timeline": func(t *testing.T) (disclosure.Named, disclosure.Named) {
			read, _, recorder := tools(t)
			if _, err := read.Timeline(ctx, rootCall, TimelineInput{}); err != nil {
				t.Fatal(err)
			}
			got := recorder.all()
			for _, chapter := range got.Chapters {
				if chapter.Precision != disclosure.Cards {
					t.Fatalf("the timeline named %+v", got)
				}
			}
			return got, disclosure.Named{Chapters: of(disclosure.Cards, "c1", "c2", "c3")}
		},
		"saga.changes": func(t *testing.T) (disclosure.Named, disclosure.Named) {
			read, _, recorder := tools(t)
			if _, err := read.Changes(ctx, rootCall, ChangesInput{Subject: "Мирон"}); err != nil {
				t.Fatal(err)
			}
			return recorder.all(), disclosure.Named{Chapters: of(disclosure.Cards, "c1", "c3")}
		},
		"saga.threads": func(t *testing.T) (disclosure.Named, disclosure.Named) {
			read, repository, _ := newThreadTools(researchdomain.ScopeProject, "", builtLayer())
			repository.authority.RootThreadID = "root"
			recorder := &namingRecorder{}
			read.WithNaming(recorder)
			if _, err := read.Threads(ctx, rootCall, ThreadsInput{}); err != nil {
				t.Fatal(err)
			}
			return recorder.all(), disclosure.Named{Threads: true}
		},
		"saga.scan": func(t *testing.T) (disclosure.Named, disclosure.Named) {
			read, repository, _ := newTestToolsWithScan(researchdomain.ScopeProject, "", ScanSettings{Parallel: 2, ChunkRunes: 30})
			repository.authority.RootThreadID = "root"
			recorder := &namingRecorder{}
			read.WithNaming(recorder)
			if _, err := read.Scan(ctx, rootCall, ScanInput{Task: "все упоминания вьюжниц"}); err != nil {
				t.Fatal(err)
			}
			return recorder.all(), disclosure.Named{Chapters: of(disclosure.Excerpts, "c1", "c2", "c3")}
		},
		"saga.attachment": func(t *testing.T) (disclosure.Named, disclosure.Named) {
			long := strings.Repeat("слово ", maxAttachmentWordsPerCall/2)
			read, repository, _ := attachmentTools(map[int]researchdomain.Attachment{
				2: {Number: 2, Title: "Застава", Paragraphs: []string{long, long, long, long}, Words: 2 * maxAttachmentWordsPerCall},
			})
			repository.authority.RootThreadID = "root"
			recorder := &namingRecorder{}
			read.WithNaming(recorder)
			if _, err := read.Attachment(ctx, rootCall, AttachmentInput{Attachment: 2}); err != nil {
				t.Fatal(err)
			}
			// The limit of the words of a call shows two paragraphs of the four.
			return recorder.all(), disclosure.Named{Attachments: []disclosure.Attachment{{Chat: "chat-1", Number: 2, Paragraphs: []disclosure.Range{{From: 1, To: 2}}}}}
		},
		"saga.memory": func(t *testing.T) (disclosure.Named, disclosure.Named) {
			repository := &memoryRepositoryStub{authority: researchdomain.ReadAuthority{ProjectID: "project-1", RootThreadID: "root"}, page: authormemory.Page{Total: 2, Records: []authormemory.Record{
				record("r1", authormemory.KindPlan, "Сватов", "Сватов уедет на север."), record("r2", authormemory.KindCorrection, "", "Застава стоит у моря."),
			}}}
			recorder := &namingRecorder{}
			memory := NewAuthorMemoryTools(repository, chapterVersionStub{memoryVersion()}).WithNaming(recorder)
			if _, err := memory.Memory(ctx, memoryCall, MemoryInput{}); err != nil {
				t.Fatal(err)
			}
			// The block «Автор сказал об этом» of an answer names its records too.
			read, _, readRecorder := tools(t)
			read.WithAuthorMemory(&authorWordsStub{records: []authormemory.Record{record("r3", authormemory.KindCorrection, "Мирон", "Мирону сорок лет.")}})
			if _, err := read.Changes(ctx, rootCall, ChangesInput{Subject: "Мирон"}); err != nil {
				t.Fatal(err)
			}
			if words := readRecorder.all(); !slices.Equal(words.Memory, []string{"r3"}) {
				t.Fatalf("the block of the author's words named %+v", words)
			}
			return recorder.all(), disclosure.Named{Memory: []string{"r1", "r2"}}
		},
		"saga.remember": func(t *testing.T) (disclosure.Named, disclosure.Named) {
			repository := &memoryRepositoryStub{authority: researchdomain.ReadAuthority{ProjectID: "project-1", RootThreadID: "root", ActorUserID: "author"}}
			recorder := &namingRecorder{}
			memory := NewAuthorMemoryTools(repository, chapterVersionStub{memoryVersion()}).WithNaming(recorder)
			if _, err := memory.Remember(ctx, memoryCall, RememberInput{Kind: "plan", Text: "Сватов уедет на север."}); err != nil {
				t.Fatal(err)
			}
			return recorder.all(), disclosure.Named{}
		},
	}
	for tool := range table.Tools {
		if examples[tool] == nil {
			t.Errorf("the tool %s of the table has no example", tool)
		}
	}
	for tool, example := range examples {
		if table.Tools[tool] == "" {
			t.Errorf("the example %s is no tool of the table", tool)
			continue
		}
		t.Run(tool, func(t *testing.T) {
			got, want := example(t)
			if !reflect.DeepEqual(got, disclosure.Merge(disclosure.Named{}, want)) {
				t.Fatalf("named %+v, want %+v", got, want)
			}
		})
	}
}

// JR1.4: a tool names in the thread of its turn and saga; a stale source — the chapter changed under the call — named nothing,
// and returned nothing.
func TestAStaleSourceNamesNothing(t *testing.T) {
	read, repository := newTestTools(researchdomain.ScopeProject, "")
	repository.authority.RootThreadID = "root"
	recorder := &namingRecorder{}
	read.WithNaming(recorder)
	if _, err := read.Read(context.Background(), rootCall, ReadInput{Chapter: "1"}); err != nil {
		t.Fatal(err)
	}
	if recorder.threads[0] != "root|project-1" {
		t.Fatalf("named in %q", recorder.threads[0])
	}
	repository.retainErr = researchdomain.ErrStaleSource
	if _, err := read.Read(context.Background(), rootCall, ReadInput{Chapter: "3"}); err == nil {
		t.Fatal("a stale source answered")
	}
	if len(recorder.named) != 1 {
		t.Fatalf("a stale source named %+v", recorder.named[1:])
	}
}

// The site has no naming: the answers of its tools are the same to the sign with the port or without it.
func TestTheNamingChangesNoAnswer(t *testing.T) {
	plain, _ := newTestTools(researchdomain.ScopeProject, "")
	named, repository := newTestTools(researchdomain.ScopeProject, "")
	repository.authority.RootThreadID = "root"
	named.WithNaming(&namingRecorder{})
	for _, call := range []func(*ReadTools) (string, error){
		func(tools *ReadTools) (string, error) {
			return tools.Read(context.Background(), rootCall, ReadInput{Chapter: "1"})
		},
		func(tools *ReadTools) (string, error) {
			return tools.Search(context.Background(), rootCall, SearchInput{Queries: []string{"вьюжница"}})
		},
		func(tools *ReadTools) (string, error) {
			return tools.Chapters(context.Background(), rootCall, ChaptersInput{Detail: ChaptersCard})
		},
	} {
		before, err1 := call(plain)
		after, err2 := call(named)
		if err1 != nil || err2 != nil || before != after {
			t.Fatalf("the answer changed: %q / %q (%v %v)", before, after, err1, err2)
		}
	}
	_ = sagamemory.ErrSearchNotReady
}

// questionRunner calls what the turn gives before turn/start, as the adapter of Codex does for a new turn.
type questionRunner struct{ agentRunnerStub }

func (runner *questionRunner) Run(ctx context.Context, request AgentRunRequest) (agent.RunOutcome, error) {
	if err := request.BindRoot(ctx, runner.result.RootThreadID); err != nil {
		return agent.RunOutcome{}, err
	}
	if request.BeforeTurn != nil {
		if err := request.BeforeTurn(ctx, runner.result.RootThreadID); err != nil {
			return agent.RunOutcome{}, err
		}
	}
	return runner.result, request.BindTurn(ctx, runner.result.RootThreadID, runner.result.TurnID)
}

// JR1.3: a turn names its question in its thread and saga before Codex starts it; the site gives no BeforeTurn.
func TestATurnNamesItsQuestionBeforeTurnStart(t *testing.T) {
	run := researchdomain.TurnRun{
		Kind: agent.SessionResearch, ProjectID: "project-1", ChatID: "chat-1", TurnID: "chat-turn-1", JobID: "job-1",
		ActorUserID: "user-1", ClaimIncarnation: 1, SourceView: "draft", Scope: researchdomain.ScopeProject, Question: "Где застава?",
	}
	runner := &questionRunner{agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnCompleted, FinalResponse: "У перевала."}}}
	recorder := &namingRecorder{}
	claim := researchdomain.WorkerClaim{ProjectID: "project-1", JobID: "job-1", ClaimIncarnation: 1}
	bundles := chatBundles(Bundle{SkillName: "chat-skill", Handler: successfulToolHandler})
	if _, err := NewRunTurn(&runRepositoryStub{run: run}, runner, bundles).WithNaming(recorder).Execute(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	if len(recorder.named) != 1 || !recorder.named[0].Question || recorder.threads[0] != "root-1|project-1" {
		t.Fatalf("named %+v in %v", recorder.named, recorder.threads)
	}
	plain := &questionRunner{agentRunnerStub{result: runner.result}}
	if _, err := NewRunTurn(&runRepositoryStub{run: run}, plain, bundles).Execute(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
}

// JR1.3: a correction is named before turn/steer; one Codex refused is taken back, one it took stays.
func TestACorrectionIsNamedAndTakenBackWhenRefused(t *testing.T) {
	recorder := &namingRecorder{}
	accepted := NewSteerTurn(steerFixture(), func(context.Context, TurnSteerRequest) error {
		if len(recorder.named) != 1 || !recorder.named[0].Correction {
			t.Fatal("the correction went before it was named")
		}
		return nil
	}, steerClock{}).WithNaming(recorder)
	if _, err := accepted.Execute(t.Context(), researchdomain.Actor{UserID: "user-1"}, "chat-1", "turn-1", steerInput()); err != nil {
		t.Fatal(err)
	}
	if len(recorder.taken) != 0 || recorder.threads[0] != "root-1|project-1" {
		t.Fatalf("taken %v, named in %v", recorder.taken, recorder.threads)
	}
	refused := NewSteerTurn(steerFixture(), func(context.Context, TurnSteerRequest) error {
		return errors.New("the turn refused")
	}, steerClock{}).WithNaming(recorder)
	if _, err := refused.Execute(t.Context(), researchdomain.Actor{UserID: "user-1"}, "chat-1", "turn-1", steerInput()); err == nil {
		t.Fatal("a refused correction went")
	}
	if len(recorder.named) != 2 || len(recorder.taken) != 1 || recorder.taken[0] != "naming-c" {
		t.Fatalf("named %d, taken %v", len(recorder.named), recorder.taken)
	}
}

// namedScanModel keeps what each chunk of a scan named in its context; the chunks run in parallel.
type namedScanModel struct {
	scanModelStub
	namedMu sync.Mutex
	chunks  []disclosure.Named
}

func (model *namedScanModel) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	named, _ := disclosure.Of(ctx)
	model.namedMu.Lock()
	model.chunks = append(model.chunks, named)
	model.namedMu.Unlock()
	return model.scanModelStub.Complete(ctx, instructions, input)
}

// JR1.3: every chunk of a scan names the chapters it gives whole in the context of its own call — the chunks in parallel; at
// the site, without the port, no chunk makes a value (the third check, C8).
func TestEveryChunkOfAScanNamesItsChaptersWhole(t *testing.T) {
	repository := &readRepositoryStub{authority: researchdomain.ReadAuthority{ProjectID: "project-1", SourceView: "draft", Scope: researchdomain.ScopeProject, RootThreadID: "root"}}
	site := &namedScanModel{}
	bare := NewReadTools(repository, sagamemorytest.NewSaga(testSaga(), testStems, testCards()), site, nil, ScanSettings{Parallel: 3, ChunkRunes: 30}, noThreadLayer{})
	if _, err := bare.Scan(context.Background(), rootCall, ScanInput{Task: "все упоминания вьюжниц"}); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range site.chunks {
		if !chunk.Empty() || chunk.Chapters != nil {
			t.Fatalf("a chunk of the site made a value: %+v", chunk)
		}
	}
	model := &namedScanModel{}
	read := NewReadTools(repository, sagamemorytest.NewSaga(testSaga(), testStems, testCards()), model, nil, ScanSettings{Parallel: 3, ChunkRunes: 30}, noThreadLayer{})
	read.WithNaming(&namingRecorder{})
	if _, err := read.Scan(context.Background(), rootCall, ScanInput{Task: "все упоминания вьюжниц"}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, chunk := range model.chunks {
		if len(chunk.Chapters) != 1 || chunk.Chapters[0].Precision != disclosure.Whole {
			t.Fatalf("a chunk named %+v", chunk)
		}
		seen[chunk.Chapters[0].ID] = true
	}
	if len(model.chunks) != 3 || !seen["c1"] || !seen["c2"] || !seen["c3"] {
		t.Fatalf("the chunks named %+v", model.chunks)
	}
}

// The path of a turn of the site keeps nothing of what an answer shows (the third check, C8): without the port a chapter used
// by an answer allocates nothing for the journal; with it, it is kept.
func TestTheSiteKeepsNothingOfWhatAnAnswerShows(t *testing.T) {
	chapter := &testSaga().Chapters[0]
	site := &sagaScope{used: map[int]bool{chapter.Index: true}, shownAs: disclosure.Cards}
	if allocations := testing.AllocsPerRun(200, func() {
		site.use(chapter)
		site.show(chapter, disclosure.Paragraphs, 1, 3)
		site.showMaterial("material-1")
	}); allocations != 0 || !site.shown.Empty() {
		t.Fatalf("the site allocated %.1f a use and kept %+v", allocations, site.shown)
	}
	app := &sagaScope{used: map[int]bool{chapter.Index: true}, shownAs: disclosure.Cards, names: true}
	app.use(chapter)
	app.show(chapter, disclosure.Paragraphs, 1, 3)
	app.showMaterial("material-1")
	if len(app.shown.Chapters) != 2 || len(app.shown.Materials) != 1 {
		t.Fatalf("the app kept %+v", app.shown)
	}
}
