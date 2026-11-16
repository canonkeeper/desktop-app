// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	coverrepo "canonkeeper/backend/internal/repository/postgres/library"
	coverservice "canonkeeper/backend/internal/services/library"
	service "canonkeeper/backend/internal/services/sagaarchive"
	librarystore "canonkeeper/backend/internal/store/library"

	"github.com/jackc/pgx/v5"
)

// repacked is a saga file with a record changed or added and its manifest signed again.
func repacked(t *testing.T, file []byte, change func(name string, body []byte) []byte, extra map[string][]byte) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		name string
		body []byte
	}
	var entries []entry
	for _, item := range reader.File {
		source, err := item.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(source)
		if err != nil {
			t.Fatal(err)
		}
		if change != nil && item.Name != domain.EntryManifest {
			body = change(item.Name, body)
		}
		entries = append(entries, entry{item.Name, body})
	}
	manifest := entries[len(entries)-1]
	entries = entries[:len(entries)-1]
	for name, body := range extra {
		entries = append(entries, entry{name, body})
	}
	var fields map[string]any
	if err := json.Unmarshal(manifest.body, &fields); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, item := range entries {
		sum := sha256.Sum256(item.body)
		files[item.name] = hex.EncodeToString(sum[:])
	}
	fields["files"] = files
	manifest.body, _ = json.Marshal(fields)
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, item := range append(entries, manifest) {
		target, err := writer.Create(item.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Write(item.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// trace is what the product holds: the rows of the base an open writes and the files of the build.
func (world *world) trace() string {
	world.t.Helper()
	var parts []string
	for _, table := range []string{"projects", "books", "chapters", "chapter_paragraphs", "chapter_cards", "saga_names", "saga_threads",
		"author_materials", "author_memory", "personal_notes", "saga_transcripts", "book_cover_artifacts", "saga_map_hidden", "jobs", "idempotency_records"} {
		parts = append(parts, table+"="+strings.Repeat("|", world.count(`SELECT count(*) FROM `+table)))
	}
	var files []string
	_ = filepath.WalkDir(world.root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	temp, _ := os.ReadDir(world.temp.Path())
	return strings.Join(parts, " ") + " files=" + strings.Join(files, ",") + " temp=" + strings.Repeat("|", len(temp))
}

func refusal(err error) string {
	var refused *domain.Refusal
	if errors.As(err, &refused) {
		return refused.Code
	}
	if err != nil {
		return "error: " + err.Error()
	}
	return ""
}

// specs/2026-11-13-desk-sagafile.md SF3.1, SF3.2: an evil file is refused with its code and leaves no trace in the base, among
// the files of the build or in the temporary directory.
func TestAnEvilFileLeavesNoTrace(t *testing.T) {
	world := newWorld(t, "ck_saga_file_evil")
	good := northernLimit(t, fixtureOptions{covers: map[int][]byte{1: coverPNG(t, 600, 900)}})
	before := world.trace()
	cases := map[string]struct {
		file []byte
		code string
	}{
		"not a zip":            {[]byte("Глава 1. Перевал"), domain.CodeNotArchive},
		"cut short":            {good[:len(good)/2], domain.CodeDamaged},
		"a record of no table": {repacked(t, good, nil, map[string][]byte{"extra.txt": []byte("x")}), domain.CodeInvalid},
		"a sign of control in a paragraph": {repacked(t, good, func(name string, body []byte) []byte {
			if name == domain.EntryChapters {
				return bytes.Replace(body, []byte("Лёд держал"), []byte(`Лёд\u0001держал`), 1)
			}
			return body
		}, nil), domain.CodeInvalid},
		"a name of a material twice": {repacked(t, good, func(name string, body []byte) []byte {
			if name == domain.EntryMaterials {
				return append(body, bytes.SplitN(body, []byte("\n"), 2)[0]...)
			}
			return body
		}, nil), domain.CodeInvalid},
		// What the base cannot keep is a refusal of the file, never an error of the server.
		"a zero escaped in a card": {repacked(t, good, func(name string, body []byte) []byte {
			if name == domain.EntryCards {
				return bytes.Replace(body, []byte("Мара идёт"), []byte(`Мара\u0000идёт`), 1)
			}
			return body
		}, nil), domain.CodeInvalid},
		"turns of other members over an integer": {repacked(t, good, func(name string, body []byte) []byte {
			if name == domain.EntryChats {
				return bytes.Replace(body, []byte(`"othersTurns":2`), []byte(`"othersTurns":3000000000`), 1)
			}
			return body
		}, nil), domain.CodeInvalid},
	}
	for name, item := range cases {
		if _, err := world.openFile(item.file, "evil-"+strings.ReplaceAll(name, " ", "-")); refusal(err) != item.code {
			t.Fatalf("%s = %v; want %s", name, err, item.code)
		}
		if after := world.trace(); after != before {
			t.Fatalf("%s left a trace:\n%s\nwas\n%s", name, after, before)
		}
	}
}

// SF3.3, SF3.12: a file opened twice with two keys is two sagas, the second named by the date of its file and the first
// unchanged; the same request twice is one saga and one answer; the key with another file is a conflict. A saga transferred
// from the site whose name is taken is named as one from the site (specs/2026-11-16-desk-site-saga.md TR5.2).
func TestAFileOpensOncePerKey(t *testing.T) {
	world := newWorld(t, "ck_saga_file_key")
	file := northernLimit(t, fixtureOptions{})
	first := world.mustOpen(file, "the-first-key")
	rows := world.rows(first.ProjectID)
	again := world.mustOpen(file, "the-first-key")
	if again.ProjectID != first.ProjectID || world.count(`SELECT count(*) FROM projects`) != 1 {
		t.Fatalf("a repeat of the request = %+v; want the first saga", again)
	}
	if _, err := world.openFile(northernLimit(t, fixtureOptions{name: "Другая сага"}), "the-first-key"); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("the key with another file = %v; want a conflict", err)
	}
	second := world.mustOpen(file, "the-second-key")
	if second.ProjectID == first.ProjectID || second.Name != "Северный предел — из файла от 12.11.2026" || !second.Renamed {
		t.Fatalf("the second open = %+v", second)
	}
	after := world.rows(first.ProjectID)
	for part, values := range rows {
		if !slices.Equal(values, after[part]) {
			t.Fatalf("the %s of the first saga changed: %v → %v", part, values, after[part])
		}
	}
	transferred, err := world.openFrom(file, "the-transfer-key", service.FromSite)
	if err != nil || transferred.Name != "Северный предел — с сайта от 12.11.2026" || !transferred.Renamed {
		t.Fatalf("a saga transferred from the site = %+v %v", transferred, err)
	}
}

// SF3.9, SF3.12: two requests of one key at once — a repeat while the first open is under way — are one saga and one answer,
// and the covers the second request put among the files of the build go: no file of a cover without its row, no list of an
// open left behind.
func TestTwoRequestsOfOneKeyAtOnceLeaveOneCover(t *testing.T) {
	world := newWorld(t, "ck_saga_file_same_key")
	held, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	world.open.Store = world.open.Store.(*Opener).WithFailpoint(func() error {
		first.Do(func() {
			close(held)
			<-release
		})
		return nil
	})
	file := northernLimit(t, fixtureOptions{covers: map[int][]byte{1: coverPNG(t, 600, 900)}})
	type result struct {
		opened service.Opened
		err    error
	}
	results := make(chan result, 2)
	go func() {
		opened, err := world.openFile(file, "one-key")
		results <- result{opened, err}
	}()
	<-held
	go func() {
		opened, err := world.openFile(file, "one-key")
		results <- result{opened, err}
	}()
	// The second request waits for the key the first one holds in its transaction.
	deadline := time.Now().Add(10 * time.Second)
	for world.count(`SELECT count(*) FROM pg_locks WHERE NOT granted`) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the second request does not wait for the first")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	one, two := <-results, <-results
	if one.err != nil || two.err != nil || one.opened.ProjectID != two.opened.ProjectID {
		t.Fatalf("two requests of one key = %+v, %v and %+v, %v; want one saga", one.opened, one.err, two.opened, two.err)
	}
	covers, _ := os.ReadDir(filepath.Join(world.root, "book-covers"))
	marked, err := world.temp.Marked()
	if err != nil || len(covers) != 1 || len(marked) != 0 || world.count(`SELECT count(*) FROM book_cover_artifacts`) != 1 ||
		world.count(`SELECT count(*) FROM projects`) != 1 {
		t.Fatalf("after two requests of one key: %d files of covers, lists %v, %v", len(covers), marked, err)
	}
}

// SF3.13: while an open writes its saga the session of the window is free — a write that locks it goes at once — and another
// write of the same author waits no longer than the open: it ends once the open commits.
func TestTheOpenHoldsTheSessionNoLongerThanAWrite(t *testing.T) {
	world := newWorld(t, "ck_saga_file_session")
	held, release := make(chan struct{}), make(chan struct{})
	world.open.Store = world.open.Store.(*Opener).WithFailpoint(func() error {
		close(held)
		<-release
		return nil
	})
	opened := make(chan error, 1)
	go func() {
		_, err := world.openFile(northernLimit(t, fixtureOptions{}), "session-key")
		opened <- err
	}()
	<-held
	if err := world.db.WithTx(world.ctx, func(tx pgx.Tx) error {
		var id string
		return tx.QueryRow(world.ctx, `SELECT id::text FROM sessions WHERE user_id = $1 FOR UPDATE NOWAIT`, world.user).Scan(&id)
	}); err != nil {
		t.Fatalf("the session is held while the open writes its saga: %v", err)
	}
	type outcome struct {
		err  error
		took time.Duration
	}
	other := make(chan outcome, 1)
	started := time.Now()
	go func() {
		_, err := world.projects().CreateProject(world.ctx, world.user, projects.CreateProjectInput{Title: "Другая сага"})
		other <- outcome{err, time.Since(started)}
	}()
	const hold = 300 * time.Millisecond
	time.Sleep(hold)
	close(release)
	if err := <-opened; err != nil {
		t.Fatalf("the open: %v", err)
	}
	write := <-other
	if write.err != nil || write.took > hold+5*time.Second {
		t.Fatalf("another write of the author = %v after %v; want it to end once the open commits", write.err, write.took)
	}
	t.Logf("another write of the author waited %v while the open was held %v", write.took, hold)
}

// SF3.9: a cover that unpacks too large and a cover of a type other than its book names are not taken: the saga opens, the
// report names the books, no file of theirs is among the files of the build.
func TestACoverOutOfTheRulesStaysOut(t *testing.T) {
	world := newWorld(t, "ck_saga_file_covers")
	huge := coverPNG(t, 600, 900)
	huge = append([]byte(nil), huge...)
	for index, value := range []uint32{50_000, 50_000} {
		at := 16 + 4*index
		huge[at], huge[at+1], huge[at+2], huge[at+3] = byte(value>>24), byte(value>>16), byte(value>>8), byte(value)
	}
	opened := world.mustOpen(northernLimit(t, fixtureOptions{
		covers: map[int][]byte{1: huge, 2: coverPNG(t, 600, 900)}, mime: map[int]string{2: "image/jpeg"},
	}), "covers-key")
	if !slices.Equal(opened.Report.CoversSkipped, []string{"Перевал", "Маяк"}) {
		t.Fatalf("the covers not taken = %v", opened.Report.CoversSkipped)
	}
	if covers := world.count(`SELECT count(*) FROM book_cover_artifacts`); covers != 0 || !strings.HasSuffix(world.trace(), "files= temp=") {
		t.Fatalf("covers = %d, files: %s", covers, world.trace())
	}
}

// SF3.5, SF3.6: a chapter whose text changed in a file signed again opens without its card, named by its place, and the
// registry is not fresh; a card of a version newer than the product knows is not taken either.
func TestAChangedTextLosesItsCard(t *testing.T) {
	world := newWorld(t, "ck_saga_file_changed")
	file := repacked(t, northernLimit(t, fixtureOptions{}), func(name string, body []byte) []byte {
		switch name {
		case domain.EntryChapters:
			return bytes.Replace(body, []byte("Смотритель ушёл в море."), []byte("Смотритель ушёл в горы."), 1)
		case domain.EntryCards:
			lines := bytes.Split(body, []byte("\n"))
			lines[5] = bytes.Replace(lines[5], []byte(`"version":4`), []byte(`"version":99`), 1)
			return bytes.Join(lines, []byte("\n"))
		}
		return body
	}, nil)
	opened := world.mustOpen(file, "changed-key")
	report := opened.Report
	if report.Cards != 4 || report.ChaptersWithoutCards.Count != 2 || report.NamesFresh ||
		!slices.Equal(report.ChaptersWithoutCards.Places, []string{"Книга 1, гл. 2 „Смотритель“", "Книга 3, гл. 1 „Лёд“"}) {
		t.Fatalf("the report = %+v", report)
	}
}

// CP1.1 (specs/2026-11-14-chapter-place-number.md): a chapter after the gap in the order of its book is named in the report
// by its place among the chapters of its book in the file, not by its order — «Застава», order 3 and the second chapter of
// «Перевал», is «гл. 2».
func TestAChapterAfterAGapIsNamedByItsPlaceInItsBook(t *testing.T) {
	world := newWorld(t, "ck_saga_file_gap_place")
	file := repacked(t, northernLimit(t, fixtureOptions{}), func(name string, body []byte) []byte {
		if name == domain.EntryChapters {
			return bytes.Replace(body, []byte("Огонь почти погас."), []byte("Огонь погас."), 1)
		}
		return body
	}, nil)
	report := world.mustOpen(file, "gap-key").Report
	if without := report.ChaptersWithoutCards; without.Count != 1 || !slices.Equal(without.Places, []string{"Книга 2, гл. 2 „Застава“"}) {
		t.Fatalf("the chapters without cards = %+v", without)
	}
}

// SF3.1, SF3.7: a note of a chat whose thought is as long as a note may be opens: its quote gives way to the thought, and
// the note keeps within its limit.
func TestALongThoughtBesideAQuoteOpens(t *testing.T) {
	world := newWorld(t, "ck_saga_file_long_note")
	thought := strings.Repeat("м", 99_998)
	file := repacked(t, northernLimit(t, fixtureOptions{}), func(name string, body []byte) []byte {
		if name == domain.EntryNotes {
			return bytes.Replace(body, []byte(`"body":"Проверить."`), []byte(`"body":"`+thought+`"`), 1)
		}
		return body
	}, nil)
	opened := world.mustOpen(file, "long-note-key")
	var longest int
	if err := world.db.Pool.QueryRow(world.ctx, `SELECT max(char_length(body)) FROM personal_notes WHERE owner_user_id = $1`, world.user).Scan(&longest); err != nil {
		t.Fatal(err)
	}
	if opened.Report.Notes != 3 || longest > 100_000 || world.count(`SELECT count(*) FROM personal_notes WHERE body LIKE '%' || $1`, thought) != 1 {
		t.Fatalf("the notes = %d, the longest %d signs", opened.Report.Notes, longest)
	}
}

// SF1.10, SF3.1: a saga whose names hold a sign the product keeps — a tab in the name of the saga, of a book and of a
// chapter — opens and is saved into its file again.
func TestNamesWithATabOpenAndAreSavedAgain(t *testing.T) {
	world := newWorld(t, "ck_saga_file_names")
	file := repacked(t, northernLimit(t, fixtureOptions{name: "Северный\tпредел"}), func(name string, body []byte) []byte {
		switch name {
		case domain.EntryBooks:
			return bytes.Replace(body, []byte(`"title":"Перевал"`), []byte(`"title":"Перевал\tпервый"`), 1)
		case domain.EntryChapters:
			return bytes.Replace(body, []byte(`"title":"Метель"`), []byte(`"title":"Метель\tи буран"`), 1)
		}
		return body
	}, nil)
	opened := world.mustOpen(file, "names-key")
	archive := read(t, world.save(opened.ProjectID))
	titles := []string{archive.Saga.Name}
	for _, book := range archive.Books {
		titles = append(titles, book.Title)
	}
	for _, chapter := range archive.Chapters {
		titles = append(titles, chapter.Title)
	}
	for _, want := range []string{"Северный\tпредел", "Перевал\tпервый", "Метель\tи буран"} {
		if !slices.Contains(titles, want) {
			t.Fatalf("the names of the next file = %q; want %q among them", titles, want)
		}
	}
}

// SF3.10: a record of a workshop this build does not read is named in the report and the saga opens.
func TestARecordOfAWorkshopIsNamed(t *testing.T) {
	world := newWorld(t, "ck_saga_file_workshop")
	opened := world.mustOpen(repacked(t, northernLimit(t, fixtureOptions{}), nil, map[string][]byte{"workshops/writing/x.jsonl": []byte("{}\n")}), "workshop-key")
	if !slices.Equal(opened.Report.Sections, []string{"workshops/writing/x.jsonl"}) {
		t.Fatalf("the sections = %v", opened.Report.Sections)
	}
}

// SF3.9: an open that falls between the files of its covers and its commit leaves no file of a cover without its row once
// the build starts again.
func TestAFallBeforeTheCommitLeavesNoCover(t *testing.T) {
	world := newWorld(t, "ck_saga_file_fall")
	fall := errors.New("the process fell")
	world.open.Store = world.open.Store.(*Opener).WithFailpoint(func() error {
		// The files are there and the transaction is not committed; the process is gone before it takes them away.
		if entries, _ := os.ReadDir(filepath.Join(world.root, "book-covers")); len(entries) == 0 {
			t.Error("no file of a cover before the commit")
		}
		return fall
	})
	world.open.Covers, world.open.Marks = keptCovers{world.open.Covers}, keptMarks{world.open.Marks}
	if _, err := world.openFile(northernLimit(t, fixtureOptions{covers: map[int][]byte{1: coverPNG(t, 600, 900)}}), "fall-key"); !errors.Is(err, fall) {
		t.Fatalf("the open = %v", err)
	}
	marked, err := world.temp.Marked()
	if err != nil || len(marked) == 0 {
		t.Fatalf("the list of the covers of the open = %v, %v", marked, err)
	}
	stray, err := New(world.db).StrayCovers(world.ctx, marked)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range stray {
		_ = world.covers.Remove(world.ctx, key)
	}
	if err := world.temp.Clear(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(world.root, "book-covers")); len(entries) != 0 || world.count(`SELECT count(*) FROM book_cover_artifacts`) != 0 {
		t.Fatalf("after the start the covers = %v", entries)
	}
}

// keptCovers are covers a fallen process could not take away: their removal is lost.
type keptCovers struct{ service.CoverFiles }

func (keptCovers) Remove(context.Context, string) error { return nil }

// keptMarks are the lists of a fallen process: they stay for its next start.
type keptMarks struct{ service.Marks }

type keptMark struct{}

func (keptMark) Remove() error { return nil }

func (marks keptMarks) Mark(keys []string) (service.Mark, error) {
	if _, err := marks.Marks.Mark(keys); err != nil {
		return nil, err
	}
	return keptMark{}, nil
}

// SF3.8, SF3.14: the chats of the file go with the saga and come back with it.
func TestTheChatsOfAFileGoAndComeBackWithTheSaga(t *testing.T) {
	world := newWorld(t, "ck_saga_file_chats")
	opened := world.mustOpen(northernLimit(t, fixtureOptions{}), "chats-key")
	store := New(world.db)
	if err := world.projects().DeleteProject(world.ctx, world.user, opened.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transcripts(world.ctx, world.user, opened.ProjectID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("the chats of a deleted saga = %v", err)
	}
	if _, err := world.projects().RestoreProject(world.ctx, world.user, opened.ProjectID); err != nil {
		t.Fatal(err)
	}
	if transcripts, err := store.Transcripts(world.ctx, world.user, opened.ProjectID); err != nil || len(transcripts) != 1 {
		t.Fatalf("the chats of a returned saga = %v, %v", transcripts, err)
	}
}

// SF2.1: someone outside the saga gets nothing, a member below the owner is refused.
func TestOnlyTheOwnerSavesASaga(t *testing.T) {
	world := newWorld(t, "ck_saga_file_rights")
	opened := world.mustOpen(northernLimit(t, fixtureOptions{}), "rights-key")
	var stranger, editor string
	if err := world.db.Pool.QueryRow(world.ctx, `SELECT gen_random_uuid()::text, gen_random_uuid()::text`).Scan(&stranger, &editor); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{stranger, editor} {
		if _, err := world.db.Pool.Exec(world.ctx, `INSERT INTO users (id, email, display_name, password_hash) VALUES ($1, $2, 'Читатель', 'hash')`,
			user, "reader-"+user[:8]+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := world.db.Pool.Exec(world.ctx, `
		INSERT INTO project_memberships (id, project_id, user_id, role, created_at) VALUES (gen_random_uuid(), $1, $2, 'editor', now())
	`, opened.ProjectID, editor); err != nil {
		t.Fatal(err)
	}
	read := func(user string) error {
		return New(world.db).Snapshot(world.ctx, user, opened.ProjectID, func(service.Snapshot) error { return nil })
	}
	if err := read(stranger); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("a stranger = %v", err)
	}
	if err := read(editor); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("an editor = %v", err)
	}
	if err := world.projects().DeleteProject(world.ctx, world.user, opened.ProjectID); err != nil {
		t.Fatal(err)
	}
	if err := read(world.user); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("a deleted saga = %v", err)
	}
}

// SF1.3, SF1.7, SF1.9, SF3.14: a saga opened from a file is a saga of the product — its book is deleted and returned, its
// chapter deleted — and its next file carries no book or chapter of «Недавно удалённые»: the record of the memory about the
// deleted chapter goes without its chapter, and the manifest counts what was left out.
func TestTheFileCarriesNothingOfTheTrash(t *testing.T) {
	world := newWorld(t, "ck_saga_file_trash")
	opened := world.mustOpen(northernLimit(t, fixtureOptions{}), "trash-key")
	chapterOf := func(title string) string {
		var id string
		if err := world.db.Pool.QueryRow(world.ctx, `SELECT id::text FROM chapters WHERE project_id = $1 AND title = $2`, opened.ProjectID, title).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	var ice string
	if err := world.db.Pool.QueryRow(world.ctx, `SELECT id::text FROM books WHERE project_id = $1 AND title = 'Лёд'`, opened.ProjectID).Scan(&ice); err != nil {
		t.Fatal(err)
	}
	if err := world.library.DeleteBook(world.ctx, world.actor(), ice); err != nil {
		t.Fatal(err)
	}
	if _, err := world.library.RestoreBook(world.ctx, world.actor(), ice); err != nil {
		t.Fatalf("the return of a book of an opened saga: %v", err)
	}
	if err := world.library.DeleteBook(world.ctx, world.actor(), ice); err != nil {
		t.Fatal(err)
	}
	if err := world.library.DeleteChapter(world.ctx, world.actor(), chapterOf("Застава")); err != nil {
		t.Fatal(err)
	}
	archive := read(t, world.save(opened.ProjectID))
	var titles []string
	for _, chapter := range archive.Chapters {
		titles = append(titles, chapter.Title)
	}
	slices.Sort(titles)
	if !slices.Equal(titles, []string{"Глава 5. Маяк", "Глава 5. Перевал", "Метель", "Смотритель"}) {
		t.Fatalf("the chapters of the file = %v", titles)
	}
	for _, record := range archive.Memory {
		if record.Chapter != "" {
			t.Fatalf("a record about a chapter of the trash kept it: %+v", record)
		}
	}
	if left := archive.Manifest.LeftOut; left.TrashedBooks != 1 || left.TrashedChapters != 1 {
		t.Fatalf("what the file left out = %+v", left)
	}
	// The note on «Застава» has no chapter in the file: its quote stays in its body.
	for _, note := range archive.Notes {
		if note.Title == "Застава" && (note.Source != nil || !strings.HasPrefix(note.Body, "> заставе")) {
			t.Fatalf("the note of a chapter of the trash = %+v", note)
		}
	}
}

// SF3.14, SF1.7: a chapter of an opened saga is edited and its book saved as a file of a book by the code of the product;
// the note made on the earlier revision of the chapter goes into the next saga file with its chapter and without its
// paragraph.
func TestAnOpenedSagaIsASagaOfTheProduct(t *testing.T) {
	world := newWorld(t, "ck_saga_file_product")
	opened := world.mustOpen(northernLimit(t, fixtureOptions{}), "product-key")
	var bookID, chapterID string
	if err := world.db.Pool.QueryRow(world.ctx, `SELECT book_id::text, id::text FROM chapters WHERE project_id = $1 AND title = 'Застава'`,
		opened.ProjectID).Scan(&bookID, &chapterID); err != nil {
		t.Fatal(err)
	}
	store := librarystore.New(world.db)
	if err := store.WithTx(world.ctx, func(tx pgx.Tx) error {
		_, err := store.ReplaceChapterTextInTx(world.ctx, tx, library.ReplaceChapterTextInput{
			ProjectID: opened.ProjectID, BookID: bookID, ChapterID: chapterID, ExpectedRevision: 1, Title: "Застава",
			Paragraphs: []string{"На заставе Тарен ждал её четвёртый день.", "Огонь погас."},
		})
		return err
	}); err != nil {
		t.Fatalf("the edit of a chapter of an opened saga: %v", err)
	}
	covers := coverrepo.New(world.db)
	file, err := coverservice.NewExportBook(covers, coverservice.NewGetCover(covers, world.covers), time.Now).Execute(world.ctx, coverservice.ExportBookInput{
		Actor: domainlibrary.Actor{UserID: world.user}, BookID: bookID, Format: "docx",
	})
	if err != nil {
		t.Fatalf("the file of a book of an opened saga: %v", err)
	}
	var docx bytes.Buffer
	if err := file.Write(&docx); err != nil || docx.Len() == 0 {
		t.Fatalf("the file of the book = %d bytes, %v", docx.Len(), err)
	}
	archive := read(t, world.save(opened.ProjectID))
	for _, note := range archive.Notes {
		if note.Title == "Застава" && (note.Source == nil || note.Source.Paragraph != "" || note.Source.Range != nil) {
			t.Fatalf("the note on the earlier revision = %+v; want its chapter without its paragraph", note.Source)
		}
	}
}

// specs/2026-11-15-reader-chapter-number.md RN1.2: a chapter of a saga file without a published text — «Застава», between
// «Глава 5. Перевал» and «Метель» of «Перевал» — is not in the contents of the reader, which shows the published text, and
// the number of every chapter is its place in those contents: «Метель» is chapter 2 in the list, in its own head and as the
// neighbour of «Перевал», whose step goes to «Метель», and the place of the chat names it «гл. 2» too. The chapter without a
// published text keeps its draft and is named by the place it would take.
func TestAChapterWithoutAPublishedTextTakesNoPlaceInTheReader(t *testing.T) {
	world := newWorld(t, "ck_saga_file_unpublished")
	file := repacked(t, northernLimit(t, fixtureOptions{}), func(name string, body []byte) []byte {
		if name != domain.EntryChapters {
			return body
		}
		lines := bytes.Split(body, []byte("\n"))
		for index, line := range lines {
			if bytes.Contains(line, []byte(chapterRef(2))) {
				lines[index] = bytes.Replace(line, []byte(`"publishedOrder":1,`), nil, 1)
			}
		}
		return bytes.Join(lines, []byte("\n"))
	}, nil)
	opened := world.mustOpen(file, "unpublished-key")
	world.index(opened.ProjectID, governance.SourceViewPublished)
	books, err := world.library.ListBooks(world.ctx, world.actor(), opened.ProjectID, library.Page{})
	if err != nil {
		t.Fatal(err)
	}
	var bookID string
	for _, book := range books.Data {
		if book.Title == "Перевал" {
			bookID = book.ID
		}
	}
	page, err := world.library.ListChapters(world.ctx, world.actor(), bookID, library.Page{})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	var contents, hidden []string
	for _, summary := range page.Data {
		ids[summary.Title] = summary.ID
		number, title := summary.Numbered()
		// The contents of the reader: the chapters with a published text (ManuscriptReaderMode).
		if summary.Status == library.ChapterStatusPublished || summary.Status == library.ChapterStatusStale {
			contents = append(contents, number+" "+title)
		} else {
			hidden = append(hidden, number+" "+title)
		}
	}
	if want := []string{"5 Перевал", "2 Метель"}; !slices.Equal(contents, want) || !slices.Equal(hidden, []string{"2 Застава"}) {
		t.Fatalf("the contents of the reader = %v, the chapter without a published text %v; want %v and «2 Застава»", contents, hidden, want)
	}
	chapterOf := func(title string, variant library.ParagraphVariant) library.ChapterContent {
		t.Helper()
		chapter, err := world.library.GetChapter(world.ctx, world.actor(), ids[title], variant)
		if err != nil {
			t.Fatalf("the chapter %s: %v", title, err)
		}
		return chapter
	}
	named := func(item *library.ChapterNavigationItem) string {
		if item == nil {
			return "-"
		}
		number, title := item.Numbered()
		return number + " " + title
	}
	for _, test := range []struct {
		title                string
		variant              library.ParagraphVariant
		head, previous, next string
		position, total      int
	}{
		{"Глава 5. Перевал", library.ParagraphVariantPublished, "5 Перевал", "-", "2 Метель", 1, 2},
		{"Метель", library.ParagraphVariantPublished, "2 Метель", "5 Перевал", "-", 2, 2},
		{"Застава", library.ParagraphVariantDraft, "2 Застава", "5 Перевал", "2 Метель", 2, 2},
	} {
		chapter := chapterOf(test.title, test.variant)
		number, title := chapter.Numbered()
		navigation := chapter.Navigation
		if head := number + " " + title; head != test.head || named(navigation.Previous) != test.previous || named(navigation.Next) != test.next ||
			navigation.Position != test.position || navigation.Total != test.total {
			t.Errorf("the chapter %s = %s, ‹ %s, %s ›, %d of %d; want %s, ‹ %s, %s ›, %d of %d", test.title, head, named(navigation.Previous),
				named(navigation.Next), navigation.Position, navigation.Total, test.head, test.previous, test.next, test.position, test.total)
		}
	}
	outline, err := memoryOutlines{world}.Outline(world.ctx, opened.ProjectID, string(governance.SourceViewPublished))
	if err != nil {
		t.Fatal(err)
	}
	var places []string
	for index := range outline.Version.Chapters {
		if chapter := &outline.Version.Chapters[index]; chapter.BookID == bookID {
			places = append(places, chapter.Place())
		}
	}
	if want := []string{"Книга 2, гл. 5 „Перевал“", "Книга 2, гл. 2 „Метель“"}; !slices.Equal(places, want) {
		t.Fatalf("the places of the chat = %v; want %v", places, want)
	}
}
