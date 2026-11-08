package jobworker

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/observability"
)

// RunOnce выполняет не больше одного задания. Паника обработчика становится отказом задания
// worker.panic, а паника вне обработчика — ошибкой попытки; процесс продолжает работать.
func (w *Worker) RunOnce(ctx context.Context) (ran bool, runErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			runErr = observability.RecoveredPanic(recovered)
		}
	}()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	decision, err := w.store.ClaimNextQueuedJobForWorker(ctx, w.kinds)
	if errors.Is(err, jobs.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	claim := decision.Claim
	job, err := w.store.LoadJobForWorkerClaim(ctx, claim)
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			return true, jobs.ErrConflict
		}
		return true, err
	}
	handler, ok := w.handlers[job.Kind]
	if !ok || job.WorkerClaim != claim || job.ProjectID != claim.ProjectID || job.ID != claim.JobID {
		return true, jobs.ErrConflict
	}
	startedAt := time.Now()
	logJobStarted(w.logger, job, startedAt)
	var confirmed jobs.Job
	var requeueCause, handleErr error
	defer func() {
		logJobFinished(w.logger, job, startedAt, time.Now(), confirmed, runErr, requeueCause, handleErr)
	}()
	if decision.ExhaustedStatus != "" {
		confirmed, err = w.completeExhausted(ctx, job, handler, decision.ExhaustedStatus)
		return true, err
	}
	if job.Status == jobs.StatusCanceling {
		confirmed, err = w.markCanceledIfCanceling(ctx, job, handler)
		return true, err
	}
	if job.Status != jobs.StatusRunning {
		return true, jobs.ErrConflict
	}

	result, handled, heartbeat := w.handleWithHeartbeat(ctx, job, handler)
	handleErr, requeueCause = handled, result.RequeueCause
	if err := ctx.Err(); err != nil {
		if confirmed = w.releaseOnShutdown(ctx, job); confirmed.Status == jobs.StatusQueued {
			requeueCause = errWorkerShutdown
		}
		return true, err
	}
	var heartbeatPanic *observability.PanicError
	if errors.As(heartbeat.err, &heartbeatPanic) {
		// Паника heartbeat — сбой этой попытки, как паника обработчика: задание получает отказ.
		handleErr = heartbeat.err
	} else if heartbeat.err != nil {
		if errors.Is(heartbeat.err, jobs.ErrConflict) {
			current, loadErr := w.store.LoadJobForWorkerClaim(ctx, job.WorkerClaim)
			if loadErr == nil && current.Status == jobs.StatusCanceling {
				confirmed, err = w.markCanceledIfCanceling(ctx, current, handler)
				return true, err
			}
		}
		return true, heartbeat.err
	}
	if heartbeat.terminalJob.Status != "" {
		confirmed = heartbeat.terminalJob
		if handleErr != nil || result.Status != heartbeat.terminalJob.Status {
			return true, ErrTerminalProtocolInvariant
		}
		if err := validateCommittedHandlerResult(result); err != nil {
			return true, err
		}
		return true, nil
	}
	if heartbeat.requeuedJob.Status != "" {
		confirmed = heartbeat.requeuedJob
		if handleErr != nil || result.Status != jobs.StatusQueued {
			return true, ErrTerminalProtocolInvariant
		}
		return true, validateCommittedHandlerResult(result)
	}
	if handleErr != nil {
		current, loadErr := w.store.LoadJobForWorkerClaim(ctx, job.WorkerClaim)
		if loadErr == nil && isWorkerTerminalStatus(current.Status) {
			confirmed = current
			return true, nil
		}
		if errors.Is(handleErr, ErrRetryableHandler) {
			confirmed, err = w.requeue(ctx, job, handler)
			if err != nil {
				err = errors.Join(err, handleErr)
			}
			return true, err
		}
		confirmed, err = w.completeFailure(ctx, job, handler, &jobs.Error{
			Code: handlerFailureErrorCode(handleErr), Message: HandlerFailedMessage,
		})
		return true, err
	}
	confirmed, err = w.verifyCommittedResult(ctx, job.WorkerClaim, result)
	return true, err
}

// releaseOnShutdown возвращает в очередь задание, обработчик которого прервала штатная остановка
// процесса, без расхода попытки. Контекст worker уже отменён, поэтому запрос идёт с отдельным
// коротким сроком; завершённое обработчиком задание не меняется (точный захват `running`).
func (w *Worker) releaseOnShutdown(ctx context.Context, job jobs.Job) jobs.Job {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownReleaseTimeout)
	defer cancel()
	released, err := w.store.ReleaseWorkerClaim(releaseCtx, job.WorkerClaim)
	if err != nil {
		return jobs.Job{}
	}
	return released
}

