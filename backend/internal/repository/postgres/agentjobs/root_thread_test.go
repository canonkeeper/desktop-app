package agentjobs

import (
	"context"
	"errors"
	"testing"
	"time"

	agentdomain "canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
	"canonkeeper/backend/internal/store/sqlc"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	testActorID       = "71000000-0000-4000-8000-000000000001"
	testProjectID     = "71000000-0000-4000-8000-000000000002"
	testMembershipID  = "71000000-0000-4000-8000-000000000003"
	testAuthSessionID = "71000000-0000-4000-8000-000000000004"
	testBookID        = "71000000-0000-4000-8000-000000000005"
	testChatID        = "71000000-0000-4000-8000-000000000008"
	testTurnID        = "71000000-0000-4000-8000-000000000009"
	testChatJobID     = "71000000-0000-4000-8000-000000000010"
	testChatOwnerID   = "71000000-0000-4000-8000-000000000011"
	testTurnOwnerID   = "71000000-0000-4000-8000-000000000012"
	testDecoyTurnID   = "71000000-0000-4000-8000-000000000013"
	testDecoyJobID    = "71000000-0000-4000-8000-000000000014"
)

type postgresFixture struct {
	db    *storedb.DB
	store *Store
	now   time.Time
}

func newPostgresFixture(t *testing.T, through ...int64) *postgresFixture {
	t.Helper()
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_native_agent_jobs", through...)
	now := time.Now().UTC().Truncate(time.Microsecond)
	statements := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO users (id, email, display_name, password_hash, email_verified_at) VALUES ($1, 'native-agent@example.test', 'Native Agent', 'hash', $2)", []any{testActorID, now}},
		{"INSERT INTO sessions (id, user_id, session_hash, csrf_secret_hash, expires_at, created_at) VALUES ($1, $2, repeat('a', 64), repeat('b', 64), $3, $4)", []any{testAuthSessionID, testActorID, now.Add(time.Hour), now}},
		{"INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at) VALUES ($1, $2, 'Native Agent Project', '', $3, $3)", []any{testProjectID, testActorID, now}},
		{"INSERT INTO project_memberships (id, project_id, user_id, role, created_at, updated_at) VALUES ($1, $2, $3, 'editor', $4, $4)", []any{testMembershipID, testProjectID, testActorID, now}},
		{"INSERT INTO project_source_views (project_id, source_view, project_lifecycle_generation, catalog_revision) VALUES ($1, 'draft', 1, 1)", []any{testProjectID}},
		{"INSERT INTO books (id, project_id, title, \"order\", display_number, status, revision, created_at, updated_at) VALUES ($1, $2, 'Native Agent Book', 1, '1', 'draft', 1, $3, $3)", []any{testBookID, testProjectID, now}},
		{"INSERT INTO book_source_members (project_id, source_view, project_lifecycle_generation, book_id, book_lifecycle_generation, title) VALUES ($1, 'draft', 1, $2, 1, 'Native Agent Book')", []any{testProjectID, testBookID}},
	}
	for _, statement := range statements {
		if _, err := db.Pool.Exec(context.Background(), statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return &postgresFixture{db: db, store: New(db), now: now}
}

func (fixture *postgresFixture) seedChatJob(t *testing.T) {
	t.Helper()
	if _, err := fixture.db.Pool.Exec(context.Background(), `
		INSERT INTO chat_sessions (id, project_id, source_view, scope, title, created_at, updated_at)
		VALUES ($1, $2, 'draft', 'project', 'Research', $3, $3)
	`, testChatID, testProjectID, fixture.now); err != nil {
		t.Fatal(err)
	}
	subjectJSON, err := jobs.EncodeSubject(jobs.KindResearchChatTurn, jobs.ResearchChatTurnSubject{
		Type: jobs.KindResearchChatTurn, ProjectID: testProjectID, ChatID: testChatID,
		TurnID: testTurnID, SourceView: governance.SourceViewDraft, Scope: "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.seedJob(t, testChatJobID, testChatOwnerID, jobs.KindResearchChatTurn, subjectJSON, "user")
	fixture.seedChatTurn(t, testTurnID, testChatJobID, testTurnOwnerID, "turn-chat-1")
}

func (fixture *postgresFixture) seedChatTurn(t *testing.T, turnID, jobID, ownerID, providerTurnID string) {
	t.Helper()
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	chatArm, err := governance.PresentChatArm(testProjectID, testChatID, 1)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewBookArtifactRef(governance.SourceGenerationVector{
		ProjectID: testProjectID, ProjectGeneration: 1, BookID: testBookID, BookGeneration: 1,
	}, view, chatArm)
	if err != nil {
		t.Fatal(err)
	}
	refs := []governance.ArtifactSourceRef{ref}
	manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: governance.ArtifactChatTurn, ProjectID: testProjectID, ArtifactID: turnID,
		OwnerSourceView: view, Refs: refs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		return governancestore.New(fixture.db).CreateArtifactWithSources(context.Background(), tx, governancestore.SealArtifactSourcesInput{
			OwnerID: ownerID, ProjectID: testProjectID, ArtifactID: turnID,
			Kind: governance.ArtifactChatTurn, SourceView: view, Refs: refs, Manifest: manifest,
		}, func(ctx context.Context, tx pgx.Tx, storedOwner pgtype.UUID) error {
			_, err := tx.Exec(ctx, `
				INSERT INTO chat_turns (id, chat_id, project_id, source_view, job_id, artifact_source_owner_id, provider_turn_id)
				VALUES ($1, $2, $3, 'draft', $4, $5, $6)
			`, turnID, testChatID, testProjectID, jobID, storedOwner, providerTurnID)
			return err
		})
	}); err != nil {
		t.Fatal(err)
	}
}

func (fixture *postgresFixture) seedJob(t *testing.T, jobID, ownerID string, kind jobs.Kind, subjectJSON []byte, origin string) {
	t.Helper()
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewBookArtifactRef(governance.SourceGenerationVector{
		ProjectID: testProjectID, ProjectGeneration: 1, BookID: testBookID, BookGeneration: 1,
	}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	refs := []governance.ArtifactSourceRef{ref}
	manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: testProjectID, ArtifactID: jobID,
		OwnerSourceView: view, Refs: refs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		return governancestore.New(fixture.db).CreateArtifactWithSources(context.Background(), tx, governancestore.SealArtifactSourcesInput{
			OwnerID: ownerID, ProjectID: testProjectID, ArtifactID: jobID,
			Kind: governance.ArtifactJob, SourceView: view, Refs: refs, Manifest: manifest,
		}, func(ctx context.Context, tx pgx.Tx, storedOwner pgtype.UUID) error {
			queueClass, creator, canCancel := "automatic", pgtype.UUID{}, false
			if origin == "user" {
				creator, err = storedb.ParseUUID(testActorID)
				if err != nil {
					return err
				}
				queueClass, canCancel = "interactive", true
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO jobs (
				  id, project_id, kind, status, progress_json, subject_json,
				  creator_user_id, can_cancel, queue_class, retry_count, max_attempts,
				  next_attempt_at, claimed_at, started_at, claim_incarnation,
				  artifact_source_owner_id, governance_payload_state, created_at, updated_at
				) VALUES ($1, $2, $3, 'running', '{}'::jsonb, $4::jsonb,
				          $5, $6, $7, 0, 3, $8, $8, $8, 1, $9, 'active', $8, $8)
			`, jobID, testProjectID, string(kind), subjectJSON, creator, canCancel, queueClass, fixture.now, storedOwner); err != nil {
				return err
			}
			actor, authSession := pgtype.UUID{}, pgtype.UUID{}
			if origin == "user" {
				actor = creator
				parsed, err := storedb.ParseUUID(testAuthSessionID)
				if err != nil {
					return err
				}
				authSession = parsed
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO job_model_bindings (
				  job_id, project_id, artifact_source_owner_id, job_kind, origin,
				  actor_user_id, initiating_auth_session_id, input_contract_version
				) VALUES ($1, $2, $3, $4, $5, $6, $7, 1)
			`, jobID, testProjectID, storedOwner, string(kind), origin, actor, authSession)
			return err
		})
	}); err != nil {
		t.Fatal(err)
	}
}

