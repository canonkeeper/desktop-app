// SPDX-License-Identifier: AGPL-3.0-only

package indexingstore

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// specs/2026-11-14-desk-journal.md JR1.8, JR2.6 (the third check, C4): migration 0155 over a journal with rows — the requests
// of a sample of three chapters, of a cards Job, of the chat and of a check — keeps every row of a request, gives none of
// them a number of a check or a class of a failure, and carries the requests of the sample alone into their own table with
// what they spent; the Jobs there are, whatever their state, stay.
func TestMigration0155MovesTheRequestsOfTheSample(t *testing.T) {
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDBWithTracer(t, "ck_store_0155", nil, 154)
	fixture := newStoreFixtureOn(t, db, 8400, governance.SourceViewPublished)
	indexCurrentCanonTextChaptersForTest(t, ctx, fixture)
	var sample jobs.Job
	if err := pgx.BeginFunc(ctx, db.Pool, func(tx pgx.Tx) error {
		var err error
		sample, err = fixture.store.QueueCardsSampleInTx(ctx, tx, fixture.projectID, jobs.CardsSample{Host: "provider.example.test",
			Chapters: []jobs.SampleChapter{{ChapterID: fixture.chapterID, ContentHash: "hash"}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if queued, err := fixture.store.QueueChapterCards(ctx, fixture.projectID, governance.SourceViewPublished); err != nil || !queued {
		t.Fatalf("the cards Job = %v, %v", queued, err)
	}
	var cards string
	if err := db.Pool.QueryRow(ctx, `SELECT id::text FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND NOT (subject_json ? 'sample')`,
		fixture.projectID).Scan(&cards); err != nil {
		t.Fatal(err)
	}
	// The rows of the journal of a version before 0155, as its gateway wrote them: no table keeps them otherwise.
	for index, row := range []struct {
		job, purpose string
		tokens       int64
	}{{sample.ID, "card", 1000}, {sample.ID, "card_pass", 300}, {cards, "card", 900}, {"", "chat", 50}, {"", "probe", 5}} {
		var job, project any
		if row.job != "" {
			job, project = row.job, fixture.projectID
		}
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO model_requests (at, project_id, job_id, purpose, profile_name, provider_host, model, input_tokens, cached_input_tokens,
			  output_tokens, outcome, duration_ms, request_bytes, cost_micros, currency, estimated)
			VALUES (now() + make_interval(secs => $1), $2, $3, $4, 'own', 'provider.example.test', 'north-cards', $5, 10, 20, 'completed',
			  1500, 100, 700, 'RUB', false)`, index, project, job, row.purpose, row.tokens); err != nil {
			t.Fatal(err)
		}
	}
	if err := testutil.ApplyNextMigration(t, db, 154); err != nil {
		t.Fatal(err)
	}
	var rows, marked, jobsAfter int
	if err := db.Pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM model_requests), (SELECT count(*) FROM model_requests WHERE check_id IS NOT NULL OR failure IS NOT NULL),
		  (SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards')`, fixture.projectID).Scan(&rows, &marked, &jobsAfter); err != nil {
		t.Fatal(err)
	}
	if rows != 5 || marked != 0 || jobsAfter != 2 {
		t.Fatalf("after 0155: %d rows, %d marked, %d cards Jobs", rows, marked, jobsAfter)
	}
	moved, err := db.Pool.Query(ctx, `
		SELECT moved.job_id::text, moved.project_id::text, moved.input_tokens, moved.duration_ms, moved.provider_host, moved.model, moved.cost_micros, moved.currency
		FROM reading_sample_requests AS moved ORDER BY moved.at`)
	if err != nil {
		t.Fatal(err)
	}
	defer moved.Close()
	var tokens []int64
	for moved.Next() {
		var job, project, host, model, currency string
		var input, cost int64
		var duration int
		if err := moved.Scan(&job, &project, &input, &duration, &host, &model, &cost, &currency); err != nil {
			t.Fatal(err)
		}
		if job != sample.ID || project != fixture.projectID || duration != 1500 || host != "provider.example.test" || model != "north-cards" || cost != 700 || currency != "RUB" {
			t.Fatalf("a request of the sample = %s %s %d %s %s %d %s", job, project, duration, host, model, cost, currency)
		}
		tokens = append(tokens, input)
	}
	if len(tokens) != 2 || tokens[0] != 1000 || tokens[1] != 300 {
		t.Fatalf("the requests of the sample carried over = %v", tokens)
	}
}
