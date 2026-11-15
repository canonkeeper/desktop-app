package researchchat

import (
	"context"
	"strings"
	"testing"
	"time"

	appworkdomain "canonkeeper/backend/internal/domain/appwork"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	appworkrepo "canonkeeper/backend/internal/repository/postgres/appwork"
	indexingstore "canonkeeper/backend/internal/store/indexing"
	jobstore "canonkeeper/backend/internal/store/jobs"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5"
)

// recordedWork is the counter of the work of the app as the fence of a test sees it: like the desktop build
// (app/desktop/account_work.go, charges.OpenInTx) it writes the row of the work in the transaction of its start, with the
// source of the settings and the mark of the link the test names.
type recordedWork struct {
	source appworkdomain.Source
	link   string
}

func (work *recordedWork) OpenInTx(ctx context.Context, tx pgx.Tx, opened quota.Work) error {
	return appworkrepo.RecordInTx(ctx, tx, opened.JobID, appworkdomain.Kind(opened.Kind), work.source, work.link, opened.At)
}
func (*recordedWork) SteerInTx(context.Context, pgx.Tx, quota.Steer) error   { return nil }
func (*recordedWork) UnsteerInTx(context.Context, pgx.Tx, quota.Steer) error { return nil }

// specs/2026-11-15-desk-turns-here.md TH2.1: the turns this app still answers are counted by the rows of its work and the
// status of their Jobs, of any chat — a turn of the account of the link now that is queued, running or canceling. An upload
// of materials and a reading of the same link are not turns, a turn of another link and one of «Свой ИИ» are not of this
// link, and a turn that ended is not answered any more.
func TestTheTurnsAnsweredHereAreTheTurnsOfTheLinkNotOverPostgres(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_app_work_answering")
	ctx := context.Background()
	now := chats.now
	link, other := strings.Repeat("a", 64), strings.Repeat("b", 64)
	work := &recordedWork{source: appworkdomain.SourceAccount, link: link}
	chats.repository = New(chats.db).WithQuota(quotastore.Fence{Charges: work})
	chats.setLimits(map[quota.Key]int64{quota.KeyActiveTurns: 20, quota.KeyChatTurnsPerDay: 50})
	store := appworkrepo.New(chats.db)
	answering := func() int64 {
		t.Helper()
		running, err := store.Answering(ctx, link)
		if err != nil {
			t.Fatal(err)
		}
		return running
	}

	// Turns of the account of the link now: queued, running, canceling.
	chats.askOrFail(chats.chat(), "answering-queued", now)
	chats.startTurnForTest(chats.askOrFail(chats.chat(), "answering-running", now.Add(time.Second)), "root-running", "turn-running")
	canceled := chats.askOrFail(chats.chat(), "answering-canceling", now.Add(2*time.Second))
	chats.startTurnForTest(canceled, "root-canceling", "turn-canceling")
	// The author stops the running turn: it is canceling until its thread is interrupted.
	canceled.Status = jobs.StatusRunning
	if _, updated, err := chats.repository.BeginTargetCancel(ctx, jobs.TargetCancelInput{
		ActorUserID: submitTurnActorID, Job: canceled, TargetReadable: true, UpdatedAt: now.Add(3 * time.Second),
	}); err != nil || updated.Status != jobs.StatusCanceling {
		t.Fatalf("the cancel of a running turn = %+v, %v", updated, err)
	}
	if got := answering(); got != 3 {
		t.Fatalf("the turns answered here = %d, want queued, running and canceling", got)
	}

	// A turn of the link now that ended.
	ended := chats.startTurnForTest(chats.askOrFail(chats.chat(), "answering-ended", now.Add(4*time.Second)), "root-ended", "turn-ended")
	if err := chats.repository.FinishResponse(ctx, researchdomain.FinishResponse{
		ProjectID: ended.ProjectID, ChatID: ended.ChatID, TurnID: ended.TurnID, JobID: ended.JobID,
		ClaimIncarnation: 1, RootThreadID: "root-ended", ProviderTurnID: "turn-ended", Response: "Ответ.",
	}); err != nil {
		t.Fatal(err)
	}
	// A turn of another link and a turn of «Свой ИИ», both queued.
	work.link = other
	chats.askOrFail(chats.chat(), "answering-other-link", now.Add(5*time.Second))
	work.source, work.link = appworkdomain.SourceOwn, ""
	chats.askOrFail(chats.chat(), "answering-own", now.Add(6*time.Second))
	// An upload of materials and a reading of the link now, both queued: their Jobs by their stores, their rows by the store of
	// the work, in one transaction each.
	if err := chats.db.WithTx(ctx, func(tx pgx.Tx) error {
		upload, err := jobstore.New(chats.db).CreateAuthorMaterialsImportJobInTx(ctx, tx, jobstore.AuthorMaterialsImportJobInput{
			JobID: "93000000-0000-4000-8000-000000000001", CreatorUserID: submitTurnActorID, CreatedAt: now.Add(7 * time.Second),
			Subject: jobs.AuthorMaterialsImportSubject{
				Type: jobs.KindAuthorMaterialsImport, ProjectID: submitTurnProjectID, ImportID: "93000000-0000-4000-8000-000000000002",
			},
		})
		if err != nil {
			return err
		}
		return appworkrepo.RecordInTx(ctx, tx, upload.ID, appworkdomain.KindMaterials, appworkdomain.SourceAccount, link, now.Add(7*time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	if err := chats.db.WithTx(ctx, func(tx pgx.Tx) error {
		reading, err := indexingstore.New(chats.db).QueueCardsSampleInTx(ctx, tx, submitTurnProjectID, jobs.CardsSample{
			Host: "canonkeeper.example.test", Chapters: []jobs.SampleChapter{{ChapterID: "93000000-0000-4000-8000-000000000003", ContentHash: "hash-of-the-chapter"}},
		})
		if err != nil {
			return err
		}
		return appworkrepo.RecordInTx(ctx, tx, reading.ID, appworkdomain.KindReading, appworkdomain.SourceAccount, link, now.Add(8*time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	var queuedElse int64
	if err := chats.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM app_work AS work JOIN jobs AS job ON job.id = work.job_id WHERE job.status = 'queued' AND work.kind <> 'chat_turn'
	`).Scan(&queuedElse); err != nil || queuedElse != 2 {
		t.Fatalf("the queued upload and reading = %d, %v", queuedElse, err)
	}
	if got := answering(); got != 3 {
		t.Fatalf("the turns answered here = %d, want still 3: a turn that ended, of another link, of «Свой ИИ», an upload and a reading are not", got)
	}
	if got, err := store.Answering(ctx, other); err != nil || got != 1 {
		t.Fatalf("the turns answered under the other link = %d, %v", got, err)
	}
}
