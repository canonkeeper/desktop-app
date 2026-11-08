package indexingstore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"github.com/jackc/pgx/v5"
)

func TestStoreFailpointsRollbackWholeReplace(t *testing.T) {
	points := []string{"after_state_upsert"}
	for index, point := range points {
		t.Run(point, func(t *testing.T) {
			fixture := newStoreFixture(t, 7500+index*20, governance.SourceViewDraft)
			oldSnapshot := fixture.snapshot(t)
			if _, err := fixture.store.ReplaceCanonTextChapterForClaim(context.Background(), ReplaceCanonTextChapterInput{Snapshot: oldSnapshot, IndexedAt: fixture.now}); err != nil {
				t.Fatal(err)
			}
			before := canonicalChapterRows(t, fixture)
			replacement := strings.Repeat("replacement ", 300)
			if _, err := fixture.db.Pool.Exec(context.Background(), `INSERT INTO chapter_paragraphs (id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count,created_at,updated_at) SELECT id,chapter_id,2,variant,kind,"order",$2,$2,300,created_at,updated_at FROM chapter_paragraphs WHERE chapter_id=$1 AND variant='draft' AND revision=1`, fixture.chapterID, replacement); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.db.Pool.Exec(context.Background(), `UPDATE chapter_source_members SET source_revision=2 WHERE project_id=$1 AND source_view='draft' AND chapter_id=$2`, fixture.projectID, fixture.chapterID); err != nil {
				t.Fatal(err)
			}
			snapshot := fixture.snapshot(t)
			fixture.store = New(fixture.db, withFailpoint(func(got string) error {
				if got == point {
					return errors.New("stop")
				}
				return nil
			}))
			if _, err := fixture.store.ReplaceCanonTextChapterForClaim(context.Background(), ReplaceCanonTextChapterInput{Snapshot: snapshot, IndexedAt: fixture.now}); err == nil {
				t.Fatal("replace succeeded at failpoint")
			}
			if after := canonicalChapterRows(t, fixture); !reflect.DeepEqual(after, before) {
				t.Fatalf("rollback changed old chapter row: %v -> %v", before, after)
			}
			var revision int64
			if err := fixture.db.Pool.QueryRow(context.Background(), `SELECT source_revision FROM chapter_text_index_state WHERE project_id=$1 AND source_view='draft' AND chapter_id=$2`, fixture.projectID, fixture.chapterID).Scan(&revision); err != nil {
				t.Fatal(err)
			}
			if revision != 1 {
				t.Fatalf("rolled back state revision = %d, want 1", revision)
			}
		})
	}
}

func TestStoreReplacesDifferentChaptersForOneClaimConcurrently(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fixture := newStoreFixture(t, 7680, governance.SourceViewDraft)
	descriptors, err := fixture.store.ListCanonTextChapterDescriptorsForClaim(ctx, fixture.claim)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 2 {
		t.Fatalf("descriptors = %d, want 2", len(descriptors))
	}
	inputs := make([]ReplaceCanonTextChapterInput, len(descriptors))
	for index, descriptor := range descriptors {
		snapshot, err := fixture.store.LoadCanonTextChapterSnapshotForClaim(ctx, fixture.claim, descriptor)
		if err != nil {
			t.Fatal(err)
		}
		inputs[index] = ReplaceCanonTextChapterInput{
			Snapshot: snapshot, IndexedAt: fixture.now,
		}
	}

	reached := make(chan struct{}, len(inputs))
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()
	store := New(fixture.db, withFailpoint(func(point string) error {
		if point == "after_state_upsert" {
			reached <- struct{}{}
			<-release
		}
		return nil
	}))
	results := make(chan error, len(inputs))
	for _, input := range inputs {
		go func() {
			_, err := store.ReplaceCanonTextChapterForClaim(ctx, input)
			results <- err
		}()
	}
	for range inputs {
		select {
		case <-reached:
		case <-ctx.Done():
			t.Fatal("chapter replacements did not overlap before commit")
		}
	}
	releaseAll()
	for range inputs {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

func TestStoreChapterReplaceSerializesCancelMutation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fixture := newStoreFixture(t, 7690, governance.SourceViewDraft)
	snapshot := fixture.snapshot(t)
	reached, release := make(chan struct{}), make(chan struct{})
	var pauseOnce, releaseOnce sync.Once
	releaseReplace := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseReplace()
	store := New(fixture.db, withFailpoint(func(point string) error {
		if point == "after_state_upsert" {
			pauseOnce.Do(func() { close(reached); <-release })
		}
		return nil
	}))
	replaceResult := make(chan error, 1)
	go func() {
		_, err := store.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{
			Snapshot: snapshot, IndexedAt: fixture.now,
		})
		replaceResult <- err
	}()
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal("chapter replacement did not reach post-row pause")
	}
	cancelResult := make(chan error, 1)
	go func() {
		_, err := jobstore.New(fixture.db).CancelJobStatus(ctx, jobs.CancelJobStatusStoreInput{
			ActorUserID: fixture.actorID, ProjectID: fixture.projectID, JobID: fixture.claim.JobID,
			From: jobs.StatusRunning, To: jobs.StatusCanceling, UpdatedAt: fixture.now.Add(time.Hour),
		})
		cancelResult <- err
	}()
	waitForCanonicalDBContender(t, ctx, fixture.db, cancelResult)
	releaseReplace()
	if err := <-replaceResult; err != nil {
		t.Fatal(err)
	}
	if err := <-cancelResult; err != nil {
		t.Fatal(err)
	}
	if got := jobStatusForStoreTest(t, fixture.db, fixture.claim.JobID); got != jobs.StatusCanceling {
		t.Fatalf("job status = %q, want canceling", got)
	}
}

