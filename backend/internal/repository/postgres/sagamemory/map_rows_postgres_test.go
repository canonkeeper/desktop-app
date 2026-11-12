package sagamemory

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
	authormaterialsrepo "canonkeeper/backend/internal/repository/postgres/authormaterials"
	service "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
	reference "canonkeeper/backend/internal/services/sagareference"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

type anyReader struct{}

func (anyReader) ReadableView(context.Context, string, string, string) error { return nil }

var mapScope = reference.Scope{ActorUserID: "actor", ProjectID: memoryProjectID, View: "draft"}

func mapCardsJob(t *testing.T, repository *Repository, model *namesCardModel) *service.BuildCards {
	t.Helper()
	failed := func(_, stage string, err error) { t.Errorf("the step %s of the Job failed: %v", stage, err) }
	return service.NewBuildCards(repository, repository, model, nil, "test-model", 2, failed, nil).
		WithMap(service.NewMapBuilder(service.NewSaga(repository, repository), repository, sagamemorytest.Materials(nil)), failed)
}

func mapRowsForTest(t *testing.T, db *storedb.DB) (graphs, nodes, names int) {
	t.Helper()
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM saga_map_graphs WHERE project_id = $1),
		       (SELECT count(*) FROM saga_map_nodes WHERE project_id = $1),
		       (SELECT count(*) FROM saga_map_names WHERE project_id = $1)
	`, memoryProjectID).Scan(&graphs, &nodes, &names); err != nil {
		t.Fatal(err)
	}
	var parts int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM saga_map_node_parts WHERE project_id = $1`, memoryProjectID).Scan(&parts); err != nil {
		t.Fatal(err)
	}
	if parts < nodes || nodes == 0 && parts != 0 {
		t.Fatalf("%d parts of %d nodes", parts, nodes)
	}
	return graphs, nodes, names
}

func mapUseCases(repository *Repository) (*reference.GetMap, *reference.GetMapNode) {
	return reference.NewGetMap(anyReader{}, service.NewSaga(repository, repository), repository, repository),
		reference.NewGetMapNode(anyReader{}, repository)
}

