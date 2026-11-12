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
	ID             string
	QueueID        string
	ProjectID      string
	ClientItemID   string
	Position       int
	SourceFileName string
	StorageKey     string
	MIMEType       string
	SizeBytes      int64
	SHA256         string
	// ConfirmedBookOrder is the place the volume takes after the books of the shelf, given at the start of its queue by
	// its position (specs/2026-11-11-ux7-upload.md UP2.3).
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
	// SkippedAt — the author took the volume out of the upload with «Пропустить» (specs/2026-11-11-ux7-upload.md UP3.2).
	SkippedAt *time.Time
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
	ID             string
	ClientItemID   string
	Position       int
	SourceFileName string
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
	ExpiresAt          *time.Time
}

// ReplaceImportQueueItemStoreInput is «Заменить файл» of a volume that did not read (UP3.2): its new file, checked as
// every file of a queue is, and the request its new import Job is admitted by.
type ReplaceImportQueueItemStoreInput struct {
	ProjectID          string
	QueueID            string
	ItemID             string
	ActorUserID        string
	StorageKey         string
	SourceFileName     string
	MIMEType           string
	SizeBytes          int64
	SHA256             string
	FileBytes          []byte
	PreflightStatus    string
	PreflightErrorJSON []byte
	IdempotencyKey     string
	RequestMethod      string
	RequestPath        string
	RequestFingerprint string
	SessionWriteProof  auth.SessionWriteProof
}

// SkipImportQueueItemStoreInput is «Пропустить» of a volume that did not read (UP3.2).
type SkipImportQueueItemStoreInput struct {
	ProjectID   string
	QueueID     string
	ItemID      string
	ActorUserID string
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
