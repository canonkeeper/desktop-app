package researchchat

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	authorityActorID         = "76000000-0000-4000-8000-000000000001"
	authorityProjectID       = "76000000-0000-4000-8000-000000000002"
	authorityMembershipID    = "76000000-0000-4000-8000-000000000003"
	authoritySessionID       = "76000000-0000-4000-8000-000000000004"
	authorityBookID          = "76000000-0000-4000-8000-000000000005"
	authorityChatID          = "76000000-0000-4000-8000-000000000006"
	authorityTurnID          = "76000000-0000-4000-8000-000000000007"
	authorityJobID           = "76000000-0000-4000-8000-000000000008"
	authorityOwnerID         = "76000000-0000-4000-8000-000000000009"
	authorityTurnOwnerID     = "76000000-0000-4000-8000-000000000010"
	authorityChatTombstoneID = "76000000-0000-4000-8000-000000000011"
)

func TestReadAuthorityRejectsCrossRootRoleDowngradeAndDeletedChatPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_read_authority")
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedReadAuthority(t, db, now)
	repository := New(db)
	call := service.ToolContext{
		Caller: agent.ToolCaller{ThreadID: "root-rch-1", RootThreadID: "root-rch-1"},
		TurnID: "provider-turn-rch-1",
	}
	read := func(call service.ToolContext) error {
		return db.WithTx(context.Background(), func(tx pgx.Tx) error {
			_, err := repository.readAuthorityInTx(context.Background(), tx, call)
			return err
		})
	}
	if err := read(call); err != nil {
		t.Fatalf("current editor authority: %v", err)
	}
	wrongRoot := call
	wrongRoot.Caller.ThreadID, wrongRoot.Caller.RootThreadID = "root-other", "root-other"
	if err := read(wrongRoot); !errors.Is(err, researchdomain.ErrForbidden) {
		t.Fatalf("cross-root error = %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE project_memberships SET role='viewer' WHERE id=$1::uuid
	`, authorityMembershipID); err != nil {
		t.Fatal(err)
	}
	if err := read(call); !errors.Is(err, researchdomain.ErrForbidden) {
		t.Fatalf("downgraded draft authority error = %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE project_memberships SET role='editor' WHERE id=$1::uuid
	`, authorityMembershipID); err != nil {
		t.Fatal(err)
	}
	markResearchChatDeletedForTest(t, db, authorityProjectID, authorityChatID, authorityChatTombstoneID, now)
	if err := read(call); !errors.Is(err, researchdomain.ErrForbidden) {
		t.Fatalf("deleted chat authority error = %v", err)
	}
}

func markResearchChatDeletedForTest(t *testing.T, db *storedb.DB, projectID, chatID, tombstoneID string, now time.Time) {
	t.Helper()
	if err := db.WithTx(context.Background(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(), `SET LOCAL session_replication_role=replica`); err != nil {
			return err
		}
		if _, err := tx.Exec(context.Background(), `
			INSERT INTO deletion_tombstones (
			  id, project_id, origin, entity_kind, purge_mode, entity_id,
			  chat_session_id, chat_lifecycle_epoch, status, fenced_at, purge_deadline
			) VALUES ($1,$2,'governed_online','chat','chat_epoch',$3,$3,1,'pending',$4,$4::timestamptz + interval '24 hours')
		`, tombstoneID, projectID, chatID, now); err != nil {
			return err
		}
		_, err := tx.Exec(context.Background(), `
			INSERT INTO deletion_target_tasks (tombstone_id, project_id, target, state)
			VALUES ($1,$2,'runtime','pending'), ($1,$2,'filesystem','pending'), ($1,$2,'postgresql','pending')
		`, tombstoneID, projectID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE chat_sessions
		SET deleted_at=$1, purge_after=$1::timestamptz + interval '24 hours', lifecycle_epoch=lifecycle_epoch+1
		WHERE id=$2::uuid
	`, now, chatID); err != nil {
		t.Fatal(err)
	}
}

func TestReadAuthorityRejectsFencedChatTurnOwnerPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_read_turn_authority")
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedReadAuthority(t, db, now)
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE artifact_source_owners
		SET read_state='source_deleted', fenced_at=clock_timestamp()
		WHERE id=$1::uuid AND project_id=$2::uuid
	`, authorityTurnOwnerID, authorityProjectID); err != nil {
		t.Fatal(err)
	}
	repository := New(db)
	err := db.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := repository.readAuthorityInTx(context.Background(), tx, service.ToolContext{
			Caller: agent.ToolCaller{ThreadID: "root-rch-1", RootThreadID: "root-rch-1"},
			TurnID: "provider-turn-rch-1",
		})
		return err
	})
	if !errors.Is(err, researchdomain.ErrForbidden) {
		t.Fatalf("fenced chat turn authority error = %v", err)
	}
}

