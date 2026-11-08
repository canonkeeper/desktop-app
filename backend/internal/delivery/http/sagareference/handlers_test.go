package sagareference

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
	service "canonkeeper/backend/internal/services/sagareference"

	"github.com/go-chi/chi/v5"
)

type user struct{ err error }

func (current user) CurrentUser(context.Context, string) (auth.User, error) {
	return auth.User{ID: "actor"}, current.err
}

func (current user) AuthenticateWrite(_ context.Context, _, csrf string) (auth.User, auth.SessionWriteProof, error) {
	if csrf == "" {
		return auth.User{}, auth.SessionWriteProof{}, auth.ErrUnauthenticated
	}
	return auth.User{ID: "actor"}, auth.SessionWriteProof{SessionHash: "session", CSRFSecretHash: "csrf"}, current.err
}

// hiddenStore keeps the decisions of the author in memory, in the maps the reads see.
type hiddenStore struct{ maps *sagamemorytest.Maps }

func (store hiddenStore) HideMapItem(_ context.Context, _ service.MapWrite, item sagamemory.MapHiddenItem) (sagamemory.MapHiddenItem, error) {
	item.ID = "hidden-1"
	item.CreatedAt = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	store.maps.Hidden = append(store.maps.Hidden, item)
	return item, nil
}

func (store hiddenStore) RestoreMapItem(_ context.Context, _ service.MapWrite, id string) error {
	if len(store.maps.Hidden) == 0 || store.maps.Hidden[0].ID != id {
		return service.ErrNotFound
	}
	store.maps.Hidden = nil
	return nil
}

type access struct{ err error }

func (check access) ReadableView(context.Context, string, string, string) error { return check.err }

// memory is a published version of two chapters; missing lists chapters without search rows.
func memory(missing ...int) (*sagamemorytest.Text, *sagamemoryservice.Saga) {
	version := sagamemory.NewVersion("project", "published", "fp", []sagamemory.Chapter{
		{ID: "c1", BookID: "b1", BookOrder: 1, BookTitle: "Том 1", Order: 1, Title: "Глава 1. Порт", Paragraphs: []sagamemory.Paragraph{{Text: "Мила."}}},
		{ID: "c2", BookID: "b1", BookOrder: 1, BookTitle: "Том 1", Order: 2, Title: "Глава 2. Дым", Paragraphs: []sagamemory.Paragraph{{Text: "Дым."}}},
	})
	text := sagamemorytest.NewText(version, map[string]string{"мила": "мил"}, map[int]*sagamemory.Card{
		0: {Summary: "Мила в порту.", Characters: []sagamemory.Mention{{Name: "Мила", P: 1}, {Name: "Сава", P: 2}},
			Events: []sagamemory.Event{{Event: "Мила и Сава входят в порт", Who: []string{"Мила", "Сава"}, P: 1, Key: true}, {Event: "Мила и Сава ищут корабль", Who: []string{"Мила", "Сава"}, P: 2}}},
	}, missing...)
	return text, sagamemoryservice.NewSaga(text, text)
}

// memoryWhole is the same text with its search rows, for the build.
func memoryWhole() (*sagamemorytest.Text, *sagamemorytest.Text) {
	text, _ := memory()
	return text, text
}

func router(current user, check access, missing ...int) http.Handler {
	routes := chi.NewRouter()
	_, saga := memory(missing...)
	maps := sagamemorytest.NewMaps()
	// The prepared map is built before the text loses its search rows: a restore leaves the rows of the map where they are.
	if _, err := sagamemoryservice.NewMapBuilder(sagamemoryservice.NewSaga(memoryWhole()), maps, sagamemorytest.Materials(nil)).Refresh(context.Background(), jobs.WorkerClaim{ProjectID: "project"}, "published"); err != nil {
		panic(err)
	}
	NewHandlers(
		service.NewListGlossary(check, saga), service.NewGetMap(check, saga, maps, maps), service.NewGetMapNode(check, maps), service.NewSearchMap(check, maps),
		service.NewGetContents(check, saga),
		MapHiddenUseCases{List: service.NewListMapHidden(check, maps), Hide: service.NewHideOnMap(check, maps, hiddenStore{maps}), Restore: service.NewRestoreOnMap(hiddenStore{maps})},
		current, []string{"http://app.test"},
	).Mount(routes)
	return routes
}