func (w *Worker) requeue(ctx context.Context, job jobs.Job, handler Handler) (jobs.Job, error) {
	requeued, err := w.store.RequeueWorkerClaim(ctx, jobs.RequeueWorkerClaimStoreInput{
		Claim:              job.WorkerClaim,
		Kind:               job.Kind,
		ExpectedRetryCount: job.RetryCount,
	})
	if err == nil {
		return requeued, nil
	}
	if errors.Is(err, jobs.ErrRetryExhausted) {
		return w.completeFailure(ctx, job, handler, &jobs.Error{
			Code: jobs.RetryExhaustedCode, Message: jobs.RetryExhaustedMessage,
		})
	}
	if !errors.Is(err, jobs.ErrConflict) {
		return jobs.Job{}, err
	}
	current, loadErr := w.store.LoadJobForWorkerClaim(ctx, job.WorkerClaim)
	if loadErr != nil {
		return jobs.Job{}, jobs.ErrConflict
	}
	if current.Status != jobs.StatusCanceling {
		return jobs.Job{}, jobs.ErrConflict
	}
	return w.markCanceledIfCanceling(ctx, current, handler)
}

func (w *Worker) completeFailure(ctx context.Context, job jobs.Job, handler Handler, jobError *jobs.Error) (jobs.Job, error) {
	job.Error = jobError
	result, err := handler.HandleFailedResult(ctx, job)
	if err != nil {
		current, loadErr := w.store.LoadJobForWorkerClaim(ctx, job.WorkerClaim)
		if loadErr == nil && current.Status == jobs.StatusCanceling {
			return w.markCanceledIfCanceling(ctx, current, handler)
		}
		if loadErr == nil && isWorkerTerminalStatus(current.Status) {
			// Ход чата отменяет сама операция отмены: она записала итог под этим захватом раньше.
			return current, nil
		}
		return jobs.Job{}, err
	}
	return w.verifyCommittedResult(ctx, job.WorkerClaim, result)
}

type heartbeatObservation struct {
	terminalJob jobs.Job
	// requeuedJob is the Job its handler committed back to the queue under the next claim
	// incarnation while the heartbeat ran.
	requeuedJob jobs.Job
	err         error
}

func (w *Worker) handleWithHeartbeat(ctx context.Context, job jobs.Job, handler Handler) (HandlerResult, error, heartbeatObservation) {
	handlerCtx, cancelHandler := context.WithCancel(ctx)
	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	heartbeatDone := make(chan heartbeatObservation, 1)
	go w.heartbeatLoop(heartbeatCtx, ctx, cancelHandler, job, heartbeatDone)

	result, handleErr := w.handle(handlerCtx, job, handler)
	cancelHeartbeat()
	heartbeat := <-heartbeatDone
	cancelHandler()
	return result, handleErr, heartbeat
}

func (w *Worker) heartbeatLoop(ctx context.Context, parent context.Context, cancelHandler context.CancelFunc, job jobs.Job, done chan<- heartbeatObservation) {
	done <- w.heartbeat(ctx, parent, cancelHandler, job)
}

// heartbeat продлевает захват, пока работает обработчик. Его паника отменяет обработчик и
// возвращается как *observability.PanicError: goroutine не роняет процесс.
func (w *Worker) heartbeat(ctx context.Context, parent context.Context, cancelHandler context.CancelFunc, job jobs.Job) (observation heartbeatObservation) {
	defer func() {
		if recovered := recover(); recovered != nil {
			cancelHandler()
			observation = heartbeatObservation{err: observability.RecoveredPanic(recovered)}
		}
	}()
	claim := job.WorkerClaim
	interval := w.heartbeatInterval
	if interval <= 0 || interval > defaultHeartbeatInterval {
		interval = defaultHeartbeatInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return heartbeatObservation{}
		case <-ticker.C:
			if err := w.store.HeartbeatWorkerClaim(ctx, claim); err != nil {
				if ctx.Err() != nil {
					return heartbeatObservation{}
				}
				if errors.Is(err, jobs.ErrConflict) {
					current, loadErr := w.store.LoadJobForWorkerClaim(parent, claim)
					if loadErr == nil && isWorkerTerminalStatus(current.Status) {
						return heartbeatObservation{terminalJob: current}
					}
					// A handler that yields its Job (a cards portion) commits queued with the next
					// incarnation itself; that is its outcome, not a lost claim.
					next := claim
					next.ClaimIncarnation++
					if requeued, loadErr := w.store.LoadJobForWorkerClaim(parent, next); loadErr == nil && requeued.Status == jobs.StatusQueued {
						return heartbeatObservation{requeuedJob: requeued}
					}
					cancelHandler()
					return heartbeatObservation{err: jobs.ErrConflict}
				}
				cancelHandler()
				return heartbeatObservation{err: err}
			}
		}
	}
}

