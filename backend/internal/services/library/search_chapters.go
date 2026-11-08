package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	domain "canonkeeper/backend/internal/domain/library"
)

type ChapterSearchRepository interface {
	SearchChapterMetadata(context.Context, domain.ChapterSearchInput) ([]domain.ChapterNavigationSearchResult, error)
}

type SearchChapters struct{ repository ChapterSearchRepository }

func NewSearchChapters(repository ChapterSearchRepository) *SearchChapters {
	if repository == nil {
		panic("chapter search: repository is required")
	}
	return &SearchChapters{repository: repository}
}

type chapterSearchCursor struct {
	Version int                     `json:"v"`
	Request string                  `json:"q"`
	After   domain.ChapterSearchKey `json:"after"`
}

// Execute searches actor-readable chapter metadata independently of indexing (global chapter search UC1).
func (search *SearchChapters) Execute(ctx context.Context, actor domain.Actor, request domain.ChapterSearchRequest) (domain.ChapterSearchPage, error) {
	request.Query = strings.TrimSpace(request.Query)
	if request.Limit == 0 {
		request.Limit = 10
	}
	if actor.UserID == "" || !utf8.ValidString(request.Query) || request.Query == "" || utf8.RuneCountInString(request.Query) > 500 ||
		(request.SourceView != "draft" && request.SourceView != "published") || request.Limit < 1 || request.Limit > 50 || len(request.Cursor) > 512 {
		return domain.ChapterSearchPage{}, domain.ErrValidation
	}
	input := domain.ChapterSearchInput{Actor: actor, Query: strings.ToLower(request.Query), SourceView: request.SourceView, Limit: request.Limit + 1}
	words := strings.Fields(request.Query)
	var number string
	if len(words) == 1 {
		number = words[0]
	} else if len(words) == 2 && strings.EqualFold(words[0], "глава") {
		number = words[1]
	}
	if number != "" && strings.IndexFunc(number, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		input.Number = strings.TrimLeft(number, "0")
		if input.Number == "" {
			input.Number = "0"
		}
	}
	fingerprintBytes, _ := json.Marshal([]any{actor.UserID, input.Query, input.Number, input.SourceView, request.Limit})
	fingerprint := sha256.Sum256(fingerprintBytes)
	requestHash := hex.EncodeToString(fingerprint[:])
	if request.Cursor != "" {
		payload, err := base64.RawURLEncoding.Strict().DecodeString(request.Cursor)
		var cursor chapterSearchCursor
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err != nil || decoder.Decode(&cursor) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || cursor.Version != 1 || cursor.Request != requestHash ||
			!chapterSearchUUID(cursor.After.ProjectID) || !chapterSearchUUID(cursor.After.BookID) || !chapterSearchUUID(cursor.After.ChapterID) {
			return domain.ChapterSearchPage{}, domain.ErrValidation
		}
		input.After = &cursor.After
	}
	rows, err := search.repository.SearchChapterMetadata(ctx, input)
	if err != nil {
		return domain.ChapterSearchPage{}, err
	}
	page := domain.ChapterSearchPage{Data: rows, Meta: domain.ChapterSearchPageMeta{HasMore: len(rows) > request.Limit}}
	if page.Data == nil {
		page.Data = []domain.ChapterNavigationSearchResult{}
	}
	if page.Meta.HasMore {
		page.Data = page.Data[:request.Limit]
		last := page.Data[len(page.Data)-1].Locator
		payload, _ := json.Marshal(chapterSearchCursor{Version: 1, Request: requestHash, After: domain.ChapterSearchKey{ProjectID: last.ProjectID, BookID: last.BookID, ChapterID: last.ChapterID}})
		page.Meta.NextCursor = base64.RawURLEncoding.EncodeToString(payload)
	}
	return page, nil
}

func chapterSearchUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil && len(decoded) == 16
}
