// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"slices"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
	librarystore "canonkeeper/backend/internal/store/library"
)

// obsidianShelf is the queue of the volumes and «Обновить из файла» of the core as the bridge calls their use cases
// (specs/2026-11-14-desk-obsidian-books.md OK1.3, OK2.1): the use cases of the library, the cancel of a queue by its store —
// the one the shelf uses —, and the keys the core keeps for its replays.
type obsidianShelf struct {
	library *library.Service
	store   *librarystore.Store
	jobs    *jobs.Service
	keys    *obsidianrepo.Books
}

var _ service.Shelf = obsidianShelf{}

func (shelf obsidianShelf) LiveQueue(ctx context.Context, actorUserID, projectID string) (service.QueueRef, bool, error) {
	queues, err := shelf.library.ListImportQueues(ctx, library.ListImportQueuesInput{ProjectID: projectID, ActorUserID: actorUserID, Limit: 100})
	if err != nil {
		return service.QueueRef{}, false, shelfError(err)
	}
	for _, queue := range queues {
		if live(queue.Status) {
			return service.QueueRef{ID: queue.ID, Status: string(queue.Status)}, true, nil
		}
	}
	return service.QueueRef{}, false, nil
}

func live(status library.ImportQueueStatus) bool {
	return status == library.ImportQueueUploading || status == library.ImportQueueDraft || status == library.ImportQueueRunning
}

func (shelf obsidianShelf) CreateQueue(ctx context.Context, write service.Write, volumes []service.QueueVolume) (string, []string, error) {
	items := make([]library.CreateImportQueueItemInput, len(volumes))
	for index, volume := range volumes {
		items[index] = library.CreateImportQueueItemInput{ClientItemID: volume.Folder, Position: index + 1, SourceFileName: volume.FileName}
	}
	queue, err := shelf.library.CreateImportQueue(ctx, library.CreateImportQueueInput{
		ProjectID: write.ProjectID, ActorUserID: write.ActorUserID, IdempotencyKey: write.Key, RequestMethod: write.Method,
		RequestPath: write.Path, SessionWriteProof: write.Proof, Items: items,
	})
	if errors.Is(err, library.ErrActiveImportQueueExists) {
		return "", nil, service.ErrQueueExists
	}
	if err != nil {
		return "", nil, shelfError(err)
	}
	ordered := slices.Clone(queue.Items)
	slices.SortFunc(ordered, func(left, right library.ImportQueueItem) int { return left.Position - right.Position })
	ids := make([]string, len(ordered))
	for index, item := range ordered {
		ids[index] = item.ID
	}
	return queue.ID, ids, nil
}

func (shelf obsidianShelf) Upload(ctx context.Context, write service.Write, queueID, itemID string, volume service.QueueVolume) (int64, error) {
	item, err := shelf.library.UploadImportQueueItem(ctx, library.UploadImportQueueItemInput{
		ProjectID: write.ProjectID, QueueID: queueID, ItemID: itemID, ActorUserID: write.ActorUserID, FileName: volume.FileName,
		Content: volume.Content, IdempotencyKey: write.Key, RequestMethod: write.Method, RequestPath: write.Path,
		SessionWriteProof: write.Proof,
	})
	if err != nil {
		return 0, shelfError(err)
	}
	return item.QueueVersion, nil
}

func (shelf obsidianShelf) Start(ctx context.Context, write service.Write, queueID string, version int64) (service.QueueRef, error) {
	queue, err := shelf.library.StartImportQueue(ctx, library.StartImportQueueInput{
		ProjectID: write.ProjectID, QueueID: queueID, ActorUserID: write.ActorUserID, ExpectedVersion: version,
		IdempotencyKey: write.Key, RequestMethod: write.Method, RequestPath: write.Path, SessionWriteProof: write.Proof,
	})
	if err != nil {
		return service.QueueRef{}, shelfError(err)
	}
	return service.QueueRef{ID: queue.ID, Status: string(queue.Status)}, nil
}

// CancelDraft cancels a draft of the bridge by the store of the queue, as the shelf does (OK1.4): it needs no session —
// the start of the backend has none. A queue the author started or ended meanwhile stays as it is.
func (shelf obsidianShelf) CancelDraft(ctx context.Context, draft service.Draft) error {
	for range 2 {
		queue, err := shelf.store.GetImportQueueForActor(ctx, library.GetImportQueueStoreInput{
			ProjectID: draft.ProjectID, QueueID: draft.QueueID, ActorUserID: draft.ActorUserID,
		})
		if errors.Is(err, library.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if queue.Status != library.ImportQueueUploading && queue.Status != library.ImportQueueDraft {
			return nil
		}
		_, err = shelf.store.CancelImportQueueForActor(ctx, library.CancelImportQueueStoreInput{
			ProjectID: draft.ProjectID, QueueID: draft.QueueID, ActorUserID: draft.ActorUserID, ExpectedVersion: queue.Version,
		})
		if !errors.Is(err, library.ErrConflict) {
			return err
		}
	}
	return service.ErrConflict
}

func (shelf obsidianShelf) QueueOfKey(ctx context.Context, actorUserID, projectID, key string) (bool, error) {
	return shelf.keys.QueueOfKey(ctx, actorUserID, projectID, key)
}

// ImportJob is «Обновить из файла» with the glued text (OK2.1): the refusals of the core come back as the core says them.
func (shelf obsidianShelf) ImportJob(ctx context.Context, write service.Write, update service.UpdateJob) (jobs.Job, error) {
	mode := library.ImportModeUpdatePreview
	if update.Apply {
		mode = library.ImportModeUpdateBook
	}
	return shelf.library.CreateImportJob(ctx, library.Actor{UserID: write.ActorUserID}, write.ProjectID, library.CreateImportJobInput{
		SourceFileName: update.FileName, SizeBytes: int64(len(update.Content)), FileBytes: update.Content, IdempotencyKey: write.Key,
		RequestMethod: write.Method, RequestPath: write.Path, ImportMode: mode, BookID: update.BookID, SessionWriteProof: write.Proof,
	})
}

func (shelf obsidianShelf) JobOfKey(ctx context.Context, write service.Write) (jobs.Job, bool, error) {
	jobID, ok, err := shelf.keys.JobOfKey(ctx, write.ActorUserID, write.Method, write.Path, write.Key)
	if err != nil || !ok {
		return jobs.Job{}, false, err
	}
	job, err := shelf.jobs.GetJob(ctx, jobs.Actor{UserID: write.ActorUserID}, jobID)
	return job, err == nil, err
}

// shelfError is a refusal of the queue as the bridge reads it; any other error — a limit, the session — stays as it is.
func shelfError(err error) error {
	switch {
	case errors.Is(err, library.ErrNotFound):
		return service.ErrNotFound
	case errors.Is(err, library.ErrForbidden):
		return service.ErrForbidden
	case errors.Is(err, library.ErrConflict), errors.Is(err, library.ErrImportExpectedOrderConflict):
		return service.ErrConflict
	}
	return err
}