func TestChatJobBindingReadsSessionThreadAndProviderTurn(t *testing.T) {
	fixture := newPostgresFixture(t)
	fixture.seedChatJob(t)
	ctx := context.Background()
	key := JobKey{ProjectID: testProjectID, JobID: testChatJobID}
	binding, err := fixture.store.JobBinding(ctx, key)
	if err != nil || binding.Flow != agentdomain.FlowResearchChat || binding.RootThreadID != "" || binding.TurnID != "turn-chat-1" {
		t.Fatalf("unbound chat job binding = (%+v, %v)", binding, err)
	}
	fixture.bindChatRoot(t, "root-chat-1")
	binding, err = fixture.store.JobBinding(ctx, key)
	if err != nil || binding.RootThreadID != "root-chat-1" || binding.TurnID != "turn-chat-1" {
		t.Fatalf("chat job binding = (%+v, %v)", binding, err)
	}
	// The thread of the helper of a drawing is in the flow of the workshop (specs/2026-11-02-image-workshop.md IW4.1).
	if _, err := fixture.db.Pool.Exec(ctx, `UPDATE chat_sessions SET kind = 'drawing' WHERE id = $1`, testChatID); err != nil {
		t.Fatal(err)
	}
	if binding, err = fixture.store.JobBinding(ctx, key); err != nil || binding.Flow != agentdomain.FlowImageWorkshop || binding.RootThreadID != "root-chat-1" {
		t.Fatalf("the job binding of a helper = (%+v, %v)", binding, err)
	}
	if _, err := fixture.store.JobBinding(ctx, JobKey{ProjectID: testProjectID, JobID: testDecoyJobID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown job binding error = %v", err)
	}
	if _, err := fixture.store.JobBinding(ctx, JobKey{ProjectID: "not-a-uuid", JobID: testChatJobID}); !errors.Is(err, ErrValidation) {
		t.Fatalf("malformed key error = %v", err)
	}
}

// A fenced chat turn source hides the thread of its Job.
func TestChatJobBindingRejectsFencedTurnSource(t *testing.T) {
	fixture := newPostgresFixture(t)
	fixture.seedChatJob(t)
	fixture.bindChatRoot(t, "root-chat-fenced-turn")
	ctx := context.Background()
	if _, err := fixture.db.Pool.Exec(ctx, `
		UPDATE artifact_source_owners
		SET read_state='source_deleted', fenced_at=$2, updated_at=$2
		WHERE id=$1 AND project_id=$3
	`, testTurnOwnerID, fixture.now, testProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.JobBinding(ctx, JobKey{ProjectID: testProjectID, JobID: testChatJobID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fenced chat turn binding error = %v", err)
	}
}

func (fixture *postgresFixture) bindChatRoot(t *testing.T, threadID string) {
	t.Helper()
	params := sqlc.BindChatRootThreadParams{ThreadID: pgtype.Text{String: threadID, Valid: true}}
	for target, value := range map[*pgtype.UUID]string{
		&params.ProjectID: testProjectID, &params.ChatID: testChatID, &params.TurnID: testTurnID, &params.ActorUserID: testActorID,
	} {
		parsed, err := storedb.ParseUUID(value)
		if err != nil {
			t.Fatal(err)
		}
		*target = parsed
	}
	if _, err := sqlc.New(fixture.db.Pool).BindChatRootThread(context.Background(), params); err != nil {
		t.Fatal(err)
	}
}
