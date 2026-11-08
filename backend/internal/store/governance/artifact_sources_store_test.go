package governancestore

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var errArtifactSourcesInjected = errors.New("artifact sources injected failure")

func TestPrepareArtifactSourcesPersistsExplicitManifestV2Identity(t *testing.T) {
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	vector := governance.SourceGenerationVector{
		ProjectID: artifactConstraintProjectID, ProjectGeneration: 1,
		BookID: "00000000-0000-4000-8000-000000000b01", BookGeneration: 2,
		ChapterID: "00000000-0000-4000-8000-000000000c01", ChapterGeneration: 3,
	}
	generation, err := governance.SourceGenerationDigest(vector)
	if err != nil {
		t.Fatal(err)
	}
	contentHash := [32]byte{1, 2, 3}
	exactInput := governance.ChapterSourceRefInput{
		Source: vector, SourceRevision: 4, SourceView: view,
		SourceGenerationDigest: generation, ContentHash: contentHash,
		Chat: governance.AbsentChatArm(),
	}
	exact, err := governance.NewExactChapterArtifactRef(exactInput)
	if err != nil {
		t.Fatal(err)
	}
	book, err := governance.NewBookArtifactRef(governance.SourceGenerationVector{
		ProjectID: vector.ProjectID, ProjectGeneration: vector.ProjectGeneration,
		BookID: vector.BookID, BookGeneration: vector.BookGeneration,
	}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	manifestInput := governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: vector.ProjectID,
		ArtifactID: artifactConstraintJobID, OwnerSourceView: view,
		Refs: []governance.ArtifactSourceRef{book, exact},
	}
	manifest, err := governance.NewArtifactManifestV2(manifestInput)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareArtifactSources(SealArtifactSourcesInput{
		OwnerID: artifactConstraintOwnerID, ProjectID: vector.ProjectID, ArtifactID: artifactConstraintJobID,
		Kind: manifestInput.Kind, SourceView: view, Refs: manifestInput.Refs, Manifest: manifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ownerParams.ManifestVersion != 2 || prepared.ownerParams.SourceIdentityState != "text_derived_current" {
		t.Fatalf("owner version/state = %d/%q", prepared.ownerParams.ManifestVersion, prepared.ownerParams.SourceIdentityState)
	}
	if len(prepared.refs) != 2 {
		t.Fatalf("refs = %d", len(prepared.refs))
	}
	exactParams := prepared.refs[1]
	if exactParams.RefKind != "chapter" || exactParams.SourceRevision.Int64 != 4 ||
		string(exactParams.ContentHash) != string(contentHash[:]) || exactParams.TextIdentityState.String != "text_derived_current" {
		t.Fatalf("exact params = %#v", exactParams)
	}

	overflow := exactInput
	overflow.SourceRevision = math.MaxInt64 + 1
	overflowRef, err := governance.NewExactChapterArtifactRef(overflow)
	if err != nil {
		t.Fatal(err)
	}
	overflowInput := manifestInput
	overflowInput.Refs = []governance.ArtifactSourceRef{book, overflowRef}
	overflowManifest, err := governance.NewArtifactManifestV2(overflowInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareArtifactSources(SealArtifactSourcesInput{
		OwnerID: artifactConstraintOwnerID, ProjectID: vector.ProjectID, ArtifactID: artifactConstraintJobID,
		Kind: overflowInput.Kind, SourceView: view, Refs: overflowInput.Refs, Manifest: overflowManifest,
	}); !errors.Is(err, ErrArtifactSourcesValidation) {
		t.Fatalf("overflow prepare error = %v", err)
	}
}

func TestCreateArtifactWithSourcesSealsCompleteOwnerManifest(t *testing.T) {
	t.Run("current path", func(t *testing.T) {
		db := openArtifactSourcesStoreTestDB(t)
		ctx := context.Background()
		input := artifactSourcesStoreInput(t, artifactConstraintOwnerID, artifactConstraintJobID, 1)

		if err := db.WithTx(ctx, func(tx pgx.Tx) error {
			return New(nil).CreateArtifactWithSources(ctx, tx, input, insertArtifactSourcesTestJob)
		}); err != nil {
			t.Fatal(err)
		}
		assertArtifactSourcesStoreState(t, db, input, 1, "queued")
	})
}

func TestCreateArtifactWithSourcesIsAtomicAndProjectScoped(t *testing.T) {
	db := openArtifactSourcesStoreTestDB(t)
	ctx := context.Background()
	input := artifactSourcesStoreInput(t, artifactConstraintOwnerID, artifactConstraintJobID, 1)
	store := New(nil)

	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return store.CreateArtifactWithSources(ctx, tx, input, insertArtifactSourcesTestJob)
	}); err != nil {
		t.Fatal(err)
	}
	assertArtifactSourcesStoreState(t, db, input, 1, "queued")

	otherProjectID := artifactConstraintOtherID
	insertProjectStoreProjectForTest(
		t, db, otherProjectID, artifactConstraintUserID, "Artifact Sources Other",
		time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC),
	)
	wrongProjectDelete := DeleteArtifactWithSourcesInput{
		OwnerID:          input.OwnerID,
		ProjectID:        otherProjectID,
		ArtifactID:       input.ArtifactID,
		Kind:             input.Kind,
		ExpectedManifest: input.Manifest,
	}
	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return store.DeleteArtifactWithSources(ctx, tx, wrongProjectDelete, func(context.Context, pgx.Tx) error {
			t.Fatal("wrong-project delete invoked the artifact mutation")
			return nil
		})
	})
	if !errors.Is(err, ErrArtifactSourcesConflict) {
		t.Fatalf("wrong-project delete error = %v, want conflict", err)
	}
	assertArtifactSourcesStoreState(t, db, input, 1, "queued")
}