func decodedMapRow(t *testing.T, result reference.MapResult) domain.MapDocument {
	t.Helper()
	var document domain.MapDocument
	if err := domain.DecodeStored(result.Document, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

// specs/2026-11-04-saga-map.md R10.1, R10.2: the last step of the cards Job prepares the map, and the card is read from its row:
// it answers the same with no card of a chapter left to read, by any name and at any chapter of the scale.
func TestCardsJobPreparesTheMapTheReadsServe(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_map_rows")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	repository := New(db)
	graph, nodes := mapUseCases(repository)
	empty, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope})
	if err != nil {
		t.Fatal(err)
	}
	if document := decodedMapRow(t, empty); document.Building || document.Coverage.WithCards != 0 || document.Coverage.Chapters != 3 {
		t.Fatalf("before the cards = %+v", document)
	}
	if status, err := mapCardsJob(t, repository, &namesCardModel{}).RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	if graphs, rows, names := mapRowsForTest(t, db); graphs != 1 || rows == 0 || names == 0 {
		t.Fatalf("%d graphs, %d nodes, %d names", graphs, rows, names)
	}
	result, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope})
	if err != nil {
		t.Fatal(err)
	}
	document := decodedMapRow(t, result)
	if document.Building || document.Revision != result.Revision || len(document.Chapters) != 3 || document.Coverage.WithCards != 3 {
		t.Fatalf("map = %+v", document)
	}
	if again, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope, Known: []string{result.Revision}}); err != nil || !again.NotModified || again.Document != nil {
		t.Fatalf("again = %+v, %v", again, err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM chapter_cards WHERE project_id = $1`, memoryProjectID); err != nil {
		t.Fatal(err)
	}
	for _, input := range []reference.MapNodeInput{
		{Scope: mapScope, Key: "Мирон"},
		{Scope: mapScope, Key: "первый король"},
		{Scope: mapScope, Key: "мирон", To: document.Chapters[0].Key},
	} {
		found, err := nodes.Execute(ctx, input)
		if err != nil {
			t.Fatalf("%+v: %v", input, err)
		}
		var card domain.MapCardDocument
		if err := domain.DecodeStored(found.Document, &card); err != nil || card.Name != "Мирон" {
			t.Fatalf("%+v: card %+v, %v", input, card, err)
		}
		if input.To == "" && card.Mentions != 3 || input.To != "" && card.Mentions != 1 {
			t.Fatalf("%+v: %d mentions", input, card.Mentions)
		}
	}
	if _, err := nodes.Execute(ctx, reference.MapNodeInput{Scope: mapScope, Key: "никто"}); !errors.Is(err, reference.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := nodes.Execute(ctx, reference.MapNodeInput{Scope: mapScope, Key: "Мирон", To: "99"}); !errors.Is(err, reference.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// R10.3: a renamed book leaves the map behind its cards: it answers, marked, and asks for one Job, whose last step brings it up to
// date without a call of the model; the next start queues nothing, and a saga carded before the map gets one from the start.
func TestAMapBehindItsCardsGetsAJobWithoutACallOfTheModel(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_map_behind")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	repository := siteRepository(db)
	model := &namesCardModel{}
	withoutMap := service.NewBuildCards(repository, repository, model, nil, "test-model", 2, nil, nil)
	if status, err := withoutMap.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	cards, registryCalls := model.count(), model.registryCalls
	catchUp := service.NewQueueMissingCards(repository, repository, repository, repository)
	if queued, err := catchUp.Execute(ctx); err != nil || queued != 1 {
		t.Fatalf("start without a map = %d, %v", queued, err)
	}
	build := mapCardsJob(t, repository, model)
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	if graphs, _, _ := mapRowsForTest(t, db); graphs != 1 || model.count() != cards || model.registryCalls != registryCalls {
		t.Fatalf("%d graphs, %d cards, %d registry calls; want the map and no call", graphs, model.count()-cards, model.registryCalls-registryCalls)
	}
	if queued, err := catchUp.Execute(ctx); err != nil || queued != 0 {
		t.Fatalf("next start = %d, %v; want nothing", queued, err)
	}

	graph, _ := mapUseCases(repository)
	fresh, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE books SET title = 'Новое имя тома' WHERE id = $1`, memoryBookOne); err != nil {
		t.Fatal(err)
	}
	behind, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope, Known: []string{fresh.Revision}})
	if err != nil {
		t.Fatal(err)
	}
	if document := decodedMapRow(t, behind); !document.Building || document.Chapters[0].BookTitle == "Новое имя тома" || behind.Revision == fresh.Revision {
		t.Fatalf("behind = %+v (%s)", document, behind.Revision)
	}
	if count := cardsJobsForTest(t, db); count != 1 {
		t.Fatalf("%d Jobs waiting after a read, want one", count)
	}
	if _, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope, Known: []string{behind.Revision}}); err != nil {
		t.Fatal(err)
	}
	if count := cardsJobsForTest(t, db); count != 1 {
		t.Fatalf("%d Jobs waiting after the next read, want the same one", count)
	}
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	rebuilt, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope, Known: []string{behind.Revision}})
	if err != nil {
		t.Fatal(err)
	}
	if document := decodedMapRow(t, rebuilt); document.Building || document.Chapters[0].BookTitle != "Новое имя тома" || model.count() != cards {
		t.Fatalf("rebuilt = %+v, %d cards built", document, model.count()-cards)
	}
}

// statementLog keeps the statements of the connection, to say what a read did.
type statementLog struct {
	mu         sync.Mutex
	statements []string
}

func (log *statementLog) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.statements = append(log.statements, data.SQL)
	return ctx
}

