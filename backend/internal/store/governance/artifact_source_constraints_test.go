package governancestore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

const (
	artifactConstraintUserID           = "00000000-0000-4000-8000-000000000a01"
	artifactConstraintProjectID        = "00000000-0000-4000-8000-000000000a02"
	artifactConstraintJobID            = "00000000-0000-4000-8000-000000000a03"
	artifactConstraintOwnerID          = "00000000-0000-4000-8000-000000000a04"
	artifactConstraintRefID            = "00000000-0000-4000-8000-000000000a05"
	artifactConstraintOtherID          = "00000000-0000-4000-8000-000000000a06"
	artifactConstraintThirdID          = "00000000-0000-4000-8000-000000000a07"
	artifactConstraintChatID           = "00000000-0000-4000-8000-000000000a08"
	artifactConstraintBookID           = "00000000-0000-4000-8000-000000000a09"
	artifactConstraintChapterID        = "00000000-0000-4000-8000-000000000a0a"
	artifactConstraintParaID           = "00000000-0000-4000-8000-000000000a0b"
	artifactConstraintAssistantID      = "00000000-0000-4000-8000-000000000e01"
	artifactConstraintAssistantOwnerID = "00000000-0000-4000-8000-000000000e02"
	artifactConstraintAssistantRefID   = "00000000-0000-4000-8000-000000000e03"
)

type artifactConstraintParticipantCase struct {
	name       string
	kind       string
	artifactID string
	insert     func(*testing.T, pgx.Tx, string)
	delete     func(*testing.T, pgx.Tx)
}

func TestArtifactSourceConstraintsAcceptExactProjectScopedOwner(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	ctx := context.Background()

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	insertProjectScopeArtifactOwner(t, tx, artifactConstraintOwnerID, artifactConstraintRefID, artifactConstraintJobID, 1)
	setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
	insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintJobID, artifactConstraintOwnerID)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit exact artifact owner/ref set: %v", err)
	}
}

func TestArtifactSourceConstraintsValidateLargeRefSetOncePerTransaction(t *testing.T) {
	const (
		refCount = 3844
		ownerID  = "00000000-0000-4000-8000-000000000a20"
		jobID    = "00000000-0000-4000-8000-000000000a21"
	)
	db := openArtifactConstraintTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	insertBookForExportStoreTest(t, db, artifactConstraintBookID, artifactConstraintProjectID, "Large Ref Set", now)

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO chapters (id, book_id, project_id, title, "order")
		SELECT md5('large-ref-chapter-' || ordinal)::uuid, $1, $2,
			'Chapter ' || ordinal, ordinal
		FROM generate_series(1, $3) AS ordinal
	`, artifactConstraintBookID, artifactConstraintProjectID, refCount)
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO artifact_source_owners (
			id, project_id, artifact_kind, artifact_id, source_view,
			manifest_version, manifest_hash, ref_count, read_state,
			source_identity_state
		) VALUES ($1, $2, 'job', $3, 'draft', 1,
			decode(repeat('11', 32), 'hex'), $4, 'active', 'scope_only')
	`, ownerID, artifactConstraintProjectID, jobID, refCount)
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO artifact_source_refs (
			id, owner_id, project_id, ref_kind, scope_kind, source_view,
			project_generation, book_id, book_generation, chapter_id,
			chapter_generation, source_independent
		)
		SELECT md5('large-ref-' || ordinal)::uuid, $1, $2, 'scope', 'chapter',
			'draft', 1, $3, 1, md5('large-ref-chapter-' || ordinal)::uuid, 1, false
		FROM generate_series(1, $4) AS ordinal
	`, ownerID, artifactConstraintProjectID, artifactConstraintBookID, refCount)
	setArtifactOwnerManifestHashFromRefs(t, tx, ownerID, artifactConstraintProjectID)
	insertArtifactConstraintJobWithOwner(t, tx, jobID, ownerID)

	commitStarted := time.Now()
	commitCtx, cancelCommit := context.WithTimeout(ctx, 10*time.Second)
	defer cancelCommit()
	if err := tx.Commit(commitCtx); err != nil {
		t.Fatalf("commit %d source refs: %v", refCount, err)
	}
	commitElapsed := time.Since(commitStarted)
	t.Logf("validated %d source refs at commit in %s", refCount, commitElapsed.Round(time.Millisecond))

	// После принудительной проверки новая мутация обязана создать новую проверку.
	tx, err = db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	execArtifactConstraintSQL(t, tx, `
		DELETE FROM artifact_source_refs WHERE id = md5('large-ref-1')::uuid
	`)
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO artifact_source_refs (
			id, owner_id, project_id, ref_kind, scope_kind, source_view,
			project_generation, book_id, book_generation, chapter_id,
			chapter_generation, source_independent
		) VALUES (
			md5('large-ref-1')::uuid, $1, $2, 'scope', 'chapter', 'draft',
			1, $3, 1, md5('large-ref-chapter-1')::uuid, 1, false
		)
	`, ownerID, artifactConstraintProjectID, artifactConstraintBookID)
	execArtifactConstraintSQL(t, tx, `SET CONSTRAINTS ALL IMMEDIATE`)
	if _, err := tx.Exec(ctx, `
		DELETE FROM artifact_source_refs WHERE id = md5('large-ref-1')::uuid
	`); err == nil {
		t.Fatal("SET CONSTRAINTS IMMEDIATE allowed a later invalid ref mutation")
	}
}

