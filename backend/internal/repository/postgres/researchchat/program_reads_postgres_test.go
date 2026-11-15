package researchchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	codexresearch "canonkeeper/backend/internal/delivery/codex/researchchat"
	"canonkeeper/backend/internal/delivery/mcp/door"
	"canonkeeper/backend/internal/delivery/mcp/programtools"
	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/authormemory"
	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modeljournal"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
	authormaterialsrepo "canonkeeper/backend/internal/repository/postgres/authormaterials"
	authormemoryrepo "canonkeeper/backend/internal/repository/postgres/authormemory"
	modeljournalrepo "canonkeeper/backend/internal/repository/postgres/modeljournal"
	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	authormemoryservice "canonkeeper/backend/internal/services/authormemory"
	"canonkeeper/backend/internal/services/programreads"
	service "canonkeeper/backend/internal/services/researchchat"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	librarystore "canonkeeper/backend/internal/store/library"
	projectstore "canonkeeper/backend/internal/store/projects"
	quotastore "canonkeeper/backend/internal/store/quota"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-15-desk-mcp.md MC2.4, MC2.5: a program reads the saga by the rules of a turn — every reading of the set with
// the table of its arguments answers under the authority without a turn and the memory read by ListAuthorMemory what the
// handler answers a turn of the chat over the whole published saga, to the byte, twice the same; the turn keeps manifests,
// the program none.
func TestTheReadingsOfAProgramAreTheReadingsOfATurnPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_program_reads")
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	proof := seedSubmitTurnCanonicalBaseline(t, db, now)
	extra := seedAdditionalTimelineChapter(t, db, now)
	manuscripts := librarystore.New(db)
	publishBookChapterForTest(t, manuscripts, submitTurnChapterID, 1)
	publishBookChapterForTest(t, manuscripts, extra, 1)
	seedPublishedSearchRows(t, ctx, db)
	turn := startTurnInChatOf(t, db, proof, now, extra, submitTurnChatID, researchdomain.ScopeProject, governance.SourceViewPublished, "Что было на заставе?")

	saga := sagamemoryservice.NewSaga(sagamemoryrepo.New(db), sagamemoryrepo.New(db))
	materials := authormaterialsrepo.New(db, jobstore.New(db))
	chat := codexresearch.NewHandler(
		service.NewReadTools(turn.repository, saga, noScanModel{}, nil, service.ScanSettings{}, sagamemoryrepo.New(db)).
			WithMaterials(materials).WithAuthorMemory(turn.repository),
		service.NewAuthorMemoryTools(turn.repository, saga),
	)
	call := func(handle door.Handler, caller agent.ToolCaller, turnID, tool, arguments string) agent.ToolResult {
		t.Helper()
		result, err := handle(ctx, agent.ToolCall{Caller: caller, TurnID: turnID, CallID: "c", Namespace: "saga", Tool: tool, Arguments: json.RawMessage(arguments)})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	// The record of the memory the turn writes the standard way: a correction about what the search asks.
	if remembered := call(chat.Handle, turn.call.Caller, turn.call.TurnID, "remember", `{"kind":"correction","text":"Additional chapter text — это решение автора.","name":"additional"}`); !remembered.Success {
		t.Fatalf("remember: %s", remembered.Payload)
	}
	members := authormemoryrepo.New(db)
	program := door.ChatHandlers(door.ChatParts{
		Reads: func(projectID string) service.ReadRepository {
			return turn.repository.VoiceReads(submitTurnActorID, projectID)
		},
		Memory: func(ctx context.Context, projectID string, filter authormemory.Filter) (authormemory.Page, error) {
			return members.ListAuthorMemory(ctx, authormemoryservice.Actor{UserID: submitTurnActorID, ProjectID: projectID}, filter)
		},
		Saga: saga, Layer: sagamemoryrepo.New(db), Materials: materials, Chapters: saga,
	})
	refs := func() int {
		t.Helper()
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM artifact_source_refs WHERE project_id = $1::uuid`, submitTurnProjectID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	table := map[string][]string{
		"search":   {`{"queries":["additional"]}`, `{"queries":["canonical","text"],"match":"all","order":"story"}`},
		"read":     {`{"chapter":"b1c1"}`, `{"chapter":"b1c0","around":1,"radius":2}`},
		"chapters": {`{}`, `{"detail":"summary"}`, `{"detail":"card","title":"Additional"}`},
		"glossary": {`{}`, `{"query":"additional"}`},
		"entity":   {`{"name":"additional"}`},
		"timeline": {`{}`, `{"query":"additional"}`},
		"changes":  {`{"subject":"additional"}`},
		"threads":  {`{}`, `{"part":"arcs"}`},
		"memory":   {`{}`, `{"kind":"correction"}`, `{"query":"нет такого"}`},
	}
	programCaller := agent.ToolCaller{ThreadID: "program", RootThreadID: "program"}
	block := false
	for _, name := range programtools.Names() {
		if name == programtools.Sagas {
			continue
		}
		arguments, ok := table[name]
		if !ok {
			t.Fatalf("no arguments of %s", name)
		}
		for _, raw := range arguments {
			before := refs()
			first := call(program(submitTurnProjectID, programreads.NewNaming()), programCaller, "program", name, raw)
			second := call(program(submitTurnProjectID, programreads.NewNaming()), programCaller, "program", name, raw)
			if refs() != before {
				t.Fatalf("%s %s: the reading of a program kept a manifest", name, raw)
			}
			turnResult := call(chat.Handle, turn.call.Caller, turn.call.TurnID, name, raw)
			if !first.Success || first.Success != turnResult.Success || string(first.Payload) != string(turnResult.Payload) {
				t.Fatalf("%s %s:\nprogram %v %s\nturn    %v %s", name, raw, first.Success, first.Payload, turnResult.Success, turnResult.Payload)
			}
			if string(second.Payload) != string(first.Payload) {
				t.Fatalf("%s %s: two calls on the same saga differ", name, raw)
			}
			block = block || strings.Contains(string(first.Payload), "Автор сказал об этом")
		}
	}
	if refs() == 0 {
		t.Fatal("the turn kept no manifest")
	}
	// MC2.2: what the readings are built of can call no model and change nothing — the handler of a call, asked past the
	// closed list of the door, refuses the scan without a model and the writing of the memory without a row; no Job is made.
	counts := func() (memory, jobs int) {
		t.Helper()
		if err := db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM author_memory WHERE project_id = $1::uuid), (SELECT count(*) FROM jobs WHERE project_id = $1::uuid)`,
			submitTurnProjectID).Scan(&memory, &jobs); err != nil {
			t.Fatal(err)
		}
		return memory, jobs
	}
	memoryBefore, jobsBefore := counts()
	if result := call(program(submitTurnProjectID, programreads.NewNaming()), programCaller, "program", "scan", `{"task":"все места заставы"}`); !strings.HasPrefix(string(result.Payload), "Ни одна глава не просмотрена") {
		t.Fatalf("the scan of a program reached a model: %s", result.Payload)
	}
	if result := call(program(submitTurnProjectID, programreads.NewNaming()), programCaller, "program", "remember", `{"kind":"plan","text":"Чужая запись."}`); result.Success {
		t.Fatalf("remember went through the handler of a program: %s", result.Payload)
	}
	if memory, jobs := counts(); memory != memoryBefore || jobs != jobsBefore || memory != 1 {
		t.Fatalf("records of the memory %d → %d, Jobs %d → %d", memoryBefore, memory, jobsBefore, jobs)
	}
	if !block {
		t.Fatal("no answer brought the author's words: the memory of a program is not read")
	}
}

