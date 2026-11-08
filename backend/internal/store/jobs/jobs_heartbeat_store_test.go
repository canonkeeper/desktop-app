package jobstore

import (
	"errors"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"

	"github.com/jackc/pgx/v5"
)

func TestHeartbeatWorkerClaimUsesLifecycleReadabilityOnlyForAgentJobs(t *testing.T) {
	tests := []struct {
		kind          jobs.Kind
		wantHeartbeat bool
	}{
		{kind: jobs.KindResearchChatTurn, wantHeartbeat: true},
		{kind: jobs.KindCanonTextIndex, wantHeartbeat: false},
	}

	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			fixture := newModelTerminalFixture(t, jobs.StatusRunning)
			configureHeartbeatJobKindForTest(t, fixture, test.kind)
			makeHeartbeatOwnerLifecycleReadableOnly(t, fixture)

			err := New(fixture.db).HeartbeatWorkerClaim(fixture.ctx, fixture.claim())
			if test.wantHeartbeat && err != nil {
				t.Fatalf("HeartbeatWorkerClaim() error = %v", err)
			}
			if !test.wantHeartbeat && !errors.Is(err, jobs.ErrConflict) {
				t.Fatalf("HeartbeatWorkerClaim() error = %v, want ErrConflict", err)
			}
			if test.kind == jobs.KindResearchChatTurn {
				fenceHeartbeatOwnerForTest(t, fixture)
				if err := New(fixture.db).HeartbeatWorkerClaim(fixture.ctx, fixture.claim()); !errors.Is(err, jobs.ErrConflict) {
					t.Fatalf("HeartbeatWorkerClaim(fenced owner) error = %v, want ErrConflict", err)
				}
			}
		})
	}
}

func configureHeartbeatJobKindForTest(t *testing.T, fixture modelTerminalFixture, kind jobs.Kind) {
	t.Helper()
	switch kind {
	case jobs.KindResearchChatTurn:
		configureModelTerminalUserClaim(t, fixture, kind, jobs.ResearchChatTurnSubject{
			Type: kind, ProjectID: fixture.projectID,
			ChatID:     "00000000-0000-4000-8000-000000009842",
			TurnID:     "00000000-0000-4000-8000-000000009843",
			SourceView: governance.SourceViewDraft, Scope: "project",
		})
	case jobs.KindCanonTextIndex:
		if err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(fixture.ctx, `
				DELETE FROM job_model_bindings WHERE project_id = $1 AND job_id = $2
			`, fixture.projectID, fixture.jobID); err != nil {
				return err
			}
			_, err := tx.Exec(fixture.ctx, `
				UPDATE jobs
				SET kind = 'canon_text_index',
				    subject_json = jsonb_build_object(
				      'type', 'canon_text_index', 'projectId', project_id::text,
				      'sourceView', 'draft', 'catalogRevision', 1
				    ),
				    creator_user_id = $1, can_cancel = true, queue_class = 'user_demanded'
				WHERE project_id = $2 AND id = $3
			`, fixture.actorID, fixture.projectID, fixture.jobID)
			return err
		}); err != nil {
			t.Fatalf("configure canon text index: %v", err)
		}
	default:
		t.Fatalf("unsupported heartbeat test kind %q", kind)
	}
}

func makeHeartbeatOwnerLifecycleReadableOnly(t *testing.T, fixture modelTerminalFixture) {
	t.Helper()
	conn, err := fixture.db.Pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(fixture.ctx, `SET session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	// Имитируем обычное устаревание текстовой ревизии: lifecycle остаётся тем же,
	// но exact chapter больше не имеет current text-search строки.
	result, driftErr := conn.Exec(fixture.ctx, `
		WITH changed_ref AS (
		  UPDATE artifact_source_refs AS ref
		  SET ref_kind = 'chapter', scope_kind = NULL, source_view = 'draft',
		      project_generation = 1, book_id = $3, book_generation = 1,
		      chapter_id = $4, chapter_generation = 1, source_revision = 1,
		      locator_source_generation = ck_source_generation_digest(
		        $1::uuid, 1::bigint, $3::uuid, 1::bigint, $4::uuid, 1::bigint
		      ),
		      text_identity_state = 'text_derived_current', source_independent = false,
		      chat_session_id = NULL, chat_lifecycle_epoch = NULL,
		      content_hash = decode(repeat('11', 32), 'hex')
		  WHERE ref.owner_id = $2::uuid AND ref.project_id = $1::uuid
		  RETURNING ref.*
		), expected AS (
		  SELECT array_agg(
		    ck_artifact_source_ref_projection_v2(
		      ref_kind, scope_kind, project_id, project_generation,
		      book_id, book_generation, chapter_id, chapter_generation,
		      source_revision, source_view, locator_source_generation, content_hash,
		      text_identity_state, source_independent, chat_session_id, chat_lifecycle_epoch
		    ) ORDER BY ref_kind, chapter_id
		  ) AS ref_projections
		  FROM changed_ref
		), changed_owner AS (
		  UPDATE artifact_source_owners
		  SET manifest_version = 2, source_identity_state = 'text_derived_current',
		      manifest_hash = ck_artifact_manifest_digest_v2(
		        'job', $1::uuid, $5::uuid, 'draft', (SELECT ref_projections FROM expected)
		      )
		  WHERE project_id = $1 AND id = $2
		  RETURNING id
		)
		SELECT count(*) FROM changed_owner
	`, fixture.projectID, fixture.jobOwnerID, fixture.bookID, fixture.chapterID, fixture.jobID)
	_, resetErr := conn.Exec(fixture.ctx, `SET session_replication_role = origin`)
	if driftErr != nil || resetErr != nil {
		t.Fatalf("drift heartbeat owner: update=%v reset=%v", driftErr, resetErr)
	}
	if result.RowsAffected() != 1 {
		t.Fatalf("drifted refs = %d, want 1", result.RowsAffected())
	}

	var current, lifecycleReadable bool
	if err := conn.QueryRow(fixture.ctx, `
		SELECT
		  ck_artifact_source_owner_is_readable($1, $2, 'job', $3),
		  ck_artifact_source_owner_is_lifecycle_readable($1, $2, 'job', $3)
	`, fixture.jobOwnerID, fixture.projectID, fixture.jobID).Scan(&current, &lifecycleReadable); err != nil {
		t.Fatal(err)
	}
	if current || !lifecycleReadable {
		t.Fatalf("owner readability = current:%t lifecycle:%t", current, lifecycleReadable)
	}
}

func fenceHeartbeatOwnerForTest(t *testing.T, fixture modelTerminalFixture) {
	t.Helper()
	conn, err := fixture.db.Pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(fixture.ctx, `SET session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	result, fenceErr := conn.Exec(fixture.ctx, `
		UPDATE artifact_source_owners
		SET read_state = 'source_deleted', fenced_at = clock_timestamp()
		WHERE project_id = $1 AND id = $2
	`, fixture.projectID, fixture.jobOwnerID)
	_, resetErr := conn.Exec(fixture.ctx, `SET session_replication_role = origin`)
	if fenceErr != nil || resetErr != nil {
		t.Fatalf("fence heartbeat owner: update=%v reset=%v", fenceErr, resetErr)
	}
	if result.RowsAffected() != 1 {
		t.Fatalf("fenced owners = %d, want 1", result.RowsAffected())
	}
}
