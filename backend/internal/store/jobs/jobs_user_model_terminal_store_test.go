package jobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	governancestore "canonkeeper/backend/internal/store/governance"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestTerminalizeUserModelJobInTxClosesAndScrubsPrivateInput(t *testing.T) {
	tests := []struct {
		name       string
		from       jobs.Status
		to         jobs.Status
		claim      int64
		result     jobs.Result
		jobError   *jobs.Error
		wantResult bool
		wantError  bool
	}{
		{
			name: "success", from: jobs.StatusRunning, to: jobs.StatusSucceeded,
			claim: 1, wantResult: true,
		},
		{
			name: "failure", from: jobs.StatusRunning, to: jobs.StatusFailed,
			claim: 1, jobError: &jobs.Error{Code: "provider.failed", Message: "provider failed"}, wantError: true,
		},
		{name: "running cancel", from: jobs.StatusCanceling, to: jobs.StatusCanceled, claim: 1},
		{name: "queued cancel", from: jobs.StatusQueued, to: jobs.StatusCanceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, ownerID := newUserModelTerminalFixture(t, 2, test.from)
			if test.wantResult {
				test.result = jobs.ResearchChatTurnResult{
					Type: jobs.KindResearchChatTurn, ChatID: userModelStartChatID, TurnID: fixture.targetID,
					Response: "answer",
				}
			}
			err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
				return fixture.store.TerminalizeUserModelJobInTx(fixture.ctx, tx, TerminalizeUserModelJobInput{
					Kind: jobs.KindResearchChatTurn, ProjectID: fixture.projectID, JobID: fixture.jobID,
					ClaimIncarnation: test.claim, From: test.from, To: test.to, Result: test.result, Error: test.jobError,
				})
			})
			if err != nil {
				t.Fatal(err)
			}
			assertUserModelTerminalState(t, fixture, ownerID, test.to, test.wantResult, test.wantError)
		})
	}
}

func TestTerminalizeUserModelJobInTxRollsBackV1OwnerMismatch(t *testing.T) {
	fixture, ownerID := newUserModelTerminalFixture(t, 1, jobs.StatusRunning)
	before := userModelTerminalSnapshot(t, fixture, ownerID)
	err := fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		return fixture.store.TerminalizeUserModelJobInTx(fixture.ctx, tx, TerminalizeUserModelJobInput{
			Kind: jobs.KindResearchChatTurn, ProjectID: fixture.projectID, JobID: fixture.jobID,
			ClaimIncarnation: 1, From: jobs.StatusRunning, To: jobs.StatusSucceeded,
			Result: jobs.ResearchChatTurnResult{
				Type: jobs.KindResearchChatTurn, ChatID: userModelStartChatID, TurnID: fixture.targetID,
				Response: "answer",
			},
		})
	})
	if !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("error = %v, want conflict", err)
	}
	after := userModelTerminalSnapshot(t, fixture, ownerID)
	if before != after {
		t.Fatalf("mismatch changed rows: before=%+v after=%+v", before, after)
	}
}

func newUserModelTerminalFixture(
	t *testing.T,
	manifestVersion uint32,
	status jobs.Status,
) (userModelTerminalFixture, pgtype.UUID) {
	t.Helper()
	base := newModelTerminalFixture(t, status)
	fixture := userModelTerminalFixture{
		modelTerminalFixture: base, store: New(base.db),
		kind: jobs.KindResearchChatTurn, targetID: "00000000-0000-4000-8000-000000009840",
	}
	configureModelTerminalUserClaim(t, base, jobs.KindResearchChatTurn, jobs.ResearchChatTurnSubject{
		Type: jobs.KindResearchChatTurn, ProjectID: base.projectID,
		ChatID: userModelStartChatID, TurnID: fixture.targetID,
		SourceView: governance.SourceViewDraft, Scope: "project",
	})
	return fixture, insertAgentWorkflowInputForTerminalTest(t, fixture, manifestVersion)
}

type userModelTerminalFixture struct {
	modelTerminalFixture
	store    *Store
	kind     jobs.Kind
	targetID string
}

