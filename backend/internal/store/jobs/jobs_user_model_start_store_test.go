package jobstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/observability"
	governancestore "canonkeeper/backend/internal/store/governance"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const userModelStartChatID = "00000000-0000-4000-8000-000000009821"

func TestStartUserModelJobInTxCreatesAndReplaysSavedIDs(t *testing.T) {
	fixture, source := newUserModelStartFixture(t)
	input := fixture.userModelStartInput("same-key")
	var targetCalls, createCalls atomic.Int32
	view, _ := governance.PresentSourceView(governance.SourceViewDraft)
	chat, _ := governance.PresentChatArm(fixture.projectID, userModelStartChatID, 1)
	ambientRef, err := governance.NewBookArtifactRef(governance.SourceGenerationVector{
		ProjectID: fixture.projectID, ProjectGeneration: 1, BookID: fixture.bookID, BookGeneration: 1,
	}, view, chat)
	if err != nil {
		t.Fatal(err)
	}
	fullSource := jobs.JobSourceManifest{SourceView: view, Refs: append(append([]governance.ArtifactSourceRef{}, source.Refs...), ambientRef)}
	createProduct := fixture.userModelProductCreator(1, &createCalls)
	createWithAmbient := func(ctx context.Context, tx pgx.Tx, ids UserModelStartIDs, target UserModelLockedTarget) (UserModelCreated, error) {
		target.Source = fullSource
		return createProduct(ctx, tx, ids, target)
	}
	lastStage := ""
	fixture.store.modelStartFailpoint = func(point string) error {
		lastStage = point
		return nil
	}

	first, err := runUserModelStart(
		fixture, input,
		fixture.userModelTargetLocker(source, "request-same", &targetCalls),
		createWithAmbient,
	)
	if err != nil {
		t.Fatalf("%v after %s (target calls %d)", err, lastStage, targetCalls.Load())
	}
	second, err := runUserModelStart(
		fixture, input,
		fixture.userModelTargetLocker(source, "request-same", &targetCalls),
		createWithAmbient,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Job.ID != second.Job.ID || first.WorkflowInputID != second.WorkflowInputID || first.TargetID != second.TargetID {
		t.Fatalf("replay ids differ: first=%+v second=%+v", first, second)
	}
	if targetCalls.Load() != 2 || createCalls.Load() != 1 {
		t.Fatalf("callbacks: target=%d create=%d", targetCalls.Load(), createCalls.Load())
	}
	if first.Job.QueueClass != jobs.QueueClassInteractive || first.Job.CreatorUserID != fixture.actorID || !first.Job.CanCancel {
		t.Fatalf("created job = %+v", first.Job)
	}
	assertUserModelStartRowCounts(t, fixture, 1, 1, 1, 1)
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `
		UPDATE jobs SET status = 'failed', progress_json = '{}'::jsonb, updated_at = clock_timestamp()
		WHERE project_id = $1 AND id = $2
	`, fixture.projectID, first.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `
		DELETE FROM job_model_bindings WHERE project_id = $1 AND job_id = $2
	`, fixture.projectID, first.Job.ID); err != nil {
		t.Fatal(err)
	}
	terminal, err := runUserModelStart(
		fixture, input,
		fixture.userModelTargetLocker(source, "request-same", nil),
		createWithAmbient,
	)
	if err != nil || terminal.Job.Status != jobs.StatusFailed || createCalls.Load() != 1 {
		t.Fatalf("terminal replay: result=%+v err=%v create=%d", terminal, err, createCalls.Load())
	}

	_, err = runUserModelStart(
		fixture, input,
		fixture.userModelTargetLocker(source, "changed-request", nil),
		createWithAmbient,
	)
	if !errors.Is(err, jobs.ErrConflict) || createCalls.Load() != 1 {
		t.Fatalf("changed request: err=%v create=%d", err, createCalls.Load())
	}
}

