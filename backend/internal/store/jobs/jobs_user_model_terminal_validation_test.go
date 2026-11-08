package jobstore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	canonstore "canonkeeper/backend/internal/store/canon"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestTombstoneSourceDeletedUserModelJobAfterInputRowPurged(t *testing.T) {
	fixture, _ := newUserModelStartFixture(t)
	const (
		paragraphID = "00000000-0000-4000-8000-000000009833"
		content     = "current chapter"
	)
	vector := governance.SourceGenerationVector{
		ProjectID: fixture.projectID, ProjectGeneration: 1,
		BookID: fixture.bookID, BookGeneration: 1,
		ChapterID: fixture.chapterID, ChapterGeneration: 1,
	}
	generation, err := governance.SourceGenerationDigest(vector)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := governance.PresentSourceView(governance.SourceViewDraft)
	chat, _ := governance.PresentChatArm(fixture.projectID, userModelStartChatID, 1)
	chapterHash, err := governance.CanonicalChapterContentDigest([]governance.CanonicalParagraphContent{{
		ParagraphID: paragraphID, Order: 0, Content: content,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewExactChapterArtifactRef(governance.ChapterSourceRefInput{
		Source: vector, SourceRevision: 1, SourceView: view,
		SourceGenerationDigest: generation, ContentHash: chapterHash, Chat: chat,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		statements := []struct {
			query string
			args  []any
		}{
			{`INSERT INTO book_source_members (
			   project_id, source_view, project_lifecycle_generation, book_id, book_lifecycle_generation, title
			 ) VALUES ($1,'draft',1,$2,1,'Model Book')`, []any{fixture.projectID, fixture.bookID}},
			{`INSERT INTO chapter_source_members (
			   project_id, source_view, project_lifecycle_generation, book_id, book_lifecycle_generation,
			   chapter_id, chapter_lifecycle_generation, source_revision, title, chapter_order
			 ) VALUES ($1,'draft',1,$2,1,$3,1,1,'Model Chapter',1)`,
				[]any{fixture.projectID, fixture.bookID, fixture.chapterID}},
			{`INSERT INTO chapter_paragraphs (
			   id, chapter_id, revision, variant, kind, "order", markdown, plain_text, word_count
			 ) VALUES ($1,$2,1,'draft','paragraph',0,$3,$3,2)`, []any{paragraphID, fixture.chapterID, content}},
			{`INSERT INTO chapter_text_search_rows (
			   project_id, source_view, book_id, chapter_id, source_revision,
			   locator_source_generation, title, content, chapter_content_hash, indexed_at
			 ) VALUES ($1,'draft',$2,$3,1,$4,'Model Chapter',$5,$6,clock_timestamp())`,
				[]any{fixture.projectID, fixture.bookID, fixture.chapterID, generation[:], content, chapterHash[:]}},
		}
		for _, statement := range statements {
			if _, err := tx.Exec(fixture.ctx, statement.query, statement.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	source := jobs.JobSourceManifest{SourceView: view, Refs: []governance.ArtifactSourceRef{ref}}
	lockTarget := fixture.userModelTargetLocker(source, "source-deleted-input-purged", nil)
	started, err := runUserModelStart(
		fixture,
		fixture.userModelStartInput("source-deleted-input-purged"),
		func(ctx context.Context, tx pgx.Tx) (UserModelLockedTarget, error) {
			target, err := lockTarget(ctx, tx)
			target.ManifestVersion = 2
			return target, err
		},
		fixture.userModelProductCreator(2, nil),
	)
	if err != nil {
		t.Fatalf("StartUserModelJobInTx() error = %v", err)
	}
	var ownerID pgtype.UUID
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT artifact_source_owner_id
		FROM agent_workflow_inputs
		WHERE project_id = $1 AND id = $2 AND job_id = $3
	`, fixture.projectID, started.WorkflowInputID, started.Job.ID).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}

	var projectGeneration int64
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT lifecycle_generation FROM projects WHERE id = $1
	`, fixture.projectID).Scan(&projectGeneration); err != nil {
		t.Fatal(err)
	}
	deletedAt := time.Now().UTC().Truncate(time.Microsecond)
	conn, err := fixture.db.Pool.Acquire(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(fixture.ctx, `SET session_replication_role = replica`); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	_, updateErr := conn.Exec(fixture.ctx, `
		UPDATE projects
		SET deleted_at = $2::timestamptz,
		    governance_retention_started_at = $2::timestamptz,
		    purge_after = $2::timestamptz + interval '7 days',
		    purge_deadline = $2::timestamptz + interval '8 days',
		    lifecycle_generation = lifecycle_generation + 1,
		    revision = revision + 1,
		    updated_at = $2::timestamptz
		WHERE id = $1
	`, fixture.projectID, deletedAt)
	_, resetErr := conn.Exec(fixture.ctx, `SET session_replication_role = origin`)
	conn.Release()
	if updateErr != nil || resetErr != nil {
		t.Fatalf("delete project source fixture: update=%v reset=%v", updateErr, resetErr)
	}
	if err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		return canonstore.New(fixture.db).RevokeExactSourceArtifactOwnersAtFence(
			fixture.ctx,
			tx,
			governance.DeletionSelectionProject,
			governance.SourceGenerationVector{
				ProjectID: fixture.projectID, ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
			},
			deletedAt,
		)
	}); err != nil {
		t.Fatalf("fence private input source owner: %v", err)
	}
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `
		DELETE FROM agent_workflow_inputs
		WHERE project_id = $1 AND id = $2 AND job_id = $3
	`, fixture.projectID, started.WorkflowInputID, started.Job.ID); err != nil {
		t.Fatalf("purge workflow input row: %v", err)
	}

	err = fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		return fixture.store.TombstoneSourceDeletedModelJobInTx(
			fixture.ctx, tx, fixture.projectID, started.Job.ID,
		)
	})
	if err != nil {
		t.Fatalf("TombstoneSourceDeletedModelJobInTx() error = %v", err)
	}

	var inputCount, ownerCount, refCount, bindingCount int
	var status, payloadState string
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT
		  (SELECT count(*) FROM agent_workflow_inputs WHERE project_id = $1 AND job_id = $2),
		  (SELECT count(*) FROM artifact_source_owners WHERE project_id = $1 AND id = $3),
		  (SELECT count(*) FROM artifact_source_refs WHERE project_id = $1 AND owner_id = $3),
		  (SELECT count(*) FROM job_model_bindings WHERE project_id = $1 AND job_id = $2),
		  (SELECT status FROM jobs WHERE project_id = $1 AND id = $2),
		  (SELECT governance_payload_state FROM jobs WHERE project_id = $1 AND id = $2)
	`, fixture.projectID, started.Job.ID, ownerID).Scan(
		&inputCount, &ownerCount, &refCount, &bindingCount, &status, &payloadState,
	); err != nil {
		t.Fatal(err)
	}
	if inputCount != 0 || ownerCount != 0 || refCount != 0 || bindingCount != 0 ||
		status != string(jobs.StatusExpired) || payloadState != "source_deleted" {
		t.Fatalf(
			"purged-input state input/owner/ref/binding/status/payload = %d/%d/%d/%d/%q/%q",
			inputCount, ownerCount, refCount, bindingCount, status, payloadState,
		)
	}
}