func TestCreateArtifactWithSourcesRejectsManifestWithMissingConstituent(t *testing.T) {
	db := openArtifactSourcesStoreTestDB(t)
	ctx := context.Background()
	input := artifactSourcesStoreInput(t, artifactConstraintOwnerID, artifactConstraintJobID, 1)
	secondRef, err := governance.NewSourceIndependentProjectRef(governance.SourceGenerationVector{
		ProjectID: artifactConstraintProjectID, ProjectGeneration: 2,
	}, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	input.Manifest, err = governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: input.Kind, ProjectID: input.ProjectID, ArtifactID: input.ArtifactID,
		OwnerSourceView: input.SourceView,
		Refs:            []governance.ArtifactSourceRef{input.Refs[0], secondRef},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		return New(nil).CreateArtifactWithSources(ctx, tx, input, func(context.Context, pgx.Tx, pgtype.UUID) error {
			t.Fatal("manifest mismatch invoked the artifact mutation")
			return nil
		})
	})
	if !errors.Is(err, ErrArtifactSourcesValidation) {
		t.Fatalf("CreateArtifactWithSources error = %v, want validation failure", err)
	}
	assertArtifactSourcesStoreAbsent(t, db, input)
}

func TestCreateArtifactWithSourcesPreservesProvidedRefIDs(t *testing.T) {
	db := openArtifactSourcesStoreTestDB(t)
	ctx := context.Background()
	input := artifactSourcesStoreInput(t, artifactConstraintOwnerID, artifactConstraintJobID, 1)
	input.RefIDs = []string{artifactConstraintRefID}

	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return New(nil).CreateArtifactWithSources(ctx, tx, input, insertArtifactSourcesTestJob)
	}); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM artifact_source_refs
		WHERE id = $1 AND owner_id = $2 AND project_id = $3
	`, artifactConstraintRefID, input.OwnerID, input.ProjectID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("provided ref row count = %d, want 1", count)
	}
}

func TestCreateArtifactWithSourcesRejectsStaleProjectGenerationWithoutMutation(t *testing.T) {
	db := openArtifactSourcesStoreTestDB(t)
	ctx := context.Background()
	input := artifactSourcesStoreInput(t, artifactConstraintOwnerID, artifactConstraintJobID, 2)
	mutationCalled := false

	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return New(nil).CreateArtifactWithSources(
			ctx,
			tx,
			input,
			func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
				mutationCalled = true
				return insertArtifactSourcesTestJob(ctx, tx, ownerID)
			},
		)
	})
	if !errors.Is(err, ErrArtifactSourcesConflict) {
		t.Fatalf("CreateArtifactWithSources(stale generation) error = %v, want ErrArtifactSourcesConflict", err)
	}
	if mutationCalled {
		t.Fatal("stale source generation invoked artifact mutation")
	}
	assertArtifactSourcesStoreAbsent(t, db, input)
}

func TestReplaceArtifactSourcesUsesManifestCAS(t *testing.T) {
	db := openArtifactSourcesStoreTestDB(t)
	ctx := context.Background()
	store := New(nil)
	original := artifactSourcesStoreInput(t, artifactConstraintOwnerID, artifactConstraintJobID, 1)
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return store.CreateArtifactWithSources(ctx, tx, original, insertArtifactSourcesTestJob)
	}); err != nil {
		t.Fatal(err)
	}

	replacement := artifactSourcesStoreProjectViewInput(
		t, artifactConstraintOwnerID, artifactConstraintJobID, governance.SourceViewDraft,
	)
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return store.ReplaceArtifactSources(ctx, tx, ReplaceArtifactSourcesInput{
			SealArtifactSourcesInput: replacement,
			ExpectedManifest:         original.Manifest,
		}, func(ctx context.Context, tx pgx.Tx, ownerID string) error {
			command, err := tx.Exec(ctx, `
				UPDATE jobs SET status = 'running', updated_at = clock_timestamp()
				WHERE project_id = $1 AND id = $2 AND artifact_source_owner_id = $3
			`, replacement.ProjectID, replacement.ArtifactID, ownerID)
			if err != nil {
				return err
			}
			if command.RowsAffected() != 1 {
				return ErrArtifactSourcesConflict
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	assertArtifactSourcesStoreState(t, db, replacement, 1, "running")

	staleReplacement := artifactSourcesStoreProjectViewInput(
		t, artifactConstraintOwnerID, artifactConstraintJobID, governance.SourceViewPublished,
	)
	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return store.ReplaceArtifactSources(ctx, tx, ReplaceArtifactSourcesInput{
			SealArtifactSourcesInput: staleReplacement,
			ExpectedManifest:         original.Manifest,
		}, func(context.Context, pgx.Tx, string) error {
			t.Fatal("stale manifest CAS invoked the artifact mutation")
			return nil
		})
	})
	if !errors.Is(err, ErrArtifactSourcesConflict) {
		t.Fatalf("stale replacement error = %v, want conflict", err)
	}
	assertArtifactSourcesStoreState(t, db, replacement, 1, "running")
}

func TestReplaceArtifactSourcesConcurrentCASHasOneWinner(t *testing.T) {
	db := openArtifactSourcesStoreTestDB(t)
	ctx := context.Background()
	store := New(nil)
	original := artifactSourcesStoreInput(t, artifactConstraintOwnerID, artifactConstraintJobID, 1)
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return store.CreateArtifactWithSources(ctx, tx, original, insertArtifactSourcesTestJob)
	}); err != nil {
		t.Fatal(err)
	}
	replacements := []SealArtifactSourcesInput{
		artifactSourcesStoreProjectViewInput(t, artifactConstraintOwnerID, artifactConstraintJobID, governance.SourceViewDraft),
		artifactSourcesStoreProjectViewInput(t, artifactConstraintOwnerID, artifactConstraintJobID, governance.SourceViewPublished),
	}
	statuses := []string{"running", "succeeded"}
	start := make(chan struct{})
	results := make(chan error, len(replacements))
	for index := range replacements {
		index := index
		go func() {
			<-start
			results <- db.WithTx(ctx, func(tx pgx.Tx) error {
				return store.ReplaceArtifactSources(ctx, tx, ReplaceArtifactSourcesInput{
					SealArtifactSourcesInput: replacements[index],
					ExpectedManifest:         original.Manifest,
				}, func(ctx context.Context, tx pgx.Tx, ownerID string) error {
					command, err := tx.Exec(ctx, `
						UPDATE jobs SET status = $1, updated_at = clock_timestamp()
						WHERE project_id = $2 AND id = $3 AND artifact_source_owner_id = $4
					`, statuses[index], original.ProjectID, original.ArtifactID, ownerID)
					if err != nil {
						return err
					}
					if command.RowsAffected() != 1 {
						return ErrArtifactSourcesConflict
					}
					return nil
				})
			})
		}()
	}
	close(start)
	var succeeded, conflicted int
	for range replacements {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrArtifactSourcesConflict):
			conflicted++
		default:
			t.Fatalf("concurrent replace error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent CAS results = succeeded %d conflicted %d", succeeded, conflicted)
	}

	var sourceView string
	if err := db.Pool.QueryRow(ctx, `
		SELECT source_view
		FROM artifact_source_refs
		WHERE owner_id = $1 AND project_id = $2
	`, original.OwnerID, original.ProjectID).Scan(&sourceView); err != nil {
		t.Fatal(err)
	}
	if sourceView != string(governance.SourceViewDraft) && sourceView != string(governance.SourceViewPublished) {
		t.Fatalf("winning source view = %q, want draft or published", sourceView)
	}
}

func TestDeleteArtifactWithSourcesRemovesOneExactOwnerSet(t *testing.T) {
	db := openArtifactSourcesStoreTestDB(t)
	ctx := context.Background()
	store := New(nil)
	input := artifactSourcesStoreInput(t, artifactConstraintOwnerID, artifactConstraintJobID, 1)
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return store.CreateArtifactWithSources(ctx, tx, input, insertArtifactSourcesTestJob)
	}); err != nil {
		t.Fatal(err)
	}

	deleteInput := DeleteArtifactWithSourcesInput{
		OwnerID:          input.OwnerID,
		ProjectID:        input.ProjectID,
		ArtifactID:       input.ArtifactID,
		Kind:             input.Kind,
		ExpectedManifest: input.Manifest,
	}
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return store.DeleteArtifactWithSources(ctx, tx, deleteInput, func(ctx context.Context, tx pgx.Tx) error {
			command, err := tx.Exec(ctx, `
				DELETE FROM jobs
				WHERE project_id = $1 AND id = $2 AND artifact_source_owner_id = $3
			`, input.ProjectID, input.ArtifactID, input.OwnerID)
			if err != nil {
				return err
			}
			if command.RowsAffected() != 1 {
				return ErrArtifactSourcesConflict
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	assertArtifactSourcesStoreAbsent(t, db, input)
}

func TestArtifactSourcesWriterRollsBackEveryCreateBoundary(t *testing.T) {
	points := []string{
		"before_owner",
		"after_owner",
		"after_ref_0",
		"before_artifact",
		"after_artifact",
		"before_seal",
		"after_seal",
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			db := openArtifactSourcesStoreTestDB(t)
			ctx := context.Background()
			input := artifactSourcesStoreInput(t, artifactConstraintOwnerID, artifactConstraintJobID, 1)
			store := New(nil, WithFailpoint(func(got string) error {
				if got == point {
					return errArtifactSourcesInjected
				}
				return nil
			}))
			err := db.WithTx(ctx, func(tx pgx.Tx) error {
				return store.CreateArtifactWithSources(ctx, tx, input, insertArtifactSourcesTestJob)
			})
			if !errors.Is(err, errArtifactSourcesInjected) {
				t.Fatalf("CreateArtifactWithSources error = %v, want injected failure", err)
			}
			assertArtifactSourcesStoreAbsent(t, db, input)
		})
	}
}

func TestArtifactSourcesWriterRollsBackReplaceAndDelete(t *testing.T) {
	for _, operation := range []string{"replace", "delete"} {
		for _, point := range []string{
			"before_owner", "after_owner", "after_ref_0", "before_artifact",
			"after_artifact", "before_seal", "after_seal",
		} {
			t.Run(operation+"_"+point, func(t *testing.T) {
				db := openArtifactSourcesStoreTestDB(t)
				ctx := context.Background()
				original := artifactSourcesStoreInput(t, artifactConstraintOwnerID, artifactConstraintJobID, 1)
				if err := db.WithTx(ctx, func(tx pgx.Tx) error {
					return New(nil).CreateArtifactWithSources(ctx, tx, original, insertArtifactSourcesTestJob)
				}); err != nil {
					t.Fatal(err)
				}
				store := New(nil, WithFailpoint(func(got string) error {
					if got == point {
						return errArtifactSourcesInjected
					}
					return nil
				}))
				var err error
				if operation == "replace" {
					replacement := artifactSourcesStoreProjectViewInput(
						t, artifactConstraintOwnerID, artifactConstraintJobID, governance.SourceViewDraft,
					)
					err = db.WithTx(ctx, func(tx pgx.Tx) error {
						return store.ReplaceArtifactSources(ctx, tx, ReplaceArtifactSourcesInput{
							SealArtifactSourcesInput: replacement,
							ExpectedManifest:         original.Manifest,
						}, func(ctx context.Context, tx pgx.Tx, ownerID string) error {
							_, updateErr := tx.Exec(ctx, `
								UPDATE jobs SET status = 'running'
								WHERE project_id = $1 AND id = $2 AND artifact_source_owner_id = $3
							`, original.ProjectID, original.ArtifactID, ownerID)
							return updateErr
						})
					})
				} else {
					err = db.WithTx(ctx, func(tx pgx.Tx) error {
						return store.DeleteArtifactWithSources(ctx, tx, DeleteArtifactWithSourcesInput{
							OwnerID: original.OwnerID, ProjectID: original.ProjectID,
							ArtifactID: original.ArtifactID, Kind: original.Kind,
							ExpectedManifest: original.Manifest,
						}, func(ctx context.Context, tx pgx.Tx) error {
							_, deleteErr := tx.Exec(ctx, `
								DELETE FROM jobs
								WHERE project_id = $1 AND id = $2 AND artifact_source_owner_id = $3
							`, original.ProjectID, original.ArtifactID, original.OwnerID)
							return deleteErr
						})
					})
				}
				if !errors.Is(err, errArtifactSourcesInjected) {
					t.Fatalf("%s error = %v, want injected failure", operation, err)
				}
				assertArtifactSourcesStoreState(t, db, original, 1, "queued")
			})
		}
	}
}

func openArtifactSourcesStoreTestDB(t *testing.T) *storedb.DB {
	t.Helper()
	return openArtifactConstraintTestDB(t)
}

func artifactSourcesStoreInput(
	t *testing.T,
	ownerID string,
	artifactID string,
	generation governance.LifecycleGeneration,
) SealArtifactSourcesInput {
	t.Helper()
	ref, err := governance.NewSourceIndependentProjectRef(governance.SourceGenerationVector{
		ProjectID: artifactConstraintProjectID, ProjectGeneration: generation,
	}, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	manifestInput := governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: artifactConstraintProjectID,
		ArtifactID: artifactID, OwnerSourceView: governance.AbsentSourceView(),
		Refs: []governance.ArtifactSourceRef{ref},
	}
	manifest, err := governance.NewArtifactManifest(manifestInput)
	if err != nil {
		t.Fatal(err)
	}
	return SealArtifactSourcesInput{
		OwnerID: ownerID, ProjectID: artifactConstraintProjectID,
		ArtifactID: artifactID, Kind: governance.ArtifactJob,
		SourceView: governance.AbsentSourceView(), Refs: manifestInput.Refs,
		Manifest: manifest,
	}
}

func artifactSourcesStoreProjectViewInput(
	t *testing.T,
	ownerID string,
	artifactID string,
	view governance.SourceView,
) SealArtifactSourcesInput {
	t.Helper()
	sourceView, err := governance.PresentSourceView(view)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{
		ProjectID: artifactConstraintProjectID, ProjectGeneration: 1,
	}, sourceView, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	manifestInput := governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: artifactConstraintProjectID,
		ArtifactID: artifactID, OwnerSourceView: sourceView,
		Refs: []governance.ArtifactSourceRef{ref},
	}
	manifest, err := governance.NewArtifactManifest(manifestInput)
	if err != nil {
		t.Fatal(err)
	}
	return SealArtifactSourcesInput{
		OwnerID: ownerID, ProjectID: artifactConstraintProjectID,
		ArtifactID: artifactID, Kind: governance.ArtifactJob,
		SourceView: sourceView, Refs: manifestInput.Refs, Manifest: manifest,
	}
}

func insertArtifactSourcesTestJob(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO jobs (
			id, project_id, kind, status, progress_json,
			artifact_source_owner_id, governance_payload_state, subject_json,
			creator_user_id, can_cancel, queue_class, retry_count, max_attempts,
			next_attempt_at, created_at, updated_at
		) VALUES (
			$1, $2, 'import', 'queued', jsonb_build_object('value', 0),
			$3, 'active', jsonb_build_object('type', 'import', 'id', $2::uuid::text, 'projectId', $2::uuid::text),
			$4, true, 'user_demanded', 0, 3,
			statement_timestamp(), statement_timestamp(), statement_timestamp()
		)
	`, artifactConstraintJobID, artifactConstraintProjectID, ownerID, artifactConstraintUserID)
	return err
}

