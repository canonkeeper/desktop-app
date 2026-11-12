package researchchat

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/chapterfile"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"
)

func (chats *quotaChats) askWithChapter(session researchdomain.Session, key string, attachment *researchdomain.Attachment, at time.Time) (jobs.Job, error) {
	return chats.repository.SubmitTurn(context.Background(), service.SubmitTurnCommand{
		ActorUserID: submitTurnActorID, Session: session, Question: "Ложится ли эта глава на канон?", Attachment: attachment,
		SessionWriteProof: chats.proof, ScopeHash: "turn-chapter-scope", OperationID: "research_chat.turn.create.v1",
		IdempotencyKeyHash: key, RequestHash: key + "-request", CreatedAt: at,
	})
}

func turnOf(t *testing.T, job jobs.Job) string {
	t.Helper()
	subject, ok := job.Subject.(jobs.ResearchChatTurnSubject)
	if !ok || subject.TurnID == "" {
		t.Fatalf("subject = %#v", job.Subject)
	}
	return subject.TurnID
}

// specs/2026-11-07-chat-attach-chapter.md CA2.4–CA2.6, CA3.1, CA4.3: the chapter of a question goes in with its turn,
// numbered in its chat; a replay keeps one row and counts nothing; a refused question leaves no chapter; the turn reads
// its header back, the chat reads the chapter by number.
func TestTheChapterOfAQuestionIsKeptWithItsTurnNumberedInItsChat(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_attachment")
	chats.setLimits(map[quota.Key]int64{quota.KeyChatTurnsPerDay: 3, quota.KeyActiveTurns: 10})
	ctx := context.Background()
	repository := chats.repository
	chat := chats.chat()
	// The parse whole, with the look of the Word file, for the proofreading started from the chat (CA5.5).
	parse := &chapterfile.Document{Format: chapterfile.FormatDOCX, Paragraphs: []chapterfile.Paragraph{
		{Runs: []chapterfile.Run{{Text: "Кайл "}, {Text: "шёл", Italic: true, Props: "<w:i/>"}, {Text: "."}}, Props: `<w:ind w:firstLine="567"/>`},
		{Runs: []chapterfile.Run{{Text: "Ольм отстал."}}},
	}, Layout: &chapterfile.Layout{Styles: "<w:styles/>"}}
	first := &researchdomain.Attachment{Title: "Глава 634", FileName: "634.docx", Format: "docx", Paragraphs: []string{"Кайл шёл.", "Ольм отстал."}, Words: 4, Document: parse}

	job, err := chats.askWithChapter(chat, "chapter-1", first, chats.now)
	if err != nil {
		t.Fatalf("a question with a chapter: %v", err)
	}
	read, err := repository.ChatAttachment(ctx, submitTurnProjectID, chat.ID, 0)
	if err != nil || read.Number != 1 || read.Title != "Глава 634" || read.FileName != "634.docx" || read.Format != "docx" ||
		!reflect.DeepEqual(read.Paragraphs, first.Paragraphs) || read.Words != 4 || read.CreatedAt.IsZero() {
		t.Fatalf("the attachment = %+v, %v", read, err)
	}
	if kept, err := repository.ChatAttachmentDocument(ctx, submitTurnProjectID, chat.ID, 1); err != nil || !reflect.DeepEqual(kept, parse) {
		t.Fatalf("the parse of the attachment = %+v, %v", kept, err)
	}
	// The worker takes the turn: its run carries the header of the chapter, never its text.
	claim := claimQueuedResearchTurnForTest(t, chats.db)
	bound := researchdomain.TurnRun{
		ProjectID: submitTurnProjectID, ChatID: chat.ID, TurnID: turnOf(t, job), JobID: job.ID, ActorUserID: submitTurnActorID,
		ClaimIncarnation: claim.ClaimIncarnation,
	}
	if err := repository.BindRoot(ctx, bound, "root-attach"); err != nil {
		t.Fatal(err)
	}
	if err := repository.BindTurn(ctx, bound, "root-attach", "native-attach"); err != nil {
		t.Fatal(err)
	}
	run, err := repository.LoadRun(ctx, researchdomain.WorkerClaim{ProjectID: submitTurnProjectID, JobID: job.ID, ClaimIncarnation: claim.ClaimIncarnation})
	if err != nil || run.Attachment == nil || *run.Attachment != (researchdomain.AssignmentAttachment{
		Number: 1, Title: "Глава 634", Paragraphs: 2, Words: 4, Note: researchdomain.AttachmentNote,
	}) {
		t.Fatalf("the header of the turn = %+v, %v", run.Attachment, err)
	}

	replayed, err := chats.askWithChapter(chat, "chapter-1", first, chats.now)
	if err != nil || replayed.ID != job.ID {
		t.Fatalf("a replay = %s, %v; want %s", replayed.ID, err, job.ID)
	}
	if numbers, err := repository.ChatAttachmentNumbers(ctx, submitTurnProjectID, chat.ID); err != nil || !reflect.DeepEqual(numbers, []int{1}) {
		t.Fatalf("after a replay the chat has %v, %v", numbers, err)
	}

	_, err = chats.askWithChapter(chat, "chapter-2", &researchdomain.Attachment{Format: "text", Paragraphs: []string{"Вставка."}, Words: 1}, chats.now.Add(time.Minute))
	if err != nil {
		t.Fatalf("a second chapter in the chat: %v", err)
	}
	latest, err := repository.ChatAttachment(ctx, submitTurnProjectID, chat.ID, 0)
	if err != nil || latest.Number != 2 || latest.Title != "" || latest.FileName != "" || latest.Format != "text" {
		t.Fatalf("the latest attachment = %+v, %v", latest, err)
	}
	// An attachment without a parse (a text, or one kept before the parse was) reads none.
	if kept, err := repository.ChatAttachmentDocument(ctx, submitTurnProjectID, chat.ID, 2); err != nil || kept != nil {
		t.Fatalf("the parse of an attachment without one = %+v, %v", kept, err)
	}
	if again, err := repository.ChatAttachment(ctx, submitTurnProjectID, chat.ID, 1); err != nil || again.Title != "Глава 634" {
		t.Fatalf("the first attachment by number = %+v, %v", again, err)
	}
	if _, err := repository.ChatAttachment(ctx, submitTurnProjectID, chat.ID, 3); !errors.Is(err, researchdomain.ErrNotFound) {
		t.Fatalf("a number the chat has not = %v", err)
	}
	if other, err := repository.ChatAttachment(ctx, submitTurnProjectID, chats.chat().ID, 0); !errors.Is(err, researchdomain.ErrNotFound) {
		t.Fatalf("another chat reads none: %+v, %v", other, err)
	}

	plain, err := chats.ask(chat, "chapter-3", chats.now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("a question without a chapter: %v", err)
	}
	if header, err := repository.turnAttachmentHeader(ctx, submitTurnProjectID, turnOf(t, plain)); err != nil || header != nil {
		t.Fatalf("a turn without a chapter has a header: %+v, %v", header, err)
	}

	wantQuota(t, func() error {
		_, err := chats.askWithChapter(chat, "chapter-4", first, chats.now.Add(3*time.Minute))
		return err
	}(), quota.KeyChatTurnsPerDay)
	if numbers, err := repository.ChatAttachmentNumbers(ctx, submitTurnProjectID, chat.ID); err != nil || !reflect.DeepEqual(numbers, []int{1, 2}) {
		t.Fatalf("a refused question left a chapter: %v, %v", numbers, err)
	}

}

// CA6.1, CA6.2: the chapter goes with its turn and its project — the deletion of a chat or a project deletes the turns,
// and the rows of their chapters with them — and nothing ties it to a draft of «Письмо».
func TestTheChapterGoesWithItsTurnAndItsProject(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_attachment_fk")
	rows, err := chats.db.Pool.Query(context.Background(), `
		SELECT confrelid::regclass::text, confdeltype
		FROM pg_constraint
		WHERE conrelid = 'chat_turn_attachments'::regclass AND contype = 'f'
		ORDER BY 1
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var table, action string
		if err := rows.Scan(&table, &action); err != nil {
			t.Fatal(err)
		}
		got[table] = action
	}
	if !reflect.DeepEqual(got, map[string]string{"chat_turns": "c", "projects": "c"}) {
		t.Fatalf("the links of a chapter = %v; want a cascade from its turn and its project only", got)
	}
}
