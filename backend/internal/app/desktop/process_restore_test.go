// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// The programs of a return: the earlier one is the tree, the later knows one migration more.
func returnPrograms(t *testing.T) (older, newer string) {
	t.Helper()
	return program(t, versioned("tree", "1.2.0", 0)), program(t, versioned("ahead", "1.3.0", 1))
}

// restoreStart is the start the shell sends for a return.
func restoreStart(binary, resources, data string) ControlMessage {
	return ControlMessage{
		Type: messageStart, DataDir: data, Resources: resources, SecretsStore: "file", Version: versionOfProgram(binary),
		Restore: true,
	}
}

// opened is the window of a running backend, signed in.
func opened(t *testing.T, process *backend) *client {
	t.Helper()
	browser := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 60 * time.Second}}
	if response := browser.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	return browser
}

// newSaga makes a saga and returns its id.
func (browser *client) newSaga(title string) string {
	browser.t.Helper()
	response, body := browser.do(http.MethodPost, "/api/v1/projects", browser.csrf(), map[string]string{"title": title})
	var project struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &project) != nil {
		browser.t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	return project.ID
}

// sagaTitles are the titles of the sagas of the window.
func (browser *client) sagaTitles() []string {
	browser.t.Helper()
	var listed struct {
		Data []struct {
			Title string `json:"title"`
		} `json:"data"`
	}
	response, body := browser.do(http.MethodGet, "/api/v1/projects", "", nil)
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &listed) != nil {
		browser.t.Fatalf("listProjects = %d %s", response.StatusCode, body)
	}
	var titles []string
	for _, item := range listed.Data {
		titles = append(titles, item.Title)
	}
	slices.Sort(titles)
	return titles
}

// bookOf imports a short book into the saga, waits for it and returns the id of the book.
func (browser *client) bookOf(projectID string) string {
	browser.t.Helper()
	job := browser.importBookKeyed(projectID, syntheticBook(3, 4), "import-of-the-return-test")
	if status := browser.untilFinished(job, 2*time.Minute); status != "succeeded" {
		browser.t.Fatalf("the import = %s", status)
	}
	var books struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	response, body := browser.do(http.MethodGet, "/api/v1/projects/"+projectID+"/books", "", nil)
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &books) != nil || len(books.Data) != 1 {
		browser.t.Fatalf("listBooks = %d %s", response.StatusCode, body)
	}
	return books.Data[0].ID
}

// cover uploads a cover of 600×900 to the book.
func (browser *client) cover(bookID string) {
	browser.t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 600, 900))
	for y := range 900 {
		for x := range 600 {
			picture.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		browser.t.Fatal(err)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "cover.png")
	_, _ = part.Write(encoded.Bytes())
	_ = form.Close()
	request, _ := http.NewRequest(http.MethodPost, browser.origin+"/api/v1/books/"+bookID+"/cover", &body)
	request.Header.Set("Origin", browser.origin)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("X-CSRF-Token", browser.csrf())
	request.AddCookie(&http.Cookie{Name: "ck_session", Value: browser.cookie})
	response, err := browser.http.Do(request)
	if err != nil {
		browser.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		browser.t.Fatalf("uploadBookCover = %d %s", response.StatusCode, raw)
	}
}

// coverFiles are the files of the covers in the data directory.
func coverFiles(t *testing.T, data string) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(data, "files", "book-covers", "*"))
	return files
}

