package researchchat

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/chapterfile"
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

func (stub *attachmentRepositoryStub) ChatAttachmentDocument(_ context.Context, projectID, chatID string, number int) (*chapterfile.Document, error) {
	if projectID+"/"+chatID != stub.asked || number != 1 {
		return nil, researchdomain.ErrNotFound
	}
	return &chapterfile.Document{Format: chapterfile.FormatDOCX, Layout: &chapterfile.Layout{Styles: "<w:styles/>"}}, nil
}

// specs/2026-11-07-chat-attach-chapter.md CA5.5: the proofreading reads the attachment with its parse whole and the
// project of its chat, with the rights of a reader of that chat.
func TestGetAttachmentGivesTheProofreadingTheParseAndTheProjectOfTheChat(t *testing.T) {
	repository := &attachmentRepositoryStub{sessionRepositoryStub: sessionRepositoryStub{session: researchdomain.Session{
		ID: "chat-1", ProjectID: "project-1", Scope: researchdomain.ScopeProject, Kind: agent.SessionResearch,
	}}}
	attachment, project, err := NewGetAttachment(repository).Chapter(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", 1)
	if err != nil || project != "project-1" || attachment.Document == nil || attachment.Document.Layout == nil {
		t.Fatalf("chapter = %+v in %q, %v", attachment, project, err)
	}
	repository.session.Kind = agent.SessionDraft
	if _, _, err := NewGetAttachment(repository).Chapter(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", 1); !errors.Is(err, researchdomain.ErrNotFound) {
		t.Fatalf("a helper's session = %v", err)
	}
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
