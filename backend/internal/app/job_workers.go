package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"
	"canonkeeper/backend/internal/observability"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
)

type queuedJobClaims interface {
	ClaimNextQueuedJobForWorker(context.Context, []jobs.Kind) (jobs.WorkerClaimDecision, error)
}

type claimedJobStore interface {
	LoadJobForWorkerClaim(context.Context, jobs.WorkerClaim) (jobs.Job, error)
	HeartbeatWorkerClaim(context.Context, jobs.WorkerClaim) error
	RequeueWorkerClaim(context.Context, jobs.RequeueWorkerClaimStoreInput) (jobs.Job, error)
	ReleaseWorkerClaim(context.Context, jobs.WorkerClaim) (jobs.Job, error)
}

type workerJobStore struct {
	claims  queuedJobClaims
	product claimedJobStore
}

func (store workerJobStore) ClaimNextQueuedJobForWorker(ctx context.Context, kinds []jobs.Kind) (jobs.WorkerClaimDecision, error) {
	return store.claims.ClaimNextQueuedJobForWorker(ctx, kinds)
}

func (store workerJobStore) LoadJobForWorkerClaim(ctx context.Context, claim jobs.WorkerClaim) (jobs.Job, error) {
	return store.product.LoadJobForWorkerClaim(ctx, claim)
}

func (store workerJobStore) HeartbeatWorkerClaim(ctx context.Context, claim jobs.WorkerClaim) error {
	return store.product.HeartbeatWorkerClaim(ctx, claim)
}

func (store workerJobStore) RequeueWorkerClaim(ctx context.Context, input jobs.RequeueWorkerClaimStoreInput) (jobs.Job, error) {
	return store.product.RequeueWorkerClaim(ctx, input)
}

func (store workerJobStore) ReleaseWorkerClaim(ctx context.Context, claim jobs.WorkerClaim) (jobs.Job, error) {
	return store.product.ReleaseWorkerClaim(ctx, claim)
}

type importExpiryClaims interface {
	ClaimNextImportExpiryForWorker(context.Context) (jobs.WorkerClaim, error)
}

type importExpiryProductStore interface {
	ExpireImportStagingForClaim(context.Context, jobs.WorkerClaim) (jobs.Status, error)
}

type workerImportExpiryStore struct {
	claims  importExpiryClaims
	product importExpiryProductStore
}

func (store workerImportExpiryStore) ClaimNextImportExpiryForWorker(ctx context.Context) (jobs.WorkerClaim, error) {
	return store.claims.ClaimNextImportExpiryForWorker(ctx)
}

func (store workerImportExpiryStore) ExpireImportStagingForClaim(ctx context.Context, claim jobs.WorkerClaim) (jobs.Status, error) {
	return store.product.ExpireImportStagingForClaim(ctx, claim)
}

const importStagingExpiryInterval = time.Hour

type importStagingExpiryStore interface {
	ClaimNextImportExpiryForWorker(context.Context) (jobs.WorkerClaim, error)
	ExpireImportStagingForClaim(context.Context, jobs.WorkerClaim) (jobs.Status, error)
}

func startImportStagingExpiryRunner(ctx context.Context, store importStagingExpiryStore, logger *log.Logger) <-chan struct{} {
	return startImportStagingExpiryRunnerWithInterval(ctx, store, importStagingExpiryInterval, logger)
}

func startImportStagingExpiryRunnerWithInterval(
	ctx context.Context,
	store importStagingExpiryStore,
	interval time.Duration,
	logger *log.Logger,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		expireNextImportStaging(ctx, store, logger)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				expireNextImportStaging(ctx, store, logger)
			}
		}
	}()
	return done
}

func expireNextImportStaging(ctx context.Context, store importStagingExpiryStore, logger *log.Logger) bool {
	claim, err := store.ClaimNextImportExpiryForWorker(ctx)
	if errors.Is(err, jobs.ErrNotFound) {
		return false
	}
	if err != nil {
		logger.Print("import staging expiry claim failed")
		return false
	}
	status, err := store.ExpireImportStagingForClaim(ctx, claim)
	if err != nil || status != jobs.StatusExpired {
		logger.Print("import staging expiry failed")
		return true
	}
	logger.Print("import staging expiry completed")
	return true
}

type catchUpQueue interface {
	Execute(context.Context) (int, error)
}

// startCatchUp queues once at start the Jobs of what lacks them: the cards of indexed text, so text
// indexed before a release with new card instructions needs no reindex, and the thread layer of a saga
// with cards. The class of a failure goes to the log, and the end of every catch-up says how many versions it queued —
// none too: the one sign of its end, which the tests of the app count the rows of jobs after
// (specs/2026-11-15-desk-reading-queue.md RQ2.2). The line names no saga and no text.
func startCatchUp(ctx context.Context, queue catchUpQueue, work, failureEvent string, logger *log.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		queued, err := queue.Execute(ctx)
		if err != nil {
			logCatchUpFailure(logger, failureEvent, err)
		}
		logger.Printf("%s catch-up queued %d text versions", work, queued)
	}()
	return done
}

type catchUpFailureLogEntry struct {
	Event      string                       `json:"event"`
	ErrorClass governance.RequestErrorClass `json:"errorClass"`
	ErrorCause governance.RequestErrorCause `json:"errorCause"`
}

