package projects

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

const (
	collectionCursorVersion          = 1
	maxEncodedCollectionCursorLength = 2048
	projectCollectionCursorKind      = "projects"
	deletedProjectCursorKind         = "projects_deleted"
	invitationCollectionCursorKind   = "project_invitations"
)

var errInvalidPaginationPosition = errors.New("projects: invalid pagination position")

type PageMeta struct {
	HasMore    bool
	NextCursor *string
}

type ProjectPage struct {
	Data []ProjectRecord
	Meta PageMeta
}

type ProjectInvitationPage struct {
	Data []ProjectInvitationRecord
	Meta PageMeta
}

type ListMyProjectInvitationsInput struct {
	Limit  int
	Cursor string
}

type collectionCursor struct {
	Version   int       `json:"v"`
	Kind      string    `json:"kind"`
	Scope     string    `json:"scope"`
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

func collectionLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultPageLimit, nil
	}
	if limit < 1 || limit > maxPageLimit {
		return 0, ErrValidation
	}
	return limit, nil
}

func decodeCollectionCursor(encoded, kind, actorUserID, emailHash string) (collectionCursor, error) {
	if encoded == "" {
		return collectionCursor{}, nil
	}
	if len(encoded) > maxEncodedCollectionCursorLength {
		return collectionCursor{}, ErrValidation
	}

	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 {
		return collectionCursor{}, ErrValidation
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor collectionCursor
	if err := decoder.Decode(&cursor); err != nil {
		return collectionCursor{}, ErrValidation
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return collectionCursor{}, ErrValidation
	}
	if cursor.Version != collectionCursorVersion ||
		cursor.Kind != kind ||
		cursor.Scope != collectionCursorScope(kind, actorUserID, emailHash) ||
		cursor.CreatedAt.IsZero() ||
		cursor.ID == "" ||
		len(cursor.ID) > 256 ||
		strings.TrimSpace(cursor.ID) != cursor.ID {
		return collectionCursor{}, ErrValidation
	}
	cursor.CreatedAt = cursor.CreatedAt.UTC()
	return cursor, nil
}

func encodeCollectionCursor(kind, actorUserID, emailHash string, createdAt time.Time, id string) (string, error) {
	if kind == "" || actorUserID == "" || createdAt.IsZero() || id == "" {
		return "", errInvalidPaginationPosition
	}
	raw, err := json.Marshal(collectionCursor{
		Version:   collectionCursorVersion,
		Kind:      kind,
		Scope:     collectionCursorScope(kind, actorUserID, emailHash),
		CreatedAt: createdAt.UTC(),
		ID:        id,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func collectionCursorScope(kind, actorUserID, emailHash string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + actorUserID + "\x00" + emailHash))
	return hex.EncodeToString(sum[:])
}

func projectPageForState(records []ProjectRecord, limit int, actorUserID string, state ProjectListState) (ProjectPage, error) {
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	page := ProjectPage{Data: records, Meta: PageMeta{HasMore: hasMore}}
	if !hasMore {
		return page, nil
	}
	last := records[len(records)-1]
	kind := projectCollectionCursorKind
	position := last.CreatedAt
	if state == ProjectListDeleted {
		kind = deletedProjectCursorKind
		if last.DeletedAt == nil {
			return ProjectPage{}, errInvalidPaginationPosition
		}
		position = *last.DeletedAt
	}
	cursor, err := encodeCollectionCursor(kind, actorUserID, "", position, last.ID)
	if err != nil {
		return ProjectPage{}, err
	}
	page.Meta.NextCursor = &cursor
	return page, nil
}

func projectInvitationPage(records []ProjectInvitationRecord, limit int, actorUserID, emailHash string) (ProjectInvitationPage, error) {
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	page := ProjectInvitationPage{Data: records, Meta: PageMeta{HasMore: hasMore}}
	if !hasMore {
		return page, nil
	}
	last := records[len(records)-1]
	cursor, err := encodeCollectionCursor(invitationCollectionCursorKind, actorUserID, emailHash, last.CreatedAt, last.ID)
	if err != nil {
		return ProjectInvitationPage{}, err
	}
	page.Meta.NextCursor = &cursor
	return page, nil
}
