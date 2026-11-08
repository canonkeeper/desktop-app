package indexingstore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/search"
	canonstore "canonkeeper/backend/internal/store/canon"

	"github.com/jackc/pgx/v5"
)

func TestStoreExactSnapshotAndAtomicReplace(t *testing.T) {
	ctx := context.Background()
	fixture := newStoreFixture(t, 7100, governance.SourceViewDraft)
	descriptors, err := fixture.store.ListCanonTextChapterDescriptorsForClaim(ctx, fixture.claim)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 2 || descriptors[0].ChapterID != fixture.chapterID {
		t.Fatalf("descriptors = %#v", descriptors)
	}
	snapshot, err := fixture.store.LoadCanonTextChapterSnapshotForClaim(ctx, fixture.claim, descriptors[0])
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Unchanged || len(snapshot.Paragraphs) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	indexedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	got, err := fixture.store.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{
		Snapshot: snapshot, IndexedAt: indexedAt,
	})
	if err != nil || got != CanonTextChapterReplaced {
		t.Fatalf("replace = %q, %v", got, err)
	}
	assertCanonicalIndexRows(t, fixture, governance.SourceViewDraft, fixture.chapterID, 1)
	assertCanonicalIndexRows(t, fixture, governance.SourceViewPublished, fixture.chapterID, 0)
	assertCanonicalIndexRows(t, fixture, governance.SourceViewDraft, fixture.siblingChapterID, 0)

	var owners, refs int
	if err := fixture.db.Pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM artifact_source_owners WHERE project_id=$1 AND artifact_kind='text_chunk' AND manifest_version=2),
		  (SELECT count(*) FROM artifact_source_refs WHERE project_id=$1 AND text_identity_state='text_derived_current')
	`, fixture.projectID).Scan(&owners, &refs); err != nil {
		t.Fatal(err)
	}
	if owners != 0 || refs != 0 {
		t.Fatalf("text-chunk owners/refs = %d/%d, want 0/0", owners, refs)
	}
	if total, indexed := claimedTextCoverageForTest(t, fixture); total != 2 || indexed != 1 {
		t.Fatalf("coverage = %d/%d, want 1 of 2 indexed", indexed, total)
	}

	second, err := fixture.store.LoadCanonTextChapterSnapshotForClaim(ctx, fixture.claim, descriptors[0])
	if err != nil || !second.Unchanged {
		t.Fatalf("second snapshot = %#v, %v", second, err)
	}
	beforeRows := canonicalChapterRows(t, fixture)
	got, err = fixture.store.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{Snapshot: second, IndexedAt: indexedAt.Add(time.Hour)})
	if err != nil || got != CanonTextChapterUnchanged {
		t.Fatalf("unchanged replace = %q, %v", got, err)
	}
	var storedAt time.Time
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT indexed_at FROM chapter_text_index_state WHERE project_id=$1 AND source_view='draft' AND chapter_id=$2`, fixture.projectID, fixture.chapterID).Scan(&storedAt); err != nil {
		t.Fatal(err)
	}
	if !storedAt.Equal(indexedAt) {
		t.Fatalf("indexed_at rewritten: %s", storedAt)
	}
	if afterRows := canonicalChapterRows(t, fixture); !reflect.DeepEqual(afterRows, beforeRows) {
		t.Fatalf("chapter row changed: %v -> %v", beforeRows, afterRows)
	}
}

func TestStoreEmptyChapterCommitsOneSearchRow(t *testing.T) {
	fixture := newStoreFixture(t, 7200, governance.SourceViewDraft)
	if _, err := fixture.db.Pool.Exec(context.Background(), `UPDATE chapter_paragraphs SET plain_text='   ', markdown='   ' WHERE chapter_id=$1 AND variant='draft'`, fixture.chapterID); err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.snapshot(t)
	got, err := fixture.store.ReplaceCanonTextChapterForClaim(context.Background(), ReplaceCanonTextChapterInput{Snapshot: snapshot, IndexedAt: fixture.now})
	if err != nil || got != CanonTextChapterReplaced {
		t.Fatalf("replace = %q, %v", got, err)
	}
	assertCanonicalIndexRows(t, fixture, governance.SourceViewDraft, fixture.chapterID, 1)
}