func TestArtifactSourceConstraintsExactChapterTuple(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	legacy := seedArtifactConstraintExactSource(t, db)
	chapterHash, err := governance.CanonicalChapterContentDigest([]governance.CanonicalParagraphContent{{
		ParagraphID: artifactConstraintParaID, Order: 0, Content: "exact canonical content",
	}})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewExactChapterArtifactRef(governance.ChapterSourceRefInput{
		Source: legacy.Source, SourceRevision: legacy.SourceRevision, SourceView: legacy.SourceView,
		SourceGenerationDigest: legacy.SourceGenerationDigest, ContentHash: chapterHash,
		Chat: governance.AbsentChatArm(),
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := governance.NewArtifactManifestV2(governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: artifactConstraintProjectID,
		ArtifactID: artifactConstraintJobID, OwnerSourceView: legacy.SourceView,
		Refs: []governance.ArtifactSourceRef{ref},
	})
	if err != nil {
		t.Fatal(err)
	}
	fields, err := ref.Fields()
	if err != nil {
		t.Fatal(err)
	}
	view, _ := fields.SourceView.View()
	hash := manifest.Hash()
	tx, err := db.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO artifact_source_owners (
			id, project_id, artifact_kind, artifact_id, source_view,
			manifest_version, manifest_hash, ref_count, read_state, source_identity_state
		) VALUES ($1, $2, 'job', $3, $4, 2, $5, 1, 'active', 'text_derived_current')
	`, artifactConstraintOwnerID, artifactConstraintProjectID, artifactConstraintJobID, string(view), hash[:])
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO artifact_source_refs (
			id, owner_id, project_id, ref_kind, source_view,
			project_generation, book_id, book_generation, chapter_id, chapter_generation,
			source_revision, content_hash, locator_source_generation, text_identity_state, source_independent
		) VALUES ($1, $2, $3, 'chapter', $4, $5, $6, $7, $8, $9, $10, $11, $12, 'text_derived_current', false)
	`, artifactConstraintRefID, artifactConstraintOwnerID, artifactConstraintProjectID, string(view),
		fields.ProjectGeneration, fields.BookID, fields.BookGeneration, fields.ChapterID, fields.ChapterGeneration,
		fields.SourceRevision, fields.ContentHash[:], fields.LocatorSourceGenerationDigest[:])
	insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintJobID, artifactConstraintOwnerID)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit exact chapter owner: %v", err)
	}
}

func TestArtifactSourceConstraintsRejectManifestHashDrift(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	ctx := context.Background()

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	insertProjectScopeArtifactOwner(t, tx, artifactConstraintOwnerID, artifactConstraintRefID, artifactConstraintJobID, 1)
	insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintJobID, artifactConstraintOwnerID)
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("artifact owner committed with a manifest hash that does not describe its exact refs")
	}
}

func TestArtifactSourceConstraintsRejectRefCountDrift(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	ctx := context.Background()

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	insertProjectScopeArtifactOwner(t, tx, artifactConstraintOwnerID, artifactConstraintRefID, artifactConstraintJobID, 2)
	setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
	insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintJobID, artifactConstraintOwnerID)
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("artifact owner committed with ref_count different from its exact refs")
	}
}

func TestArtifactSourceConstraintsRejectActiveOwnerWithStaleProjectGeneration(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	ctx := context.Background()

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	insertArtifactConstraintOwnerRow(t, tx, "job", artifactConstraintOwnerID, artifactConstraintJobID, 1)
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO artifact_source_refs (
			id, owner_id, project_id, ref_kind, scope_kind, source_view,
			project_generation, source_independent
		) VALUES ($1, $2, $3, 'scope', 'project', NULL, 2, true)
	`, artifactConstraintRefID, artifactConstraintOwnerID, artifactConstraintProjectID)
	setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
	insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintJobID, artifactConstraintOwnerID)
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("active artifact owner committed with a stale project generation")
	}
}

func TestArtifactSourceConstraintsRejectActiveOwnerWithStaleChatEpoch(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	ctx := context.Background()

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO chat_sessions (id, project_id, source_view, scope, title)
		VALUES ($1, $2, 'draft', 'project', 'Constraint Chat')
	`, artifactConstraintChatID, artifactConstraintProjectID)
	insertArtifactConstraintOwnerRow(t, tx, "job", artifactConstraintOwnerID, artifactConstraintJobID, 1)
	execArtifactConstraintSQL(t, tx, `
		INSERT INTO artifact_source_refs (
			id, owner_id, project_id, ref_kind, scope_kind, source_view,
			project_generation, source_independent, chat_session_id, chat_lifecycle_epoch
		) VALUES ($1, $2, $3, 'scope', 'project', NULL, 1, true, $4, 2)
	`, artifactConstraintRefID, artifactConstraintOwnerID, artifactConstraintProjectID, artifactConstraintChatID)
	setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
	insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintJobID, artifactConstraintOwnerID)
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("active artifact owner committed with a stale chat lifecycle epoch")
	}
}

