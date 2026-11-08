package sagareference

import (
	"canonkeeper/backend/internal/domain/authormaterials"
	"context"
	"errors"
	"slices"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

// countingCards counts the reads of the cards of a saga.
type countingCards struct {
	*sagamemorytest.Text
	reads int
}

func (cards *countingCards) Cards(ctx context.Context, projectID string, keys []service.CardKey, parts sagamemory.CardParts) (map[service.CardKey]sagamemory.Card, error) {
	if parts.Summary || parts.Mentions || parts.Events || parts.Changes {
		cards.reads++
	}
	return cards.Text.Cards(ctx, projectID, keys, parts)
}

// mapText is three chapters: Мила and Сава run together in chapters 1 and 3, Сава alone beside her in 2.
func mapText() *sagamemorytest.Text {
	version := sagamemory.NewVersion("project", "draft", "fp", []sagamemory.Chapter{
		{ID: "c1", BookID: "b1", BookOrder: 1, BookTitle: "Том 1", Order: 1, Title: "Глава 1. Порт", Paragraphs: []sagamemory.Paragraph{{Text: "Мила получила кинжал и прячет Саву."}}},
		{ID: "c2", BookID: "b1", BookOrder: 1, BookTitle: "Том 1", Order: 2, Title: "Глава 2. Дым", Paragraphs: []sagamemory.Paragraph{{Text: "Сава."}}},
		{ID: "c3", BookID: "b2", BookOrder: 2, BookTitle: "Том 2", Order: 1, Title: "Глава 3. Суд", Paragraphs: []sagamemory.Paragraph{{Text: "Мила."}}},
	})
	stems := map[string]string{"мила": "мил", "милы": "мил", "саву": "сав", "сава": "сав", "кинжал": "кинжал", "прячет": "пряч", "получила": "получ"}
	return sagamemorytest.NewText(version, stems, map[int]*sagamemory.Card{
		0: {Summary: "Мила приходит в порт.",
			Characters: []sagamemory.Mention{{Name: "Мила", Aliases: []string{"Лиса"}, Note: "воровка", P: 2}, {Name: "Сава", P: 3}},
			Entities:   []sagamemory.Mention{{Name: "Кинжал", Type: "оружие", P: 4}},
			Events:     []sagamemory.Event{{Event: "Лиса и Сава бегут из порта", Who: []string{"Лиса", "Сава"}, P: 3, Key: true}},
			Changes:    []sagamemory.Change{{Subject: "Мила", Change: "Получает кинжал."}, {Subject: "Мила", Change: "Прячет Саву."}}},
		1: {Summary: "Сава замечает дым.",
			Characters: []sagamemory.Mention{{Name: "Сава", P: 1}, {Name: "Мила", P: 4}},
			Events:     []sagamemory.Event{{Event: "Сава видит дым", Who: []string{"Сава"}}}},
		2: {Summary: "Суд над Милой.",
			Characters: []sagamemory.Mention{{Name: "Мила", P: 1}, {Name: "Сава", P: 2}},
			Events:     []sagamemory.Event{{Event: "Милу и Саву судят", Who: []string{"Мила", "Сава"}, P: 2}}},
	})
}

// mapSaga is the saga with a prepared map, built by the same builder as the Job.
func mapSaga(t *testing.T) (*countingCards, *service.Saga, *sagamemorytest.Maps, func()) {
	t.Helper()
	cards := &countingCards{Text: mapText()}
	saga := service.NewSaga(cards, cards)
	maps := sagamemorytest.NewMaps()
	builder := service.NewMapBuilder(saga, maps, sagamemorytest.Materials(nil))
	build := func() {
		t.Helper()
		if stage, err := builder.Refresh(context.Background(), jobs.WorkerClaim{ProjectID: "project"}, "draft"); err != nil {
			t.Fatalf("build at %s: %v", stage, err)
		}
	}
	build()
	cards.reads = 0
	return cards, saga, maps, build
}

func decodedMap(t *testing.T, result MapResult) sagamemory.MapDocument {
	t.Helper()
	var document sagamemory.MapDocument
	if err := sagamemory.DecodeStored(result.Document, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

// specs/2026-11-04-saga-map.md R10.1, R1.5: the stored graph is served without reading a card, and a browser holding it gets «not modified».
func TestGetMapServesThePreparedGraphAndAnswersNotModified(t *testing.T) {
	cards, saga, maps, _ := mapSaga(t)
	useCase := NewGetMap(fakeAccess{}, saga, maps, maps)
	result, err := useCase.Execute(context.Background(), MapInput{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	document := decodedMap(t, result)
	if result.NotModified || len(document.Nodes) != 3 || document.Hero == nil || *document.Hero != "мила" || len(document.Chapters) != 3 || document.Building {
		t.Fatalf("map = %+v", document)
	}
	if len(document.Links) != 2 || len(document.Links[0].Events) != 2 || len(document.Links[1].Changes) != 1 || document.Coverage.WithCards != 3 {
		t.Fatalf("links = %+v coverage %+v", document.Links, document.Coverage)
	}
	if len(result.Revision) != 32 || document.Revision != result.Revision || cards.reads != 0 || maps.Queued != 0 {
		t.Fatalf("revision %q (%q), card reads %d, queued %d", result.Revision, document.Revision, cards.reads, maps.Queued)
	}
	again, err := useCase.Execute(context.Background(), MapInput{Scope: scope, Known: []string{"other", result.Revision}})
	if err != nil || !again.NotModified || again.Revision != result.Revision || again.Document != nil {
		t.Fatalf("again = %+v, %v", again, err)
	}
	if _, err := NewGetMap(fakeAccess{err: ErrDenied}, saga, maps, maps).Execute(context.Background(), MapInput{Scope: scope}); !errors.Is(err, ErrDenied) {
		t.Fatalf("err = %v, want ErrDenied", err)
	}
}

// R10.3: a map behind its cards still answers, marked as being rebuilt, with a tag of its own: a browser holding the map from
// before learns of it, a page asking again waits without the document, and the new build ends the mark.
func TestGetMapBehindItsCardsAsksForABuild(t *testing.T) {
	cards, saga, maps, build := mapSaga(t)
	useCase := NewGetMap(fakeAccess{}, saga, maps, maps)
	fresh, err := useCase.Execute(context.Background(), MapInput{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	cards.RewriteCards()
	behind, err := useCase.Execute(context.Background(), MapInput{Scope: scope, Known: []string{fresh.Revision}})
	if err != nil {
		t.Fatal(err)
	}
	if document := decodedMap(t, behind); behind.NotModified || !document.Building || len(document.Nodes) != 3 || maps.Queued != 1 {
		t.Fatalf("behind = %+v, queued %d", document, maps.Queued)
	}
	if behind.Revision != fresh.Revision+buildingTag {
		t.Fatalf("tag = %q", behind.Revision)
	}
	waiting, err := useCase.Execute(context.Background(), MapInput{Scope: scope, Known: []string{behind.Revision}})
	if err != nil || !waiting.NotModified || waiting.Revision != behind.Revision || maps.Queued != 2 {
		t.Fatalf("asking again = %+v, %v, queued %d", waiting, err, maps.Queued)
	}
	build()
	rebuilt, err := useCase.Execute(context.Background(), MapInput{Scope: scope, Known: []string{behind.Revision}})
	if err != nil {
		t.Fatal(err)
	}
	if document := decodedMap(t, rebuilt); rebuilt.NotModified || document.Building || rebuilt.Revision == fresh.Revision {
		t.Fatalf("rebuilt = %+v (%q)", document, rebuilt.Revision)
	}
	cards.SetNames(&sagamemory.NameRegistry{})
	if named, err := useCase.Execute(context.Background(), MapInput{Scope: scope, Known: []string{rebuilt.Revision}}); err != nil || named.NotModified || !decodedMap(t, named).Building {
		t.Fatalf("a new name registry leaves the map behind: %+v, %v", named, err)
	}
}

// R10.3: a saga with cards and no map asks for it and says so; a saga without cards asks for nothing.
func TestGetMapWithoutARow(t *testing.T) {
	_, saga, maps, _ := mapSaga(t)
	maps.Forget("project", "draft")
	result, err := NewGetMap(fakeAccess{}, saga, maps, maps).Execute(context.Background(), MapInput{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	document := decodedMap(t, result)
	if result.Revision != "" || result.NotModified || !document.Building || document.Coverage.WithCards != 3 || document.Nodes == nil || maps.Queued != 1 {
		t.Fatalf("no row = %+v (%+v), queued %d", result, document, maps.Queued)
	}

	bare := sagamemorytest.NewText(sagamemory.NewVersion("project", "draft", "fp", []sagamemory.Chapter{
		{ID: "c1", BookID: "b1", BookOrder: 1, BookTitle: "Том 1", Order: 1, Title: "Глава 1", Paragraphs: []sagamemory.Paragraph{{Text: "Мила."}}},
	}), nil, map[int]*sagamemory.Card{})
	empty := sagamemorytest.NewMaps()
	result, err = NewGetMap(fakeAccess{}, service.NewSaga(bare, bare), empty, empty).Execute(context.Background(), MapInput{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if document := decodedMap(t, result); document.Building || document.Coverage.WithCards != 0 || document.Coverage.Chapters != 1 || empty.Queued != 0 || empty.Reads != 0 {
		t.Fatalf("no cards = %+v, queued %d", document, empty.Queued)
	}
}

// UC2, R10.2: the card of a node comes from its rows, read by key; no card of a chapter is read.
func TestGetMapNodeUpToAChapter(t *testing.T) {
	cards, _, maps, _ := mapSaga(t)
	nodes := NewGetMapNode(fakeAccess{}, maps)
	card := func(key, to string) sagamemory.MapCardDocument {
		t.Helper()
		reads := maps.Reads
		result, err := nodes.Execute(context.Background(), MapNodeInput{Scope: scope, Key: key, To: to})
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if to != "" {
			want = 2 // The chapters of the map, then the part of the node holding the chapter.
		}
		if maps.Reads != reads+want {
			t.Fatalf("%d reads of the rows, want %d", maps.Reads-reads, want)
		}
		var document sagamemory.MapCardDocument
		if err := sagamemory.DecodeStored(result.Document, &document); err != nil {
			t.Fatal(err)
		}
		return document
	}
	whole := card("лиса", "")
	if whole.Name != "Мила" || whole.Category == nil || *whole.Category != "character" || whole.Mentions != 3 || whole.First == nil || whole.First.Paragraph != 2 {
		t.Fatalf("Мила = %+v", whole)
	}
	if whole.EventsTotal != 2 || whole.Events[0].Chapter.Key != "1" || !whole.Events[0].Key {
		t.Fatalf("events = %+v", whole.Events)
	}
	if len(whole.Links) != 2 || whole.Links[0].Name != "Сава" || whole.Links[0].Together != 2 || whole.Links[0].Nearby != 1 || whole.Links[1].Name != "Кинжал" || whole.Links[1].Together != 0 {
		t.Fatalf("links = %+v", whole.Links)
	}
	if len(whole.Links[0].Changes) != 1 || whole.Links[0].Changes[0].Change != "Прячет Саву." {
		t.Fatalf("changes of the pair = %+v", whole.Links[0].Changes)
	}
	if early := card("Мила", "2"); early.Mentions != 2 || early.EventsTotal != 1 || early.Links[0].Together != 1 || early.Links[0].Nearby != 1 {
		t.Fatalf("Мила to chapter 2 = %+v", early)
	}
	if knife := card("кинжал", ""); knife.Category == nil || *knife.Category != "item" || len(knife.Links) != 1 || knife.Links[0].Name != "Мила" || knife.ChangesTotal != 1 {
		t.Fatalf("a change links a thing of one chapter to the one it names: %+v", knife)
	}
	if cards.reads != 0 {
		t.Fatalf("%d reads of the cards", cards.reads)
	}
	if _, err := nodes.Execute(context.Background(), MapNodeInput{Scope: scope, Key: "нет такого"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := nodes.Execute(context.Background(), MapNodeInput{Scope: scope, Key: "нет такого", To: "2"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := nodes.Execute(context.Background(), MapNodeInput{Scope: scope, Key: "Мила", To: "99"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if _, err := NewGetMapNode(fakeAccess{err: ErrNotFound}, maps).Execute(context.Background(), MapNodeInput{Scope: scope, Key: "Мила"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// R10.6: a search reads the prepared rows once and finds what the glossary finds, by a name, an alias or a note, most mentioned first.
func TestSearchMapReadsThePreparedRows(t *testing.T) {
	cards, _, maps, _ := mapSaga(t)
	search := NewSearchMap(fakeAccess{}, maps)
	names := func(query string, limit int) []string {
		t.Helper()
		reads := maps.Reads
		found, err := search.Execute(context.Background(), MapSearchInput{Scope: scope, Query: query, Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		if maps.Reads != reads+1 {
			t.Fatalf("%d reads of the rows, want one", maps.Reads-reads)
		}
		got := []string{}
		for _, entry := range found {
			got = append(got, entry.Name)
		}
		return got
	}
	if got := names("ЛИСА", 0); !slices.Equal(got, []string{"Мила"}) {
		t.Fatalf("by an alias = %v", got)
	}
	if got := names("воровка", 0); !slices.Equal(got, []string{"Мила"}) {
		t.Fatalf("by a note = %v", got)
	}
	if got := names("а", 0); !slices.Equal(got, []string{"Мила", "Сава", "Кинжал"}) {
		t.Fatalf("most mentioned first = %v", got)
	}
	if got := names("а", 1); !slices.Equal(got, []string{"Мила"}) {
		t.Fatalf("limited = %v", got)
	}
	if cards.reads != 0 {
		t.Fatalf("%d reads of the cards", cards.reads)
	}
	reads := maps.Reads
	if found, err := search.Execute(context.Background(), MapSearchInput{Scope: scope, Query: "« »"}); err != nil || found == nil || len(found) != 0 || maps.Reads != reads {
		t.Fatalf("a query of no letters = %v, %v", found, err)
	}
	if _, err := search.Execute(context.Background(), MapSearchInput{Scope: scope, Query: "а", Limit: maxMapSearchLimit + 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if _, err := NewSearchMap(fakeAccess{err: ErrDenied}, maps).Execute(context.Background(), MapSearchInput{Scope: scope, Query: "а"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("err = %v, want ErrDenied", err)
	}
}

// specs/2026-11-04-saga-map.md R9.5: a record the book does not know is a node with a card of its own; a record of a name the book knows
// is the block of the book's card; a skill is read through the card it names and found by a search. A changed record leaves the map
// behind its revision until the next build.
func TestMapAndCardsTakeTheMaterials(t *testing.T) {
	text := mapText()
	saga := service.NewSaga(text, text)
	maps := sagamemorytest.NewMaps()
	materials := sagamemorytest.Materials{
		{ID: "a", Draft: authormaterials.Draft{Name: "Белый порт", Kind: authormaterials.KindPlace, Description: "Здесь живёт Мила."}},
		{ID: "b", Draft: authormaterials.Draft{Name: "омут снов", Kind: authormaterials.KindSkill, Description: "Пугает Милу."}},
		{ID: "c", Draft: authormaterials.Draft{Name: "Сава", Kind: authormaterials.KindCharacter, Description: "По материалам автора.", Properties: []authormaterials.Property{{Key: "Возраст", Value: "двадцать"}}}},
	}
	if stage, err := service.NewMapBuilder(saga, maps, materials).Refresh(context.Background(), jobs.WorkerClaim{ProjectID: "project"}, "draft"); err != nil {
		t.Fatalf("build at %s: %v", stage, err)
	}
	result, err := NewGetMap(fakeAccess{}, saga, maps, maps).Execute(context.Background(), MapInput{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	document := decodedMap(t, result)
	last := document.Nodes[len(document.Nodes)-1]
	if len(document.Nodes) != 4 || !last.Material || last.Name != "Белый порт" || last.Category != "place" || len(last.Chapters) != 0 || last.Chapters == nil {
		t.Fatalf("nodes = %+v", document.Nodes)
	}
	if len(document.Links) != 3 || !document.Links[2].Material || document.Links[2].Events == nil {
		t.Fatalf("links = %+v", document.Links)
	}

	cards := NewGetMapNode(fakeAccess{}, maps)
	card := func(key, to string) sagamemory.MapCardDocument {
		t.Helper()
		found, err := cards.Execute(context.Background(), MapNodeInput{Scope: scope, Key: key, To: to})
		if err != nil {
			t.Fatalf("%q: %v", key, err)
		}
		var document sagamemory.MapCardDocument
		if err := sagamemory.DecodeStored(found.Document, &document); err != nil {
			t.Fatal(err)
		}
		return document
	}
	port := card("белый порт", "")
	if port.Material == nil || port.Material.ID != "a" || port.Category == nil || *port.Category != "place" || port.Mentions != 0 || port.NamedIn == nil {
		t.Fatalf("the card of a record = %+v", port)
	}
	if len(port.Links) != 1 || !port.Links[0].FromMaterials || port.Links[0].Name != "Мила" {
		t.Fatalf("its links = %+v", port.Links)
	}
	nightmare := card("ОМУТ снов", "")
	if nightmare.Material == nil || nightmare.Category != nil || len(nightmare.Links) != 0 {
		t.Fatalf("a skill has a card without a category: %+v", nightmare)
	}
	mila := card("Мила", "")
	if mila.Material != nil || len(mila.NamedIn) != 2 || mila.NamedIn[0].ID != "a" || mila.NamedIn[1].ID != "b" {
		t.Fatalf("Мила is named in two records: %+v", mila)
	}
	if len(mila.Links) != 3 || mila.Links[0].FromMaterials || !mila.Links[2].FromMaterials {
		t.Fatalf("the links of the book come first: %+v", mila.Links)
	}
	sava := card("Сава", "")
	if sava.Material == nil || sava.Material.ID != "c" || sava.Mentions != 3 || sava.Material.Properties[0].Value != "двадцать" {
		t.Fatalf("a record of a name the book knows is on the book's card: %+v", sava)
	}
	if early := card("Мила", "1"); !early.Links[len(early.Links)-1].FromMaterials || len(early.NamedIn) != 2 {
		t.Fatalf("a record stands at any chapter: %+v", early)
	}

	search := NewSearchMap(fakeAccess{}, maps)
	find := func(query string) []sagamemory.MapSearchEntry {
		t.Helper()
		found, err := search.Execute(context.Background(), MapSearchInput{Scope: scope, Query: query})
		if err != nil {
			t.Fatal(err)
		}
		return found
	}
	if found := find("омут"); len(found) != 1 || found[0].Name != "омут снов" || !found[0].Material {
		t.Fatalf("a skill is found: %+v", found)
	}
	if found := find("двадцать"); len(found) != 1 || found[0].Name != "Сава" || found[0].Material {
		t.Fatalf("by what a record says, on the book's entry: %+v", found)
	}
	if found := find(""); len(found) == 0 || found[0].Name != "Мила" {
		t.Fatalf("an empty query gives the most mentioned: %+v", found)
	}
}

// R11.3: what the author took off the map leaves the graph and the cards when they are read, at once; a browser holding the map
// learns of a new decision by the revision, and the rows of the map stay as they were.
func TestWhatTheAuthorHidIsTakenOutWhenTheMapIsRead(t *testing.T) {
	_, saga, maps, _ := mapSaga(t)
	useCase := NewGetMap(fakeAccess{}, saga, maps, maps)
	before, err := useCase.Execute(context.Background(), MapInput{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	maps.Hidden = []sagamemory.MapHiddenItem{{ID: "1", NodeKey: "кинжал", NodeName: "Кинжал"}}
	after, err := useCase.Execute(context.Background(), MapInput{Scope: scope, Known: []string{before.Revision}})
	if err != nil {
		t.Fatal(err)
	}
	document := decodedMap(t, after)
	if after.NotModified || after.Revision == before.Revision || len(document.Nodes) != 2 || len(document.Links) != 1 {
		t.Fatalf("after a decision = %+v, document %+v", after, document)
	}
	if again, err := useCase.Execute(context.Background(), MapInput{Scope: scope, Known: []string{after.Revision}}); err != nil || !again.NotModified {
		t.Fatalf("again = %+v, %v", again, err)
	}
	maps.Hidden = nil
	if back, err := useCase.Execute(context.Background(), MapInput{Scope: scope, Known: []string{after.Revision}}); err != nil || back.NotModified || back.Revision != before.Revision || len(decodedMap(t, back).Nodes) != 3 {
		t.Fatalf("restored = %+v, %v", back, err)
	}

	maps.Hidden = []sagamemory.MapHiddenItem{{ID: "2", NodeKey: "кинжал", NodeName: "Кинжал"}, {ID: "3", NodeKey: "мила", OtherKey: "сава", NodeName: "Мила", OtherName: "Сава"}}
	cards := NewGetMapNode(fakeAccess{}, maps)
	card := func(key, to string) sagamemory.MapCardDocument {
		t.Helper()
		found, err := cards.Execute(context.Background(), MapNodeInput{Scope: scope, Key: key, To: to})
		if err != nil {
			t.Fatalf("%q: %v", key, err)
		}
		var document sagamemory.MapCardDocument
		if err := sagamemory.DecodeStored(found.Document, &document); err != nil {
			t.Fatal(err)
		}
		return document
	}
	if whole := card("Мила", ""); len(whole.Links) != 0 || whole.Mentions != 3 || whole.EventsTotal != 2 || whole.HiddenID != "" {
		t.Fatalf("the card of the whole saga = %+v", whole)
	}
	if early := card("Мила", "2"); len(early.Links) != 0 || early.Mentions != 2 {
		t.Fatalf("the card at a chapter = %+v", early)
	}
	if knife := card("кинжал", ""); knife.HiddenID != "2" || knife.Name != "Кинжал" {
		t.Fatalf("the card of a hidden node = %+v", knife)
	}
}

type hiddenStore struct {
	item    sagamemory.MapHiddenItem
	removed string
}

func (store *hiddenStore) HideMapItem(_ context.Context, _ MapWrite, item sagamemory.MapHiddenItem) (sagamemory.MapHiddenItem, error) {
	store.item = item
	item.ID = "kept"
	return item, nil
}

func (store *hiddenStore) RestoreMapItem(_ context.Context, _ MapWrite, id string) error {
	store.removed = id
	return nil
}

// R11.3: a decision names what the saga knows, by the names of its rows; a link has one order of its ends.
func TestHideOnMapNamesWhatTheSagaKnows(t *testing.T) {
	_, _, maps, _ := mapSaga(t)
	store := &hiddenStore{}
	useCase := NewHideOnMap(fakeAccess{}, maps, store)
	write := MapWrite{Scope: scope, Proof: auth.SessionWriteProof{SessionHash: "session", CSRFSecretHash: "csrf"}}
	item, err := useCase.Execute(context.Background(), write, HideInput{NodeKey: "лиса"})
	if err != nil || item.ID != "kept" || item.NodeKey != "мила" || item.NodeName != "Мила" || item.OtherKey != "" {
		t.Fatalf("node = %+v, %v", item, err)
	}
	if _, err := useCase.Execute(context.Background(), write, HideInput{NodeKey: "сава", OtherKey: "Лиса"}); err != nil || store.item.NodeKey != "мила" || store.item.OtherKey != "сава" || store.item.OtherName != "Сава" {
		t.Fatalf("link = %+v, %v", store.item, err)
	}
	for name, input := range map[string]HideInput{
		"no node": {}, "unknown": {NodeKey: "никто"}, "unknown other": {NodeKey: "мила", OtherKey: "никто"}, "same": {NodeKey: "лиса", OtherKey: "Мила"},
	} {
		if _, err := useCase.Execute(context.Background(), write, input); err == nil {
			t.Fatalf("%s: a decision is kept", name)
		}
	}
	if _, err := useCase.Execute(context.Background(), MapWrite{Scope: scope}, HideInput{NodeKey: "мила"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("without the write proof: %v", err)
	}
	if _, err := NewHideOnMap(fakeAccess{err: ErrDenied}, maps, store).Execute(context.Background(), write, HideInput{NodeKey: "мила"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("a reader who may not read: %v", err)
	}
	if err := NewRestoreOnMap(store).Execute(context.Background(), write, " id "); err != nil || store.removed != "id" {
		t.Fatalf("restore = %q, %v", store.removed, err)
	}
}
