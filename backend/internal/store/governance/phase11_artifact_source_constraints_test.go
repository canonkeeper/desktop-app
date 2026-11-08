package governancestore

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

const (
	phase11ConstraintChatID       = "00000000-0000-4000-8000-000000000d01"
	phase11ConstraintTurnID       = "00000000-0000-4000-8000-000000000d02"
	phase11ConstraintInputID      = "00000000-0000-4000-8000-000000000d05"
	phase11ConstraintJobID        = "00000000-0000-4000-8000-000000000d06"
	phase11ConstraintJobOwnerID   = "00000000-0000-4000-8000-000000000d07"
	phase11ConstraintJobRefID     = "00000000-0000-4000-8000-000000000d08"
	phase11ConstraintTurnOwnerID  = "00000000-0000-4000-8000-000000000d09"
	phase11ConstraintTurnRefID    = "00000000-0000-4000-8000-000000000d0a"
	phase11ConstraintInputOwnerID = "00000000-0000-4000-8000-000000000d0d"
	phase11ConstraintInputRefID   = "00000000-0000-4000-8000-000000000d0e"
)

func TestPhase11ChatStartOwnerManifestConstraints(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	tx, err := db.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	insertPhase11ResearchChatConstraintFixture(t, tx)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit V1 research Chat start: %v", err)
	}
}

func insertPhase11ResearchChatConstraintFixture(t *testing.T, tx pgx.Tx) {
	t.Helper()
	insertProjectScopeArtifactOwner(t, tx, phase11ConstraintJobOwnerID, phase11ConstraintJobRefID, phase11ConstraintJobID, 1)
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO chat_sessions (id, project_id, source_view, scope, title)
		VALUES ($1, $2, 'draft', 'project', 'Phase 11 constraint test')
	`, phase11ConstraintChatID, artifactConstraintProjectID)
	insertPhase11ChatStartOwner(t, tx, "chat_turn", phase11ConstraintTurnOwnerID, phase11ConstraintTurnRefID, phase11ConstraintTurnID)
	insertPhase11ChatStartOwner(t, tx, "agent_workflow_input", phase11ConstraintInputOwnerID, phase11ConstraintInputRefID, phase11ConstraintInputID)

	for _, ownerID := range []string{phase11ConstraintJobOwnerID, phase11ConstraintTurnOwnerID, phase11ConstraintInputOwnerID} {
		setArtifactOwnerManifestHashFromRefs(t, tx, ownerID, artifactConstraintProjectID)
	}

	execArtifactConstraintSQL(t, tx, `
		INSERT INTO jobs (
			id, project_id, kind, status, progress_json,
			artifact_source_owner_id, governance_payload_state, subject_json,
			creator_user_id, can_cancel, queue_class, retry_count, max_attempts,
			next_attempt_at
		) VALUES (
			$1, $2, 'research_chat_turn', 'queued', jsonb_build_object('value', 0),
			$3, 'active', jsonb_build_object('type', 'research_chat_turn', 'projectId', $2::uuid::text),
			$4, true, 'interactive', 0, 3, statement_timestamp()
		)
	`, phase11ConstraintJobID, artifactConstraintProjectID, phase11ConstraintJobOwnerID, artifactConstraintUserID)
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO agent_workflow_inputs (
			id, project_id, job_id, job_kind, target_id, source_view,
			body_json, body_digest, artifact_source_owner_id
		) VALUES ($1, $2, $3, 'research_chat_turn', $4, 'draft',
			'{"body":"question"}', decode(repeat('11', 32), 'hex'), $5)
	`, phase11ConstraintInputID, artifactConstraintProjectID, phase11ConstraintJobID, phase11ConstraintTurnID, phase11ConstraintInputOwnerID)
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO chat_turns (
			id, chat_id, project_id, source_view, job_id, workflow_input_id,
			artifact_source_owner_id
		) VALUES ($1, $2, $3, 'draft', $4, $5, $6)
	`, phase11ConstraintTurnID, phase11ConstraintChatID, artifactConstraintProjectID,
		phase11ConstraintJobID, phase11ConstraintInputID, phase11ConstraintTurnOwnerID)
}

func insertPhase11ChatStartOwner(t *testing.T, tx pgx.Tx, kind, ownerID, refID, artifactID string) {
	t.Helper()
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO artifact_source_owners (
			id, project_id, artifact_kind, artifact_id, source_view,
			manifest_version, manifest_hash, ref_count, read_state,
			source_identity_state
		) VALUES ($1, $2, $3, $4, 'draft', 1,
			decode(repeat('44', 32), 'hex'), 1, 'active', 'scope_only')
	`, ownerID, artifactConstraintProjectID, kind, artifactID)
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO artifact_source_refs (
			id, owner_id, project_id, ref_kind, scope_kind, source_view,
			project_generation, source_independent, chat_session_id,
			chat_lifecycle_epoch
		) VALUES ($1, $2, $3, 'scope', 'project', 'draft',
			1, false, $4, 1)
	`, refID, ownerID, artifactConstraintProjectID, phase11ConstraintChatID)
}