func TestStartUserModelJobInTxKeepsCreatingRequestID(t *testing.T) {
	const creatingRequestID = "00000000-0000-4000-8000-000000009831"
	fixture, source := newUserModelStartFixture(t)
	input := fixture.userModelStartInput("creating-request-key")
	creating := fixture
	creating.ctx = observability.ContextWithRequestID(fixture.ctx, creatingRequestID)
	replaying := fixture
	replaying.ctx = observability.ContextWithRequestID(fixture.ctx, "00000000-0000-4000-8000-000000009832")

	first, err := runUserModelStart(creating, input,
		creating.userModelTargetLocker(source, "request-same", nil), creating.userModelProductCreator(1, nil))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := runUserModelStart(replaying, input,
		replaying.userModelTargetLocker(source, "request-same", nil), replaying.userModelProductCreator(1, nil))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := fixture.store.GetJob(fixture.ctx, jobs.GetJobStoreInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, JobID: first.Job.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, job := range map[string]jobs.Job{"created": first.Job, "replayed": replayed.Job, "loaded": loaded} {
		if job.CreatorRequestID != creatingRequestID {
			t.Fatalf("%s job creator request = %q, want %q", name, job.CreatorRequestID, creatingRequestID)
		}
	}
}

func TestStartUserModelJobInTxConcurrentReplayCreatesOneProduct(t *testing.T) {
	fixture, source := newUserModelStartFixture(t)
	input := fixture.userModelStartInput("concurrent-key")
	var createCalls atomic.Int32
	results := make(chan UserModelStartResult, 2)
	errorsChannel := make(chan error, 2)
	for range 2 {
		go func() {
			result, err := runUserModelStart(
				fixture, input,
				fixture.userModelTargetLocker(source, "concurrent-request", nil),
				fixture.userModelProductCreator(1, &createCalls),
			)
			results <- result
			errorsChannel <- err
		}()
	}
	first, second := <-results, <-results
	for range 2 {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
	}
	if first.Job.ID != second.Job.ID || first.WorkflowInputID != second.WorkflowInputID ||
		first.TargetID != second.TargetID || createCalls.Load() != 1 {
		t.Fatalf("concurrent results: first=%+v second=%+v create=%d", first, second, createCalls.Load())
	}
	assertUserModelStartRowCounts(t, fixture, 1, 1, 1, 1)
}

func TestStartUserModelJobInTxFailpointRollsBackEveryRow(t *testing.T) {
	fixture, source := newUserModelStartFixture(t)
	wantErr := errors.New("injected model start failure")
	fixture.store.modelStartFailpoint = func(point string) error {
		if point == "after_binding_insert" {
			return wantErr
		}
		return nil
	}
	_, err := runUserModelStart(
		fixture,
		fixture.userModelStartInput("rollback-key"),
		fixture.userModelTargetLocker(source, "rollback-request", nil),
		fixture.userModelProductCreator(1, nil),
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want injected failure", err)
	}
	assertUserModelStartRowCounts(t, fixture, 0, 0, 0, 0)
}

func TestUserModelWorkflowInputOwnerRequiresExactV2Identity(t *testing.T) {
	fixture := newModelStartFixture(t)
	projectID := fixture.projectID
	inputID := "00000000-0000-4000-8000-000000009832"
	ownerID := "00000000-0000-4000-8000-000000009837"
	refID := "00000000-0000-4000-8000-000000009838"
	vector := governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: 1,
		BookID: fixture.bookID, BookGeneration: 1,
		ChapterID: fixture.chapterID, ChapterGeneration: 1,
	}
	generation, err := governance.SourceGenerationDigest(vector)
	if err != nil {
		t.Fatal(err)
	}
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	chapterHash, err := governance.CanonicalChapterContentDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewExactChapterArtifactRef(governance.ChapterSourceRefInput{
		Source: vector, SourceRevision: 1, SourceView: view,
		SourceGenerationDigest: generation, ContentHash: chapterHash, Chat: governance.AbsentChatArm(),
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := newModelArtifactManifest(2, governance.ArtifactManifestInput{
		Kind: governance.ArtifactAgentWorkflowInput, ProjectID: projectID, ArtifactID: inputID,
		OwnerSourceView: view, Refs: []governance.ArtifactSourceRef{ref},
	})
	if err != nil {
		t.Fatal(err)
	}
	fields, err := ref.Fields()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := fixture.db.Pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(fixture.ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	hash := manifest.Hash()
	bodyDigest := sha256.Sum256([]byte(`{}`))
	sourceRows := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO book_source_members (
		   project_id, source_view, project_lifecycle_generation,
		   book_id, book_lifecycle_generation, title
		 ) VALUES ($1, 'draft', 1, $2, 1, 'Model Start Book')`, []any{projectID, fixture.bookID}},
		{`INSERT INTO chapter_source_members (
		   project_id, source_view, project_lifecycle_generation,
		   book_id, book_lifecycle_generation, chapter_id,
		   chapter_lifecycle_generation, source_revision, title, chapter_order
		 ) VALUES ($1, 'draft', 1, $2, 1, $3, 1, 1, 'Model Start Chapter', 1)`,
			[]any{projectID, fixture.bookID, fixture.chapterID}},
		{`INSERT INTO chapter_text_search_rows (
		   project_id, source_view, book_id, chapter_id, source_revision,
		   locator_source_generation, title, content, chapter_content_hash, indexed_at
		 ) VALUES ($1, 'draft', $2, $3, 1, $4, 'Model Start Chapter', '', $5, clock_timestamp())`,
			[]any{projectID, fixture.bookID, fixture.chapterID,
				fields.LocatorSourceGenerationDigest[:], fields.ContentHash[:]}},
	}
	for _, row := range sourceRows {
		if _, err := tx.Exec(fixture.ctx, row.query, row.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(fixture.ctx, `
		INSERT INTO artifact_source_owners (
		  id, project_id, artifact_kind, artifact_id, source_view,
		  manifest_version, manifest_hash, ref_count, read_state, source_identity_state
		) VALUES ($1, $2, 'agent_workflow_input', $3, 'draft', 2, $4, 1, 'active', 'text_derived_current')
	`, ownerID, projectID, inputID, hash[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(fixture.ctx, `
		INSERT INTO artifact_source_refs (
		  id, owner_id, project_id, ref_kind, source_view, project_generation,
		  book_id, book_generation, chapter_id, chapter_generation,
		  source_revision, content_hash,
		  locator_source_generation, text_identity_state, source_independent
		) VALUES (
		  $1, $2, $3, 'chapter', 'draft', 1,
		  $4, 1, $5, 1, 1, $6, $7,
		  'text_derived_current', false
		)
	`, refID, ownerID, projectID, fixture.bookID, fixture.chapterID,
		fields.ContentHash[:], fields.LocatorSourceGenerationDigest[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(fixture.ctx, `
		INSERT INTO agent_workflow_inputs (
		  id, project_id, job_id, job_kind, target_id, source_view,
		  body_json, body_digest, artifact_source_owner_id, created_at
		) VALUES ($1, $2, $3, 'research_chat_turn', $4, 'draft', '{}'::jsonb, $5, $6, $7)
	`, inputID, projectID, "00000000-0000-4000-8000-000000009839",
		"00000000-0000-4000-8000-000000009840", bodyDigest[:], ownerID, fixture.now); err != nil {
		t.Fatal(err)
	}
	owner, err := readModelPrivateInputOwner(fixture.ctx, tx, mustUUID(projectID), mustUUID(ownerID))
	if err != nil {
		t.Fatal(err)
	}
	target := UserModelLockedTarget{
		Source:          jobs.JobSourceManifest{SourceView: view, Refs: []governance.ArtifactSourceRef{ref}},
		ManifestVersion: 2, RequestHash: "request",
	}
	if !userModelWorkflowInputOwnerMatches(owner, UserModelStartIDs{WorkflowInputID: inputID}, target, true) {
		t.Fatal("persisted exact V2 owner was rejected")
	}
}

func newUserModelStartFixture(t *testing.T) (modelStartFixture, jobs.JobSourceManifest) {
	t.Helper()
	fixture := newModelStartFixture(t)
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `
		INSERT INTO chat_sessions (id, project_id, source_view, scope, title)
		VALUES ($1, $2, 'draft', 'project', 'Coordinator test')
	`, userModelStartChatID, fixture.projectID); err != nil {
		t.Fatal(err)
	}
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := governance.PresentChatArm(fixture.projectID, userModelStartChatID, 1)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{
		ProjectID: fixture.projectID, ProjectGeneration: 1,
	}, view, chat)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, jobs.JobSourceManifest{SourceView: view, Refs: []governance.ArtifactSourceRef{ref}}
}

func (fixture modelStartFixture) userModelStartInput(key string) UserModelStartInput {
	return UserModelStartInput{
		ProjectID: fixture.projectID, Kind: jobs.KindResearchChatTurn,
		ActorUserID:          fixture.actorID,
		SessionProof:         auth.SessionWriteProof{SessionHash: fixture.sessionHash, CSRFSecretHash: fixture.csrfHash},
		InputContractVersion: 1, CreatedAt: fixture.now,
		Idempotency: UserModelStartIdempotency{
			ScopeHash: "user-model-scope", OperationID: "research-chat-start",
			IdempotencyKeyHash: key, StatusCode: 202,
		},
	}
}

func (fixture modelStartFixture) userModelTargetLocker(
	source jobs.JobSourceManifest,
	requestHash string,
	calls *atomic.Int32,
) LockUserModelTargetInTx {
	return func(ctx context.Context, tx pgx.Tx) (UserModelLockedTarget, error) {
		if calls != nil {
			calls.Add(1)
		}
		var lifecycle int64
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_epoch
			FROM chat_sessions
			WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL
			FOR UPDATE
		`, userModelStartChatID, fixture.projectID).Scan(&lifecycle); err != nil {
			return UserModelLockedTarget{}, err
		}
		if lifecycle != 1 {
			return UserModelLockedTarget{}, jobs.ErrConflict
		}
		return UserModelLockedTarget{Source: source, ManifestVersion: 1, RequestHash: requestHash}, nil
	}
}

func (fixture modelStartFixture) userModelProductCreator(
	workflowManifestVersion uint32,
	calls *atomic.Int32,
) CreateUserModelProductInTx {
	return func(
		ctx context.Context,
		tx pgx.Tx,
		ids UserModelStartIDs,
		target UserModelLockedTarget,
	) (UserModelCreated, error) {
		if calls != nil {
			calls.Add(1)
		}
		if _, err := fixture.createUserModelArtifact(ctx, tx, governance.ArtifactChatTurn, ids.TargetID, target, target.ManifestVersion,
			func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
				_, err := tx.Exec(ctx, `
					INSERT INTO chat_turns (
					  id, chat_id, project_id, source_view, job_id, workflow_input_id,
					  artifact_source_owner_id, created_at, updated_at
					) VALUES ($1, $2, $3, 'draft', $4, $5, $6, $7, $7)
				`, ids.TargetID, userModelStartChatID, fixture.projectID, ids.JobID,
					ids.WorkflowInputID, ownerID, fixture.now)
				return err
			}); err != nil {
			return UserModelCreated{}, fmt.Errorf("create turn: %w", err)
		}
		if _, err := fixture.createUserModelArtifact(ctx, tx, governance.ArtifactAgentWorkflowInput, ids.WorkflowInputID, target, workflowManifestVersion,
			func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
				body := []byte(`{"body":"question"}`)
				digest := sha256.Sum256(body)
				_, err := sqlc.New(tx).CreateAgentWorkflowInput(ctx, sqlc.CreateAgentWorkflowInputParams{
					ID: mustUUID(ids.WorkflowInputID), ProjectID: mustUUID(fixture.projectID),
					JobID: mustUUID(ids.JobID), JobKind: string(jobs.KindResearchChatTurn),
					TargetID: mustUUID(ids.TargetID), SourceView: string(governance.SourceViewDraft),
					BodyJson: body, BodyDigest: digest[:], ArtifactSourceOwnerID: ownerID,
					CreatedAt: pgtype.Timestamptz{Time: fixture.now, Valid: true},
				})
				return err
			}); err != nil {
			return UserModelCreated{}, fmt.Errorf("create workflow input: %w", err)
		}
		return UserModelCreated{Subject: jobs.ResearchChatTurnSubject{
			Type: jobs.KindResearchChatTurn, ProjectID: fixture.projectID,
			ChatID: userModelStartChatID, TurnID: ids.TargetID,
			SourceView: governance.SourceViewDraft, Scope: "project",
		}, Target: target}, nil
	}
}

