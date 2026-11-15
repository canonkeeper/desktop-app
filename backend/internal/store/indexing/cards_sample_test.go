package indexingstore

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// specs/2026-11-13-desk-consent.md CN2.2, CN2.3: the sample of three chapters is a cards Job of its own — the cards Job the
// index or the catch-up queues does not take it and it does not take that one; one sample of a saga lives at a time; the
// rule of one cards Job of a saga in work is the claim's, as before.
func TestTheSampleIsACardsJobOfItsOwn(t *testing.T) {
	ctx := context.Background()
	fixture := newStoreFixture(t, 8200, governance.SourceViewPublished)
	indexCurrentCanonTextChaptersForTest(t, ctx, fixture)
	sample := jobs.CardsSample{Host: "api.deepseek.com", Chapters: []jobs.SampleChapter{{ChapterID: "00000000-0000-4000-8000-000000000101", ContentHash: "hash"}}}
	queue := func() (jobs.Job, error) {
		var job jobs.Job
		err := pgx.BeginFunc(ctx, fixture.db.Pool, func(tx pgx.Tx) error {
			var err error
			job, err = fixture.store.QueueCardsSampleInTx(ctx, tx, fixture.projectID, sample)
			return err
		})
		return job, err
	}
	first, err := queue()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue(); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("a second live sample = %v; want ErrConflict", err)
	}
	if queued, err := fixture.store.QueueChapterCards(ctx, fixture.projectID, governance.SourceViewPublished); err != nil || !queued {
		t.Fatalf("the cards Job beside the sample = %v, %v", queued, err)
	}
	if queued, err := fixture.store.QueueChapterCards(ctx, fixture.projectID, governance.SourceViewPublished); err != nil || !queued {
		t.Fatalf("the cards Job again = %v, %v", queued, err)
	}
	var samples, others int
	if err := fixture.db.Pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE subject_json ? 'sample'), count(*) FILTER (WHERE NOT (subject_json ? 'sample'))
		FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND status = 'queued'`, fixture.projectID).Scan(&samples, &others); err != nil {
		t.Fatal(err)
	}
	if samples != 1 || others != 1 {
		t.Fatalf("queued: %d samples, %d cards Jobs; want one of each", samples, others)
	}
	id, _ := storedb.ParseUUID(first.ID)
	var kept []byte
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT subject_json -> 'sample' FROM jobs WHERE id = $1`, id).Scan(&kept); err != nil || len(kept) == 0 {
		t.Fatalf("the subject of the sample = %s, %v", kept, err)
	}
}

// specs/2026-11-13-desk-consent.md CN2.2; AGENTS.md: migration 0148 takes the Jobs there are — the cards Jobs that wait for
// both text versions and the index Job that runs —, keeps them, and its indexes hold on them: the cards Job that waits is
// reused, the sample is a Job of its own beside it.
func TestMigration0148TakesTheJobsThereAre(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDBWithTracer(t, "ck_store_0148", nil, 147)
	fixture := newStoreFixtureOn(t, db, 8300, governance.SourceViewPublished)
	indexCurrentCanonTextChaptersForTest(t, ctx, fixture)
	for _, view := range []governance.SourceView{governance.SourceViewPublished, governance.SourceViewDraft} {
		if queued, err := fixture.store.QueueChapterCards(ctx, fixture.projectID, view); err != nil || !queued {
			t.Fatalf("the cards Job of %s before 0148 = %v, %v", view, queued, err)
		}
	}
	jobsOf := func() (cards, running int) {
		t.Helper()
		if err := db.Pool.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE kind = 'chapter_cards' AND status = 'queued'), count(*) FILTER (WHERE status = 'running')
			FROM jobs WHERE project_id = $1`, fixture.projectID).Scan(&cards, &running); err != nil {
			t.Fatal(err)
		}
		return cards, running
	}
	cards, running := jobsOf()
	if cards != 2 || running == 0 {
		t.Fatalf("before 0148: %d cards Jobs wait, %d Jobs run", cards, running)
	}
	if err := testutil.ApplyNextMigration(t, db, 147); err != nil {
		t.Fatal(err)
	}
	if after, runningAfter := jobsOf(); after != cards || runningAfter != running {
		t.Fatalf("after 0148: %d cards Jobs wait, %d run; want %d, %d", after, runningAfter, cards, running)
	}
	if queued, err := fixture.store.QueueChapterCards(ctx, fixture.projectID, governance.SourceViewPublished); err != nil || !queued {
		t.Fatalf("the cards Job after 0148 = %v, %v", queued, err)
	}
	if err := pgx.BeginFunc(ctx, db.Pool, func(tx pgx.Tx) error {
		_, err := fixture.store.QueueCardsSampleInTx(ctx, tx, fixture.projectID, jobs.CardsSample{Host: "api.example.test",
			Chapters: []jobs.SampleChapter{{ChapterID: fixture.chapterID, ContentHash: "hash"}}})
		return err
	}); err != nil {
		t.Fatalf("the sample after 0148 = %v", err)
	}
	if after, _ := jobsOf(); after != cards+1 {
		t.Fatalf("after 0148 and the sample: %d cards Jobs wait; want %d — the one of each version reused, the sample beside", after, cards+1)
	}
}