func TestStoreGovernanceCurrentnessUsesOwnerIndex(t *testing.T) {
	fixture := newStoreFixture(t, 7995, governance.SourceViewDraft)
	var hasIndex, functionScoped bool
	// pg_indexes would read the definition of every index in the database, including those of
	// test schemas other packages drop concurrently; only the definition of this one is read.
	if err := fixture.db.Pool.QueryRow(context.Background(), `
		SELECT COALESCE((
			SELECT pg_get_indexdef(idx.oid) LIKE '% USING btree (owner_id, project_id)'
			FROM pg_class AS idx
			JOIN pg_namespace AS ns ON ns.oid = idx.relnamespace
			WHERE ns.nspname = current_schema()
			  AND idx.relname = 'artifact_source_refs_owner_project_idx'
		), false)
	`).Scan(&hasIndex); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Pool.QueryRow(context.Background(), `
		SELECT COALESCE((
			SELECT proc.proconfig @> ARRAY['enable_seqscan=off']
			FROM pg_proc AS proc
			WHERE proc.oid = to_regprocedure('ck_validate_active_artifact_source_owner_currentness()')
		), false)
	`).Scan(&functionScoped); err != nil {
		t.Fatal(err)
	}
	if !hasIndex || !functionScoped {
		t.Fatalf("owner index = %t, function-local planner setting = %t", hasIndex, functionScoped)
	}

	snapshot := fixture.snapshot(t)
	if _, err := fixture.store.ReplaceCanonTextChapterForClaim(context.Background(), ReplaceCanonTextChapterInput{
		Snapshot: snapshot, IndexedAt: fixture.now,
	}); err != nil {
		t.Fatal(err)
	}
	assertCanonicalIndexRows(t, fixture, fixture.view, fixture.chapterID, 1)
}

