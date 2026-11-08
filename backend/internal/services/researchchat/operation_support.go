package researchchat

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

const (
	createSessionOperation = "research_chat.session.create.v1"
	submitTurnOperation    = "research_chat.turn.create.v1"
	defaultSessionTitle    = "Новый чат"
	minIdempotencyRunes    = 8
	maxIdempotencyRunes    = 128
)

type IDGenerator interface {
	NewID() string
}

type Clock interface {
	Now() time.Time
}

func hashText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func requestScopeHash(actorUserID, method, path string) string {
	return hashText(strings.Join([]string{actorUserID, method, path}, "\x00"))
}

func validIdempotencyKey(value string) bool {
	length := len([]rune(value))
	return length >= minIdempotencyRunes && length <= maxIdempotencyRunes
}