// R2.2.5, R3.2.2: every way a session ends stops the turns it started: a direct
// revocation and «выйти везде». A password reset and a password change on another device
// end it by the same revocation (store/account: TestResetPasswordRevokesSessionsAndWorksOnce,
// TestChangePasswordKeepsCurrentSessionOnly).
func TestRevokedInitiatingSessionRejectsResearchTurnLateEffectsPostgres(t *testing.T) {
	for name, revoke := range map[string]func(*testing.T, *storedb.DB, time.Time){
		"revoked session": func(t *testing.T, db *storedb.DB, now time.Time) {
			if _, err := db.Pool.Exec(context.Background(), `
				UPDATE sessions SET revoked_at=$1 WHERE id=$2::uuid
			`, now, authoritySessionID); err != nil {
				t.Fatal(err)
			}
		},
		"logout everywhere": func(t *testing.T, db *storedb.DB, now time.Time) {
			if err := authstore.New(db).RevokeUserSessions(context.Background(), authorityActorID, now); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_revoked_session_authority")
			now := time.Now().UTC().Truncate(time.Microsecond)
			seedReadAuthority(t, db, now)
			revoke(t, db, now)
			assertResearchTurnLateEffectsForbidden(t, db)
		})
	}
}

func assertResearchTurnLateEffectsForbidden(t *testing.T, db *storedb.DB) {
	t.Helper()
	repository := New(db)
	run := researchdomain.TurnRun{
		ProjectID: authorityProjectID, JobID: authorityJobID, ClaimIncarnation: 1,
		ActorUserID: authorityActorID, ChatID: authorityChatID, TurnID: authorityTurnID,
	}
	errorsByEffect := map[string]error{
		"bind root": repository.BindRoot(context.Background(), run, "root-rch-1"),
		"bind turn": repository.BindTurn(
			context.Background(), run, "root-rch-1", "provider-turn-rch-1",
		),
	}
	errorsByEffect["read"] = db.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := repository.readAuthorityInTx(context.Background(), tx, service.ToolContext{
			Caller: agent.ToolCaller{ThreadID: "root-rch-1", RootThreadID: "root-rch-1"},
			TurnID: "provider-turn-rch-1",
		})
		return err
	})
	errorsByEffect["finish response"] = repository.FinishResponse(context.Background(), researchdomain.FinishResponse{
		ProjectID: authorityProjectID, JobID: authorityJobID, ClaimIncarnation: 1,
		ChatID: authorityChatID, TurnID: authorityTurnID, RootThreadID: "root-rch-1",
		ProviderTurnID: "provider-turn-rch-1", Response: "exact response",
	})
	for effect, err := range errorsByEffect {
		if !errors.Is(err, researchdomain.ErrForbidden) {
			t.Errorf("%s with revoked initiating session error = %v", effect, err)
		}
	}
}