func TestArtifactSourceConstraintsSerializeOwnerCommitWithProjectGenerationAdvance(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	ctx := context.Background()

	sourceTx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sourceTx.Rollback(context.Background()) }()
	if _, err := sourceTx.Exec(ctx, `
		UPDATE projects
		SET lifecycle_generation = 2
		WHERE id = $1
	`, artifactConstraintProjectID); err != nil {
		t.Fatalf("advance project generation: %v", err)
	}

	artifactTx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = artifactTx.Rollback(context.Background()) }()
	insertProjectScopeArtifactOwner(
		t, artifactTx, artifactConstraintOwnerID, artifactConstraintRefID, artifactConstraintJobID, 1,
	)
	setArtifactOwnerManifestHashFromRefs(t, artifactTx, artifactConstraintOwnerID, artifactConstraintProjectID)
	insertArtifactConstraintJobWithOwner(t, artifactTx, artifactConstraintJobID, artifactConstraintOwnerID)

	commitResult := make(chan error, 1)
	go func() { commitResult <- artifactTx.Commit(ctx) }()
	select {
	case err := <-commitResult:
		t.Fatalf("artifact commit completed before project generation transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := sourceTx.Commit(ctx); err != nil {
		t.Fatalf("commit project generation advance: %v", err)
	}
	select {
	case err := <-commitResult:
		if err == nil {
			t.Fatal("artifact owner committed after its project generation became stale")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("artifact commit did not resume after project generation transaction committed")
	}
}

func TestArtifactSourceConstraintsSerializeOwnerCommitWithStaleChatEpochLock(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO chat_sessions (id, project_id, source_view, scope, title, lifecycle_epoch)
		VALUES ($1, $2, 'draft', 'project', 'Concurrent Constraint Chat', 2)
	`, artifactConstraintChatID, artifactConstraintProjectID); err != nil {
		t.Fatalf("insert chat fixture: %v", err)
	}

	sourceTx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sourceTx.Rollback(context.Background()) }()
	if _, err := sourceTx.Exec(ctx, `
		SELECT id FROM chat_sessions
		WHERE id = $1 AND project_id = $2
		FOR NO KEY UPDATE
	`, artifactConstraintChatID, artifactConstraintProjectID); err != nil {
		t.Fatalf("lock chat epoch fixture: %v", err)
	}

	artifactTx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = artifactTx.Rollback(context.Background()) }()
	insertArtifactConstraintOwnerRow(t, artifactTx, "job", artifactConstraintOwnerID, artifactConstraintJobID, 1)
	execArtifactConstraintSQL(t, artifactTx, `
		INSERT INTO artifact_source_refs (
			id, owner_id, project_id, ref_kind, scope_kind, source_view,
			project_generation, source_independent, chat_session_id, chat_lifecycle_epoch
		) VALUES ($1, $2, $3, 'scope', 'project', NULL, 1, true, $4, 1)
	`, artifactConstraintRefID, artifactConstraintOwnerID, artifactConstraintProjectID, artifactConstraintChatID)
	setArtifactOwnerManifestHashFromRefs(t, artifactTx, artifactConstraintOwnerID, artifactConstraintProjectID)
	insertArtifactConstraintJobWithOwner(t, artifactTx, artifactConstraintJobID, artifactConstraintOwnerID)

	commitResult := make(chan error, 1)
	go func() { commitResult <- artifactTx.Commit(ctx) }()
	select {
	case err := <-commitResult:
		t.Fatalf("artifact commit completed before chat epoch lock released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := sourceTx.Commit(ctx); err != nil {
		t.Fatalf("release chat epoch lock: %v", err)
	}
	select {
	case err := <-commitResult:
		if err == nil {
			t.Fatal("artifact owner committed after its chat lifecycle epoch became stale")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("artifact commit did not resume after chat epoch transaction committed")
	}
}

func TestArtifactSourceConstraintsRejectRefMoveBetweenOwners(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	ctx := context.Background()

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	insertProjectScopeArtifactOwner(t, tx, artifactConstraintOwnerID, artifactConstraintRefID, artifactConstraintJobID, 1)
	setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
	insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintJobID, artifactConstraintOwnerID)
	insertProjectScopeArtifactOwner(t, tx, artifactConstraintThirdID, artifactConstraintOtherID, artifactConstraintOtherID, 1)
	setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintThirdID, artifactConstraintProjectID)
	insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintOtherID, artifactConstraintThirdID)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit owner fixtures: %v", err)
	}

	tx, err = db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `DELETE FROM artifact_source_refs WHERE id = $1 AND project_id = $2`, artifactConstraintOtherID, artifactConstraintProjectID); err != nil {
		t.Fatalf("delete destination owner ref: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE artifact_source_refs
		SET owner_id = $1
		WHERE id = $2 AND project_id = $3
	`, artifactConstraintThirdID, artifactConstraintRefID, artifactConstraintProjectID); err != nil {
		return
	}
	setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintThirdID, artifactConstraintProjectID)
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("artifact source ref moved between owners and left the previous owner with a stale sealed manifest")
	}
}

