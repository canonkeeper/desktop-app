package library

import (
	"context"
	"time"

	"canonkeeper/backend/internal/auth"
)

type ImportQueueStatus string

const (
	ImportQueueUploading ImportQueueStatus = "uploading"
	ImportQueueDraft     ImportQueueStatus = "draft"
	ImportQueueRunning   ImportQueueStatus = "running"
	ImportQueueSucceeded ImportQueueStatus = "succeeded"
	ImportQueueFailed    ImportQueueStatus = "failed"
	ImportQueueCanceled  ImportQueueStatus = "canceled"
	ImportQueueExpired   ImportQueueStatus = "expired"
)

const (
	ImportQueueItemUploading = "uploading"
	ImportQueueItemReady     = "ready"
	ImportQueueItemInvalid   = "invalid"
)

type ImportQueue struct {
	ID              string
	ProjectID       string
	CreatedByUserID string
	Status          ImportQueueStatus
	Version         int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	StartedAt       *time.Time
	CompletedAt     *time.Time
	Items           []ImportQueueItem
}

type ImportQueueItem struct {
	ID                  string
	QueueID             string
	ProjectID           string
	ClientItemID        string
	Position            int
	SourceFileName      string
	StorageKey          string
	MIMEType            string
	SizeBytes           int64
	SHA256              string
	DetectedBookOrder   int
	ConfirmedBookOrder  int
	PreflightStatus     string
	PreflightErrorJSON  []byte
	ImportJobID         string
	ImportJobStatus     string
	ImportJobRetryCount int
	ImportJobErrorJSON  []byte
	BookID              string
	QueueVersion        int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type CreateImportQueueDraftStoreInput struct {
	ID                 string
	ProjectID          string
	ActorUserID        string
	IdempotencyKey     string
	RequestFingerprint string
	Items              []CreateImportQueueItemStoreInput
}

type CreateImportQueueItemStoreInput struct {
	ID                string
	ClientItemID      string
	Position          int
	SourceFileName    string
	DetectedBookOrder int
}

type GetImportQueueStoreInput struct {
	ProjectID   string
	QueueID     string
	ActorUserID string
}

type ListImportQueuesStoreInput struct {
	ProjectID   string
	ActorUserID string
	Limit       int
}

type PutImportQueueItemStoreInput struct {
	ProjectID          string
	QueueID            string
	ItemID             string
	ActorUserID        string
	ExpectedVersion    int64
	StorageKey         string
	SourceFileName     string
	MIMEType           string
	SizeBytes          int64
	SHA256             string
	FileBytes          []byte
	PreflightStatus    string
	PreflightErrorJSON []byte
	DetectedBookOrder  int
	ConfirmedBookOrder int
	ExpiresAt          *time.Time
}

type SaveImportQueueDraftStoreInput struct {
	ProjectID       string
	QueueID         string
	ActorUserID     string
	ExpectedVersion int64
	Items           []SaveImportQueueItemStoreInput
}

type SaveImportQueueItemStoreInput struct {
	ItemID             string
	Position           int
	ConfirmedBookOrder int
}

type CancelImportQueueStoreInput struct {
	ProjectID       string
	QueueID         string
	ActorUserID     string
	ExpectedVersion int64
}

type StartImportQueueStoreInput struct {
	ProjectID          string
	QueueID            string
	ActorUserID        string
	ExpectedVersion    int64
	IdempotencyKey     string
	RequestMethod      string
	RequestPath        string
	RequestFingerprint string
	SessionWriteProof  auth.SessionWriteProof
}

type StartImportQueueStore interface {
	StartImportQueueForActor(context.Context, StartImportQueueStoreInput) (ImportQueue, error)
}

func importQueueStoreFor(store Store) ImportQueueProjectionStore {
	if queueStore, ok := store.(ImportQueueProjectionStore); ok {
		return queueStore
	}
	return nil
}

func startImportQueueStoreFor(store Store) StartImportQueueStore {
	if queueStore, ok := store.(StartImportQueueStore); ok {
		return queueStore
	}
	return nil
}
