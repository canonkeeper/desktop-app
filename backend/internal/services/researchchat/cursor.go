package researchchat

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

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

const (
	defaultPageLimit        = 50
	maxPageLimit            = 100
	collectionCursorVersion = 1
	maxCursorLength         = 2048
	sessionCursorKind       = "chat_sessions"
)

type collectionCursor struct {
	Version  int       `json:"v"`
	Kind     string    `json:"kind"`
	Scope    string    `json:"scope"`
	Position time.Time `json:"position"`
	ID       string    `json:"id"`
}

func pageLimit(page researchdomain.Page) (int, error) {
	if page.Limit == 0 {
		return defaultPageLimit, nil
	}
	if page.Limit < 1 || page.Limit > maxPageLimit {
		return 0, researchdomain.ErrValidation
	}
	return page.Limit, nil
}

func decodeCollectionCursor(encoded, kind string, scopeParts ...string) (collectionCursor, error) {
	if encoded == "" {
		return collectionCursor{}, nil
	}
	if len(encoded) > maxCursorLength {
		return collectionCursor{}, researchdomain.ErrValidation
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) == 0 || base64.RawURLEncoding.EncodeToString(raw) != encoded {
		return collectionCursor{}, researchdomain.ErrValidation
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor collectionCursor
	if err := decoder.Decode(&cursor); err != nil {
		return collectionCursor{}, researchdomain.ErrValidation
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return collectionCursor{}, researchdomain.ErrValidation
	}
	if cursor.Version != collectionCursorVersion || cursor.Kind != kind ||
		cursor.Scope != collectionCursorScope(kind, scopeParts...) || cursor.Position.IsZero() ||
		cursor.ID == "" || len(cursor.ID) > 256 || strings.TrimSpace(cursor.ID) != cursor.ID {
		return collectionCursor{}, researchdomain.ErrValidation
	}
	cursor.Position = cursor.Position.UTC()
	return cursor, nil
}

func encodeCollectionCursor(kind string, position time.Time, id string, scopeParts ...string) (string, error) {
	if kind == "" || position.IsZero() || id == "" || strings.TrimSpace(id) != id {
		return "", researchdomain.ErrValidation
	}
	raw, err := json.Marshal(collectionCursor{
		Version: collectionCursorVersion, Kind: kind, Scope: collectionCursorScope(kind, scopeParts...),
		Position: position.UTC(), ID: id,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func collectionCursorScope(kind string, scopeParts ...string) string {
	value := append([]string{kind}, scopeParts...)
	sum := sha256.Sum256([]byte(strings.Join(value, "\x00")))
	return hex.EncodeToString(sum[:])
}
