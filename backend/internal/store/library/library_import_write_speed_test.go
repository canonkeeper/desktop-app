package librarystore

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/bookexport"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// importWriteRoundTripLimit is how many round trips to the database the write of a book takes at most, whatever its
// size: the book, its chapters and their paragraphs in both text versions by batches, the catalog of sources, the
// quotas and the end of the Job (specs/2026-11-11-import-speed.md IS1.1). A statement per chapter or paragraph is a
// round trip each, and on a loaded server a round trip costs milliseconds: 1006 paragraphs by rows took 3300 round
// trips and 22 s on the server; by batches the write takes 95 for any book.
const importWriteRoundTripLimit = 120

// specs/2026-11-11-import-speed.md IS1.1, IS1.2: the write of a book takes a few round trips by batches, not one per
// paragraph, and little time. A book of the shape of the example (17 chapters, 1006 paragraphs) and a book of 4000
// paragraphs in 40 chapters are written by the handler of the import Job: each within importWriteRoundTripLimit round
// trips, the first within 2 s and the large one within 5 s on the test database (measured about 0,1 s and 0,3 s: the
// limits hold far more than three times the measure). The site measures the book of the example itself as well
// (TestTheWriteOfTheExampleTakesFewRoundTripsAndLittleTime).
func TestTheWriteOfABookTakesFewRoundTripsAndLittleTime(t *testing.T) {
	counter := &roundTripCounter{}
	db := testutil.OpenIsolatedPostgresTestDBWithTracer(t, "ck_store", counter)
	fixture := newImportWriteFixture(t, db)
	writes := &timedImportWrites{Store: fixture.store, counter: counter}

	for _, book := range []struct {
		name                 string
		chapters, paragraphs int
		limit                time.Duration
	}{{"a book of the shape of the example", 17, 1006, 2 * time.Second}, {"4000 paragraphs", 40, 4000, 5 * time.Second}} {
		write := writes.importFile(fixture, "book.fb2", largeBookFile(t, book.chapters, book.paragraphs))
		if write.finalized.ChapterCount != book.chapters || write.finalized.ParagraphCount != book.paragraphs {
			t.Fatalf("%s = %d chapters, %d paragraphs; want %d and %d", book.name,
				write.finalized.ChapterCount, write.finalized.ParagraphCount, book.chapters, book.paragraphs)
		}
		t.Logf("the write of %s: %d round trips, %s", book.name, write.roundTrips, write.took)
		write.within(t, book.name, book.limit)
	}
}

// within fails the test when the write took more round trips than any book may or more time than the limit.
func (write timedImportWrite) within(t *testing.T, name string, limit time.Duration) {
	t.Helper()
	if write.roundTrips > importWriteRoundTripLimit {
		t.Errorf("the write of %s took %d round trips; want at most %d, whatever the size of the book",
			name, write.roundTrips, importWriteRoundTripLimit)
	}
	if write.took > limit {
		t.Errorf("the write of %s took %s; want at most %s", name, write.took, limit)
	}
}

// largeBookFile is a book of the given chapters with the given paragraphs in all — the last chapter takes what the
// others leave — of about 400 characters a paragraph.
func largeBookFile(t *testing.T, chapters, paragraphs int) []byte {
	t.Helper()
	sentence := "Дорога шла вдоль реки, и отряд молча считал шаги до брода, где их ждали лодки. "
	book := make([]scenarioChapter, chapters)
	for chapter := range book {
		count := paragraphs / chapters
		if chapter == chapters-1 {
			count = paragraphs - count*(chapters-1)
		}
		book[chapter].title = fmt.Sprintf("Глава %d", chapter+1)
		book[chapter].paragraphs = make([]string, count)
		for paragraph := range book[chapter].paragraphs {
			book[chapter].paragraphs[paragraph] = fmt.Sprintf("%d.%d. %s", chapter+1, paragraph+1, strings.Repeat(sentence, 5))
		}
	}
	return scenarioFile(t, bookexport.FormatFB2, book)
}

// roundTripCounter counts the round trips of the connections of the pool while it is on.
type roundTripCounter struct {
	mu         sync.Mutex
	on         bool
	roundTrips int
}

func (counter *roundTripCounter) count(statements int) {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	if counter.on {
		counter.roundTrips += statements
	}
}

func (counter *roundTripCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	counter.count(1)
	return ctx
}

func (*roundTripCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// A batch goes to the database in one round trip, whatever its queries.
func (counter *roundTripCounter) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	counter.count(1)
	return ctx
}

func (*roundTripCounter) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}

func (*roundTripCounter) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

func (counter *roundTripCounter) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromStartData) context.Context {
	counter.count(1)
	return ctx
}

func (*roundTripCounter) TraceCopyFromEnd(context.Context, *pgx.Conn, pgx.TraceCopyFromEndData) {}

// timedImportWrites is the store of the import Job with its write timed and its round trips counted.
type timedImportWrites struct {
	*Store
	counter *roundTripCounter
	last    timedImportWrite
}

type timedImportWrite struct {
	finalized  library.FinalizedImport
	roundTrips int
	took       time.Duration
}

func (writes *timedImportWrites) FinalizeImport(ctx context.Context, input library.FinalizeImportStoreInput) (library.FinalizedImport, error) {
	writes.counter.mu.Lock()
	writes.counter.on, writes.counter.roundTrips = true, 0
	writes.counter.mu.Unlock()
	started := time.Now()
	finalized, err := writes.Store.FinalizeImport(ctx, input)
	took := time.Since(started)
	writes.counter.mu.Lock()
	writes.counter.on = false
	writes.last = timedImportWrite{finalized: finalized, roundTrips: writes.counter.roundTrips, took: took}
	writes.counter.mu.Unlock()
	return finalized, err
}

// importFile imports a new book through the handler of the import Job over the timed store and returns its write.
func (writes *timedImportWrites) importFile(fixture *importWriteFixture, name string, file []byte) timedImportWrite {
	fixture.t.Helper()
	fixture.importFileThrough(writes, library.ImportModeNewBook, "", name, file)
	return writes.last
}