func TestRequeuedTurnCancelRequiresExactNativeStopPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_requeued_cancel")
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedReadAuthority(t, db, now)
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE jobs
		SET status='queued', retry_count=1, claim_incarnation=2, claimed_at=NULL
		WHERE id=$1::uuid AND project_id=$2::uuid
	`, authorityJobID, authorityProjectID); err != nil {
		t.Fatal(err)
	}
	subject := jobs.ResearchChatTurnSubject{
		Type: jobs.KindResearchChatTurn, ProjectID: authorityProjectID, ChatID: authorityChatID,
		TurnID: authorityTurnID, SourceView: governance.SourceViewDraft, Scope: "project",
	}
	input := jobs.TargetCancelInput{
		ActorUserID: authorityActorID, TargetReadable: true, UpdatedAt: now.Add(time.Minute),
		Job: jobs.Job{
			ID: authorityJobID, ProjectID: authorityProjectID, Kind: jobs.KindResearchChatTurn,
			Status: jobs.StatusQueued, Subject: subject, CreatorUserID: authorityActorID,
			CanCancel: true, RetryCount: 1,
		},
	}
	target, updated, err := New(db).BeginTargetCancel(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !target.NeedsInterrupt || target.RootThreadID != "root-rch-1" || target.ClaimIncarnation != 2 ||
		updated.Status != jobs.StatusCanceling {
		t.Fatalf("requeued cancel = (target=%+v, job=%+v)", target, updated)
	}
	var status string
	if err := db.Pool.QueryRow(context.Background(), `SELECT status FROM jobs WHERE id=$1::uuid`, authorityJobID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(jobs.StatusCanceling) {
		t.Fatalf("stored status = %q", status)
	}
}

// R1.3: a turn returned to the queue (graceful stop) and canceled stays readable as canceling.
func TestRequeuedTurnCancelKeepsJobReadablePostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_requeued_cancel_readable")
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedReadAuthority(t, db, now)
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE jobs
		SET status='queued', progress_json='{"phase":"waiting"}'::jsonb,
		    retry_count=1, claim_incarnation=2, claimed_at=NULL
		WHERE id=$1::uuid AND project_id=$2::uuid
	`, authorityJobID, authorityProjectID); err != nil {
		t.Fatal(err)
	}
	input := jobs.TargetCancelInput{
		ActorUserID: authorityActorID, TargetReadable: true, UpdatedAt: now.Add(time.Minute),
		Job: jobs.Job{
			ID: authorityJobID, ProjectID: authorityProjectID, Kind: jobs.KindResearchChatTurn,
			Status: jobs.StatusQueued, CreatorUserID: authorityActorID, CanCancel: true, RetryCount: 1,
			Subject: jobs.ResearchChatTurnSubject{
				Type: jobs.KindResearchChatTurn, ProjectID: authorityProjectID, ChatID: authorityChatID,
				TurnID: authorityTurnID, SourceView: governance.SourceViewDraft, Scope: "project",
			},
		},
	}
	if _, _, err := New(db).BeginTargetCancel(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	canceling, err := jobstore.New(db).LoadJobForWorkerClaim(context.Background(), jobs.WorkerClaim{
		ProjectID: authorityProjectID, JobID: authorityJobID, ClaimIncarnation: 2,
	})
	if err != nil {
		t.Fatalf("canceling requeued turn is unreadable: %v", err)
	}
	if canceling.Status != jobs.StatusCanceling || canceling.Phase != nil {
		t.Fatalf("canceling requeued turn = status %q phase %v", canceling.Status, canceling.Phase)
	}
}