func TestStoreConcurrentReaderSeesAtomicCommitOrRollback(t *testing.T) {
	injected := errors.New("rollback replacement")
	for index, tc := range []struct {
		name    string
		failure error
	}{
		{name: "commit"},
		{name: "rollback", failure: injected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			fixture := newStoreFixture(t, 7700+index*20, governance.SourceViewDraft)
			oldSnapshot := fixture.snapshot(t)
			if _, err := fixture.store.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{Snapshot: oldSnapshot, IndexedAt: fixture.now}); err != nil {
				t.Fatal(err)
			}
			reader, err := fixture.db.Pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Release()
			oldSet := readCanonicalStoredSet(t, reader, fixture)
			if _, err := fixture.db.Pool.Exec(ctx, `INSERT INTO chapter_paragraphs (id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count,created_at,updated_at) SELECT id,chapter_id,2,variant,kind,"order",'replacement text','replacement text',2,created_at,updated_at FROM chapter_paragraphs WHERE chapter_id=$1 AND variant='draft' AND revision=1`, fixture.chapterID); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.db.Pool.Exec(ctx, `UPDATE chapter_source_members SET source_revision=2 WHERE project_id=$1 AND source_view='draft' AND chapter_id=$2`, fixture.projectID, fixture.chapterID); err != nil {
				t.Fatal(err)
			}
			newSnapshot := fixture.snapshot(t)
			reached, release := make(chan struct{}), make(chan struct{})
			var pauseOnce, releaseOnce sync.Once
			releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseWriter()
			fixture.store = New(fixture.db, withFailpoint(func(point string) error {
				if point == "after_state_upsert" {
					pauseOnce.Do(func() { close(reached); <-release })
					return tc.failure
				}
				return nil
			}))
			result := make(chan error, 1)
			go func() {
				_, err := fixture.store.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{Snapshot: newSnapshot, IndexedAt: fixture.now.Add(time.Hour)})
				result <- err
			}()
			select {
			case <-reached:
			case <-ctx.Done():
				t.Fatal("replacement did not reach post-row pause")
			}
			if got := readCanonicalStoredSet(t, reader, fixture); !reflect.DeepEqual(got, oldSet) {
				t.Fatalf("concurrent reader saw partial replacement: %#v, want %#v", got, oldSet)
			}
			releaseWriter()
			err = <-result
			if tc.failure != nil {
				if !errors.Is(err, tc.failure) {
					t.Fatalf("replacement error = %v, want %v", err, tc.failure)
				}
				if got := readCanonicalStoredSet(t, reader, fixture); !reflect.DeepEqual(got, oldSet) {
					t.Fatalf("rollback published replacement: %#v, want %#v", got, oldSet)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := canonicalStoredSetForSnapshot(newSnapshot)
			if got := readCanonicalStoredSet(t, reader, fixture); !reflect.DeepEqual(got, want) {
				t.Fatalf("committed replacement = %#v, want %#v", got, want)
			}
		})
	}
}