func TestStoreCommitsExactTerminalLifecycle(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		fixture := newStoreFixture(t, 7250, governance.SourceViewDraft)
		indexCurrentCanonTextChaptersForTest(t, ctx, fixture)
		var ownerUpdatedBefore time.Time
		if err := fixture.db.Pool.QueryRow(ctx, `
			SELECT owner.updated_at
			FROM jobs AS job
			JOIN artifact_source_owners AS owner ON owner.id=job.artifact_source_owner_id AND owner.project_id=job.project_id
			WHERE job.project_id=$1 AND job.id=$2
		`, fixture.projectID, fixture.claim.JobID).Scan(&ownerUpdatedBefore); err != nil {
			t.Fatal(err)
		}
		if err := fixture.store.CompleteCanonTextIndexForClaim(ctx, fixture.claim, canonTextSubjectForStoreTest(fixture)); err != nil {
			t.Fatal(err)
		}
		var status string
		var resultJSON []byte
		var ownerUpdatedAfter time.Time
		if err := fixture.db.Pool.QueryRow(ctx, `
			SELECT job.status, job.result_json, owner.updated_at
			FROM jobs AS job
			JOIN artifact_source_owners AS owner ON owner.id=job.artifact_source_owner_id AND owner.project_id=job.project_id
			WHERE job.project_id=$1 AND job.id=$2
		`, fixture.projectID, fixture.claim.JobID).Scan(&status, &resultJSON, &ownerUpdatedAfter); err != nil {
			t.Fatal(err)
		}
		result, err := jobs.DecodeResult(jobs.KindCanonTextIndex, resultJSON)
		if err != nil {
			t.Fatal(err)
		}
		want := jobs.CanonTextIndexResult{Type: jobs.KindCanonTextIndex, SourceView: governance.SourceViewDraft, TextCoverage: jobs.TextCoverage{CurrentBooks: 1, TotalBooks: 1}}
		if status != string(jobs.StatusSucceeded) || result != want || !ownerUpdatedAfter.After(ownerUpdatedBefore) {
			t.Fatalf("terminal status/result/owner = %q %#v %s -> %s", status, result, ownerUpdatedBefore, ownerUpdatedAfter)
		}
		// UC2 step 1: the indexed version queues one cards Job, the draft book becomes ready and
		// no enrichment is queued.
		if got := countStoreRowsForTest(t, fixture.db, "SELECT count(*) FROM jobs WHERE project_id=$1 AND kind='canon_book_enrichment'", fixture.projectID); got != 0 {
			t.Fatalf("enrichment jobs = %d, want none", got)
		}
		if got := countStoreRowsForTest(t, fixture.db, `
			SELECT count(*) FROM jobs
			WHERE project_id=$1 AND kind='chapter_cards' AND status='queued' AND queue_class='automatic'
			  AND creator_user_id IS NULL AND subject_json->>'sourceView'='draft'
		`, fixture.projectID); got != 1 {
			t.Fatalf("queued draft cards jobs = %d, want 1", got)
		}
		if got := countStoreRowsForTest(t, fixture.db, "SELECT count(*) FROM books WHERE project_id=$1 AND status='ready'", fixture.projectID); got != 1 {
			t.Fatalf("ready books = %d, want the indexed draft book", got)
		}
	})

	t.Run("published", func(t *testing.T) {
		ctx := context.Background()
		fixture := newStoreFixture(t, 7255, governance.SourceViewPublished)
		indexCurrentCanonTextChaptersForTest(t, ctx, fixture)
		if err := fixture.store.CompleteCanonTextIndexForClaim(ctx, fixture.claim, canonTextSubjectForStoreTest(fixture)); err != nil {
			t.Fatal(err)
		}
		if got := jobStatusForStoreTest(t, fixture.db, fixture.claim.JobID); got != jobs.StatusSucceeded {
			t.Fatalf("status = %q, want succeeded", got)
		}
		if got := countStoreRowsForTest(t, fixture.db, "SELECT count(*) FROM jobs WHERE project_id=$1 AND kind='chapter_cards' AND subject_json->>'sourceView'='published'", fixture.projectID); got != 1 {
			t.Fatalf("published cards jobs = %d, want 1", got)
		}
		if got := countStoreRowsForTest(t, fixture.db, "SELECT count(*) FROM books WHERE project_id=$1 AND status='ready'", fixture.projectID); got != 0 {
			t.Fatalf("ready books after a published index = %d, want none", got)
		}

		second := addCanonicalIndexClaimForTest(t, fixture, 12)
		if err := fixture.store.CompleteCanonTextIndexForClaim(ctx, second, canonTextSubjectForStoreTest(fixture)); err != nil {
			t.Fatal(err)
		}
		if got := countStoreRowsForTest(t, fixture.db, "SELECT count(*) FROM jobs WHERE project_id=$1 AND kind='chapter_cards'", fixture.projectID); got != 1 {
			t.Fatalf("cards jobs after a second index = %d, want the waiting Job reused", got)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		ctx := context.Background()
		fixture := newStoreFixture(t, 7260, governance.SourceViewDraft)
		if _, err := fixture.db.Pool.Exec(ctx, `UPDATE jobs SET status='canceling', cancel_requested_at=clock_timestamp() WHERE project_id=$1 AND id=$2`, fixture.projectID, fixture.claim.JobID); err != nil {
			t.Fatal(err)
		}
		if err := fixture.store.CancelCanonTextIndexForClaim(ctx, fixture.claim, canonTextSubjectForStoreTest(fixture)); err != nil {
			t.Fatal(err)
		}
		if got := jobStatusForStoreTest(t, fixture.db, fixture.claim.JobID); got != jobs.StatusCanceled {
			t.Fatalf("status = %q, want canceled", got)
		}
	})

	t.Run("safe_failure", func(t *testing.T) {
		ctx := context.Background()
		fixture := newStoreFixture(t, 7270, governance.SourceViewDraft)
		failure := jobs.Error{Code: jobs.RetryExhaustedCode, Message: jobs.RetryExhaustedMessage}
		if err := fixture.store.FailCanonTextIndexForClaim(ctx, fixture.claim, canonTextSubjectForStoreTest(fixture), failure); err != nil {
			t.Fatal(err)
		}
		var status string
		var errorJSON []byte
		if err := fixture.db.Pool.QueryRow(ctx, `SELECT status,error_json FROM jobs WHERE project_id=$1 AND id=$2`, fixture.projectID, fixture.claim.JobID).Scan(&status, &errorJSON); err != nil {
			t.Fatal(err)
		}
		got, err := jobs.DecodeError(errorJSON)
		if err != nil || status != string(jobs.StatusFailed) || got != failure {
			t.Fatalf("terminal failure = %q %#v, %v", status, got, err)
		}
	})

	t.Run("catalog_drift", func(t *testing.T) {
		ctx := context.Background()
		fixture := newStoreFixture(t, 7280, governance.SourceViewDraft)
		if _, err := fixture.db.Pool.Exec(ctx, `UPDATE project_source_views SET catalog_revision=2 WHERE project_id=$1 AND source_view=$2`, fixture.projectID, fixture.view); err != nil {
			t.Fatal(err)
		}
		err := fixture.store.CompleteCanonTextIndexForClaim(ctx, fixture.claim, canonTextSubjectForStoreTest(fixture))
		if !errors.Is(err, ErrTextIndexConflict) {
			t.Fatalf("CompleteCanonTextIndexForClaim() error = %v, want conflict", err)
		}
		if got := jobStatusForStoreTest(t, fixture.db, fixture.claim.JobID); got != jobs.StatusRunning {
			t.Fatalf("status after drift = %q, want running", got)
		}
	})

	t.Run("live chapter generation drift", func(t *testing.T) {
		fixture := newStoreFixture(t, 7290, governance.SourceViewDraft)
		if _, err := fixture.db.Pool.Exec(context.Background(), `UPDATE chapters SET lifecycle_generation=2 WHERE project_id=$1 AND id=$2`, fixture.projectID, fixture.siblingChapterID); err != nil {
			t.Fatal(err)
		}
		err := fixture.store.CompleteCanonTextIndexForClaim(context.Background(), fixture.claim, canonTextSubjectForStoreTest(fixture))
		if !errors.Is(err, ErrTextIndexConflict) {
			t.Fatalf("CompleteCanonTextIndexForClaim() error = %v, want conflict", err)
		}
	})
}

