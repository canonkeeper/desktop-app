package library

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

// The lists of books and chapters are pages by key (specs/2026-11-07-chapter-list-paging.md, P1).
const (
	defaultPageLimit    = 50
	maxPageLimit        = 100
	listCursorVersion   = 1
	maxListCursorLength = 2048
	booksListKind       = "books"
	chaptersListKind    = "chapters"
)

var errInvalidListPosition = errors.New("library: invalid list position")

// PageMeta says whether the list goes on and where.
type PageMeta struct {
	HasMore    bool
	NextCursor *string
}

type BookPage struct {
	Data []Book
	Meta PageMeta
}

type ChapterPage struct {
	Data []ChapterSummary
	Meta PageMeta
}

// listScope is the list a cursor belongs to: a cursor of another list, state, parent or actor is refused.
type listScope struct {
	kind        string
	state       SourceListState
	parentID    string
	actorUserID string
}

type listCursor struct {
	Version   int        `json:"v"`
	Scope     string     `json:"scope"`
	Order     int        `json:"order,omitempty"`
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
	ID        string     `json:"id"`
}

func (scope listScope) hash() string {
	sum := sha256.Sum256([]byte(scope.kind + "\x00" + string(scope.state) + "\x00" + scope.parentID + "\x00" + scope.actorUserID))
	return hex.EncodeToString(sum[:])
}

// listPage is the page asked for: its size and the position its first row comes after (P1.1, P1.5).
func listPage(page Page, scope listScope) (int, *ListPosition, error) {
	limit := page.Limit
	if limit == 0 {
		limit = defaultPageLimit
	}
	if limit < 1 || limit > maxPageLimit {
		return 0, nil, ErrValidation
	}
	if page.Cursor == "" {
		return limit, nil, nil
	}
	if len(page.Cursor) > maxListCursorLength {
		return 0, nil, ErrValidation
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(page.Cursor)
	if err != nil || len(raw) == 0 {
		return 0, nil, ErrValidation
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor listCursor
	if err := decoder.Decode(&cursor); err != nil {
		return 0, nil, ErrValidation
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return 0, nil, ErrValidation
	}
	deleted := scope.state == SourceListDeleted
	if cursor.Version != listCursorVersion || cursor.Scope != scope.hash() ||
		cursor.ID == "" || len(cursor.ID) > 256 || strings.TrimSpace(cursor.ID) != cursor.ID ||
		deleted != (cursor.DeletedAt != nil) || deleted && cursor.DeletedAt.IsZero() {
		return 0, nil, ErrValidation
	}
	position := ListPosition{Order: cursor.Order, ID: cursor.ID}
	if deleted {
		position = ListPosition{DeletedAt: cursor.DeletedAt.UTC(), ID: cursor.ID}
	}
	return limit, &position, nil
}

// pageOf cuts the limit+1 rows read to the page and names the next one by its last row (P1.4).
func pageOf[T any](rows []T, limit int, scope listScope, position func(T) ListPosition) ([]T, PageMeta, error) {
	if len(rows) <= limit {
		return rows, PageMeta{}, nil
	}
	rows = rows[:limit]
	last := position(rows[limit-1])
	cursor := listCursor{Version: listCursorVersion, Scope: scope.hash(), ID: last.ID}
	if scope.state == SourceListDeleted {
		if last.DeletedAt.IsZero() {
			return nil, PageMeta{}, errInvalidListPosition
		}
		deletedAt := last.DeletedAt.UTC()
		cursor.DeletedAt = &deletedAt
	} else {
		cursor.Order = last.Order
	}
	if last.ID == "" {
		return nil, PageMeta{}, errInvalidListPosition
	}
	raw, err := json.Marshal(cursor)
	if err != nil {
		return nil, PageMeta{}, err
	}
	next := base64.RawURLEncoding.EncodeToString(raw)
	return rows, PageMeta{HasMore: true, NextCursor: &next}, nil
}

// bookPosition and chapterPosition are the keys of a row in both orders; the state of the list picks one.
func bookPosition(book Book) ListPosition {
	position := ListPosition{Order: book.Order, ID: book.ID}
	if book.DeletedAt != nil {
		position.DeletedAt = *book.DeletedAt
	}
	return position
}

func chapterPosition(chapter Chapter) ListPosition {
	position := ListPosition{Order: chapter.Order, ID: chapter.ID}
	if chapter.DeletedAt != nil {
		position.DeletedAt = *chapter.DeletedAt
	}
	return position
}