func (fixture modelStartFixture) createUserModelArtifact(
	ctx context.Context,
	tx pgx.Tx,
	kind governance.ArtifactKind,
	artifactID string,
	target UserModelLockedTarget,
	manifestVersion uint32,
	create governancestore.ArtifactSourceCreateMutation,
) (string, error) {
	ownerID, err := newUUIDValue()
	if err != nil {
		return "", err
	}
	manifest, err := newModelArtifactManifest(manifestVersion, governance.ArtifactManifestInput{
		Kind: kind, ProjectID: fixture.projectID, ArtifactID: artifactID,
		OwnerSourceView: target.Source.SourceView, Refs: target.Source.Refs,
	})
	if err != nil {
		return "", err
	}
	err = governancestore.New(fixture.db).CreateArtifactWithSources(ctx, tx, governancestore.SealArtifactSourcesInput{
		OwnerID: ownerID.String(), ProjectID: fixture.projectID, ArtifactID: artifactID,
		Kind: kind, SourceView: target.Source.SourceView, Refs: target.Source.Refs, Manifest: manifest,
	}, create)
	return ownerID.String(), err
}

func runUserModelStart(
	fixture modelStartFixture,
	input UserModelStartInput,
	lockTarget LockUserModelTargetInTx,
	createProduct CreateUserModelProductInTx,
) (UserModelStartResult, error) {
	var result UserModelStartResult
	err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		var err error
		result, err = fixture.store.StartUserModelJobInTx(fixture.ctx, tx, input, lockTarget, createProduct)
		return err
	})
	return result, err
}

func assertUserModelStartRowCounts(
	t *testing.T,
	fixture modelStartFixture,
	wantJobs, wantInputs, wantTurns, wantIdempotency int,
) {
	t.Helper()
	var gotJobs, gotInputs, gotTurns, gotIdempotency int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT
		  (SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'research_chat_turn'),
		  (SELECT count(*) FROM agent_workflow_inputs WHERE project_id = $1),
		  (SELECT count(*) FROM chat_turns WHERE project_id = $1),
		  (SELECT count(*) FROM idempotency_records WHERE operation_id = 'research-chat-start')
	`, fixture.projectID).Scan(&gotJobs, &gotInputs, &gotTurns, &gotIdempotency); err != nil {
		t.Fatal(err)
	}
	if gotJobs != wantJobs || gotInputs != wantInputs || gotTurns != wantTurns || gotIdempotency != wantIdempotency {
		t.Fatalf("rows = jobs:%d inputs:%d turns:%d idempotency:%d", gotJobs, gotInputs, gotTurns, gotIdempotency)
	}
}
