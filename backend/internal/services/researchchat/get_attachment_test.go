package researchchat

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

type attachmentRepositoryStub struct {
	sessionRepositoryStub
	asked string
}

func (stub *attachmentRepositoryStub) ChatAttachment(_ context.Context, projectID, chatID string, number int) (researchdomain.Attachment, error) {
	stub.asked = projectID + "/" + chatID
	if number != 1 {
		return researchdomain.Attachment{}, researchdomain.ErrNotFound
	}
	return researchdomain.Attachment{Number: 1, Title: "Глава 634", Paragraphs: []string{"а"}, Words: 1}, nil
}

// specs/2026-11-07-chat-attach-chapter.md CA4.3: the reader of the chat reads its attachment; the session of a helper
// and a number below 1 have none.
func TestGetAttachmentReadsTheAttachmentOfAChatItsReaderSees(t *testing.T) {
	repository := &attachmentRepositoryStub{sessionRepositoryStub: sessionRepositoryStub{session: researchdomain.Session{
		ID: "chat-1", ProjectID: "project-1", Scope: researchdomain.ScopeProject, Kind: agent.SessionResearch,
	}}}
	attachment, err := NewGetAttachment(repository).Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", 1)
	if err != nil || attachment.Title != "Глава 634" || repository.asked != "project-1/chat-1" || repository.actor.UserID != "user-1" {
		t.Fatalf("attachment = %+v, %v, asked %q", attachment, err, repository.asked)
	}
	if _, err := NewGetAttachment(repository).Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", 0); !errors.Is(err, researchdomain.ErrNotFound) {
		t.Fatalf("number 0 = %v", err)
	}
	repository.session.Kind = agent.SessionDraft
	if _, err := NewGetAttachment(repository).Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", 1); !errors.Is(err, researchdomain.ErrNotFound) {
		t.Fatalf("a helper's session = %v", err)
	}
}
