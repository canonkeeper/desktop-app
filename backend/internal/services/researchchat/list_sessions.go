package researchchat

import (
	"context"
	"strings"
	"time"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

type ListSessionsInput struct {
	ProjectID string
	Scope     researchdomain.Scope
	BookID    string
	Page      researchdomain.Page
}

type ListSessionsQuery struct {
	Actor           researchdomain.Actor
	ProjectID       string
	Scope           researchdomain.Scope
	BookID          string
	CursorUpdatedAt time.Time
	CursorID        string
	Limit           int
}

type ListSessionsRepository interface {
	ListSessions(context.Context, ListSessionsQuery) ([]researchdomain.Session, error)
}

type ListSessions struct {
	repository ListSessionsRepository
}

func NewListSessions(repository ListSessionsRepository) *ListSessions {
	if repository == nil {
		panic("research chat: list-sessions repository is required")
	}
	return &ListSessions{repository: repository}
}

func (useCase *ListSessions) Execute(ctx context.Context, actor researchdomain.Actor, input ListSessionsInput) (researchdomain.SessionPage, error) {
	actor.UserID = strings.TrimSpace(actor.UserID)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.BookID = strings.TrimSpace(input.BookID)
	input.Page.Cursor = strings.TrimSpace(input.Page.Cursor)
	if ctx == nil || actor.UserID == "" || input.ProjectID == "" || !validListShape(input) {
		return researchdomain.SessionPage{}, researchdomain.ErrValidation
	}
	input.Scope = researchdomain.ScopeProject
	limit, err := pageLimit(input.Page)
	if err != nil {
		return researchdomain.SessionPage{}, err
	}
	scope := []string{actor.UserID, input.ProjectID, string(input.Scope), input.BookID}
	cursor, err := decodeCollectionCursor(input.Page.Cursor, sessionCursorKind, scope...)
	if err != nil {
		return researchdomain.SessionPage{}, err
	}
	fetchLimit := limit + 1
	if fetchLimit > maxPageLimit {
		fetchLimit = maxPageLimit
	}
	query := ListSessionsQuery{
		Actor: actor, ProjectID: input.ProjectID, Scope: input.Scope,
		BookID: input.BookID, CursorUpdatedAt: cursor.Position, CursorID: cursor.ID, Limit: fetchLimit,
	}
	rows, err := useCase.repository.ListSessions(ctx, query)
	if err != nil {
		return researchdomain.SessionPage{}, err
	}
	hasMore := len(rows) > limit
	if limit == maxPageLimit && len(rows) == limit {
		last := rows[len(rows)-1]
		probe := query
		probe.CursorUpdatedAt, probe.CursorID, probe.Limit = last.UpdatedAt, last.ID, 1
		remaining, probeErr := useCase.repository.ListSessions(ctx, probe)
		if probeErr != nil {
			return researchdomain.SessionPage{}, probeErr
		}
		hasMore = len(remaining) > 0
	}
	if hasMore {
		rows = rows[:limit]
	}
	page := researchdomain.SessionPage{Data: rows, Meta: researchdomain.PageMeta{HasMore: hasMore}}
	if hasMore {
		last := rows[len(rows)-1]
		next, encodeErr := encodeCollectionCursor(sessionCursorKind, last.UpdatedAt, last.ID, scope...)
		if encodeErr != nil {
			return researchdomain.SessionPage{}, encodeErr
		}
		page.Meta.NextCursor = &next
	}
	return page, nil
}

func validListShape(input ListSessionsInput) bool {
	if input.Scope != "" && input.Scope != researchdomain.ScopeProject {
		return false
	}
	return input.BookID == ""
}