func TestStoreRaceWithSourceEditRejectsStaleReplace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fixture := newStoreFixture(t, 7750, governance.SourceViewDraft)
	staleSnapshot := fixture.snapshot(t)
	reached, release := make(chan struct{}), make(chan struct{})
	var pauseOnce, releaseOnce sync.Once
	releaseEdit := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseEdit()
	editResult := make(chan error, 1)
	go func() {
		editResult <- fixture.db.WithTx(ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT lifecycle_generation FROM projects WHERE id=$1 FOR SHARE`, fixture.projectID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `SELECT lifecycle_generation FROM books WHERE project_id=$1 AND id=$2 FOR UPDATE`, fixture.projectID, fixture.bookID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `SELECT lifecycle_generation FROM chapters WHERE project_id=$1 AND book_id=$2 AND id=$3 FOR UPDATE`, fixture.projectID, fixture.bookID, fixture.chapterID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE chapter_source_members SET source_revision=2 WHERE project_id=$1 AND source_view='draft' AND chapter_id=$2`, fixture.projectID, fixture.chapterID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE project_source_views SET catalog_revision=catalog_revision+1 WHERE project_id=$1 AND source_view='draft'`, fixture.projectID); err != nil {
				return err
			}
			pauseOnce.Do(func() { close(reached); <-release })
			return nil
		})
	}()
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal("source edit did not reach catalog mutation pause")
	}
	replaceStarted := make(chan struct{})
	replaceResult := make(chan error, 1)
	go func() {
		close(replaceStarted)
		_, err := fixture.store.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{Snapshot: staleSnapshot, IndexedAt: fixture.now.Add(time.Hour)})
		replaceResult <- err
	}()
	<-replaceStarted
	waitForCanonicalDBContender(t, ctx, fixture.db, replaceResult)
	releaseEdit()
	if err := <-editResult; err != nil {
		t.Fatalf("source edit: %v", err)
	}
	if err := <-replaceResult; !errors.Is(err, ErrTextIndexConflict) {
		t.Fatalf("stale replacement error = %v, want conflict", err)
	}
	assertNoCanonicalIndexRows(t, fixture)
}

func TestStoreRaceCancelAgainstTerminalClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fixture := newStoreFixture(t, 7770, governance.SourceViewDraft)
	indexCurrentCanonTextChaptersForTest(t, ctx, fixture)
	reached, release := make(chan struct{}), make(chan struct{})
	var pauseOnce, releaseOnce sync.Once
	releaseTerminal := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseTerminal()
	terminalStore := New(fixture.db, withFailpoint(func(point string) error {
		if point == "after_terminal_job" {
			pauseOnce.Do(func() { close(reached); <-release })
		}
		return nil
	}))
	terminalResult := make(chan error, 1)
	go func() {
		terminalResult <- terminalStore.CompleteCanonTextIndexForClaim(ctx, fixture.claim, canonTextSubjectForStoreTest(fixture))
	}()
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal("terminal transaction did not reach job CAS pause")
	}
	cancelStarted := make(chan struct{})
	cancelResult := make(chan error, 1)
	go func() {
		close(cancelStarted)
		_, err := jobstore.New(fixture.db).CancelJobStatus(ctx, jobs.CancelJobStatusStoreInput{
			ActorUserID: fixture.actorID, ProjectID: fixture.projectID, JobID: fixture.claim.JobID,
			From: jobs.StatusRunning, To: jobs.StatusCanceling, UpdatedAt: fixture.now.Add(time.Hour),
		})
		cancelResult <- err
	}()
	<-cancelStarted
	waitForCanonicalDBContender(t, ctx, fixture.db, cancelResult)
	releaseTerminal()
	if err := <-terminalResult; err != nil {
		t.Fatalf("terminal completion: %v", err)
	}
	if err := <-cancelResult; !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("cancel error = %v, want conflict", err)
	}
	if got := jobStatusForStoreTest(t, fixture.db, fixture.claim.JobID); got != jobs.StatusSucceeded {
		t.Fatalf("terminal status = %q, want succeeded", got)
	}
}