func assertArtifactSourcesStoreState(
	t *testing.T,
	db *storedb.DB,
	input SealArtifactSourcesInput,
	wantGeneration int64,
	wantStatus string,
) {
	t.Helper()
	var (
		ownerCount int
		refCount   int
		jobCount   int
		generation int64
		status     string
		manifest   []byte
	)
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM artifact_source_owners
			 WHERE id = $1 AND project_id = $2 AND artifact_kind = $3 AND artifact_id = $4),
			(SELECT count(*) FROM artifact_source_refs
			 WHERE owner_id = $1 AND project_id = $2),
			(SELECT count(*) FROM jobs
			 WHERE id = $4 AND project_id = $2 AND artifact_source_owner_id = $1),
			(SELECT project_generation FROM artifact_source_refs
			 WHERE owner_id = $1 AND project_id = $2),
			(SELECT status FROM jobs WHERE id = $4 AND project_id = $2),
			(SELECT manifest_hash FROM artifact_source_owners
			 WHERE id = $1 AND project_id = $2)
	`, input.OwnerID, input.ProjectID, string(input.Kind), input.ArtifactID).Scan(
		&ownerCount, &refCount, &jobCount, &generation, &status, &manifest,
	); err != nil {
		t.Fatal(err)
	}
	wantManifest := input.Manifest.Hash()
	if ownerCount != 1 || refCount != 1 || jobCount != 1 || generation != wantGeneration ||
		status != wantStatus || string(manifest) != string(wantManifest[:]) {
		t.Fatalf(
			"artifact source state = owner %d refs %d job %d generation %d status %s hash %x",
			ownerCount, refCount, jobCount, generation, status, manifest,
		)
	}
}

func assertArtifactSourcesStoreAbsent(t *testing.T, db *storedb.DB, input SealArtifactSourcesInput) {
	t.Helper()
	var ownerCount, refCount, jobCount int
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM artifact_source_owners WHERE id = $1 AND project_id = $2),
			(SELECT count(*) FROM artifact_source_refs WHERE owner_id = $1 AND project_id = $2),
			(SELECT count(*) FROM jobs WHERE id = $3 AND project_id = $2)
	`, input.OwnerID, input.ProjectID, input.ArtifactID).Scan(&ownerCount, &refCount, &jobCount); err != nil {
		t.Fatal(err)
	}
	if ownerCount != 0 || refCount != 0 || jobCount != 0 {
		t.Fatalf("partial artifact source write remains: owner %d refs %d job %d", ownerCount, refCount, jobCount)
	}
}
