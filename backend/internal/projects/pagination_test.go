package projects

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCollectionCursorRoundTripBindsScopeWithoutExposingActorOrEmailHash(t *testing.T) {
	t.Parallel()

	actorUserID := "user-private"
	emailHash := strings.Repeat("a", 64)
	createdAt := time.Date(2026, 6, 24, 12, 30, 0, 123000000, time.UTC)
	encoded, err := encodeCollectionCursor(invitationCollectionCursorKind, actorUserID, emailHash, createdAt, "invitation-1")
	if err != nil {
		t.Fatalf("encodeCollectionCursor() error = %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode base64 cursor: %v", err)
	}
	if bytes.Contains(raw, []byte(actorUserID)) || bytes.Contains(raw, []byte(emailHash)) {
		t.Fatalf("cursor exposed actor/email scope: %s", raw)
	}

	decoded, err := decodeCollectionCursor(encoded, invitationCollectionCursorKind, actorUserID, emailHash)
	if err != nil {
		t.Fatalf("decodeCollectionCursor() error = %v", err)
	}
	if decoded.ID != "invitation-1" || !decoded.CreatedAt.Equal(createdAt) {
		t.Fatalf("decoded cursor = %#v, want original position", decoded)
	}
	if _, err := decodeCollectionCursor(encoded, invitationCollectionCursorKind, "user-other", emailHash); !errors.Is(err, ErrValidation) {
		t.Fatalf("cross-actor cursor error = %v, want ErrValidation", err)
	}
	if _, err := decodeCollectionCursor(encoded, projectCollectionCursorKind, actorUserID, emailHash); !errors.Is(err, ErrValidation) {
		t.Fatalf("cross-collection cursor error = %v, want ErrValidation", err)
	}
}

func TestCollectionCursorRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	encoded, err := encodeCollectionCursor(projectCollectionCursorKind, "user-1", "", time.Date(2026, 6, 24, 12, 30, 0, 0, time.UTC), "project-1")
	if err != nil {
		t.Fatalf("encodeCollectionCursor() error = %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode base64 cursor: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode cursor JSON: %v", err)
	}
	payload["unexpected"] = true
	raw, err = json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode cursor JSON: %v", err)
	}

	if _, err := decodeCollectionCursor(base64.RawURLEncoding.EncodeToString(raw), projectCollectionCursorKind, "user-1", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown-field cursor error = %v, want ErrValidation", err)
	}
}
