package jobs

import (
	"errors"
	"time"
)

const (
	MaxJobAttempts        = 3
	RetryExhaustedCode    = "worker.retry_exhausted"
	RetryExhaustedMessage = "job retry attempts exhausted"
)

var (
	ErrRetryExhausted            = errors.New("jobs: retry exhausted")
	ErrModelRetryFenceRequired   = errors.New("jobs: model retry fence required")
	ErrModelOwnerCleanupRequired = errors.New("store: model owner cleanup required")
)

func RetryDelay(retryCount int) (time.Duration, error) {
	switch retryCount {
	case 1:
		return 15 * time.Second, nil
	case 2:
		return 60 * time.Second, nil
	default:
		return 0, ErrValidation
	}
}

type Kind string

const (
	KindImport           Kind = "import"
	KindCanonTextIndex   Kind = "canon_text_index"
	KindResearchChatTurn Kind = "research_chat_turn"
	KindChapterCards     Kind = "chapter_cards"
	KindIllustration     Kind = "illustration"
	KindSagaLayer        Kind = "saga_layer"
	KindChapterDraft     Kind = "chapter_draft"
	// KindAuthorMaterialsImport maps the sections of a file of the author (specs/2026-11-04-saga-map.md, R9.2).
	KindAuthorMaterialsImport Kind = "author_materials_import"
)

type PublicStatus string

const (
	PublicStatusQueued    PublicStatus = "queued"
	PublicStatusRunning   PublicStatus = "running"
	PublicStatusCanceling PublicStatus = "canceling"
	PublicStatusSucceeded PublicStatus = "succeeded"
	PublicStatusFailed    PublicStatus = "failed"
	PublicStatusCanceled  PublicStatus = "canceled"
)

type QueueClass string

const (
	QueueClassInteractive  QueueClass = "interactive"
	QueueClassUserDemanded QueueClass = "user_demanded"
	QueueClassAutomatic    QueueClass = "automatic"
)

var currentKinds = [...]Kind{
	KindImport,
	KindCanonTextIndex,
	KindResearchChatTurn,
	KindChapterCards,
	KindIllustration,
	KindSagaLayer,
	KindChapterDraft,
	KindAuthorMaterialsImport,
}

func CurrentKinds() []Kind {
	return append([]Kind(nil), currentKinds[:]...)
}

type TextCoverage struct {
	CurrentBooks         int  `json:"currentBooks"`
	TotalBooks           int  `json:"totalBooks"`
	NonCurrentBooks      int  `json:"nonCurrentBooks"`
	TotalNonCurrentBooks int  `json:"totalNonCurrentBooks"`
	Truncated            bool `json:"truncated"`
}