func (*statementLog) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// during runs a read and returns the names of its statements.
func (log *statementLog) during(t *testing.T, read func() error) []string {
	t.Helper()
	log.mu.Lock()
	log.statements = nil
	log.mu.Unlock()
	if err := read(); err != nil {
		t.Fatal(err)
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	names := make([]string, 0, len(log.statements))
	for _, statement := range log.statements {
		if strings.Contains(statement, "card_json") {
			t.Fatalf("a read of the cards: %s", statement)
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(statement, "-- name: "), " ")
		names = append(names, name)
	}
	return names
}

// R10.1, R10.2: the card of a node is one statement by key at any chapter and touches no card of a chapter; the map adds to its one
// statement only what says the revision of the cards, reading no card either.
func TestTheReadsOfTheMapReadNoCards(t *testing.T) {
	log := &statementLog{}
	db := testutil.OpenIsolatedPostgresTestDBWithTracer(t, "ck_saga_map_statements", log)
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	repository := New(db)
	if status, err := mapCardsJob(t, repository, &namesCardModel{}).RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	graph, nodes := mapUseCases(repository)
	var result reference.MapResult
	got := log.during(t, func() (err error) {
		result, err = graph.Execute(ctx, reference.MapInput{Scope: mapScope})
		return err
	})
	if want := []string{"ListCanonicalTextSelectionRows", "ListSagaMemoryBooks", "GetChapterCardsStamp", "ListSagaMapHidden", "GetSagaMapGraph"}; !slices.Equal(got, want) {
		t.Fatalf("statements of the map = %v, want %v", got, want)
	}
	chapter := decodedMapRow(t, result).Chapters[0].Key
	got = log.during(t, func() error {
		_, err := nodes.Execute(ctx, reference.MapNodeInput{Scope: mapScope, Key: "Мирон"})
		return err
	})
	if want := []string{"ListSagaMapHidden", "GetSagaMapCard"}; !slices.Equal(got, want) {
		t.Fatalf("statements of the card = %v, want %v", got, want)
	}
	got = log.during(t, func() error {
		_, err := nodes.Execute(ctx, reference.MapNodeInput{Scope: mapScope, Key: "Мирон", To: chapter})
		return err
	})
	if want := []string{"ListSagaMapHidden", "GetSagaMapChapters", "GetSagaMapNodePart"}; !slices.Equal(got, want) {
		t.Fatalf("statements of the card at a chapter = %v, want %v", got, want)
	}
	search := reference.NewSearchMap(anyReader{}, repository)
	got = log.during(t, func() error {
		_, err := search.Execute(ctx, reference.MapSearchInput{Scope: mapScope, Query: "мирон"})
		return err
	})
	if want := []string{"SearchSagaMapNodes"}; !slices.Equal(got, want) {
		t.Fatalf("statements of the search = %v, want %v", got, want)
	}
}

func cardsJobsForTest(t *testing.T, db *storedb.DB) (count int) {
	t.Helper()
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND status IN ('queued', 'running')
	`, memoryProjectID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// R10.4: a deleted source takes the map of the project with it in its own transaction; the page says the map is coming and the
// next Job builds it from what is left.
func TestDeletingASourceRemovesTheMap(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_map_gone")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	repository := siteRepository(db)
	build := mapCardsJob(t, repository, &namesCardModel{})
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	deleteChapterForTest(t, db, memoryChapters[2])
	if graphs, nodes, names := mapRowsForTest(t, db); graphs != 0 || nodes != 0 || names != 0 {
		t.Fatalf("after the deletion: %d graphs, %d nodes, %d names", graphs, nodes, names)
	}
	graph, nodes := mapUseCases(repository)
	result, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope})
	if err != nil {
		t.Fatal(err)
	}
	if document := decodedMapRow(t, result); !document.Building || result.Revision != "" || document.Coverage.Chapters != 2 || len(document.Nodes) != 0 {
		t.Fatalf("after the deletion = %+v (%q)", document, result.Revision)
	}
	if _, err := nodes.Execute(ctx, reference.MapNodeInput{Scope: mapScope, Key: "Мирон"}); !errors.Is(err, reference.ErrNotFound) {
		t.Fatalf("card after the deletion = %v, want ErrNotFound", err)
	}
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	result, err = graph.Execute(ctx, reference.MapInput{Scope: mapScope})
	if err != nil {
		t.Fatal(err)
	}
	if document := decodedMapRow(t, result); document.Building || len(document.Chapters) != 2 {
		t.Fatalf("after the next Job = %+v", document)
	}
}

// R10.4: a chapter gone while the long build runs leaves no row of it, the Job ends well and the failure is logged. A build under a
// lost claim writes nothing.
func TestSaveMapRefusesALostClaimAndAGoneText(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_map_fence")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	var stored []byte
	if err := db.Pool.QueryRow(ctx, `SELECT chapter_content_hash FROM chapter_text_index_state WHERE chapter_id = $1`, memoryChapters[0].id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	rows := service.StoredMapRows{
		Keys: []service.CardKey{{ChapterID: memoryChapters[0].id, ContentHash: hex.EncodeToString(stored)}},
		View: "draft", Revision: "r", Document: []byte{1}, Chapters: []byte{1},
		Nodes: []service.StoredMapRow{{Key: "мирон", Name: "Мирон", SearchText: "мирон", Card: []byte{1}, Parts: []service.StoredMapPart{{Body: []byte{1}}}}}, Names: map[string]string{"мирон": "мирон"},
	}
	claim := claimCardsJobForTest(t, db)
	lost := claim
	lost.ClaimIncarnation++
	if err := repository.SaveMap(ctx, lost, rows); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("SaveMap under a lost claim = %v", err)
	}
	gone := rows
	gone.Keys = []service.CardKey{{ChapterID: memoryChapters[0].id, ContentHash: strings.Repeat("0a", 32)}}
	if err := repository.SaveMap(ctx, claim, gone); !errors.Is(err, domain.ErrTextChanged) {
		t.Fatalf("SaveMap with a gone text = %v", err)
	}
	if graphs, _, _ := mapRowsForTest(t, db); graphs != 0 {
		t.Fatalf("%d graphs after refused saves", graphs)
	}
	if err := repository.SaveMap(ctx, claim, rows); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveMap(ctx, claim, rows); err != nil {
		t.Fatalf("a save over a save = %v", err)
	}
	if graphs, nodes, names := mapRowsForTest(t, db); graphs != 1 || nodes != 1 || names != 1 {
		t.Fatalf("%d graphs, %d nodes, %d names", graphs, nodes, names)
	}
}

// R10.6: the search of the map answers from its rows as the glossary answers from the cards, in its order: by a name, an alias, a role
// or a note, with no card of a chapter left to read. A row of an earlier build, before its text, matches nothing.
func TestSearchMapFindsWhatTheGlossaryFinds(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_map_search")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	repository := New(db)
	if status, err := mapCardsJob(t, repository, &namesCardModel{}).RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	glossary := reference.NewListGlossary(anyReader{}, service.NewSaga(repository, repository))
	search := reference.NewSearchMap(anyReader{}, repository)
	for _, query := range []string{"мирон", "ВОРОН", "первый король", "у", "нет такого"} {
		page, err := glossary.Execute(ctx, reference.GlossaryInput{Scope: mapScope, Query: query, Limit: 8})
		if err != nil {
			t.Fatal(err)
		}
		found, err := search.Execute(ctx, reference.MapSearchInput{Scope: mapScope, Query: query})
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != len(page.Entries) {
			t.Fatalf("%q: %d found, the glossary finds %d", query, len(found), len(page.Entries))
		}
		for i, item := range page.Entries {
			if found[i].Name != item.Entry.Name || found[i].Type != item.Entry.Type || found[i].Key != domain.GlossaryKey(item.Entry.Name) {
				t.Fatalf("%q, place %d: %+v, the glossary has %+v", query, i, found[i], item.Entry)
			}
		}
	}
	if found, err := search.Execute(ctx, reference.MapSearchInput{Scope: mapScope, Query: "первый король"}); err != nil || len(found) != 1 || found[0].Name != "Мирон" {
		t.Fatalf("by a role = %+v, %v", found, err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM chapter_cards WHERE project_id = $1`, memoryProjectID); err != nil {
		t.Fatal(err)
	}
	if found, err := search.Execute(ctx, reference.MapSearchInput{Scope: mapScope, Query: "мирон"}); err != nil || len(found) != 1 {
		t.Fatalf("without the cards = %+v, %v", found, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE saga_map_nodes SET search_text = '' WHERE project_id = $1`, memoryProjectID); err != nil {
		t.Fatal(err)
	}
	if found, err := search.Execute(ctx, reference.MapSearchInput{Scope: mapScope, Query: "мирон"}); err != nil || found == nil || len(found) != 0 {
		t.Fatalf("a row without its text = %+v, %v", found, err)
	}
}

// R9.5, R10.3: a record of the author changes the stamp of the map: the map goes out marked and asks for a Job, whose last step, without a
// call of the model, puts the record on the rows: its own node for a name the book does not know, the card of the book for one it knows.
func TestARecordOfTheAuthorLeavesTheMapBehindUntilItsNextBuild(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_map_materials")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	ctx := context.Background()
	repository := siteRepository(db)
	saga := service.NewSaga(repository, repository)
	model := &namesCardModel{}
	failed := func(_, stage string, err error) { t.Errorf("the step %s of the Job failed: %v", stage, err) }
	build := service.NewBuildCards(repository, repository, model, nil, "test-model", 2, failed, nil).
		WithMap(service.NewMapBuilder(saga, repository, authormaterialsrepo.New(db, jobstore.New(db))), failed)
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	cards := model.count()
	graph, nodes := mapUseCases(repository)
	fresh, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []struct{ name, kind, description string }{
		{"Ворон", "character", "Запись автора о Мироне под его именем."},
		{"Миг назад", "place", "Там бывал Мирон."},
		{"Омут снов", "skill", "Пугает Мирона."},
	} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO author_materials (project_id, name, name_key, kind, description, source_kind, created_by, updated_by) VALUES ($1, $2, lower($2), $3, $4, 'text', $5, $5)`,
			memoryProjectID, record.name, record.kind, record.description, memoryOwnerID); err != nil {
			t.Fatal(err)
		}
	}
	behind, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope, Known: []string{fresh.Revision}})
	if err != nil {
		t.Fatal(err)
	}
	if document := decodedMapRow(t, behind); !document.Building || behind.NotModified {
		t.Fatalf("after a new record = %+v", document)
	}
	if count := cardsJobsForTest(t, db); count != 1 {
		t.Fatalf("%d Jobs waiting, want one", count)
	}
	if status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("run = %q, %v", status, err)
	}
	if model.count() != cards {
		t.Fatalf("%d cards built by a change of a record", model.count()-cards)
	}
	rebuilt, err := graph.Execute(ctx, reference.MapInput{Scope: mapScope})
	if err != nil {
		t.Fatal(err)
	}
	document := decodedMapRow(t, rebuilt)
	if document.Building || rebuilt.Revision == fresh.Revision {
		t.Fatalf("rebuilt = %+v", document)
	}
	drawn := map[string]bool{}
	for _, node := range document.Nodes {
		drawn[node.Name] = node.Material
	}
	if material, ok := drawn["Миг назад"]; !ok || !material || drawn["Мирон"] {
		t.Fatalf("nodes = %v", drawn)
	}
	if _, ok := drawn["Омут снов"]; ok {
		t.Fatalf("a skill is drawn: %v", drawn)
	}
	card := func(key, to string) domain.MapCardDocument {
		t.Helper()
		found, err := nodes.Execute(ctx, reference.MapNodeInput{Scope: mapScope, Key: key, To: to})
		if err != nil {
			t.Fatalf("%q: %v", key, err)
		}
		var read domain.MapCardDocument
		if err := domain.DecodeStored(found.Document, &read); err != nil {
			t.Fatal(err)
		}
		return read
	}
	hero := card("Мирон", document.Chapters[0].Key)
	if hero.Material == nil || hero.Material.Name != "Ворон" || hero.Material.Description == "" || len(hero.NamedIn) != 2 {
		t.Fatalf("the card of the book = %+v", hero)
	}
	if skill := card("омут снов", ""); skill.Material == nil || skill.Category != nil || skill.Mentions != 0 {
		t.Fatalf("the card of a skill = %+v", skill)
	}
	found, err := reference.NewSearchMap(anyReader{}, repository).Execute(ctx, reference.MapSearchInput{Scope: mapScope, Query: "омут"})
	if err != nil || len(found) != 1 || !found[0].Material {
		t.Fatalf("a search for a skill = %+v, %v", found, err)
	}
}

