package indexing

import (
	"errors"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
)

var (
	ErrNotFound   = errors.New("indexing: not found")
	ErrForbidden  = errors.New("indexing: forbidden")
	ErrValidation = errors.New("indexing: validation failed")
	ErrConflict   = errors.New("indexing: conflict")
)

type Actor struct {
	UserID string
}

type StartIndexingInput struct {
	ProjectID         string
	SourceView        governance.SourceView
	RequestMethod     string
	RequestPath       string
	IdempotencyKey    string
	SessionWriteProof auth.SessionWriteProof
}

type StartIndexingStoreInput struct {
	CandidateJobID        string
	IdempotencyRecordID   string
	ActorUserID           string
	ProjectID             string
	SourceView            governance.SourceView
	ScopeHash             string
	OperationID           string
	IdempotencyKeyHash    string
	RequestHash           string
	IdempotencyStatusCode int
	CreatedAt             time.Time
	SessionWriteProof     auth.SessionWriteProof
}