func insertAgentWorkflowInputForTerminalTest(
	t *testing.T,
	fixture userModelTerminalFixture,
	manifestVersion uint32,
) pgtype.UUID {
	t.Helper()
	const inputID = "00000000-0000-4000-8000-000000009832"
	const ownerID = "00000000-0000-4000-8000-000000009837"
	body := []byte(`{"body":"question"}`)
	bodyDigest := sha256.Sum256(body)
	if manifestVersion == 1 {
		manifest, err := newModelArtifactManifest(1, governance.ArtifactManifestInput{
			Kind: governance.ArtifactAgentWorkflowInput, ProjectID: fixture.projectID, ArtifactID: inputID,
			OwnerSourceView: fixture.expectedManifest.SourceView, Refs: fixture.expectedManifest.Refs,
		})
		if err != nil {
			t.Fatal(err)
		}
		err = fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
			return governancestore.New(fixture.db).CreateArtifactWithSources(fixture.ctx, tx, governancestore.SealArtifactSourcesInput{
				OwnerID: ownerID, ProjectID: fixture.projectID, ArtifactID: inputID,
				Kind: governance.ArtifactAgentWorkflowInput, SourceView: fixture.expectedManifest.SourceView,
				Refs: fixture.expectedManifest.Refs, Manifest: manifest,
			}, func(ctx context.Context, tx pgx.Tx, sealedOwnerID pgtype.UUID) error {
				_, err := tx.Exec(ctx, `
					INSERT INTO agent_workflow_inputs (
					  id, project_id, job_id, job_kind, target_id, source_view,
					  body_json, body_digest, artifact_source_owner_id, created_at
					) VALUES ($1, $2, $3, $4, $5, 'draft', $6, $7, $8, clock_timestamp())
				`, inputID, fixture.projectID, fixture.jobID, string(fixture.kind), fixture.targetID, body, bodyDigest[:], sealedOwnerID)
				return err
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		return mustUUID(ownerID)
	}
	if manifestVersion != 2 {
		t.Fatalf("manifest version = %d", manifestVersion)
	}
	return insertV2AgentWorkflowInputForTerminalTest(t, fixture, inputID, ownerID, body, bodyDigest)
}

func insertV2AgentWorkflowInputForTerminalTest(
	t *testing.T,
	fixture userModelTerminalFixture,
	inputID string,
	ownerID string,
	body []byte,
	bodyDigest [sha256.Size]byte,
) pgtype.UUID {
	t.Helper()
	const paragraphID = "00000000-0000-4000-8000-000000009835"
	const content = "current chapter"
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
	chapterHash, err := governance.CanonicalChapterContentDigest([]governance.CanonicalParagraphContent{{
		ParagraphID: paragraphID, Order: 0, Content: content,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewExactChapterArtifactRef(governance.ChapterSourceRefInput{
		Source: vector, SourceRevision: 1, SourceView: view,
		SourceGenerationDigest: generation, ContentHash: chapterHash,
		Chat: governance.AbsentChatArm(),
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := newModelArtifactManifest(2, governance.ArtifactManifestInput{
		Kind: governance.ArtifactAgentWorkflowInput, ProjectID: fixture.projectID, ArtifactID: inputID,
		OwnerSourceView: view, Refs: []governance.ArtifactSourceRef{ref},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Сначала создаётся current lexical source с отдельным owner: terminal scrub
	// private input не должен затрагивать сам текстовый индекс.
	err = fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
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
			 ) VALUES ($1,'draft',1,$2,1,$3,1,1,'Model Chapter',1)`, []any{fixture.projectID, fixture.bookID, fixture.chapterID}},
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
	})
	if err != nil {
		t.Fatal(err)
	}
	err = fixture.db.WithTx(fixture.ctx, func(tx pgx.Tx) error {
		return governancestore.New(fixture.db).CreateArtifactWithSources(fixture.ctx, tx, governancestore.SealArtifactSourcesInput{
			OwnerID: ownerID, ProjectID: fixture.projectID, ArtifactID: inputID,
			Kind: governance.ArtifactAgentWorkflowInput, SourceView: view,
			Refs: []governance.ArtifactSourceRef{ref}, Manifest: manifest,
		}, func(ctx context.Context, tx pgx.Tx, sealedOwnerID pgtype.UUID) error {
			_, err := tx.Exec(ctx, `
				INSERT INTO agent_workflow_inputs (
				  id, project_id, job_id, job_kind, target_id, source_view,
				  body_json, body_digest, artifact_source_owner_id, created_at
				) VALUES ($1,$2,$3,$4,$5,'draft',$6,$7,$8,clock_timestamp())
			`, inputID, fixture.projectID, fixture.jobID, string(fixture.kind), fixture.targetID, body, bodyDigest[:], sealedOwnerID)
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return mustUUID(ownerID)
}

type userModelTerminalState struct {
	status           jobs.Status
	claimIncarnation int64
	retryCount       int
	inputs           int
	owners           int
	refs             int
	bindings         int
}

func userModelTerminalSnapshot(
	t *testing.T,
	fixture userModelTerminalFixture,
	ownerID pgtype.UUID,
) userModelTerminalState {
	t.Helper()
	var state userModelTerminalState
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT job.status, job.claim_incarnation, job.retry_count,
		       (SELECT count(*) FROM agent_workflow_inputs WHERE project_id = $1 AND job_id = $2),
		       (SELECT count(*) FROM artifact_source_owners WHERE project_id = $1 AND id = $3),
		       (SELECT count(*) FROM artifact_source_refs WHERE project_id = $1 AND owner_id = $3),
		       (SELECT count(*) FROM job_model_bindings WHERE project_id = $1 AND job_id = $2)
		FROM jobs AS job
		WHERE job.project_id = $1 AND job.id = $2
	`, fixture.projectID, fixture.jobID, ownerID).Scan(
		&state.status, &state.claimIncarnation, &state.retryCount,
		&state.inputs, &state.owners, &state.refs, &state.bindings,
	); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertUserModelTerminalState(
	t *testing.T,
	fixture userModelTerminalFixture,
	ownerID pgtype.UUID,
	wantStatus jobs.Status,
	wantResult bool,
	wantError bool,
) {
	t.Helper()
	state := userModelTerminalSnapshot(t, fixture, ownerID)
	if state.status != wantStatus || state.inputs != 0 || state.owners != 0 || state.refs != 0 || state.bindings != 0 {
		t.Fatalf("terminal state = %+v", state)
	}
	var resultJSON, errorJSON []byte
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `
		SELECT result_json, error_json FROM jobs WHERE project_id = $1 AND id = $2
	`, fixture.projectID, fixture.jobID).Scan(&resultJSON, &errorJSON); err != nil {
		t.Fatal(err)
	}
	if (len(bytes.TrimSpace(resultJSON)) > 0) != wantResult || (len(bytes.TrimSpace(errorJSON)) > 0) != wantError {
		t.Fatalf("terminal payloads: result=%s error=%s", resultJSON, errorJSON)
	}
}