func TestRequeuedTurnCancelUsesCorrelationWhenNativeBindingsArePendingPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_pending_bind_cancel")
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedReadAuthority(t, db, now)
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE jobs
		SET status='queued', progress_json='{"phase":"waiting"}'::jsonb,
		    retry_count=1, claim_incarnation=2, claimed_at=NULL
		WHERE id=$1::uuid AND project_id=$2::uuid
	`, authorityJobID, authorityProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE chat_sessions SET thread_id=NULL WHERE id=$1::uuid AND project_id=$2::uuid
	`, authorityChatID, authorityProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE chat_turns SET provider_turn_id=NULL WHERE id=$1::uuid AND project_id=$2::uuid
	`, authorityTurnID, authorityProjectID); err != nil {
		t.Fatal(err)
	}
	input := jobs.TargetCancelInput{
		ActorUserID: authorityActorID, TargetReadable: true, UpdatedAt: now.Add(time.Minute),
		Job: jobs.Job{
			ID: authorityJobID, ProjectID: authorityProjectID, Kind: jobs.KindResearchChatTurn,
			Status: jobs.StatusQueued, CreatorUserID: authorityActorID, CanCancel: true, RetryCount: 1,
			Subject: jobs.ResearchChatTurnSubject{
				Type: jobs.KindResearchChatTurn, ProjectID: authorityProjectID, ChatID: authorityChatID,
				TurnID: authorityTurnID, SourceView: governance.SourceViewDraft, Scope: "project",
			},
		},
	}
	target, updated, err := New(db).BeginTargetCancel(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !target.NeedsInterrupt || target.ChatID != authorityChatID || target.RootThreadID != "" ||
		updated.Status != jobs.StatusCanceling {
		t.Fatalf("pending-bind cancel = (target=%+v, job=%+v)", target, updated)
	}
}

func TestBindRootIsFencedByExactRunningClaimPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_bind_root_fence")
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedReadAuthority(t, db, now)
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE chat_sessions SET thread_id=NULL WHERE id=$1::uuid AND project_id=$2::uuid
	`, authorityChatID, authorityProjectID); err != nil {
		t.Fatal(err)
	}
	repository := New(db)
	run := researchdomain.TurnRun{
		ProjectID: authorityProjectID, JobID: authorityJobID, ClaimIncarnation: 1,
		ActorUserID: authorityActorID, ChatID: authorityChatID, TurnID: authorityTurnID,
	}
	if err := repository.BindRoot(context.Background(), run, "root-bound"); err != nil {
		t.Fatalf("running bind: %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE jobs SET status='canceling', cancel_requested_at=$1 WHERE id=$2::uuid AND project_id=$3::uuid
	`, now.Add(time.Minute), authorityJobID, authorityProjectID); err != nil {
		t.Fatal(err)
	}
	if err := repository.BindRoot(context.Background(), run, "root-bound"); !errors.Is(err, researchdomain.ErrConflict) {
		t.Fatalf("post-cancel bind error = %v", err)
	}
}

func TestPublishedViewerCannotBindOrReadAgentTurnPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_published_viewer_authority")
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedReadAuthorityState(t, db, now, governance.SourceViewPublished, "viewer")
	var status string
	var readable bool
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT status, ck_artifact_source_owner_is_readable(artifact_source_owner_id, project_id, 'job', id)
		FROM jobs WHERE id=$1::uuid AND project_id=$2::uuid
	`, authorityJobID, authorityProjectID).Scan(&status, &readable); err != nil {
		t.Fatal(err)
	}
	if status != string(jobs.StatusRunning) || !readable {
		t.Fatalf("published viewer fixture authority = (status=%q, readable=%v)", status, readable)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE chat_sessions SET thread_id=NULL WHERE id=$1::uuid AND project_id=$2::uuid
	`, authorityChatID, authorityProjectID); err != nil {
		t.Fatal(err)
	}
	repository := New(db)
	run := researchdomain.TurnRun{
		ProjectID: authorityProjectID, JobID: authorityJobID, ClaimIncarnation: 1,
		ActorUserID: authorityActorID, ChatID: authorityChatID, TurnID: authorityTurnID,
	}
	if err := repository.BindRoot(context.Background(), run, "root-viewer"); !errors.Is(err, researchdomain.ErrConflict) {
		t.Fatalf("published viewer BindRoot error = %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		UPDATE chat_sessions SET thread_id='root-rch-1' WHERE id=$1::uuid AND project_id=$2::uuid
	`, authorityChatID, authorityProjectID); err != nil {
		t.Fatal(err)
	}
	if err := repository.BindTurn(context.Background(), run, "root-rch-1", "provider-turn-rch-1"); !errors.Is(err, researchdomain.ErrConflict) {
		t.Fatalf("published viewer BindTurn error = %v", err)
	}
	err := db.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := repository.readAuthorityInTx(context.Background(), tx, service.ToolContext{
			Caller: agent.ToolCaller{ThreadID: "root-rch-1", RootThreadID: "root-rch-1"},
			TurnID: "provider-turn-rch-1",
		})
		return err
	})
	if !errors.Is(err, researchdomain.ErrForbidden) {
		t.Fatalf("published viewer tool authority error = %v", err)
	}
}

func seedReadAuthority(t *testing.T, db *storedb.DB, now time.Time) {
	t.Helper()
	seedReadAuthorityState(t, db, now, governance.SourceViewDraft, "editor")
}

