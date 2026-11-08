package researchchat

import (
	"context"
	"errors"
	"testing"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
)

type chapterFinderStub struct {
	project, view, key string
}

func (stub *chapterFinderStub) FindChapter(_ context.Context, projectID, view, key string) (sagamemory.Chapter, bool, error) {
	stub.project, stub.view, stub.key = projectID, view, key
	if key != "312" {
		return sagamemory.Chapter{}, false, nil
	}
	return sagamemory.Chapter{ID: "chapter-312", BookID: "book-9", BookTitle: "Том 9", Title: "Глава 312. Ворота", Key: "312"}, true, nil
}

// UC4: a citation key resolves in the chat's own project and text version.
func TestResolveChapterKeyUsesTheChatsProjectAndView(t *testing.T) {
	sessions := &sessionRepositoryStub{session: researchdomain.Session{
		ID: "chat-1", ProjectID: "project-1", SourceView: "published", Scope: researchdomain.ScopeProject,
	}}
	finder := &chapterFinderStub{}
	useCase := NewResolveChapterKey(sessions, finder)
	location, err := useCase.Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", "312")
	if err != nil {
		t.Fatal(err)
	}
	if location != (researchdomain.ChapterLocation{ChapterKey: "312", BookID: "book-9", ChapterID: "chapter-312", BookTitle: "Том 9", Title: "Глава 312. Ворота"}) ||
		finder.project != "project-1" || finder.view != "published" || sessions.actor.UserID != "user-1" {
		t.Fatalf("location = %+v, finder = %+v", location, finder)
	}
	if _, err := useCase.Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, "chat-1", "999"); !errors.Is(err, researchdomain.ErrNotFound) {
		t.Fatalf("unknown key = %v, want not found", err)
	}
}