func (w *Worker) markCanceledIfCanceling(ctx context.Context, claimed jobs.Job, installed Handler) (jobs.Job, error) {
	result, err := installed.HandleCanceledResult(ctx, claimed)
	if err != nil {
		current, loadErr := w.store.LoadJobForWorkerClaim(ctx, claimed.WorkerClaim)
		if loadErr == nil && current.Status == jobs.StatusCanceled {
			// Отмену под этим захватом уже записала сама операция отмены.
			return current, nil
		}
		return jobs.Job{}, err
	}
	if result.Status != jobs.StatusCanceled {
		return jobs.Job{}, ErrTerminalProtocolInvariant
	}
	return w.verifyCommittedResult(ctx, claimed.WorkerClaim, result)
}

func (w *Worker) verifyCommittedResult(ctx context.Context, claim jobs.WorkerClaim, result HandlerResult) (jobs.Job, error) {
	if err := validateCommittedHandlerResult(result); err != nil {
		return jobs.Job{}, err
	}
	committedClaim := claim
	if result.Status == jobs.StatusQueued {
		committedClaim.ClaimIncarnation++
	}
	current, err := w.store.LoadJobForWorkerClaim(ctx, committedClaim)
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) || errors.Is(err, jobs.ErrConflict) {
			return jobs.Job{}, jobs.ErrConflict
		}
		return jobs.Job{}, err
	}
	if result.Status == jobs.StatusQueued && current.Status == jobs.StatusCanceled {
		return current, nil
	}
	if current.Status != result.Status {
		return jobs.Job{}, ErrTerminalProtocolInvariant
	}
	return current, nil
}

func validateCommittedHandlerResult(result HandlerResult) error {
	if result.Status != jobs.StatusQueued && !isWorkerTerminalStatus(result.Status) {
		return ErrTerminalProtocolInvariant
	}
	if result.RequeueCause != nil && result.Status != jobs.StatusQueued {
		return ErrTerminalProtocolInvariant
	}
	return nil
}

func isWorkerTerminalStatus(status jobs.Status) bool {
	switch status {
	case jobs.StatusSucceeded, jobs.StatusFailed, jobs.StatusCanceled, jobs.StatusExpired:
		return true
	default:
		return false
	}
}

// handle — граница кода обработчика: его паника становится ошибкой *observability.PanicError со
// стеком, а задание получает отказ worker.panic.
func (w *Worker) handle(ctx context.Context, job jobs.Job, installed Handler) (result HandlerResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result, err = HandlerResult{}, observability.RecoveredPanic(recovered)
		}
	}()
	return installed.HandleResult(ctx, job)
}

func (w *Worker) completeExhausted(ctx context.Context, job jobs.Job, handler Handler, status jobs.Status) (jobs.Job, error) {
	switch status {
	case jobs.StatusCanceled:
		return w.markCanceledIfCanceling(ctx, job, handler)
	case jobs.StatusFailed:
		return w.completeFailure(ctx, job, handler, &jobs.Error{
			Code: jobs.RetryExhaustedCode, Message: jobs.RetryExhaustedMessage,
		})
	default:
		return jobs.Job{}, jobs.ErrConflict
	}
}

func (w *Worker) Run(ctx context.Context) error {
	errorDelay := w.errorRetryDelay
	consecutiveErrors := 0
	for {
		ran, err := w.RunOnce(ctx)
		if err != nil {
			if ctx.Err() == nil {
				consecutiveErrors++
				logWorkerLoopError(w.logger, w.kinds, err, consecutiveErrors)
			}
			if isContextError(ctx, err) {
				return err
			}
			if waitErr := waitForWorkerDelay(ctx, errorDelay); waitErr != nil {
				return waitErr
			}
			errorDelay = nextWorkerBackoff(errorDelay, w.maxErrorRetryDelay)
			continue
		}
		errorDelay = w.errorRetryDelay
		consecutiveErrors = 0
		if ran {
			continue
		}
		if err := waitForWorkerDelay(ctx, w.idleDelay); err != nil {
			return err
		}
	}
}

func isContextError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func waitForWorkerDelay(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		delay = time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextWorkerBackoff(current, max time.Duration) time.Duration {
	if current <= 0 {
		current = time.Millisecond
	}
	if max <= 0 {
		return current
	}
	next := current * 2
	if next < current || next > max {
		return max
	}
	return next
}
