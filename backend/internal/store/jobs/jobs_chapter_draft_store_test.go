package jobstore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"

	"github.com/jackc/pgx/v5"
)

// specs/2026-11-01-writer-assistant-index.md, UC6: the dispatcher claims a chapter_draft Job only for those
// who ask for its kind, with the text of its subject, and a failed attempt goes back to the queue.
func TestChapterDraftJobIsClaimedOnItsKindAndRequeued(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	actorID := "00000000-0000-4000-8000-000000000990"
	projectID := "00000000-0000-4000-8000-000000000991"
	membershipID := "00000000-0000-4000-8000-000000000992"
	jobID := "00000000-0000-4000-8000-000000000993"
	draftID := "00000000-0000-4000-8000-000000000994"
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	cleanupJobsStoreRows(t, db, []string{actorID}, projectID)
	insertProjectStoreUserForTest(t, db, actorID, "jobs-draft@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Jobs Draft Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleEditor, now, false)
	subject := jobs.ChapterDraftSubject{
		Type: jobs.KindChapterDraft, ProjectID: projectID, DraftID: draftID, Mode: jobs.ChapterDraftSelection, Revision: 2,
		Place: &jobs.ChapterDraftPlace{Quote: "строка\nстрока"}, Instruction: "короче",
	}
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := store.CreateChapterDraftJobInTx(ctx, tx, ChapterDraftJobInput{
			JobID: jobID, Subject: subject, CreatorUserID: actorID, CreatedAt: now,
		})
		return err
	}); err != nil {
		t.Fatalf("CreateChapterDraftJobInTx() error = %v", err)
	}
	authority := newWorkerClaimAuthorityForTest(db)
	if decision, err := authority.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindSagaLayer}); err == nil {
		t.Fatalf("a worker of another kind claimed %+v", decision)
	}
	decision, err := authority.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindChapterDraft})
	if err != nil || decision.Claim.JobID != jobID {
		t.Fatalf("claim = %+v, %v", decision, err)
	}
	job, err := store.LoadJobForWorkerClaim(ctx, decision.Claim)
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := job.Subject.(jobs.ChapterDraftSubject)
	if !ok || loaded.Place == nil || loaded.Place.Quote != subject.Place.Quote || loaded.Instruction != subject.Instruction || job.Kind != jobs.KindChapterDraft {
		t.Fatalf("job = %+v", job)
	}
	requeued, err := store.RequeueWorkerClaim(ctx, jobs.RequeueWorkerClaimStoreInput{
		Claim: decision.Claim, Kind: jobs.KindChapterDraft, ExpectedRetryCount: 0,
	})
	if err != nil || requeued.Status != jobs.StatusQueued || requeued.RetryCount != 1 {
		t.Fatalf("requeue = %+v, %v", requeued, err)
	}
}
