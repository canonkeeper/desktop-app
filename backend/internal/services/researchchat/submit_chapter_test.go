package researchchat

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/chapterfile"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
)

func chapterQuestion(chapter *ChapterInput) SubmitTurnInput {
	return SubmitTurnInput{
		Question: "Ложится ли эта глава на канон?", IdempotencyKey: "idempotency-chapter", RequestMethod: "post",
		RequestPath: "/api/v1/chats/chat-1/turns/chapter", Chapter: chapter,
		SessionWriteProof: auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"},
	}
}

func chapterRepository() *sessionRepositoryStub {
	return &sessionRepositoryStub{
		session: researchdomain.Session{
			ID: "chat-1", ProjectID: "10000000-0000-4000-8000-000000000001", SourceView: "draft", Scope: researchdomain.ScopeProject,
			LifecycleEpoch: 1, AgentGeneration: researchdomain.CurrentAgentGeneration, Kind: agent.SessionResearch,
		},
		job: jobs.Job{ID: "job-1", Kind: jobs.KindResearchChatTurn},
	}
}

// specs/2026-11-07-chat-attach-chapter.md CA2: the chapter of the question is read by the one reader of a chapter file
// before the repository is asked, and the hash of the request holds it — another chapter under the same key is another
// request.
func TestSubmitTurnReadsTheChapterAttachedToTheQuestion(t *testing.T) {
	clock := clockStub{value: time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)}
	repository := chapterRepository()
	if _, err := NewSubmitTurn(repository, clock).Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", chapterQuestion(&ChapterInput{
		FileName: "634.md", Content: []byte("# Глава 634. Перевал\n\nКайл шёл.\n\nОльм отстал."),
	})); err != nil {
		t.Fatal(err)
	}
	attachment := repository.started.Attachment
	if attachment == nil || attachment.Title != "Глава 634. Перевал" || attachment.FileName != "634.md" ||
		!reflect.DeepEqual(attachment.Paragraphs, []string{"Кайл шёл.", "Ольм отстал."}) || attachment.Words != 4 {
		t.Fatalf("the attachment of the question = %+v", attachment)
	}
	first := repository.started.RequestHash

	repository = chapterRepository()
	if _, err := NewSubmitTurn(repository, clock).Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", chapterQuestion(&ChapterInput{
		Text: "Кайл шёл.\nОльм догнал.", HasText: true,
	})); err != nil {
		t.Fatal(err)
	}
	if repository.started.Attachment == nil || repository.started.Attachment.Format != "text" || repository.started.RequestHash == first {
		t.Fatalf("a pasted chapter is another request: %+v", repository.started)
	}
	plain := chapterQuestion(nil)
	repository = chapterRepository()
	if _, err := NewSubmitTurn(repository, clock).Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", plain); err != nil || repository.started.Attachment != nil {
		t.Fatalf("a question without a chapter = %v, %+v", err, repository.started.Attachment)
	}
}

// CA2.3: a chapter the reader refuses starts nothing.
func TestSubmitTurnRefusesAChapterTheReaderRefuses(t *testing.T) {
	for name, testCase := range map[string]struct {
		chapter ChapterInput
		want    error
	}{
		"format":  {ChapterInput{FileName: "634.pdf", Content: []byte("%PDF")}, chapterfile.ErrFormat},
		"broken":  {ChapterInput{FileName: "634.docx", Content: []byte("not a zip")}, chapterfile.ErrUnreadable},
		"large":   {ChapterInput{FileName: "634.txt", Content: make([]byte, chapterfile.MaxFileBytes+1)}, chapterfile.ErrTooLarge},
		"long":    {ChapterInput{Text: strings.Repeat("слово ", chapterfile.MaxTextRunes/6+10), HasText: true}, chapterfile.ErrTooLong},
		"empty":   {ChapterInput{Text: " \n ", HasText: true}, chapterfile.ErrEmpty},
		"nothing": {ChapterInput{}, researchdomain.ErrValidation},
	} {
		t.Run(name, func(t *testing.T) {
			repository := chapterRepository()
			chapter := testCase.chapter
			_, err := NewSubmitTurn(repository, clockStub{value: time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)}).
				Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", chapterQuestion(&chapter))
			if !errors.Is(err, testCase.want) || repository.started.ActorUserID != "" {
				t.Fatalf("refusal = %v, want %v; started %+v", err, testCase.want, repository.started)
			}
		})
	}
}