func TestArtifactSourceConstraintsRejectOwnerParticipantRetarget(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	ctx := context.Background()

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	insertProjectScopeArtifactOwner(t, tx, artifactConstraintOwnerID, artifactConstraintRefID, artifactConstraintJobID, 1)
	setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
	insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintJobID, artifactConstraintOwnerID)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit owner fixture: %v", err)
	}

	tx, err = db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `
		UPDATE artifact_source_owners
		SET artifact_id = $1
		WHERE id = $2 AND project_id = $3
	`, artifactConstraintOtherID, artifactConstraintOwnerID, artifactConstraintProjectID); err != nil {
		return
	}
	setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
	insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintOtherID, artifactConstraintOwnerID)
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("artifact owner retarget committed while the previous participant still pointed at it")
	}
}

func TestArtifactSourceConstraintsRejectOrphanOwner(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	ctx := context.Background()

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	insertProjectScopeArtifactOwner(t, tx, artifactConstraintOwnerID, artifactConstraintRefID, artifactConstraintJobID, 1)
	setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("artifact owner committed without its exact same-project artifact")
	}
}

func TestArtifactSourceConstraintsRejectParticipantDeleteWithoutOwnerDelete(t *testing.T) {
	cases := artifactConstraintParticipantCases()
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			db := openArtifactConstraintTestDB(t)
			exactSource := insertArtifactConstraintParticipantDependencies(t, db)

			ctx := context.Background()
			if testCase.insert != nil {
				tx, err := db.Pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(context.Background()) }()
				insertArtifactConstraintParticipantOwner(t, tx, testCase, exactSource)
				testCase.insert(t, tx, artifactConstraintOwnerID)
				if err := tx.Commit(ctx); err != nil {
					t.Fatalf("commit governed %s participant: %v", testCase.name, err)
				}
			}

			tx, err := db.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			testCase.delete(t, tx)
			if err := tx.Commit(ctx); err == nil {
				t.Fatalf("%s participant was deleted while its owner remained", testCase.name)
			}
		})
	}
}

