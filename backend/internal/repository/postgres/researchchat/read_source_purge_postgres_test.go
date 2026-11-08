package researchchat

import (
	"testing"
	"time"

	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	agentjobsrepo "canonkeeper/backend/internal/repository/postgres/agentjobs"
	deletionrepo "canonkeeper/backend/internal/repository/postgres/deletion"
	deletionservice "canonkeeper/backend/internal/services/deletion"
	canonstore "canonkeeper/backend/internal/store/canon"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// assertSourceDeletionKeepsChat deletes the book and then hard-purges the chapter a finished
// chat turn read: the book fence leaves the chat artifacts active, and the purge removes the
// chapter and its paragraphs but no chat turn, Job, owner or Codex root (book deletion does
// not touch chats, specs/2026-10-16-book-deletion-keeps-chats.md R1.1–R1.4).
func assertSourceDeletionKeepsChat(t *testing.T, db *storedb.DB, chapterID string, job jobs.Job, chatID, rootThreadID string) {
	t.Helper()
	_ = testutil.IsolatedPostgresSchema(t, db)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Microsecond)
	const tombstoneID = "91000000-0000-4000-8000-00000000000b"
	jobID := job.ID
	var ownerIDs []string
	if err := db.Pool.QueryRow(ctx, `SELECT ARRAY[job.artifact_source_owner_id::text, turn_row.artifact_source_owner_id::text]
		FROM jobs job JOIN chat_turns turn_row ON turn_row.project_id=job.project_id AND turn_row.job_id=job.id
		WHERE job.project_id=$1::uuid AND job.id=$2::uuid`, submitTurnProjectID, jobID).Scan(&ownerIDs); err != nil {
		t.Fatal(err)
	}
	chatReadable := func(stage string) {
		t.Helper()
		var active, readable int
		if err := db.Pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM artifact_source_owners WHERE project_id=$1::uuid AND id=ANY($2::uuid[]) AND read_state='active'),
			(SELECT count(*) FROM jobs job WHERE job.project_id=$1::uuid AND job.id=$3::uuid
			   AND ck_artifact_source_owner_is_readable(job.artifact_source_owner_id, job.project_id, 'job', job.id))`,
			submitTurnProjectID, ownerIDs, jobID).Scan(&active, &readable); err != nil {
			t.Fatal(err)
		}
		if active != 2 || readable != 1 {
			t.Fatalf("%s: active chat owners = %d, readable Job = %d; want the chat untouched", stage, active, readable)
		}
		// The paths the chat page takes: GET /jobs/{id} and the timeline binding to the root thread.
		if allowed, err := New(db).CanReadJobTarget(ctx, jobs.Actor{UserID: submitTurnActorID}, job.Subject); err != nil || !allowed {
			t.Fatalf("%s: chat Job readable = %v, %v", stage, allowed, err)
		}
		if binding, err := agentjobsrepo.New(db).JobBinding(ctx, agentjobsrepo.JobKey{ProjectID: submitTurnProjectID, JobID: jobID}); err != nil {
			t.Fatalf("%s: timeline binding = %#v, %v", stage, binding, err)
		}
	}
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		return canonstore.New(db).RevokeExactSourceArtifactOwnersAtFence(ctx, tx, governance.DeletionSelectionBook,
			governance.SourceGenerationVector{
				ProjectID: submitTurnProjectID, ProjectGeneration: 1, BookID: submitTurnBookID, BookGeneration: 1,
			}, now)
	}); err != nil {
		t.Fatal(err)
	}
	chatReadable("after the book fence")
	vector, err := governance.ChapterDeletionVector(governance.SourceGenerationVector{
		ProjectID: submitTurnProjectID, ProjectGeneration: 1,
		BookID: submitTurnBookID, BookGeneration: 1, ChapterID: chapterID, ChapterGeneration: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline, err := governance.PresentPurgeDeadline(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var leaseOwner governance.Digest
	leaseOwner[0] = 1
	request := deletiondomain.TargetRequest{
		Claim: deletiondomain.TargetClaim{
			ProjectID: submitTurnProjectID, TombstoneID: tombstoneID, Target: governance.TargetPostgreSQL,
			ClaimIncarnation: 1, LeaseOwnerDigest: leaseOwner, ObservedLeaseExpiry: now.Add(5 * time.Minute),
		},
		Tombstone: deletiondomain.Tombstone{
			ProjectID: submitTurnProjectID, Status: deletiondomain.TombstoneProcessing,
			Identity: governance.DeletionTombstoneInput{
				TombstoneID: tombstoneID, Origin: governance.DeletionOriginGovernedOnline,
				Mode: governance.PurgeModeSourceHardPurge, SelectedVector: vector, FencedAt: now, Deadline: deadline,
			},
		},
		Attempt: 1,
	}
	store := deletionrepo.New(db, deletionrepo.WithSourceDeletedModelJobTombstoner(jobstore.New(db)))
	roots, err := store.ListDeletionRoots(ctx, request.Tombstone)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 0 {
		t.Fatalf("read chapter deletion roots = %#v, want no chat root", roots)
	}
	// Seed only the operator's hard-purge fence/claim, as in deletion's isolated
	// PostgreSQL fixture. The target itself executes with normal constraints.
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE chapters
			SET deleted_at=$3, governance_retention_started_at=$3, purge_after=$4, purge_deadline=$5, hard_purge_started_at=$6
			WHERE project_id=$1::uuid AND id=$2::uuid`, submitTurnProjectID, chapterID,
			now.Add(-9*24*time.Hour), now.Add(-2*24*time.Hour), now.Add(-24*time.Hour), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO deletion_tombstones (
			id, project_id, origin, entity_kind, purge_mode, entity_id, project_generation,
			book_id, book_generation, chapter_id, chapter_generation, status, fenced_at, purge_deadline, predecessor_set_digest
		) VALUES ($1,$2,'governed_online','chapter','source_hard_purge',$3,1,$4,1,$3,1,'processing',$5,$6,$7)`,
			tombstoneID, submitTurnProjectID, chapterID, submitTurnBookID, now, now.Add(-24*time.Hour), leaseOwner[:]); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO deletion_target_tasks (
			tombstone_id, project_id, target, state, attempts, claim_incarnation, next_attempt_at, claimed_by_digest, claim_expires_at
		) VALUES ($1,$2,'postgresql','processing',1,1,$3,$4,$5)`,
			tombstoneID, submitTurnProjectID, now, leaseOwner[:], request.Claim.ObservedLeaseExpiry); err != nil {
			return err
		}
		return deletionrepo.InsertPostgresStepRegistry(ctx, tx, tombstoneID, submitTurnProjectID,
			governance.DeletionSelectionChapter, governance.PurgeModeSourceHardPurge)
	}); err != nil {
		t.Fatal(err)
	}
	target, err := deletionservice.NewPostgresTarget(store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.Apply(ctx, request)
	if err != nil || result.Disposition != deletiondomain.DispositionPurged {
		t.Fatalf("read chapter PostgreSQL purge = %#v, %v", result, err)
	}
	var turns, answeredJobs, sourceRows, retainedOwners int
	var threadID string
	if err := db.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM chat_turns WHERE project_id=$1::uuid AND job_id=$2::uuid),
		(SELECT count(*) FROM jobs WHERE project_id=$1::uuid AND id=$2::uuid AND
			result_json->>'response' IS NOT NULL AND artifact_source_owner_id IS NOT NULL),
		(SELECT count(*) FROM chapters WHERE project_id=$1::uuid AND id=$3::uuid) +
		(SELECT count(*) FROM chapter_paragraphs WHERE chapter_id=$3::uuid),
		(SELECT count(*) FROM artifact_source_owners WHERE project_id=$1::uuid AND id=ANY($4::uuid[])),
		(SELECT coalesce(thread_id, '') FROM chat_sessions WHERE project_id=$1::uuid AND id=$5::uuid)`,
		submitTurnProjectID, jobID, chapterID, ownerIDs, chatID).Scan(&turns, &answeredJobs, &sourceRows, &retainedOwners, &threadID); err != nil {
		t.Fatal(err)
	}
	if turns != 1 || answeredJobs != 1 || sourceRows != 0 || retainedOwners != 2 || threadID != rootThreadID {
		t.Fatalf("read chapter purge: turns=%d answered Jobs=%d source rows=%d owners=%d thread=%q; want the chat kept and the chapter gone",
			turns, answeredJobs, sourceRows, retainedOwners, threadID)
	}
	chatReadable("after the chapter hard purge")
}