// Until the saga search rows are built (after a restore) the prepared map and its cards keep answering, as do the reads of the cards alone.
func TestSagaReferenceWithoutSearchRows(t *testing.T) {
	handler := router(user{}, access{}, 1)
	for _, target := range []string{
		"/projects/project/saga/glossary?sourceView=published",
		"/projects/project/saga/map?sourceView=published",
		"/projects/project/saga/map/node?sourceView=published&key=%D0%9C%D0%B8%D0%BB%D0%B0",
	} {
		if response, _ := get(t, handler, target); response.Code != http.StatusOK {
			t.Fatalf("%s without search rows = %d, want 200", target, response.Code)
		}
	}
}

func get(t *testing.T, handler http.Handler, target string, headers ...string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	handler.ServeHTTP(response, request)
	var body map[string]any
	if strings.Contains(response.Header().Get("Content-Type"), "json") && response.Header().Get("Content-Encoding") == "" {
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
	}
	return response, body
}

func TestSagaReferenceReadsAnswerTheContractShapes(t *testing.T) {
	handler := router(user{}, access{})
	response, glossary := get(t, handler, "/projects/project/saga/glossary?sourceView=published&limit=10")
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("glossary = %d %v", response.Code, response.Header())
	}
	entries := glossary["entries"].([]any)
	first := entries[0].(map[string]any)
	if first["key"] != "мила" || first["first"].(map[string]any)["key"] != "1" || first["aliases"] == nil {
		t.Fatalf("entry = %v", first)
	}
	_, contents := get(t, handler, "/projects/project/saga/contents?sourceView=published")
	chapters := contents["chapters"].([]any)
	if len(chapters) != 2 || contents["bookId"] != "b1" || chapters[1].(map[string]any)["summary"] != nil {
		t.Fatalf("contents = %v, want the chapter without a card without a summary", contents)
	}
}

// specs/2026-11-04-saga-map.md R1.1, R1.5, R2.1.
func TestSagaMapAnswersTheContractShapesAndNotModified(t *testing.T) {
	handler := router(user{}, access{})
	response, graph := get(t, handler, "/projects/project/saga/map?sourceView=published")
	tag := response.Header().Get("ETag")
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-cache" || len(tag) != 34 {
		t.Fatalf("map = %d %v", response.Code, response.Header())
	}
	nodes, links, chapters := graph["nodes"].([]any), graph["links"].([]any), graph["chapters"].([]any)
	hero := nodes[0].(map[string]any)
	if graph["hero"] != "мила" || hero["category"] != "character" || hero["chapters"].([]any)[0] != 0.0 || hero["aliases"] == nil {
		t.Fatalf("map = %v", graph)
	}
	if len(chapters) != 2 || len(links) != 1 || links[0].(map[string]any)["events"].([]any)[0] != 0.0 || links[0].(map[string]any)["mentions"] == nil {
		t.Fatalf("chapters %v links %v", chapters, links)
	}
	if `"`+graph["revision"].(string)+`"` != tag {
		t.Fatalf("revision %v, ETag %s", graph["revision"], tag)
	}
	compressed, _ := get(t, handler, "/projects/project/saga/map?sourceView=published", "Accept-Encoding", "br, gzip")
	if compressed.Header().Get("Content-Encoding") != "gzip" || compressed.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("gzipped map = %v", compressed.Header())
	}
	reader, err := gzip.NewReader(compressed.Body)
	if err != nil {
		t.Fatal(err)
	}
	var unzipped map[string]any
	if err := json.NewDecoder(reader).Decode(&unzipped); err != nil || unzipped["hero"] != "мила" {
		t.Fatalf("gzipped body = %v, %v", unzipped, err)
	}
	if plain, _ := get(t, handler, "/projects/project/saga/map?sourceView=published", "Accept-Encoding", "gzip;q=0"); plain.Header().Get("Content-Encoding") != "" {
		t.Fatalf("gzip;q=0 = %v", plain.Header())
	}
	again, _ := get(t, handler, "/projects/project/saga/map?sourceView=published", "If-None-Match", `W/"x", `+tag)
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 || again.Header().Get("ETag") != tag || again.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("map again = %d %q %v", again.Code, again.Body.String(), again.Header())
	}

	response, node := get(t, handler, "/projects/project/saga/map/node?sourceView=published&key=%D0%9C%D0%B8%D0%BB%D0%B0&to=1")
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || node["name"] != "Мила" || node["category"] != "character" {
		t.Fatalf("node = %d %v", response.Code, node)
	}
	stored, _ := get(t, handler, "/projects/project/saga/map/node?sourceView=published&key=%D0%9C%D0%B8%D0%BB%D0%B0", "Accept-Encoding", "gzip")
	if stored.Header().Get("Content-Encoding") != "gzip" || stored.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("gzipped card = %v", stored.Header())
	}
	cardReader, err := gzip.NewReader(stored.Body)
	if err != nil {
		t.Fatal(err)
	}
	var card map[string]any
	if err := json.NewDecoder(cardReader).Decode(&card); err != nil || card["name"] != "Мила" {
		t.Fatalf("gzipped card = %v, %v", card, err)
	}
	if node["first"].(map[string]any)["chapter"].(map[string]any)["key"] != "1" || node["mentions"] != 1.0 || node["note"] != nil {
		t.Fatalf("node = %v", node)
	}
	event := node["events"].([]any)[0].(map[string]any)
	if event["event"] != "Мила и Сава входят в порт" || event["key"] != true || event["paragraph"] != 1.0 || node["changes"] == nil {
		t.Fatalf("events = %v", node["events"])
	}
	if link := node["links"].([]any)[0].(map[string]any); link["name"] != "Сава" || link["together"] != 2.0 || link["changes"] == nil || len(link["shared"].([]any)) != 2 {
		t.Fatalf("links = %v", node["links"])
	}
}