func TestArtifactSourceConstraintsAcceptAtomicParticipantOwnerDelete(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	insertArtifactConstraintParticipantDependencies(t, db)
	ctx := context.Background()

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	insertProjectScopeArtifactOwner(t, tx, artifactConstraintOwnerID, artifactConstraintRefID, artifactConstraintJobID, 1)
	setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
	insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintJobID, artifactConstraintOwnerID)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit governed job: %v", err)
	}

	tx, err = db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `
		DELETE FROM jobs WHERE project_id = $1 AND id = $2
	`, artifactConstraintProjectID, artifactConstraintJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM artifact_source_owners WHERE project_id = $1 AND id = $2
	`, artifactConstraintProjectID, artifactConstraintOwnerID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit atomic participant+owner delete: %v", err)
	}
}

func TestArtifactSourceConstraintsRequireOwner(t *testing.T) {
	db := openArtifactConstraintTestDB(t)
	ctx := context.Background()

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, rejectionErr := tx.Exec(ctx, `
		INSERT INTO jobs (
			id, project_id, kind, status, progress_json,
			governance_payload_state, subject_json, creator_user_id, can_cancel,
			queue_class, retry_count, max_attempts, next_attempt_at
		) VALUES (
			$1, $2, 'import', 'queued', jsonb_build_object('value', 0),
			'active', jsonb_build_object('type', 'import', 'id', $2::uuid::text, 'projectId', $2::uuid::text),
			$3, true, 'user_demanded', 0, 3, statement_timestamp()
		)
	`, artifactConstraintJobID, artifactConstraintProjectID, artifactConstraintUserID)
	if rejectionErr == nil {
		rejectionErr = tx.Commit(ctx)
	}
	if rejectionErr == nil {
		t.Fatal("artifact without a source owner was accepted")
	}
}

func TestArtifactSourceConstraintsRejectMalformedLinksAndRefs(t *testing.T) {
	t.Run("dual owner", func(t *testing.T) {
		db := openArtifactConstraintTestDB(t)
		ctx := context.Background()
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		insertProjectScopeArtifactOwner(t, tx, artifactConstraintOwnerID, artifactConstraintRefID, artifactConstraintJobID, 1)
		if _, err := tx.Exec(ctx, `
			INSERT INTO artifact_source_owners (
				id, project_id, artifact_kind, artifact_id, source_view,
				manifest_version, manifest_hash, ref_count, read_state,
				source_identity_state
			) VALUES (
				$1, $2, 'job', $3, NULL,
				1, decode(repeat('22', 32), 'hex'), 1, 'active',
				'scope_only'
			)
		`, artifactConstraintOtherID, artifactConstraintProjectID, artifactConstraintJobID); err == nil {
			t.Fatal("artifact accepted a second owner for the same project/kind/id")
		}
	})

	t.Run("wrong kind", func(t *testing.T) {
		db := openArtifactConstraintTestDB(t)
		ctx := context.Background()
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		insertProjectScopeArtifactOwnerKind(
			t, tx, "chat_turn", artifactConstraintOwnerID, artifactConstraintRefID,
			artifactConstraintJobID, 1,
		)
		setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
		insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintJobID, artifactConstraintOwnerID)
		if err := tx.Commit(ctx); err == nil {
			t.Fatal("job committed with an owner for a different artifact kind")
		}
	})

	t.Run("wrong artifact id", func(t *testing.T) {
		db := openArtifactConstraintTestDB(t)
		ctx := context.Background()
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		insertProjectScopeArtifactOwner(
			t, tx, artifactConstraintOwnerID, artifactConstraintRefID,
			artifactConstraintOtherID, 1,
		)
		setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
		insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintJobID, artifactConstraintOwnerID)
		if err := tx.Commit(ctx); err == nil {
			t.Fatal("artifact committed with an owner for a different artifact id")
		}
	})

	t.Run("wrong project", func(t *testing.T) {
		db := openArtifactConstraintTestDB(t)
		ctx := context.Background()
		insertProjectStoreProjectForTest(
			t, db, artifactConstraintOtherID, artifactConstraintUserID,
			"Other Artifact Project", time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC),
		)
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		insertProjectScopeArtifactOwner(t, tx, artifactConstraintOwnerID, artifactConstraintRefID, artifactConstraintJobID, 1)
		setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
		insertArtifactConstraintJobWithOwnerInProject(
			t, tx, artifactConstraintJobID, artifactConstraintOtherID, artifactConstraintOwnerID,
		)
		if err := tx.Commit(ctx); err == nil {
			t.Fatal("artifact committed with an owner belonging to another project")
		}
	})

	t.Run("duplicate logical ref", func(t *testing.T) {
		db := openArtifactConstraintTestDB(t)
		ctx := context.Background()
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		insertProjectScopeArtifactOwner(t, tx, artifactConstraintOwnerID, artifactConstraintRefID, artifactConstraintJobID, 2)
		if _, err := tx.Exec(ctx, `
			INSERT INTO artifact_source_refs (
				id, owner_id, project_id, ref_kind, scope_kind, source_view,
				project_generation, source_independent
			) VALUES ($1, $2, $3, 'scope', 'project', NULL, 1, true)
		`, artifactConstraintOtherID, artifactConstraintOwnerID, artifactConstraintProjectID); err == nil {
			t.Fatal("owner accepted duplicate logical source refs with different row ids")
		}
	})

	t.Run("incomplete scope", func(t *testing.T) {
		db := openArtifactConstraintTestDB(t)
		ctx := context.Background()
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		insertArtifactConstraintOwnerRow(
			t, tx, "job", artifactConstraintOwnerID, artifactConstraintJobID, 1,
		)
		if _, err := tx.Exec(ctx, `
			INSERT INTO artifact_source_refs (
				id, owner_id, project_id, ref_kind, project_generation,
				source_independent
			) VALUES ($1, $2, $3, 'scope', 1, true)
		`, artifactConstraintRefID, artifactConstraintOwnerID, artifactConstraintProjectID); err == nil {
			t.Fatal("registry accepted an incomplete scope ref")
		}
	})

	t.Run("incomplete chapter", func(t *testing.T) {
		db := openArtifactConstraintTestDB(t)
		ctx := context.Background()
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		insertArtifactConstraintOwnerRow(
			t, tx, "job", artifactConstraintOwnerID, artifactConstraintJobID, 1,
		)
		if _, err := tx.Exec(ctx, `
			INSERT INTO artifact_source_refs (
				id, owner_id, project_id, ref_kind, source_view,
				project_generation, source_independent
			) VALUES ($1, $2, $3, 'chapter', 'draft', 1, false)
		`, artifactConstraintRefID, artifactConstraintOwnerID, artifactConstraintProjectID); err == nil {
			t.Fatal("registry accepted an incomplete exact chapter ref")
		}
	})

	t.Run("wrong view arm", func(t *testing.T) {
		db := openArtifactConstraintTestDB(t)
		ctx := context.Background()
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := tx.Exec(ctx, `
			INSERT INTO artifact_source_owners (
				id, project_id, artifact_kind, artifact_id, source_view,
				manifest_version, manifest_hash, ref_count, read_state,
				source_identity_state
			) VALUES (
				$1, $2, 'job', $3, 'draft',
				1, decode(repeat('33', 32), 'hex'), 1, 'active',
				'scope_only'
			)
		`, artifactConstraintOwnerID, artifactConstraintProjectID, artifactConstraintJobID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO artifact_source_refs (
				id, owner_id, project_id, ref_kind, scope_kind, source_view,
				project_generation, source_independent
			) VALUES ($1, $2, $3, 'scope', 'project', NULL, 1, true)
		`, artifactConstraintRefID, artifactConstraintOwnerID, artifactConstraintProjectID); err != nil {
			t.Fatal(err)
		}
		insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintJobID, artifactConstraintOwnerID)
		if err := tx.Commit(ctx); err == nil {
			t.Fatal("owner committed with a source-view arm different from its refs")
		}
	})
}