// logCatchUpFailure names the safe class of the failure (SQLSTATE, context), not its text.
func logCatchUpFailure(logger *log.Logger, event string, err error) {
	entry := catchUpFailureLogEntry{Event: event}
	entry.ErrorClass, entry.ErrorCause = observability.ClassifyError(err)
	line, _ := json.Marshal(entry)
	logger.Print(string(line))
}

type namesFailureLogEntry struct {
	Event      string                       `json:"event"`
	ProjectID  string                       `json:"projectId"`
	Stage      string                       `json:"stage"`
	ErrorClass governance.RequestErrorClass `json:"errorClass"`
	ErrorCause governance.RequestErrorCause `json:"errorCause"`
}

// logNamesFailure says that a cards Job left its name registry as it was, with the safe class of the
// failure, not its text: a model answer may quote the book.
func logNamesFailure(logger *log.Logger, projectID, stage string, err error) {
	entry := namesFailureLogEntry{Event: "saga_names_failed", ProjectID: projectID, Stage: stage}
	entry.ErrorClass, entry.ErrorCause = observability.ClassifyError(err)
	line, _ := json.Marshal(entry)
	logger.Print(string(line))
}

// cardUnbuiltLogEntry is a chapter a cards Job left without a card (specs/2026-11-17-long-chapters.md LC4.1): ids, counts
// and the outcome — no text, title, key, place or words of an error.
type cardUnbuiltLogEntry struct {
	Event        string `json:"event"`
	JobID        string `json:"jobId"`
	ProjectID    string `json:"projectId"`
	ChapterID    string `json:"chapterId"`
	Parts        int    `json:"parts"`
	Attempts     int    `json:"attempts"`
	Outcome      string `json:"outcome"`
	Seconds      int    `json:"seconds"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
}

// logCardUnbuilt says that a cards Job left a chapter without a card once the model was called (LC4.1).
func logCardUnbuilt(logger *log.Logger, unbuilt sagamemoryservice.Unbuilt) {
	line, _ := json.Marshal(cardUnbuiltLogEntry{
		Event: "card_chapter_unbuilt", JobID: unbuilt.JobID, ProjectID: unbuilt.ProjectID, ChapterID: unbuilt.ChapterID,
		Parts: unbuilt.Parts, Attempts: unbuilt.Attempts, Outcome: unbuilt.Outcome, Seconds: unbuilt.Seconds,
		InputTokens: unbuilt.InputTokens, OutputTokens: unbuilt.OutputTokens,
	})
	logger.Print(string(line))
}

// logMapFailure says that a cards Job left the prepared saga map as it was (specs/2026-11-04-saga-map.md, R10.1).
func logMapFailure(logger *log.Logger, projectID, stage string, err error) {
	entry := namesFailureLogEntry{Event: "saga_map_failed", ProjectID: projectID, Stage: stage}
	entry.ErrorClass, entry.ErrorCause = observability.ClassifyError(err)
	line, _ := json.Marshal(entry)
	logger.Print(string(line))
}

// logReadyFailure says that the wave of «Сага готова» was not closed or its letter was not queued
// (specs/2026-11-09-first-session.md, FS2.5).
func logReadyFailure(logger *log.Logger, projectID, stage string, err error) {
	entry := namesFailureLogEntry{Event: "saga_ready_failed", ProjectID: projectID, Stage: stage}
	entry.ErrorClass, entry.ErrorCause = observability.ClassifyError(err)
	line, _ := json.Marshal(entry)
	logger.Print(string(line))
}

// logLayerFailure says that a step of the thread layer went on without an answer or that its Job was not
// queued, with the safe class of the failure, not its text: a model answer may quote the book.
func logLayerFailure(logger *log.Logger, projectID, stage string, err error) {
	entry := namesFailureLogEntry{Event: "saga_layer_failed", ProjectID: projectID, Stage: stage}
	entry.ErrorClass, entry.ErrorCause = observability.ClassifyError(err)
	line, _ := json.Marshal(entry)
	logger.Print(string(line))
}

// Workers are the sizes of the open Job pools of a build (specs/2026-11-07-desktop-app.md DA1.8): the common pool
// (imports, text indexes, materials), the chat's, the chapter cards' and the thread layer's. A build without a layer
// worker has no thread layer: no worker, no catch-up at start and no Job queued after cards (DA8.8).
type Workers struct {
	Common, Chat, Cards, Layer int
}

// valid requires a worker for imports and indexes, chat turns and cards: their Jobs are queued in every build.
func (workers Workers) valid() bool {
	return workers.Common > 0 && workers.Chat > 0 && workers.Cards > 0 && workers.Layer >= 0
}

type jobRunner interface {
	Run(context.Context) error
}

// startJobWorkerPool runs count workers over the same handlers; a worker claims only the Job
// kinds of its handlers, so pools of different kinds never wait for each other.
func startJobWorkerPool(
	ctx context.Context, store jobworker.Store, handlers []jobworker.Handler, count int, name string, logger *log.Logger,
) []<-chan struct{} {
	done := make([]<-chan struct{}, 0, count)
	workerLogger := log.New(logger.Writer(), "", 0)
	for range count {
		done = append(done, startJobWorkerRunner(ctx, jobworker.NewWorker(store, handlers, workerLogger), name, logger))
	}
	return done
}

func startJobWorkerRunner(ctx context.Context, runner jobRunner, name string, logger *log.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := runner.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Printf("%s worker stopped after an internal error", name)
		}
	}()
	return done
}