func seedReadAuthorityState(t *testing.T, db *storedb.DB, now time.Time, sourceView governance.SourceView, actorRole string) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,'rch@example.test','RCH','hash',$2)", []any{authorityActorID, now}},
		{"INSERT INTO sessions (id,user_id,session_hash,csrf_secret_hash,expires_at,created_at) VALUES ($1,$2,repeat('a',64),repeat('b',64),$3,$4)", []any{authoritySessionID, authorityActorID, now.Add(time.Hour), now}},
		{"INSERT INTO projects (id,owner_user_id,name,description,created_at,updated_at) VALUES ($1,$2,'RCH','',$3,$3)", []any{authorityProjectID, authorityActorID, now}},
		{"INSERT INTO project_memberships (id,project_id,user_id,role,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$5)", []any{authorityMembershipID, authorityProjectID, authorityActorID, actorRole, now}},
		{"INSERT INTO project_source_views (project_id,source_view,project_lifecycle_generation,catalog_revision) VALUES ($1,$2,1,1)", []any{authorityProjectID, string(sourceView)}},
		{"INSERT INTO books (id,project_id,title,\"order\",display_number,status,revision,created_at,updated_at) VALUES ($1,$2,'RCH Book',1,'1','draft',1,$3,$3)", []any{authorityBookID, authorityProjectID, now}},
		{"INSERT INTO book_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,title) VALUES ($1,$2,1,$3,1,'RCH Book')", []any{authorityProjectID, string(sourceView), authorityBookID}},
		{"INSERT INTO chat_sessions (id,project_id,source_view,scope,title,thread_id,created_at,updated_at) VALUES ($1,$2,$3,'project','Research','root-rch-1',$4,$4)", []any{authorityChatID, authorityProjectID, string(sourceView), now}},
	}
	for _, statement := range statements {
		if _, err := db.Pool.Exec(context.Background(), statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	subjectJSON, err := jobs.EncodeSubject(jobs.KindResearchChatTurn, jobs.ResearchChatTurnSubject{
		Type: jobs.KindResearchChatTurn, ProjectID: authorityProjectID, ChatID: authorityChatID,
		TurnID: authorityTurnID, SourceView: sourceView, Scope: "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := governance.PresentSourceView(sourceView)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewBookArtifactRef(governance.SourceGenerationVector{
		ProjectID: authorityProjectID, ProjectGeneration: 1, BookID: authorityBookID, BookGeneration: 1,
	}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	refs := []governance.ArtifactSourceRef{ref}
	manifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: authorityProjectID, ArtifactID: authorityJobID,
		OwnerSourceView: view, Refs: refs,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.WithTx(context.Background(), func(tx pgx.Tx) error {
		return governancestore.New(db).CreateArtifactWithSources(context.Background(), tx, governancestore.SealArtifactSourcesInput{
			OwnerID: authorityOwnerID, ProjectID: authorityProjectID, ArtifactID: authorityJobID,
			Kind: governance.ArtifactJob, SourceView: view, Refs: refs, Manifest: manifest,
		}, func(ctx context.Context, tx pgx.Tx, owner pgtype.UUID) error {
			if _, err := tx.Exec(ctx, `
				INSERT INTO jobs (
				  id,project_id,kind,status,progress_json,result_json,error_json,
				  subject_json,creator_user_id,can_cancel,
				  queue_class,retry_count,max_attempts,next_attempt_at,claimed_at,started_at,claim_incarnation,
				  artifact_source_owner_id,governance_payload_state,created_at,updated_at
				) VALUES ($1,$2,'research_chat_turn','running','{"phase":"working"}'::jsonb,
				          NULL,NULL,$3::jsonb,$4,true,
				          'interactive',0,3,$5,$5,$5,1,$6,'active',$5,$5)
			`, authorityJobID, authorityProjectID, subjectJSON, authorityActorID, now, owner); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO job_model_bindings (
				  job_id,project_id,artifact_source_owner_id,job_kind,origin,
				  actor_user_id,initiating_auth_session_id,input_contract_version
				) VALUES ($1,$2,$3,'research_chat_turn','user',$4,$5,1)
			`, authorityJobID, authorityProjectID, owner, authorityActorID, authoritySessionID); err != nil {
				return err
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	chatArm, err := governance.PresentChatArm(authorityProjectID, authorityChatID, 1)
	if err != nil {
		t.Fatal(err)
	}
	turnRef, err := governance.NewBookArtifactRef(governance.SourceGenerationVector{
		ProjectID: authorityProjectID, ProjectGeneration: 1, BookID: authorityBookID, BookGeneration: 1,
	}, view, chatArm)
	if err != nil {
		t.Fatal(err)
	}
	turnRefs := []governance.ArtifactSourceRef{turnRef}
	turnManifest, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: governance.ArtifactChatTurn, ProjectID: authorityProjectID, ArtifactID: authorityTurnID,
		OwnerSourceView: view, Refs: turnRefs,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.WithTx(context.Background(), func(tx pgx.Tx) error {
		return governancestore.New(db).CreateArtifactWithSources(context.Background(), tx, governancestore.SealArtifactSourcesInput{
			OwnerID: authorityTurnOwnerID, ProjectID: authorityProjectID, ArtifactID: authorityTurnID,
			Kind: governance.ArtifactChatTurn, SourceView: view, Refs: turnRefs, Manifest: turnManifest,
		}, func(ctx context.Context, tx pgx.Tx, owner pgtype.UUID) error {
			_, err := tx.Exec(ctx, `
				INSERT INTO chat_turns (
				  id,chat_id,project_id,source_view,job_id,artifact_source_owner_id,provider_turn_id
				) VALUES ($1,$2,$3,$4,$5,$6,'provider-turn-rch-1')
			`, authorityTurnID, authorityChatID, authorityProjectID, string(sourceView), authorityJobID, owner)
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}