func artifactConstraintParticipantCases() []artifactConstraintParticipantCase {
	const (
		importJobID    = "00000000-0000-4000-8000-000000000b06"
		importUploadID = "00000000-0000-4000-8000-000000000c05"
	)
	return []artifactConstraintParticipantCase{
		{
			name: "job", kind: "job", artifactID: artifactConstraintJobID,
			insert: func(t *testing.T, tx pgx.Tx, ownerID string) {
				insertArtifactConstraintJobWithOwner(t, tx, artifactConstraintJobID, ownerID)
			},
			delete: func(t *testing.T, tx pgx.Tx) {
				execArtifactConstraintSQL(t, tx, `DELETE FROM jobs WHERE id = $1 AND project_id = $2`, artifactConstraintJobID, artifactConstraintProjectID)
			},
		},
		{
			name: "import_upload", kind: "import_upload", artifactID: importUploadID,
			insert: func(t *testing.T, tx pgx.Tx, ownerID string) {
				execArtifactConstraintSQL(t, tx, `
					INSERT INTO import_uploads (
						storage_key, project_id, mime_type, size_bytes, sha256, content,
						expires_at, artifact_id, artifact_source_owner_id
					) VALUES ('constraint-target-upload', $1, 'text/plain', 1, 'aa', NULL,
						clock_timestamp() + interval '1 hour', $2, $3)
				`, artifactConstraintProjectID, importUploadID, ownerID)
			},
			delete: func(t *testing.T, tx pgx.Tx) {
				execArtifactConstraintSQL(t, tx, `DELETE FROM import_uploads WHERE artifact_id = $1 AND project_id = $2`, importUploadID, artifactConstraintProjectID)
			},
		},
		{
			name: "import_job_request", kind: "import_job_request", artifactID: importJobID,
			insert: func(t *testing.T, tx pgx.Tx, ownerID string) {
				execArtifactConstraintSQL(t, tx, `
					INSERT INTO import_job_requests (
						job_id, project_id, actor_user_id, storage_key, idempotency_key,
						request_method, request_path, request_fingerprint, source_file_name,
						extension, mime_type, size_bytes, file_sha256, import_mode,
						can_cancel, artifact_source_owner_id
					) VALUES (
						$1, $2, $3, 'constraint-dependency-upload', 'constraint-idempotency',
						'POST', '/constraint/import', 'constraint-fingerprint', 'source.txt',
						'.txt', 'text/plain', 1, 'aa', 'new_book', true, $4
					)
				`, importJobID, artifactConstraintProjectID, artifactConstraintUserID, ownerID)
			},
			delete: func(t *testing.T, tx pgx.Tx) {
				execArtifactConstraintSQL(t, tx, `DELETE FROM import_job_requests WHERE job_id = $1 AND project_id = $2`, importJobID, artifactConstraintProjectID)
			},
		},
		{
			name: "chat_turn", kind: "chat_turn", artifactID: phase11ConstraintTurnID,
			delete: func(t *testing.T, tx pgx.Tx) {
				execArtifactConstraintSQL(t, tx, `DELETE FROM chat_turns WHERE id = $1 AND project_id = $2`, phase11ConstraintTurnID, artifactConstraintProjectID)
			},
		},
		{
			name: "agent_workflow_input", kind: "agent_workflow_input", artifactID: phase11ConstraintInputID,
			delete: func(t *testing.T, tx pgx.Tx) {
				execArtifactConstraintSQL(t, tx, `DELETE FROM agent_workflow_inputs WHERE id = $1 AND project_id = $2`, phase11ConstraintInputID, artifactConstraintProjectID)
			},
		},
	}
}

func insertArtifactConstraintParticipantOwner(
	t *testing.T,
	tx pgx.Tx,
	testCase artifactConstraintParticipantCase,
	exactSource governance.ChapterSourceRefInput,
) {
	t.Helper()
	insertProjectScopeArtifactOwnerKind(
		t, tx, testCase.kind, artifactConstraintOwnerID,
		artifactConstraintRefID, testCase.artifactID, 1,
	)
	setArtifactOwnerManifestHashFromRefs(t, tx, artifactConstraintOwnerID, artifactConstraintProjectID)
}

