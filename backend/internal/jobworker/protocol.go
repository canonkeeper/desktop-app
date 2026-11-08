package jobworker

import (
	"context"
	"errors"
	"log"
	"slices"
	"time"

	"canonkeeper/backend/internal/jobs"
)

const (
	defaultIdleDelay          = time.Second
	defaultErrorRetryDelay    = 250 * time.Millisecond
	defaultMaxErrorRetryDelay = 30 * time.Second
	defaultHeartbeatInterval  = 15 * time.Second
	// shutdownReleaseTimeout ограничивает возврат задания в очередь при остановке процесса.
	shutdownReleaseTimeout = 5 * time.Second
	HandlerFailedCode      = "worker.handler_failed"
	HandlerFailedMessage   = "job handler failed"
	// PanicCode — отказ задания, обработчик которого запаниковал; процесс продолжает работать.
	PanicCode = "worker.panic"
	// ShutdownCode — задание вернулось в очередь при штатной остановке процесса.
	ShutdownCode              = "worker.shutdown"
	terminalProtocolInvariant = "jobworker: terminal protocol invariant violated"
)

var (
	ErrTerminalProtocolInvariant = errors.New(terminalProtocolInvariant)
	ErrRetryableHandler          = errors.New("jobworker: retryable handler failure")
	errWorkerShutdown            = errors.New("jobworker: process shutdown")
)

type Handler interface {
	Kind() jobs.Kind
	HandleResult(context.Context, jobs.Job) (HandlerResult, error)
	HandleCanceledResult(context.Context, jobs.Job) (HandlerResult, error)
	HandleFailedResult(context.Context, jobs.Job) (HandlerResult, error)
}

type HandlerResult struct {
	Status       jobs.Status
	RequeueCause error
}

type Store interface {
	ClaimNextQueuedJobForWorker(context.Context, []jobs.Kind) (jobs.WorkerClaimDecision, error)
	LoadJobForWorkerClaim(context.Context, jobs.WorkerClaim) (jobs.Job, error)
	HeartbeatWorkerClaim(context.Context, jobs.WorkerClaim) error
	RequeueWorkerClaim(context.Context, jobs.RequeueWorkerClaimStoreInput) (jobs.Job, error)
	ReleaseWorkerClaim(context.Context, jobs.WorkerClaim) (jobs.Job, error)
}

type Worker struct {
	store              Store
	handlers           map[jobs.Kind]Handler
	kinds              []jobs.Kind
	logger             *log.Logger
	idleDelay          time.Duration
	errorRetryDelay    time.Duration
	maxErrorRetryDelay time.Duration
	heartbeatInterval  time.Duration
}

func NewWorker(store Store, handlers []Handler, logger *log.Logger) *Worker {
	if store == nil {
		panic("jobworker: Store is required")
	}
	if logger == nil {
		panic("jobworker: Logger is required")
	}
	if len(handlers) == 0 {
		panic("jobworker: at least one Handler is required")
	}
	handlerMap := make(map[jobs.Kind]Handler, len(handlers))
	kinds := make([]jobs.Kind, 0, len(handlers))
	for _, handler := range handlers {
		if handler == nil || !jobs.IsWorkerKind(handler.Kind()) {
			panic("jobworker: unsupported Handler kind")
		}
		if _, exists := handlerMap[handler.Kind()]; exists {
			panic("jobworker: duplicate Handler kind")
		}
		handlerMap[handler.Kind()] = handler
		kinds = append(kinds, handler.Kind())
	}
	slices.Sort(kinds)
	return &Worker{
		store:              store,
		handlers:           handlerMap,
		kinds:              kinds,
		logger:             logger,
		idleDelay:          defaultIdleDelay,
		errorRetryDelay:    defaultErrorRetryDelay,
		maxErrorRetryDelay: defaultMaxErrorRetryDelay,
		heartbeatInterval:  defaultHeartbeatInterval,
	}
}