// R9.4: the review of an import asks the map of the book by key. A record the book does not know has a node too, and is no name of the book.
func TestBookNamesAreTheNamesOfTheBook(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_map_book_names")
	seedMemoryProject(t, db)
	ctx := context.Background()
	repository := New(db)
	var stored []byte
	if err := db.Pool.QueryRow(ctx, `SELECT chapter_content_hash FROM chapter_text_index_state WHERE chapter_id = $1`, memoryChapters[0].id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	node := func(key, name, kind string) service.StoredMapRow {
		return service.StoredMapRow{Key: key, Name: name, Type: kind, SearchText: key, Card: []byte{1}, Parts: []service.StoredMapPart{{Body: []byte{1}}}}
	}
	if names, err := repository.BookNames(ctx, memoryProjectID, []string{"мирон"}); err != nil || len(names) != 0 {
		t.Fatalf("without a map = %v, %v", names, err)
	}
	if err := repository.SaveMap(ctx, claimCardsJobForTest(t, db), service.StoredMapRows{
		Keys: []service.CardKey{{ChapterID: memoryChapters[0].id, ContentHash: hex.EncodeToString(stored)}},
		View: "published", Revision: "r", Document: []byte{1}, Chapters: []byte{1},
		Nodes: []service.StoredMapRow{node("мирон", "Мирон", "персонаж"), node("омут снов", "Омут снов", domain.MaterialEntry("x").Type)},
		Names: map[string]string{"мирон": "мирон", "ворон": "мирон", "омут снов": "омут снов"},
	}); err != nil {
		t.Fatal(err)
	}
	names, err := repository.BookNames(ctx, memoryProjectID, []string{"ворон", "омут снов", "нет такого"})
	if err != nil || len(names) != 1 || names["ворон"] != "Мирон" {
		t.Fatalf("names = %v, %v", names, err)
	}
}