func insertArtifactConstraintParticipantDependencies(
	t *testing.T,
	db *storedb.DB,
) governance.ChapterSourceRefInput {
	t.Helper()
	const (
		bookID      = "00000000-0000-4000-8000-000000000b01"
		chapterID   = "00000000-0000-4000-8000-000000000b02"
		paragraphID = "00000000-0000-4000-8000-000000000b03"
		importJobID = "00000000-0000-4000-8000-000000000b06"
		chatJobID   = "00000000-0000-4000-8000-000000000b07"
	)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 12, 30, 0, 0, time.UTC)
	exactSource := seedArtifactConstraintExactSource(t, db)
	insertBookForExportStoreTest(t, db, bookID, artifactConstraintProjectID, "Constraint Book", now)
	insertChapterForExportStoreTest(
		t, db, chapterID, bookID, "Constraint Chapter", 1, "draft", 1, nil, nil, now,
	)
	insertParagraphForExportStoreTest(t, db, paragraphID, chapterID, 1, "draft", 1, "constraint paragraph", now)
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO jobs (
			id, project_id, kind, status, progress_json, result_json, error_json,
			governance_payload_state, subject_json, creator_user_id, can_cancel,
			queue_class, retry_count, max_attempts, next_attempt_at, created_at, updated_at
		) VALUES
			($1, $3, 'import', 'expired', '{}', NULL, NULL, 'source_deleted', NULL, NULL, false,
			 'user_demanded', 0, 3, statement_timestamp(), statement_timestamp(), statement_timestamp()),
			($2, $3, 'research_chat_turn', 'expired', '{}', NULL, NULL, 'source_deleted', NULL, NULL, false,
			 'interactive', 0, 3, statement_timestamp(), statement_timestamp(), statement_timestamp())
	`, importJobID, chatJobID, artifactConstraintProjectID); err != nil {
		t.Fatalf("insert dependency jobs: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO import_uploads (
			storage_key, project_id, mime_type, size_bytes, sha256, content, expires_at
		) VALUES (
			'constraint-dependency-upload', $1, 'text/plain', 1, 'aa', NULL,
			clock_timestamp() + interval '1 hour'
		)
	`, artifactConstraintProjectID); err != nil {
		t.Fatalf("insert dependency import upload: %v", err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	insertPhase11ResearchChatConstraintFixture(t, tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit dependency research Chat: %v", err)
	}
	return exactSource
}

func execArtifactConstraintSQL(t *testing.T, tx pgx.Tx, query string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("execute artifact participant fixture SQL: %v", err)
	}
}

func seedArtifactConstraintExactSource(
	t *testing.T,
	db *storedb.DB,
) governance.ChapterSourceRefInput {
	t.Helper()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	insertBookForExportStoreTest(t, db, artifactConstraintBookID, artifactConstraintProjectID, "Exact Source Book", now)
	insertChapterForExportStoreTest(t, db, artifactConstraintChapterID, artifactConstraintBookID, "Exact Source Chapter", 0, "draft", 1, nil, nil, now)
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO chapter_paragraphs (
			id, chapter_id, revision, variant, kind, "order", markdown, plain_text, word_count
		) VALUES ($1, $2, 1, 'draft', 'paragraph', 0, 'exact canonical content', 'exact canonical content', 3)
	`, artifactConstraintParaID, artifactConstraintChapterID); err != nil {
		t.Fatalf("insert exact source paragraph: %v", err)
	}
	vector := governance.SourceGenerationVector{
		ProjectID: artifactConstraintProjectID, ProjectGeneration: 1,
		BookID: artifactConstraintBookID, BookGeneration: 1,
		ChapterID: artifactConstraintChapterID, ChapterGeneration: 1,
	}
	locatorDigest, err := governance.SourceGenerationDigest(vector)
	if err != nil {
		t.Fatal(err)
	}
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	chapterHash, err := governance.CanonicalChapterContentDigest([]governance.CanonicalParagraphContent{{
		ParagraphID: artifactConstraintParaID, Order: 0, Content: "exact canonical content",
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO project_source_views (
			project_id, source_view, project_lifecycle_generation, catalog_revision
		) VALUES ($1, 'draft', 1, 1) ON CONFLICT DO NOTHING`, []any{artifactConstraintProjectID}},
		{`INSERT INTO book_source_members (
			project_id, source_view, project_lifecycle_generation,
			book_id, book_lifecycle_generation, title
		) VALUES ($1, 'draft', 1, $2, 1, 'Exact Source Book') ON CONFLICT DO NOTHING`, []any{artifactConstraintProjectID, artifactConstraintBookID}},
		{`INSERT INTO chapter_source_members (
			project_id, source_view, project_lifecycle_generation,
			book_id, book_lifecycle_generation, chapter_id,
			chapter_lifecycle_generation, source_revision, title, chapter_order
		) VALUES ($1, 'draft', 1, $2, 1, $3, 1, 1, 'Exact Source Chapter', 0) ON CONFLICT DO NOTHING`, []any{
			artifactConstraintProjectID, artifactConstraintBookID, artifactConstraintChapterID,
		}},
		{`INSERT INTO chapter_text_index_state (
			project_id, source_view, chapter_id, source_revision,
			locator_source_generation, chapter_content_hash, indexed_at
		) VALUES ($1, 'draft', $2, 1, $3, $4, $5) ON CONFLICT DO NOTHING`, []any{
			artifactConstraintProjectID, artifactConstraintChapterID, locatorDigest[:], chapterHash[:], now,
		}},
		{`INSERT INTO chapter_text_search_rows (
			project_id, source_view, book_id, chapter_id, source_revision,
			locator_source_generation, title, content, chapter_content_hash, indexed_at
		) VALUES ($1, 'draft', $2, $3, 1, $4, 'Exact Source Chapter', 'exact canonical content', $5, $6)
		ON CONFLICT DO NOTHING`, []any{
			artifactConstraintProjectID, artifactConstraintBookID, artifactConstraintChapterID,
			locatorDigest[:], chapterHash[:], now,
		}},
	} {
		if _, err := db.Pool.Exec(context.Background(), statement.query, statement.args...); err != nil {
			t.Fatalf("insert exact source catalog: %v", err)
		}
	}
	input := governance.ChapterSourceRefInput{
		Source: vector, SourceRevision: 1, SourceView: view,
		SourceGenerationDigest: locatorDigest, ContentHash: chapterHash,
		Chat: governance.AbsentChatArm(),
	}
	return input
}