// owners are the owners and the grants of every object of the database of the product: what a return must bring back
// as the migrations made it (UG4.3).
func owners(t *testing.T, data string) []string {
	t.Helper()
	connection := administrator(t, data, databaseName)
	defer connection.Close(context.Background())
	// A grant equal to the default of its object is the default: pg_dump writes no grant for it, and the order of the
	// grants is not theirs. The temporary objects of open sessions are no objects of the database.
	rows, err := connection.Query(context.Background(), `
WITH grants(kind, name, owner, acl) AS (
  SELECT 'class', n.nspname || '.' || c.relname, c.relowner,
    COALESCE(c.relacl, acldefault(CASE WHEN c.relkind = 'S' THEN 's'::"char" ELSE 'r'::"char" END, c.relowner))
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname !~ '^pg_(toast|temp)'
  UNION ALL
  SELECT 'function', p.oid::regprocedure::text, p.proowner, COALESCE(p.proacl, acldefault('f', p.proowner))
  FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
  WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
  UNION ALL
  SELECT 'schema', n.nspname, n.nspowner, COALESCE(n.nspacl, acldefault('n', n.nspowner))
  FROM pg_namespace n WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname !~ '^pg_'
  UNION ALL
  SELECT 'default ' || d.defaclobjtype::text, n.nspname, d.defaclrole, d.defaclacl
  FROM pg_default_acl d LEFT JOIN pg_namespace n ON n.oid = d.defaclnamespace
)
SELECT kind || ' ' || COALESCE(name, '') || ' ' || pg_get_userbyid(owner) || ' ' ||
  COALESCE((SELECT string_agg(item::text, ',' ORDER BY item::text) FROM unnest(acl) AS item), '')
FROM grants
ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		found = append(found, line)
	}
	if len(found) < 100 {
		t.Fatalf("the objects of the database = %d", len(found))
	}
	return found
}

// befores are the folders «до возврата» of the data directory.
func befores(t *testing.T, data string) []string {
	t.Helper()
	found, _ := filepath.Glob(filepath.Join(data, beforePrefix+"*"))
	slices.Sort(found)
	return found
}

// nothingHalfRestored fails when a mark of a return or a half restored part is left.
func nothingHalfRestored(t *testing.T, data string) {
	t.Helper()
	for _, left := range []string{restoreMark, "pg" + restoringSuffix, filepath.Join("codex", "home"+restoringSuffix), filepath.Join("codex", "sqlite"+restoringSuffix)} {
		if _, err := os.Lstat(filepath.Join(data, left)); !os.IsNotExist(err) {
			t.Fatalf("%s is left: %v", left, err)
		}
	}
}

// specs/2026-11-13-desk-upgrade.md UG4.1–UG4.5: the data of the later program — after its migration, with a saga more
// and a cover deleted — go back to the copy made before its update when the earlier program starts with the mark of a
// return: the sagas and books are as at the copy, the owners and grants of the database as the migrations made them,
// the book whose cover file is gone opens without it, the data of the later program lie in their folder «до возврата»,
// and the next ordinary start goes as usual. A second return leaves both folders.
func TestAReturnBringsTheCopyBackAndKeepsTheDataItReplaces(t *testing.T) {
	_, resources := desktopBinary(t)
	older, newer := returnPrograms(t)
	data := filepath.Join(t.TempDir(), "data")

	first := startBackend(t, older, resources, data, nil)
	browser := opened(t, first)
	saga := browser.newSaga("Северный предел")
	book := browser.bookOf(saga)
	browser.cover(book)
	if covers := coverFiles(t, data); len(covers) != 1 {
		t.Fatalf("the covers = %v", covers)
	}
	before := owners(t, data)
	first.stop()

	second := startBackend(t, newer, resources, data, nil)
	browser = opened(t, second)
	browser.newSaga("Южный перевал")
	if response, body := browser.do(http.MethodDelete, "/api/v1/books/"+book+"/cover", browser.csrf(), nil); response.StatusCode != http.StatusOK {
		t.Fatalf("deleteBookCover = %d %s", response.StatusCode, body)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for len(coverFiles(t, data)) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the file of the deleted cover stays: %v", coverFiles(t, data))
		}
		time.Sleep(200 * time.Millisecond)
	}
	later := browser.sagaTitles()
	second.stop()

	refused := launchBackend(t, older, resources, data, nil)
	refused.exited(60 * time.Second)
	if refused.ready.Reason != "newer_schema" || refused.ready.Copy == nil || len(refused.ready.Before) != 0 {
		t.Fatalf("the earlier program answered %+v", refused.ready)
	}
	returned := launchWith(t, older, restoreStart(older, resources, data))
	if returned.ready.Type != messageReady {
		t.Fatalf("the return answered %+v", returned.ready)
	}
	if len(returned.steps) != 1 || returned.steps[0].Step != "restore" || returned.steps[0].Copy == nil ||
		returned.steps[0].Copy.Name != refused.ready.Copy.Name {
		t.Fatalf("the steps of the return = %+v", returned.steps)
	}
	t.Logf("the return took %s", returned.took.Round(time.Millisecond))
	browser = opened(t, returned)
	if titles := browser.sagaTitles(); !reflect.DeepEqual(titles, []string{"Северный предел"}) {
		t.Fatalf("the sagas after the return = %v (before it %v)", titles, later)
	}
	if response, body := browser.do(http.MethodGet, "/api/v1/projects/"+saga+"/books", "", nil); response.StatusCode != http.StatusOK {
		t.Fatalf("listBooks after the return = %d %s", response.StatusCode, body)
	}
	response, body := browser.do(http.MethodGet, "/api/v1/books/"+book, "", nil)
	var shown struct {
		CoverURL *string `json:"coverUrl"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &shown) != nil || shown.CoverURL == nil {
		t.Fatalf("getBook after the return = %d %s", response.StatusCode, body)
	}
	// The row of the cover came back, its file did not: the server says the bytes are not there, and the page shows the
	// book without a cover (src/features/manuscript/shelf/BookCover.tsx falls back on an error of the picture).
	if response, _ := browser.do(http.MethodGet, *shown.CoverURL, "", nil); response.StatusCode != http.StatusServiceUnavailable && response.StatusCode != http.StatusNotFound {
		t.Fatalf("the cover whose file is gone = %d", response.StatusCode)
	}
	// «Скачать книгу…» gives the book without the cover, not an error.
	if response, body := browser.do(http.MethodGet, "/api/v1/books/"+book+"/export?format=fb2", "", nil); response.StatusCode != http.StatusOK ||
		!bytes.Contains(body, []byte("<FictionBook")) || bytes.Contains(body, []byte("<binary")) {
		t.Fatalf("getBookExport after the return = %d %.300s", response.StatusCode, body)
	}
	if after := owners(t, data); !reflect.DeepEqual(before, after) {
		var differ []string
		for _, line := range before {
			if !slices.Contains(after, line) {
				differ = append(differ, "- "+line)
			}
		}
		for _, line := range after {
			if !slices.Contains(before, line) {
				differ = append(differ, "+ "+line)
			}
		}
		t.Fatalf("the owners and grants after the return differ:\n%s", strings.Join(differ[:min(len(differ), 20)], "\n"))
	}
	returned.stop()
	nothingHalfRestored(t, data)
	folders := befores(t, data)
	if len(folders) != 1 {
		t.Fatalf("the folders «до возврата» = %v", folders)
	}
	for _, part := range restoreParts {
		if info, err := os.Stat(filepath.Join(folders[0], part)); err != nil || !info.IsDir() {
			t.Fatalf("the folder «до возврата» has no %s: %v", part, err)
		}
	}
	if complete, _ := copies(t, data); !slices.Contains(complete, refused.ready.Copy.Name) {
		t.Fatalf("the copy of the return is gone: %v", complete)
	}
	startBackend(t, older, resources, data, nil).stop()

	// A second return keeps the folder of the first.
	startBackend(t, newer, resources, data, nil).stop()
	again := launchBackend(t, older, resources, data, nil)
	again.exited(60 * time.Second)
	if len(again.ready.Before) != 1 || again.ready.Before[0].Bytes <= 0 {
		t.Fatalf("the earlier folder «до возврата» is not named: %+v", again.ready.Before)
	}
	second = launchWith(t, older, restoreStart(older, resources, data))
	if second.ready.Type != messageReady {
		t.Fatalf("the second return answered %+v", second.ready)
	}
	second.stop()
	if both := befores(t, data); len(both) != 2 || both[0] != folders[0] {
		t.Fatalf("the folders «до возврата» after two returns = %v", both)
	}
}

