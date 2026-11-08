package notes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	domain "canonkeeper/backend/internal/domain/notes"
)

type ListInput struct {
	Query     string `json:"query,omitempty"`
	Source    string `json:"source,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	BookID    string `json:"bookId,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}
type Cursor struct {
	UpdatedAt   time.Time `json:"at"`
	ID          string    `json:"id"`
	Fingerprint string    `json:"fingerprint"`
}
type ListQuery struct {
	UserID string
	Input  ListInput
	Limit  int
	Cursor Cursor
}
type ListResult struct {
	Entries    []domain.Entry
	TotalCount int64
}
type ListRepository interface {
	List(context.Context, ListQuery) (ListResult, error)
}
type List struct{ Repository ListRepository }

func (useCase List) Execute(ctx context.Context, userID string, input ListInput) (domain.Page, error) {
	input.Query = strings.TrimSpace(input.Query)
	if input.Limit == 0 {
		input.Limit = 30
	}
	if !domain.ValidID(userID) || input.Limit < 1 || input.Limit > 100 || !utf8.ValidString(input.Query) || strings.ContainsRune(input.Query, 0) || utf8.RuneCountInString(input.Query) > 200 ||
		(input.Source != "" && input.Source != "free" && input.Source != "chat" && input.Source != "chapter") ||
		(input.ProjectID != "" && !domain.ValidID(input.ProjectID)) || (input.BookID != "" && (input.ProjectID == "" || !domain.ValidID(input.BookID))) {
		return domain.Page{}, domain.ErrValidation
	}
	// Only a digest of private search text is included in the opaque cursor.
	filters, _ := json.Marshal([]string{userID, input.Query, input.Source, input.ProjectID, input.BookID})
	fingerprint := digest(string(filters))
	query := ListQuery{UserID: userID, Input: input, Limit: input.Limit + 1}
	if input.Cursor != "" {
		if len(input.Cursor) > 1024 {
			return domain.Page{}, domain.ErrValidation
		}
		raw, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		if err != nil || json.Unmarshal(raw, &query.Cursor) != nil || query.Cursor.UpdatedAt.IsZero() || !domain.ValidID(query.Cursor.ID) ||
			query.Cursor.Fingerprint != fingerprint {
			return domain.Page{}, domain.ErrValidation
		}
	}
	result, err := useCase.Repository.List(ctx, query)
	if err != nil {
		return domain.Page{}, err
	}
	page := domain.Page{Data: result.Entries, Meta: domain.PageMeta{HasMore: len(result.Entries) > input.Limit, TotalCount: result.TotalCount}}
	if page.Data == nil {
		page.Data = []domain.Entry{}
	}
	if page.Meta.HasMore {
		page.Data = page.Data[:input.Limit]
		last := page.Data[len(page.Data)-1]
		raw, err := json.Marshal(Cursor{ID: last.ID, UpdatedAt: last.UpdatedAt, Fingerprint: fingerprint})
		if err != nil {
			return domain.Page{}, err
		}
		cursor := base64.RawURLEncoding.EncodeToString(raw)
		page.Meta.NextCursor = &cursor
	}
	return page, nil
}