func TestStoreIgnoresRetainedDeletedMembers(t *testing.T) {
	ctx := context.Background()
	fixture := newStoreFixture(t, 7295, governance.SourceViewDraft)
	softDeleteIndexingStoreChapterForTest(t, fixture.db, fixture.siblingChapterID)

	descriptors, err := fixture.store.ListCanonTextChapterDescriptorsForClaim(ctx, fixture.claim)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 || descriptors[0].ChapterID != fixture.chapterID {
		t.Fatalf("descriptors = %#v", descriptors)
	}
	if total, indexed := claimedTextCoverageForTest(t, fixture); total != 1 || indexed != 0 {
		t.Fatalf("coverage before index = %d/%d, want 0 of 1 indexed", indexed, total)
	}
	snapshot, err := fixture.store.LoadCanonTextChapterSnapshotForClaim(ctx, fixture.claim, descriptors[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{
		Snapshot: snapshot, IndexedAt: fixture.now,
	}); err != nil {
		t.Fatal(err)
	}
	if total, indexed := claimedTextCoverageForTest(t, fixture); total != 1 || indexed != 1 {
		t.Fatalf("coverage after index = %d/%d, want 1 of 1 indexed", indexed, total)
	}

	canon := canonstore.New(fixture.db)
	var selection canonstore.CanonicalTextSelection
	if err := fixture.db.WithTx(ctx, func(tx pgx.Tx) error {
		chapter, err := canon.ResolveCanonicalChapterSourceInTx(ctx, tx, fixture.projectID, fixture.view, fixture.bookID, fixture.chapterID)
		if err != nil {
			return err
		}
		if chapter.SourceRef != fixture.chapterID || chapter.ChapterPosition != 1 {
			t.Fatalf("current chapter source = %#v", chapter)
		}
		selection, err = canon.LoadCurrentCanonicalTargetTextInTx(ctx, tx, canonstore.CanonicalTextSelectionInput{
			ProjectID: fixture.projectID, SourceView: fixture.view, BookID: fixture.bookID, ChapterID: fixture.chapterID,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertCanonicalCoverageForTest(t, selection.TextCoverage, jobs.TextCoverage{CurrentBooks: 1, TotalBooks: 1})
	if !reflect.DeepEqual(selection.CurrentBookIDs, []string{fixture.bookID}) {
		t.Fatalf("current book IDs = %v", selection.CurrentBookIDs)
	}
	if err := fixture.store.CompleteCanonTextIndexForClaim(ctx, fixture.claim, canonTextSubjectForStoreTest(fixture)); err != nil {
		t.Fatal(err)
	}
	var resultJSON []byte
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT result_json FROM jobs WHERE project_id=$1 AND id=$2`, fixture.projectID, fixture.claim.JobID).Scan(&resultJSON); err != nil {
		t.Fatal(err)
	}
	result, err := jobs.DecodeResult(jobs.KindCanonTextIndex, resultJSON)
	if err != nil {
		t.Fatal(err)
	}
	wantResult := jobs.CanonTextIndexResult{
		Type: jobs.KindCanonTextIndex, SourceView: fixture.view,
		TextCoverage: jobs.TextCoverage{CurrentBooks: 1, TotalBooks: 1},
	}
	if result != wantResult {
		t.Fatalf("terminal result = %#v, want %#v", result, wantResult)
	}

	softDeleteIndexingStoreBookForTest(t, fixture.db, fixture.bookID)
	err = fixture.db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := canon.LoadCurrentCanonicalProjectTextInTx(ctx, tx, canonstore.CanonicalTextSelectionInput{
			ProjectID: fixture.projectID, SourceView: fixture.view,
		})
		return err
	})
	if !errors.Is(err, search.ErrSourceViewUnavailable) {
		t.Fatalf("project text after book delete error = %v, want no current books", err)
	}
}

// claimedTextCoverageForTest считает главы задания и уже актуальные среди них тем же
// путём, которым исполнитель решает, какие главы пропустить.
func claimedTextCoverageForTest(t *testing.T, fixture storeFixture) (total, indexed int) {
	t.Helper()
	ctx := context.Background()
	descriptors, err := fixture.store.ListCanonTextChapterDescriptorsForClaim(ctx, fixture.claim)
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		snapshot, err := fixture.store.LoadCanonTextChapterSnapshotForClaim(ctx, fixture.claim, descriptor)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Unchanged {
			indexed++
		}
	}
	return len(descriptors), indexed
}

func canonTextSubjectForStoreTest(fixture storeFixture) jobs.CanonTextIndexSubject {
	return jobs.CanonTextIndexSubject{Type: jobs.KindCanonTextIndex, ProjectID: fixture.projectID, SourceView: fixture.view, CatalogRevision: 1}
}

func TestStoreDriftAndClaimFencesWriteNothing(t *testing.T) {
	cases := []struct {
		name  string
		drift func(*storeFixture) error
	}{
		{"source_revision", func(f *storeFixture) error {
			_, err := f.db.Pool.Exec(context.Background(), `UPDATE chapter_source_members SET source_revision=2 WHERE project_id=$1 AND source_view=$2 AND chapter_id=$3`, f.projectID, f.view, f.chapterID)
			return err
		}},
		{"lifecycle_generation", func(f *storeFixture) error {
			_, err := f.db.Pool.Exec(context.Background(), `UPDATE chapters SET lifecycle_generation=2 WHERE project_id=$1 AND id=$2`, f.projectID, f.chapterID)
			return err
		}},
		{"catalog_revision", func(f *storeFixture) error {
			_, err := f.db.Pool.Exec(context.Background(), `UPDATE project_source_views SET catalog_revision=2 WHERE project_id=$1 AND source_view=$2`, f.projectID, f.view)
			return err
		}},
		{"stale_claim", func(f *storeFixture) error {
			_, err := f.db.Pool.Exec(context.Background(), `UPDATE jobs SET claim_incarnation=2 WHERE project_id=$1 AND id=$2`, f.projectID, f.claim.JobID)
			return err
		}},
		{"cancel", func(f *storeFixture) error {
			_, err := f.db.Pool.Exec(context.Background(), `UPDATE jobs SET cancel_requested_at=clock_timestamp(), status='canceling' WHERE project_id=$1 AND id=$2`, f.projectID, f.claim.JobID)
			return err
		}},
		{"delete", func(f *storeFixture) error {
			return f.db.WithTx(context.Background(), func(tx pgx.Tx) error {
				if _, err := tx.Exec(context.Background(), `UPDATE artifact_source_owners SET read_state='source_deleted', fenced_at=clock_timestamp() WHERE project_id=$1 AND artifact_id=$2 AND artifact_kind='job'`, f.projectID, f.claim.JobID); err != nil {
					return err
				}
				if _, err := tx.Exec(context.Background(), `DELETE FROM jobs WHERE project_id=$1 AND id=$2`, f.projectID, f.claim.JobID); err != nil {
					return err
				}
				_, err := tx.Exec(context.Background(), `DELETE FROM artifact_source_owners WHERE project_id=$1 AND artifact_id=$2 AND artifact_kind='job'`, f.projectID, f.claim.JobID)
				return err
			})
		}},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newStoreFixture(t, 7300+index*20, governance.SourceViewDraft)
			snapshot := fixture.snapshot(t)
			if err := tc.drift(&fixture); err != nil {
				t.Fatal(err)
			}
			_, err := fixture.store.ReplaceCanonTextChapterForClaim(context.Background(), ReplaceCanonTextChapterInput{Snapshot: snapshot, IndexedAt: fixture.now})
			if !errors.Is(err, ErrTextIndexConflict) {
				t.Fatalf("error = %v, want conflict", err)
			}
			assertNoCanonicalIndexRows(t, fixture)
		})
	}
}