// UG4.2: a return killed after its mark, while the copy is being restored or between its moves leaves nothing the next
// ordinary start does not undo: the data of the later program open whole, and no mark, half restored part or empty folder
// of the return is left.
func TestAReturnKilledAnywhereLeavesTheDataWhole(t *testing.T) {
	_, resources := desktopBinary(t)
	older, newer := returnPrograms(t)
	data := filepath.Join(t.TempDir(), "data")
	first := startBackend(t, older, resources, data, nil)
	opened(t, first).newSaga("Северный предел")
	first.stop()
	second := startBackend(t, newer, resources, data, nil)
	opened(t, second).newSaga("Южный перевал")
	second.stop()

	for _, at := range []string{"the mark", "the restore"} {
		returning := beginBackend(t, older, restoreStart(older, resources, data))
		if message := returning.receive(60 * time.Second); message.Type != messageStep || message.Step != "restore" {
			t.Fatalf("the return said %+v", message)
		}
		if _, err := os.Stat(filepath.Join(data, restoreMark)); err != nil {
			t.Fatalf("the step of the return came before its mark: %v", err)
		}
		if at == "the restore" {
			deadline := time.Now().Add(60 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(data, "pg"+restoringSuffix, "postmaster.pid")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the cluster of the return was never caught running")
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		if err := returning.command.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = returning.command.Wait()

		next := startBackend(t, newer, resources, data, nil)
		if titles := opened(t, next).sagaTitles(); !reflect.DeepEqual(titles, []string{"Северный предел", "Южный перевал"}) {
			t.Fatalf("killed at %s, the next start found %v", at, titles)
		}
		next.stop()
		nothingHalfRestored(t, data)
		if folders := befores(t, data); len(folders) != 0 {
			t.Fatalf("killed at %s, folders of the return are left: %v", at, folders)
		}
	}

	// Between the moves no process is caught: they are renames of a moment. A finished return is put back where a kill
	// after its first moves leaves the data — the mark, the parts of the author not yet moved back in their places, the
	// restored ones beside them — and the next ordinary start must bring the data of the later program back whole. After
	// one move there is no cluster in its place at all: a start that did not undo the return would make an empty one.
	moves := 2 * len(restoreParts)
	for _, done := range []int{1, 2, moves - 1} {
		returned := launchWith(t, older, restoreStart(older, resources, data))
		if returned.ready.Type != messageReady || len(returned.steps) != 1 || returned.steps[0].Copy == nil {
			t.Fatalf("the return answered %+v after %+v", returned.ready, returned.steps)
		}
		returned.stop()
		folders := befores(t, data)
		if len(folders) != 1 {
			t.Fatalf("the folders «до возврата» = %v", folders)
		}
		// The moves of a return in their order: the part of the author goes (2i), the restored part comes (2i+1).
		for index := moves - 1; index >= done; index-- {
			part := restoreParts[index/2]
			from, to := filepath.Join(data, part), filepath.Join(data, part+restoringSuffix)
			if index%2 == 0 {
				from, to = filepath.Join(folders[0], part), filepath.Join(data, part)
			}
			if err := os.Rename(from, to); err != nil {
				t.Fatal(err)
			}
		}
		record, _ := json.Marshal(restoreRecord{Copy: returned.steps[0].Copy.Name, Before: filepath.Base(folders[0])})
		writeFile(t, filepath.Join(data, restoreMark), string(record))

		next := startBackend(t, newer, resources, data, nil)
		if titles := opened(t, next).sagaTitles(); !reflect.DeepEqual(titles, []string{"Северный предел", "Южный перевал"}) {
			t.Fatalf("killed after %d moves, the next start found %v", done, titles)
		}
		next.stop()
		nothingHalfRestored(t, data)
		if folders := befores(t, data); len(folders) != 0 {
			t.Fatalf("killed after %d moves, folders of the return are left: %v", done, folders)
		}
	}
}

// UG4.6: a return without the place it needs says how much more, before anything changes; a damaged copy is its own
// reason; either way the data and the directories are as they were.
func TestAReturnThatCannotBeDoneChangesNothing(t *testing.T) {
	_, resources := desktopBinary(t)
	older, newer := returnPrograms(t)
	data := filepath.Join(t.TempDir(), "data")
	startBackend(t, older, resources, data, nil).stop()
	second := startBackend(t, newer, resources, data, nil)
	opened(t, second).newSaga("Южный перевал")
	second.stop()
	listing := func() map[string]string {
		found := filesOf(t, data)
		for path := range found {
			if strings.Contains(path, string(filepath.Separator)+"logs") {
				delete(found, path)
			}
		}
		return found
	}
	before := listing()

	secrets, err := loadClusterSecrets(DataDir{Root: data}.Secret())
	if err != nil {
		t.Fatal(err)
	}
	cluster := newCluster(Resources{Root: resources}.PostgresBin(), DataDir{Root: data}, secrets)
	full := func(string) (uint64, error) { return 1 << 20, nil }
	failure := cluster.restore(context.Background(), func(ControlMessage) { t.Fatal("a step of a return without place") }, full, time.Now())
	if failure == nil {
		t.Fatal("a return without place went")
	}
	if fatal := failure.fatal(); fatal.Reason != "restore_space" || fatal.Needed <= 0 || fatal.Copy == nil {
		t.Fatalf("a return without place = %+v (%v)", fatal, failure)
	}
	if after := listing(); !reflect.DeepEqual(before, after) {
		t.Fatal("a return without place changed the data directory")
	}

	complete, _ := copies(t, data)
	if len(complete) != 1 {
		t.Fatalf("the copies = %v", complete)
	}
	writeFile(t, filepath.Join(data, "backups", complete[0], backupDump), "not an archive of pg_dump")
	before = listing()
	damaged := launchWith(t, older, restoreStart(older, resources, data))
	damaged.exited(60 * time.Second)
	if damaged.ready.Reason != "restore_copy" || damaged.ready.Copy == nil || damaged.ready.Copy.Name != complete[0] {
		t.Fatalf("a return from a damaged copy answered %+v", damaged.ready)
	}
	if after := listing(); !reflect.DeepEqual(before, after) {
		t.Fatal("a return from a damaged copy changed the data directory")
	}
	next := startBackend(t, newer, resources, data, nil)
	if titles := opened(t, next).sagaTitles(); !reflect.DeepEqual(titles, []string{"Южный перевал"}) {
		t.Fatalf("after a damaged copy the data hold %v", titles)
	}
	next.stop()
}
