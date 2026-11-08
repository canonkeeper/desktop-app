package researchchat

import (
	"context"
	"strings"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// ChapterFinder resolves chapter keys in the current text version of a project.
type ChapterFinder interface {
	FindChapter(ctx context.Context, projectID, view, key string) (sagamemory.Chapter, bool, error)
}

// ResolveChapterKey turns a citation key of a chat answer into the chapter it names, in
// the chat's project and text version, for an actor who can read the chat (UC4).
type ResolveChapterKey struct {
	sessions GetSessionRepository
	chapters ChapterFinder
}

func NewResolveChapterKey(sessions GetSessionRepository, chapters ChapterFinder) *ResolveChapterKey {
	if sessions == nil || chapters == nil {
		panic("research chat: chapter key dependencies are required")
	}
	return &ResolveChapterKey{sessions: sessions, chapters: chapters}
}

func (useCase *ResolveChapterKey) Execute(
	ctx context.Context, actor researchdomain.Actor, chatID, key string,
) (researchdomain.ChapterLocation, error) {
	actor.UserID, chatID, key = strings.TrimSpace(actor.UserID), strings.TrimSpace(chatID), strings.TrimSpace(key)
	if ctx == nil || actor.UserID == "" || chatID == "" || key == "" || len(key) > 32 {
		return researchdomain.ChapterLocation{}, researchdomain.ErrNotFound
	}
	session, err := useCase.sessions.GetSession(ctx, actor, chatID)
	if err != nil {
		return researchdomain.ChapterLocation{}, err
	}
	if session.Scope != researchdomain.ScopeProject || session.BookID != "" {
		return researchdomain.ChapterLocation{}, researchdomain.ErrNotFound
	}
	chapter, ok, err := useCase.chapters.FindChapter(ctx, session.ProjectID, session.SourceView, key)
	if err != nil {
		return researchdomain.ChapterLocation{}, err
	}
	if !ok {
		return researchdomain.ChapterLocation{}, researchdomain.ErrNotFound
	}
	return researchdomain.ChapterLocation{
		ChapterKey: chapter.Key, BookID: chapter.BookID, ChapterID: chapter.ID,
		BookTitle: chapter.BookTitle, Title: chapter.Title,
	}, nil
}