// R10.6.
func TestSagaMapSearchAnswersTheContractShape(t *testing.T) {
	handler := router(user{}, access{})
	response, found := get(t, handler, "/projects/project/saga/map/search?sourceView=published&q=%D0%A1%D0%90%D0%92&limit=5")
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("search = %d %v", response.Code, response.Header())
	}
	entries := found["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["key"] != "сава" || entries[0].(map[string]any)["name"] != "Сава" || entries[0].(map[string]any)["type"] != "персонаж" {
		t.Fatalf("entries = %v", entries)
	}
	if _, none := get(t, handler, "/projects/project/saga/map/search?sourceView=published&q=%C2%AB%C2%BB"); none["entries"] == nil || len(none["entries"].([]any)) != 0 {
		t.Fatalf("a query of punctuation = %v, want an empty list", none)
	}
}

func TestSagaReferenceRejectsBadRequestsAndAccess(t *testing.T) {
	for _, check := range []struct {
		name    string
		handler http.Handler
		target  string
		want    int
	}{
		{"no session", router(user{err: auth.ErrUnauthenticated}, access{}), "/projects/p/saga/glossary?sourceView=published", http.StatusUnauthorized},
		{"viewer draft", router(user{}, access{err: service.ErrDenied}), "/projects/p/saga/glossary?sourceView=draft", http.StatusForbidden},
		{"stranger", router(user{}, access{err: service.ErrNotFound}), "/projects/p/saga/map?sourceView=published", http.StatusNotFound},
		{"viewer draft map", router(user{}, access{err: service.ErrDenied}), "/projects/p/saga/map?sourceView=draft", http.StatusForbidden},
		{"no view", router(user{}, access{}), "/projects/p/saga/contents", http.StatusBadRequest},
		{"unknown parameter", router(user{}, access{}), "/projects/p/saga/contents?sourceView=published&page=2", http.StatusBadRequest},
		{"repeated parameter", router(user{}, access{}), "/projects/p/saga/glossary?sourceView=published&q=a&q=b", http.StatusBadRequest},
		{"bad limit", router(user{}, access{}), "/projects/p/saga/glossary?sourceView=published&limit=x", http.StatusBadRequest},
		{"map parameter", router(user{}, access{}), "/projects/p/saga/map?sourceView=published&to=1", http.StatusBadRequest},
		{"unknown node", router(user{}, access{}), "/projects/p/saga/map/node?sourceView=published&key=nobody", http.StatusNotFound},
		{"no node key", router(user{}, access{}), "/projects/p/saga/map/node?sourceView=published", http.StatusBadRequest},
		{"search limit", router(user{}, access{}), "/projects/p/saga/map/search?sourceView=published&q=a&limit=99", http.StatusBadRequest},
		{"search parameter", router(user{}, access{}), "/projects/p/saga/map/search?sourceView=published&q=a&to=1", http.StatusBadRequest},
		{"viewer draft search", router(user{}, access{err: service.ErrDenied}), "/projects/p/saga/map/search?sourceView=draft&q=a", http.StatusForbidden},
		{"unknown chapter", router(user{}, access{}), "/projects/project/saga/map/node?sourceView=published&key=%D0%9C%D0%B8%D0%BB%D0%B0&to=99", http.StatusBadRequest},
	} {
		if response, _ := get(t, check.handler, check.target); response.Code != check.want {
			t.Fatalf("%s: %d, want %d", check.name, response.Code, check.want)
		}
	}
}

