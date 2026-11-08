package canonstore

import (
	"context"
	"errors"
	"testing"
	"time"

	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
	governancestore "canonkeeper/backend/internal/store/governance"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestChapterDeletionFenceRevokesOnlyOwnersReferencingDeletedGeneration(t *testing.T) {
	fixture := newGovernedChapterFixtureForTest(t, projects.RoleEditor, false)
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	vector := governance.SourceGenerationVector{
		ProjectID:         fixture.projectID,
		ProjectGeneration: 1,
		BookID:            fixture.bookID,
		BookGeneration:    1,
		ChapterID:         fixture.chapterID,
		ChapterGeneration: 1,
	}
	chapterRef, err := governance.NewChapterArtifactRef(vector, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	projectRef, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{
		ProjectID: fixture.projectID, ProjectGeneration: 1,
	}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}

	deletedOwnerID := sealFenceTestJobArtifact(
		t, fixture.db, fixture.projectID, view, 6100,
		[]governance.ArtifactSourceRef{chapterRef, projectRef},
	)
	survivingOwnerID := sealFenceTestJobArtifact(
		t, fixture.db, fixture.projectID, view, 6200,
		[]governance.ArtifactSourceRef{projectRef},
	)

	fenceChapterDeletionForTest(t, fixture)

	assertFenceTestOwnerState(t, fixture.db, deletedOwnerID, "source_deleted", fixture.fencedAt)
	assertFenceTestOwnerState(t, fixture.db, survivingOwnerID, "active", time.Time{})
}

func sealFenceTestJobArtifact(
	t *testing.T,
	db *Store,
	projectID string,
	sourceView governance.SourceViewArm,
	idBase int,
	refs []governance.ArtifactSourceRef,
) string {
	t.Helper()
	ctx := context.Background()
	ownerID := indexingStoreUUIDForTest(idBase)
	jobID := indexingStoreUUIDForTest(idBase + 1)
	manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind:            governance.ArtifactJob,
		ProjectID:       projectID,
		ArtifactID:      jobID,
		OwnerSourceView: sourceView,
		Refs:            refs,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := governancestore.SealArtifactSourcesInput{
		OwnerID: ownerID, ProjectID: projectID, ArtifactID: jobID,
		Kind: governance.ArtifactJob, SourceView: sourceView,
		Refs: refs, Manifest: manifest,
	}
	createdAt := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return governancestore.New(db.DB).CreateArtifactWithSources(
			ctx,
			tx,
			input,
			func(ctx context.Context, tx pgx.Tx, persistedOwnerID pgtype.UUID) error {
				_, err := tx.Exec(ctx, `
					INSERT INTO jobs (
						id, project_id, kind, status, progress_json, subject_json,
						result_json, error_json, creator_user_id, can_cancel,
						queue_class, retry_count, max_attempts, next_attempt_at,
						artifact_source_owner_id, governance_payload_state,
						created_at, updated_at
					)
					SELECT $1, project.id, 'import', 'queued', jsonb_build_object('value', 0),
					       jsonb_build_object('type', 'import', 'id', project.id::text, 'projectId', project.id::text),
					       NULL, NULL, project.owner_user_id, true,
					       'user_demanded', 0, 3, $4,
					       $3, 'active', $4, $4
					FROM projects AS project
					WHERE project.id = $2
				`, jobID, projectID, persistedOwnerID, createdAt)
				return err
			},
		)
	}); err != nil {
		t.Fatalf("seal fence test artifact: %v", err)
	}
	return ownerID
}

func assertFenceTestOwnerState(t *testing.T, db *Store, ownerID, wantState string, wantFencedAt time.Time) {
	t.Helper()
	var state string
	var fencedAt pgtype.Timestamptz
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT read_state, fenced_at
		FROM artifact_source_owners
		WHERE id = $1
	`, ownerID).Scan(&state, &fencedAt); err != nil {
		t.Fatal(err)
	}
	if state != wantState {
		t.Fatalf("owner %s read_state = %q, want %q", ownerID, state, wantState)
	}
	if wantFencedAt.IsZero() {
		if fencedAt.Valid {
			t.Fatalf("owner %s unexpectedly fenced at %s", ownerID, fencedAt.Time)
		}
		return
	}
	if !fencedAt.Valid || !fencedAt.Time.Equal(wantFencedAt) {
		t.Fatalf("owner %s fenced_at = %v, want %s", ownerID, fencedAt, wantFencedAt)
	}
}

func TestExactSourceDeletionSelectionReturnsDomainValidationError(t *testing.T) {
	_, err := exactSourceDeletionSelection(
		governance.DeletionSelectionKind("unknown"),
		governance.SourceGenerationVector{},
		time.Time{},
	)
	if !errors.Is(err, deletiondomain.ErrInvalidTargetRequest) {
		t.Fatalf("error = %v, want ErrInvalidTargetRequest", err)
	}
}