// seedPublishedSearchRows writes the text index state of the published text of the seeded chapters as the draft fixtures
// write it for the draft, and the saga search rows of it.
func seedPublishedSearchRows(t *testing.T, ctx context.Context, db *storedb.DB) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO chapter_text_search_rows (project_id,source_view,book_id,chapter_id,source_revision,locator_source_generation,title,content,chapter_content_hash,indexed_at)
		 SELECT project_id,'published',book_id,chapter_id,source_revision,locator_source_generation,title,content,chapter_content_hash,indexed_at
		 FROM chapter_text_search_rows WHERE project_id = $1::uuid AND source_view = 'draft'`,
		`INSERT INTO chapter_text_index_state (project_id,source_view,chapter_id,source_revision,locator_source_generation,chapter_content_hash,indexed_at)
		 SELECT project_id,'published',chapter_id,source_revision,locator_source_generation,chapter_content_hash,indexed_at
		 FROM chapter_text_index_state WHERE project_id = $1::uuid AND source_view = 'draft'`,
	} {
		if _, err := db.Pool.Exec(ctx, statement, submitTurnProjectID); err != nil {
			t.Fatal(err)
		}
	}
	testutil.SeedSagaSearchRows(t, db, submitTurnProjectID)
}

// journalOf is the journal of the door with a step before a record: the saga deleted between the reading and its record, or a
// journal that is down.
type journalOf struct {
	next   programreads.Journal
	before func() error
}

func (journal journalOf) RecordRead(ctx context.Context, read modeljournal.ProgramRead) error {
	if journal.before != nil {
		if err := journal.before(); err != nil {
			return err
		}
	}
	return journal.next.RecordRead(ctx, read)
}

// specs/2026-11-15-desk-mcp.md MC2.3, MC2.7, MC4.2: the door over the shelf and the journal of the app — the saga of the
// argument among the living sagas of the author by the shelf (one without an argument, several only by a key or a title one
// saga has, a deleted saga not seen and its key back with it), every reading of the set recorded before its answer goes with
// its program, its page and what it named, nothing left «named, not sent»; a refusal, a failed record and a saga deleted
// before its record leave no record and no text; the pages of an answer are records of their pages; 300 sagas are refused
// without a title.
func TestTheDoorReadsTheShelfOfTheAuthorAndRecordsEveryReadingPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_program_door")
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	seedSubmitTurnCanonicalBaseline(t, db, now)
	extra := seedAdditionalTimelineChapter(t, db, now)
	manuscripts := librarystore.New(db)
	publishBookChapterForTest(t, manuscripts, submitTurnChapterID, 1)
	publishBookChapterForTest(t, manuscripts, extra, 1)
	seedPublishedSearchRows(t, ctx, db)
	saga := sagamemoryservice.NewSaga(sagamemoryrepo.New(db), sagamemoryrepo.New(db))
	members := authormemoryrepo.New(db)
	repository := New(db)
	parts := door.ChatParts{
		Reads: func(projectID string) service.ReadRepository {
			return repository.VoiceReads(submitTurnActorID, projectID)
		},
		Memory: func(ctx context.Context, projectID string, filter authormemory.Filter) (authormemory.Page, error) {
			return members.ListAuthorMemory(ctx, authormemoryservice.Actor{UserID: submitTurnActorID, ProjectID: projectID}, filter)
		},
		Saga: saga, Layer: sagamemoryrepo.New(db), Materials: authormaterialsrepo.New(db, jobstore.New(db)), Chapters: saga,
	}
	shelf := projects.NewService(projectstore.New(db), projects.NewSecureIDGenerator(), auth.SystemClock{})
	journal := journalOf{next: modeljournalrepo.New(db)}
	theDoor := door.New(programreads.New(shelf, &journal, submitTurnActorID), door.ChatHandlers(parts), "1.4.0")
	ask := func(tool, arguments string) programtools.Answer {
		t.Helper()
		return theDoor.Answer(ctx, programtools.Request{
			Fingerprint: programtools.Fingerprint(), Version: "1.4.0", Kind: programtools.KindInstall, Client: "claude-code", Title: "Claude Code",
			Tool: tool, Arguments: json.RawMessage(arguments),
		})
	}
	records := func() int {
		t.Helper()
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM model_disclosures WHERE purpose = 'mcp'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	// One saga: no argument; the record lies before the answer with the program, the page and the place of the table.
	if answer := ask("read", `{"chapter":"b1c1"}`); answer.Error || !strings.Contains(answer.Text, "additional chapter text") {
		t.Fatalf("read: %+v", answer)
	}
	var client, named string
	var page, pages int
	if err := db.Pool.QueryRow(ctx, `SELECT client, page, pages, named::text FROM model_disclosures WHERE purpose = 'mcp'`).Scan(&client, &page, &pages, &named); err != nil {
		t.Fatal(err)
	}
	if client != "Claude Code" || page != 1 || pages != 1 || !strings.Contains(named, extra) || !strings.Contains(named, `"paragraphs"`) {
		t.Fatalf("the record: %s %d/%d %s", client, page, pages, named)
	}
	for _, call := range [][2]string{
		{"search", `{"queries":["additional"]}`}, {"chapters", `{}`}, {"glossary", `{}`}, {"entity", `{"name":"additional"}`},
		{"timeline", `{}`}, {"changes", `{"subject":"additional"}`}, {"threads", `{}`}, {"memory", `{}`}, {"sagas", `{}`},
	} {
		before := records()
		if answer := ask(call[0], call[1]); answer.Error || records() != before+1 {
			t.Fatalf("%s: %+v, records %d → %d", call[0], answer, before, records())
		}
	}
	var sagas, empty string
	if err := db.Pool.QueryRow(ctx, `SELECT named::text FROM model_disclosures WHERE purpose = 'mcp' AND project_id IS NULL`).Scan(&sagas); err != nil || sagas != `{"sagas": 1}` {
		t.Fatalf("the record of sagas: %s %v", sagas, err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT named::text FROM model_disclosures WHERE purpose = 'mcp' ORDER BY at DESC OFFSET 1 LIMIT 1`).Scan(&empty); err != nil || empty != `{}` {
		t.Fatalf("the record of an answer without the books: %s %v", empty, err)
	}
	var pending int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM model_disclosures_pending`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("named, not sent: %d %v", pending, err)
	}
	before := records()
	if answer := ask("read", `{"chapter":"нет такой"}`); !answer.Error || records() != before {
		t.Fatalf("a refusal: %+v", answer)
	}
	journal.before = func() error { return errors.New("the journal is down") }
	if answer := ask("read", `{"chapter":"b1c1"}`); answer.Text != programtools.WordsNotRecord || records() != before {
		t.Fatalf("a failed record: %+v", answer)
	}
	journal.before = nil

	// Several sagas: by a key, a longer beginning of the id or a title in another case; a title two sagas have is no saga.
	second, err := shelf.CreateProject(ctx, submitTurnActorID, projects.CreateProjectInput{Title: "Северный предел"})
	if err != nil {
		t.Fatal(err)
	}
	if answer := ask("read", `{"chapter":"b1c1"}`); answer.Text != programtools.WordsSeveral {
		t.Fatalf("two sagas without an argument: %+v", answer)
	}
	for _, argument := range []string{submitTurnProjectID[:8], submitTurnProjectID[:13], "rch SUBMIT"} {
		if answer := ask("read", `{"chapter":"b1c1","saga":"`+argument+`"}`); answer.Error {
			t.Fatalf("the saga %q: %+v", argument, answer)
		}
	}
	if answer := ask("read", `{"chapter":"b1c1","saga":"`+second.ID[:8]+`"}`); !answer.Error || strings.Contains(answer.Text, "additional chapter text") {
		t.Fatalf("a saga without the chapter read another: %+v", answer)
	}
	third, err := shelf.CreateProject(ctx, submitTurnActorID, projects.CreateProjectInput{Title: "северный предел"})
	if err != nil {
		t.Fatal(err)
	}
	if answer := ask("chapters", `{"saga":"Северный предел"}`); answer.Text != programtools.WordsSeveral || second.ID[:8] == third.ID[:8] {
		t.Fatalf("one title of two sagas: %+v", answer)
	}
	// A deleted saga is not seen; back from «Недавно удалённые», its key reads it again.
	if err := shelf.DeleteProject(ctx, submitTurnActorID, second.ID); err != nil {
		t.Fatal(err)
	}
	if answer := ask("sagas", `{}`); strings.Contains(answer.Text, second.ID[:8]) || !strings.Contains(answer.Text, third.ID[:8]) {
		t.Fatalf("the sagas with a deleted one: %+v", answer)
	}
	if answer := ask("chapters", `{"saga":"`+second.ID[:8]+`"}`); answer.Text != programtools.WordsNoSuch {
		t.Fatalf("a deleted saga: %+v", answer)
	}
	if _, err := shelf.RestoreProject(ctx, submitTurnActorID, second.ID); err != nil {
		t.Fatal(err)
	}
	if answer := ask("chapters", `{"saga":"`+second.ID[:8]+`"}`); answer.Text == programtools.WordsNoSuch {
		t.Fatalf("a saga back: %+v", answer)
	}
	// Deleted between sagas and the reading, or between the reading and its record: no such saga, no record.
	if err := shelf.DeleteProject(ctx, submitTurnActorID, third.ID); err != nil {
		t.Fatal(err)
	}
	if answer := ask("chapters", `{"saga":"`+third.ID[:8]+`"}`); answer.Text != programtools.WordsNoSuch {
		t.Fatalf("deleted before the reading: %+v", answer)
	}
	before = records()
	journal.before = func() error { return shelf.DeleteProject(ctx, submitTurnActorID, submitTurnProjectID) }
	if answer := ask("read", `{"chapter":"b1c1","saga":"`+submitTurnProjectID[:8]+`"}`); answer.Text != programtools.WordsNoSuch || records() != before {
		t.Fatalf("deleted before the record: %+v, records %d → %d", answer, before, records())
	}
	journal.before = nil

	// The pages of an answer are the records of their pages: the place by pages, never whole.
	long := strings.Repeat("Он шёл к заставе.\n", 5_000)
	paged := door.New(programreads.New(shelf, &journal, submitTurnActorID), func(projectID string, naming service.Naming) door.Handler {
		return func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
			var whole disclosure.Named
			whole.Chapter(extra, disclosure.Whole, 0, 0)
			_, _ = naming.Name(ctx, "program", projectID, whole)
			return agent.ToolResult{Success: true, Payload: json.RawMessage(long)}, nil
		}
	}, "1.4.0")
	value := ""
	for number := 1; ; number++ {
		arguments := `{"chapter":"b1c1","saga":"` + second.ID[:8] + `"` + value + `}`
		answer := paged.Answer(ctx, programtools.Request{Fingerprint: programtools.Fingerprint(), Version: "1.4.0", Kind: programtools.KindInstall, Client: "codex-mcp-client", Tool: "read", Arguments: json.RawMessage(arguments)})
		if answer.Error || len(answer.Text) > 11_000 {
			t.Fatalf("page %d: %d bytes, %v", number, len(answer.Text), answer.Error)
		}
		_, next, more := strings.Cut(strings.SplitN(answer.Text, "\n", 2)[0], `"page": "`)
		if !more {
			break
		}
		value = `,"page":"` + strings.TrimSuffix(next, `"`) + `"`
	}
	var count, whole int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE named::text LIKE '%"whole"%') FROM model_disclosures
		WHERE purpose = 'mcp' AND client = 'Codex' AND pages > 1 AND named::text LIKE '%"pages"%'`).Scan(&count, &whole); err != nil || count < 6 || whole != 0 {
		t.Fatalf("the records of the pages: %d, whole %d, %v", count, whole, err)
	}

	// A chapters answer of several pages names the titles of chapters, not their text: the records keep the precision of titles,
	// and the journal says «названия глав» with the page of the answer.
	titles := door.New(programreads.New(shelf, &journal, submitTurnActorID), func(projectID string, naming service.Naming) door.Handler {
		return func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
			var named disclosure.Named
			named.Chapter(extra, disclosure.Titles, 0, 0)
			_, _ = naming.Name(ctx, "program", projectID, named)
			return agent.ToolResult{Success: true, Payload: json.RawMessage(long)}, nil
		}
	}, "1.4.0")
	if answer := titles.Answer(ctx, programtools.Request{Fingerprint: programtools.Fingerprint(), Version: "1.4.0", Kind: programtools.KindInstall, Client: "claude-ai", Tool: "chapters", Arguments: json.RawMessage(`{"saga":"` + second.ID[:8] + `"}`)}); answer.Error {
		t.Fatalf("chapters by pages: %+v", answer)
	}
	var titled string
	var titledPage, titledPages int
	if err := db.Pool.QueryRow(ctx, `SELECT named::text, page, pages FROM model_disclosures WHERE purpose = 'mcp' AND client = 'Claude Desktop'`).Scan(&titled, &titledPage, &titledPages); err != nil {
		t.Fatal(err)
	}
	read, err := disclosure.Parse(titled)
	if err != nil || titledPages < 2 || len(read.Chapters) != 1 || read.Chapters[0].Precision != disclosure.Titles {
		t.Fatalf("a chapters answer of %d pages recorded %s", titledPages, titled)
	}
	place := modeljournal.Places{Chapters: map[string]modeljournal.ChapterPlace{extra: {State: modeljournal.PlaceLive, Book: 1, Number: "1", Title: "Застава"}}}
	if words := modeljournal.WhatWent(modeljournal.KindProgram, read, place, modeljournal.Paging{Page: titledPage, Pages: titledPages}); words != fmt.Sprintf("названия глав: Книга 1, гл. 1 „Застава“ · страница 1 из %d ответа", titledPages) {
		t.Fatalf("the words of a chapters answer by pages: %s", words)
	}

	// 300 sagas: the refusal names none of them and stays short. The limit of the sagas of the author is his own, as the admin
	// sets it.
	many := int64(400)
	if _, err := quotastore.New(db, quotastore.Fence{}).SetOverrides(ctx, submitTurnActorID, []quota.OverrideChange{{Key: quota.KeyProjects, Value: &many}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for index := range 300 {
		if _, err := shelf.CreateProject(ctx, submitTurnActorID, projects.CreateProjectInput{Title: "Сага номер " + strconv.Itoa(index)}); err != nil {
			t.Fatal(err)
		}
	}
	if answer := ask("read", `{"chapter":"1"}`); answer.Text != programtools.WordsSeveral || len([]rune(answer.Text)) >= 2000 || strings.Contains(answer.Text, "Сага номер") {
		t.Fatalf("300 sagas: %+v", answer)
	}
}