func send(t *testing.T, handler http.Handler, method, target, body string, headers ...string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Origin", "http://app.test")
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	handler.ServeHTTP(response, request)
	var parsed map[string]any
	if response.Body.Len() > 0 {
		if err := json.Unmarshal(response.Body.Bytes(), &parsed); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
	}
	return response, parsed
}

// specs/2026-11-04-saga-map.md R11.3: «Убрать с карты» and «Это не связь» keep a decision under the write proof, the next reads
// do not show it, and it is returned by its id.
func TestTheAuthorTakesANodeOffTheMapAndReturnsIt(t *testing.T) {
	handler := router(user{}, access{})
	const hidden = "/projects/project/saga/map/hidden?sourceView=published"
	if response, _ := send(t, handler, http.MethodPost, hidden, `{"nodeKey":"сава"}`); response.Code != http.StatusUnauthorized {
		t.Fatalf("without the write proof = %d", response.Code)
	}
	if response, _ := send(t, handler, http.MethodPost, hidden, `{"nodeKey":"сава","extra":1}`, "X-CSRF-Token", "csrf"); response.Code != http.StatusBadRequest {
		t.Fatalf("an unknown field = %d", response.Code)
	}
	response, item := send(t, handler, http.MethodPost, hidden, `{"nodeKey":"сава"}`, "X-CSRF-Token", "csrf")
	if response.Code != http.StatusCreated || item["id"] != "hidden-1" || item["nodeName"] != "Сава" || item["otherKey"] != nil || item["createdAt"] != "2026-11-05T12:00:00Z" {
		t.Fatalf("hide = %d %v", response.Code, item)
	}
	_, list := get(t, handler, hidden)
	if items := list["items"].([]any); len(items) != 1 || items[0].(map[string]any)["nodeKey"] != "сава" {
		t.Fatalf("list = %v", list)
	}
	graph, document := get(t, handler, "/projects/project/saga/map?sourceView=published")
	if graph.Code != http.StatusOK {
		t.Fatalf("map = %d", graph.Code)
	}
	for _, node := range document["nodes"].([]any) {
		if node.(map[string]any)["name"] == "Сава" {
			t.Fatalf("the node stays on the map: %v", document["nodes"])
		}
	}
	_, card := get(t, handler, "/projects/project/saga/map/node?sourceView=published&key=%D0%A1%D0%B0%D0%B2%D0%B0")
	if card["hiddenId"] != "hidden-1" {
		t.Fatalf("the card of a hidden node = %v", card)
	}
	if response, _ := send(t, handler, http.MethodDelete, "/projects/project/saga/map/hidden/other", "", "X-CSRF-Token", "csrf"); response.Code != http.StatusNotFound {
		t.Fatalf("restore of another = %d", response.Code)
	}
	if response, _ := send(t, handler, http.MethodDelete, "/projects/project/saga/map/hidden/hidden-1", "", "X-CSRF-Token", "csrf"); response.Code != http.StatusNoContent {
		t.Fatalf("restore = %d", response.Code)
	}
	if _, card := get(t, handler, "/projects/project/saga/map/node?sourceView=published&key=%D0%A1%D0%B0%D0%B2%D0%B0"); card["hiddenId"] != nil {
		t.Fatalf("the card after the return = %v", card)
	}
}