func openArtifactConstraintTestDB(t *testing.T) *storedb.DB {
	t.Helper()
	db := openCurrentStoreTestDB(t)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	insertProjectStoreUserForTest(t, db, artifactConstraintUserID, "artifact-constraints@example.com")
	insertProjectStoreProjectForTest(t, db, artifactConstraintProjectID, artifactConstraintUserID, "Artifact Constraints", now)
	return db
}

func insertArtifactConstraintJobWithOwner(t *testing.T, tx pgx.Tx, jobID, ownerID string) {
	t.Helper()
	insertArtifactConstraintJobWithOwnerInProject(t, tx, jobID, artifactConstraintProjectID, ownerID)
}

func insertArtifactConstraintJobWithOwnerInProject(t *testing.T, tx pgx.Tx, jobID, projectID, ownerID string) {
	t.Helper()
	if _, err := tx.Exec(context.Background(), `
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
	`, jobID, projectID, ownerID, artifactConstraintUserID); err != nil {
		t.Fatalf("insert artifact job with owner: %v", err)
	}
}

func insertProjectScopeArtifactOwner(
	t *testing.T,
	tx pgx.Tx,
	ownerID string,
	refID string,
	artifactID string,
	refCount int,
) {
	t.Helper()
	insertProjectScopeArtifactOwnerKind(t, tx, "job", ownerID, refID, artifactID, refCount)
}

func insertProjectScopeArtifactOwnerKind(
	t *testing.T,
	tx pgx.Tx,
	kind string,
	ownerID string,
	refID string,
	artifactID string,
	refCount int,
) {
	t.Helper()
	ctx := context.Background()
	insertArtifactConstraintOwnerRow(t, tx, kind, ownerID, artifactID, refCount)
	if _, err := tx.Exec(ctx, `
		INSERT INTO artifact_source_refs (
			id, owner_id, project_id, ref_kind, scope_kind, source_view,
			project_generation, source_independent
		) VALUES ($1, $2, $3, 'scope', 'project', NULL, 1, true)
	`, refID, ownerID, artifactConstraintProjectID); err != nil {
		t.Fatalf("insert artifact source ref: %v", err)
	}
}

func insertArtifactConstraintOwnerRow(
	t *testing.T,
	tx pgx.Tx,
	kind string,
	ownerID string,
	artifactID string,
	refCount int,
) {
	t.Helper()
	if _, err := tx.Exec(context.Background(), `
		INSERT INTO artifact_source_owners (
			id, project_id, artifact_kind, artifact_id, source_view,
			manifest_version, manifest_hash, ref_count, read_state,
			source_identity_state
		) VALUES (
			$1, $2, $3, $4, NULL,
			1, decode(repeat('11', 32), 'hex'), $5, 'active',
			'scope_only'
		)
	`, ownerID, artifactConstraintProjectID, kind, artifactID, refCount); err != nil {
		t.Fatalf("insert artifact owner: %v", err)
	}
}

func setArtifactOwnerManifestHashFromRefs(t *testing.T, tx pgx.Tx, ownerID, projectID string) {
	t.Helper()
	if _, err := tx.Exec(context.Background(), `
		UPDATE artifact_source_owners AS owner
		SET manifest_hash = (
			SELECT ck_artifact_manifest_digest(
				owner.artifact_kind,
				owner.project_id,
				owner.artifact_id,
				owner.source_view,
				array_agg(CASE owner.manifest_version
					WHEN 2 THEN ck_artifact_source_ref_projection_v2(
						ref.ref_kind, ref.scope_kind, ref.project_id, ref.project_generation,
						ref.book_id, ref.book_generation, ref.chapter_id, ref.chapter_generation,
						ref.source_revision, ref.source_view, ref.locator_source_generation,
						ref.content_hash, ref.text_identity_state, ref.source_independent,
						ref.chat_session_id, ref.chat_lifecycle_epoch
					)
					ELSE ck_artifact_source_ref_projection(
						ref.ref_kind, ref.scope_kind, ref.project_id, ref.project_generation,
						ref.book_id, ref.book_generation, ref.chapter_id, ref.chapter_generation,
						ref.source_view, ref.source_independent,
						ref.chat_session_id, ref.chat_lifecycle_epoch
					)
				END))
			FROM artifact_source_refs AS ref
			WHERE ref.owner_id = owner.id
				AND ref.project_id = owner.project_id
		)
		WHERE owner.id = $1 AND owner.project_id = $2
	`, ownerID, projectID); err != nil {
		t.Fatalf("derive artifact manifest hash from refs: %v", err)
	}
}
