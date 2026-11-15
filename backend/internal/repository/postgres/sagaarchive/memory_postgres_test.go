// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
	service "canonkeeper/backend/internal/services/sagaarchive"
	quotastore "canonkeeper/backend/internal/store/quota"
)

// bigSaga is a made-up saga of books × chapters × paragraphs of long text.
func bigSaga(t *testing.T, books, chapters, paragraphs int) []byte {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	id := func(kind, n int) string { return fmt.Sprintf("%08x-0000-4000-8000-%012x", kind, n) }
	var out bytes.Buffer
	writer := domain.NewWriter(&out)
	must(writer.Saga(domain.Saga{Name: "Северный предел", BookKeyNumbers: books}))
	shelf, err := writer.Books()
	must(err)
	for book := 1; book <= books; book++ {
		must(shelf.Add(domain.Book{Ref: id(1, book), KeyNumber: book, Order: book, Title: fmt.Sprintf("Том %d", book)}))
	}
	must(shelf.Close())
	lines, err := writer.Chapters()
	must(err)
	words := strings.Repeat("ветер нёс снег над перевалом, и ", 6)
	for book := 1; book <= books; book++ {
		for chapter := 1; chapter <= chapters; chapter++ {
			order := chapter - 1
			filed := domain.Chapter{Ref: id(2, book*1000+chapter), Book: id(1, book), Order: chapter, PublishedOrder: &order, Title: fmt.Sprintf("Глава %d-%d", book, chapter)}
			for paragraph := 1; paragraph <= paragraphs; paragraph++ {
				filed.Paragraphs = append(filed.Paragraphs, domain.Paragraph{
					ID: id(3, (book*1000+chapter)*1000+paragraph), Order: paragraph, Kind: "paragraph",
					Markdown: fmt.Sprintf("Мара шла к заставе %d тома %d: %sабзац %d.", chapter, book, words, paragraph),
				})
			}
			must(lines.Add(filed))
		}
	}
	must(lines.Close())
	must(writer.Close(domain.Manifest{CreatedBy: "canonkeeper.ru", CreatedAt: time.Now(), Saga: domain.ManifestSaga{Name: "Северный предел"}}))
	return out.Bytes()
}

// watched is a reader of a save that measures the memory of the process as the file goes.
type watched struct {
	written, next int64
	peak          uint64
}

func (watch *watched) Write(data []byte) (int, error) {
	watch.written += int64(len(data))
	if watch.written >= watch.next {
		watch.next = watch.written + 1<<20
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		watch.peak = max(watch.peak, stats.HeapAlloc)
	}
	return len(data), nil
}

// specs/2026-11-13-desk-sagafile.md SF2.5: a saga of 60 books of 50 chapters is saved with the memory of the process grown by
// no more than a constant, far less than its text: the chapters are read and written as a stream.
func TestTheSaveOfALargeSagaKeepsItsMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("a saga of 60 books")
	}
	world := newWorld(t, "ck_saga_file_memory")
	// The app sets the limits of the platform at their largest for its author (DA2.2): a saga of 60 books is his.
	changes := make([]quota.OverrideChange, 0, len(quota.Keys()))
	for _, key := range quota.Keys() {
		largest := key.Max()
		changes = append(changes, quota.OverrideChange{Key: key, Value: &largest})
	}
	if _, err := quotastore.New(world.db, quotastore.Fence{}).SetPlatformDefaults(world.ctx, world.user, changes, time.Now()); err != nil {
		t.Fatal(err)
	}
	opened := world.mustOpen(bigSaga(t, 60, 50, 60), "large-saga-key")
	var text int64
	if err := world.db.Pool.QueryRow(world.ctx, `
		SELECT sum(octet_length(paragraph.markdown)) FROM chapter_paragraphs AS paragraph
		JOIN chapters AS chapter ON chapter.id = paragraph.chapter_id
		WHERE chapter.project_id = $1 AND paragraph.variant = 'published'
	`, opened.ProjectID).Scan(&text); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	prepared, err := world.export.Prepare(world.ctx, service.ExportInput{ActorUserID: world.user, ProjectID: opened.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	watch := &watched{}
	if err := prepared.WriteTo(world.ctx, watch); err != nil {
		t.Fatal(err)
	}
	prepared.Close()
	grew := int64(watch.peak) - int64(before.HeapAlloc)
	t.Logf("the text of the saga: %d bytes, its file: %d bytes, the memory grew by %d bytes", text, watch.written, grew)
	const bound = 24 << 20
	if text < 2*bound || grew > bound {
		t.Fatalf("the memory grew by %d bytes for a text of %d; want no more than %d", grew, text, bound)
	}
}
