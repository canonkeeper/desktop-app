package library

import (
	"context"
	"time"

	"canonkeeper/backend/internal/jobs"
)

var _ ImportQueueProjectionStore = (*importQueueMemoryStoreForTest)(nil)

type importQueueMemoryStoreForTest struct {
	*memoryStoreForTest
	queues                  map[string]*ImportQueue
	createFingerprints      map[string]string
	createdQueueIDs         map[string]string
	putImportQueueItemCalls int
	lastStartInput          *StartImportQueueStoreInput
	lastReplaceInput        *ReplaceImportQueueItemStoreInput
}

func newImportQueueMemoryStoreForTest(memory *memoryStoreForTest) *importQueueMemoryStoreForTest {
	return &importQueueMemoryStoreForTest{
		memoryStoreForTest: memory, queues: make(map[string]*ImportQueue),
		createFingerprints: make(map[string]string), createdQueueIDs: make(map[string]string),
	}
}

func (s *importQueueMemoryStoreForTest) CreateImportQueueDraftForActor(_ context.Context, input CreateImportQueueDraftStoreInput) (ImportQueue, error) {
	key := input.ProjectID + "\x00" + input.ActorUserID + "\x00" + input.IdempotencyKey
	if fingerprint, exists := s.createFingerprints[key]; exists {
		if fingerprint != input.RequestFingerprint {
			return ImportQueue{}, ErrConflict
		}
		return cloneImportQueueForTest(s.queues[s.createdQueueIDs[key]]), nil
	}
	now := staticTestTime()
	queue := &ImportQueue{
		ID: "queue-1", ProjectID: input.ProjectID, CreatedByUserID: input.ActorUserID,
		Status: ImportQueueUploading, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	for _, item := range input.Items {
		queue.Items = append(queue.Items, ImportQueueItem{
			ID: "item-1", QueueID: queue.ID, ProjectID: queue.ProjectID, ClientItemID: item.ClientItemID,
			Position: item.Position, SourceFileName: item.SourceFileName,
			PreflightStatus: ImportQueueItemUploading, CreatedAt: now, UpdatedAt: now,
		})
	}
	s.queues[queue.ID] = queue
	s.createFingerprints[key] = input.RequestFingerprint
	s.createdQueueIDs[key] = queue.ID
	return cloneImportQueueForTest(queue), nil
}

func (s *importQueueMemoryStoreForTest) GetImportQueueForActor(_ context.Context, input GetImportQueueStoreInput) (ImportQueue, error) {
	queue, exists := s.queues[input.QueueID]
	if !exists || queue.ProjectID != input.ProjectID {
		return ImportQueue{}, ErrNotFound
	}
	return cloneImportQueueForTest(queue), nil
}

func (s *importQueueMemoryStoreForTest) ListImportQueuesForActor(_ context.Context, input ListImportQueuesStoreInput) ([]ImportQueue, error) {
	queues := make([]ImportQueue, 0, len(s.queues))
	for _, queue := range s.queues {
		if queue.ProjectID == input.ProjectID {
			queues = append(queues, cloneImportQueueForTest(queue))
		}
	}
	return queues, nil
}

func (s *importQueueMemoryStoreForTest) PutImportQueueItemForActor(_ context.Context, input PutImportQueueItemStoreInput) (ImportQueueItem, error) {
	queue, exists := s.queues[input.QueueID]
	if !exists || queue.ProjectID != input.ProjectID || queue.Version != input.ExpectedVersion {
		return ImportQueueItem{}, ErrConflict
	}
	for index, item := range queue.Items {
		if item.ID != input.ItemID {
			continue
		}
		updated := item
		updated.StorageKey, updated.MIMEType, updated.SizeBytes, updated.SHA256 = input.StorageKey, input.MIMEType, input.SizeBytes, input.SHA256
		updated.SourceFileName, updated.PreflightStatus, updated.PreflightErrorJSON = input.SourceFileName, input.PreflightStatus, input.PreflightErrorJSON
		updated.QueueVersion, updated.UpdatedAt = queue.Version+1, staticTestTime()
		queue.Items[index] = updated
		queue.Version++
		queue.UpdatedAt = updated.UpdatedAt
		if importQueueAllItemsReadyForTest(queue) && queue.Status == ImportQueueUploading {
			queue.Status = ImportQueueDraft
		}
		s.putImportQueueItemCalls++
		return updated, nil
	}
	return ImportQueueItem{}, ErrNotFound
}

func (s *importQueueMemoryStoreForTest) ReplaceImportQueueItemFileForActor(_ context.Context, input ReplaceImportQueueItemStoreInput) (ImportQueue, error) {
	queue, exists := s.queues[input.QueueID]
	if !exists || queue.ProjectID != input.ProjectID || queue.Status != ImportQueueRunning {
		return ImportQueue{}, ErrConflict
	}
	for index, item := range queue.Items {
		if item.ID != input.ItemID {
			continue
		}
		if item.SkippedAt != nil || item.ImportJobStatus != string(jobs.StatusFailed) {
			return ImportQueue{}, ErrConflict
		}
		updated := item
		updated.SourceFileName, updated.PreflightStatus, updated.PreflightErrorJSON = input.SourceFileName, input.PreflightStatus, input.PreflightErrorJSON
		if input.PreflightStatus == ImportQueueItemReady {
			updated.StorageKey, updated.MIMEType, updated.SizeBytes, updated.SHA256 = input.StorageKey, input.MIMEType, input.SizeBytes, input.SHA256
			updated.ImportJobID, updated.ImportJobStatus = "job-again-"+item.ID, string(jobs.StatusQueued)
		}
		queue.Items[index] = updated
		queue.Version++
		captured := input
		s.lastReplaceInput = &captured
		return cloneImportQueueForTest(queue), nil
	}
	return ImportQueue{}, ErrNotFound
}

func (s *importQueueMemoryStoreForTest) SkipImportQueueItemForActor(_ context.Context, input SkipImportQueueItemStoreInput) (ImportQueue, error) {
	queue, exists := s.queues[input.QueueID]
	if !exists || queue.ProjectID != input.ProjectID || queue.Status != ImportQueueRunning {
		return ImportQueue{}, ErrConflict
	}
	for index, item := range queue.Items {
		if item.ID == input.ItemID && item.SkippedAt == nil && item.ImportJobStatus == string(jobs.StatusFailed) {
			skippedAt := staticTestTime()
			queue.Items[index].SkippedAt = &skippedAt
			queue.Version++
			return cloneImportQueueForTest(queue), nil
		}
	}
	return ImportQueue{}, ErrConflict
}

func (s *importQueueMemoryStoreForTest) CancelImportQueueForActor(_ context.Context, input CancelImportQueueStoreInput) (ImportQueue, error) {
	queue, exists := s.queues[input.QueueID]
	if !exists || queue.ProjectID != input.ProjectID || queue.Version != input.ExpectedVersion {
		return ImportQueue{}, ErrConflict
	}
	queue.Status = ImportQueueCanceled
	queue.Version++
	queue.UpdatedAt = staticTestTime()
	for index := range queue.Items {
		if queue.Items[index].ImportJobID != "" {
			queue.Items[index].ImportJobStatus = string(jobs.StatusCanceled)
		}
	}
	return cloneImportQueueForTest(queue), nil
}

func (s *importQueueMemoryStoreForTest) StartImportQueueForActor(_ context.Context, input StartImportQueueStoreInput) (ImportQueue, error) {
	queue, exists := s.queues[input.QueueID]
	if !exists || queue.ProjectID != input.ProjectID || queue.Version != input.ExpectedVersion || queue.Status != ImportQueueDraft {
		return ImportQueue{}, ErrConflict
	}
	for index := range queue.Items {
		if queue.Items[index].PreflightStatus != ImportQueueItemReady || queue.Items[index].StorageKey == "" {
			return ImportQueue{}, ErrValidation
		}
		queue.Items[index].ImportJobID = "job-" + queue.Items[index].ID
		queue.Items[index].ImportJobStatus = string(jobs.StatusQueued)
		queue.Items[index].UpdatedAt = staticTestTime()
	}
	startedAt := staticTestTime()
	queue.Status = ImportQueueRunning
	queue.Version++
	queue.StartedAt = &startedAt
	queue.UpdatedAt = startedAt
	captured := input
	s.lastStartInput = &captured
	return cloneImportQueueForTest(queue), nil
}

func importQueueAllItemsReadyForTest(queue *ImportQueue) bool {
	for _, item := range queue.Items {
		if item.StorageKey == "" || item.PreflightStatus != ImportQueueItemReady {
			return false
		}
	}
	return true
}

func cloneImportQueueForTest(queue *ImportQueue) ImportQueue {
	cloned := *queue
	cloned.Items = append([]ImportQueueItem(nil), queue.Items...)
	cloned.StartedAt = cloneImportQueueTimeForTest(queue.StartedAt)
	cloned.CompletedAt = cloneImportQueueTimeForTest(queue.CompletedAt)
	return cloned
}

func cloneImportQueueTimeForTest(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
