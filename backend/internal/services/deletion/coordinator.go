package deletion

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"time"

	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
)

const (
	DeletionTargetLeaseTTL          = deletiondomain.DeletionTargetLeaseTTL
	DeletionTargetHeartbeatInterval = deletiondomain.DeletionTargetHeartbeatInterval
)

var ErrInvalidCoordinator = errors.New("deletion: invalid coordinator configuration")

type TargetHandler interface {
	Apply(context.Context, deletiondomain.TargetRequest) (deletiondomain.TargetResult, error)
}

type TargetHandlers map[governance.DeletionTarget]TargetHandler

type DeletionTargetClaimer interface {
	ClaimNextDeletionTargetTaskForWorker(context.Context, governance.Digest, bool) (deletiondomain.ClaimedTarget, error)
}

type DeletionTargetStore interface {
	LoadDeletionTargetRequestForClaim(context.Context, deletiondomain.TargetClaim) (deletiondomain.TargetRequest, error)
	HeartbeatDeletionTargetClaim(context.Context, deletiondomain.TargetClaim) (time.Time, error)
	CompleteDeletionTargetForClaim(context.Context, deletiondomain.TargetClaim, deletiondomain.TargetResult) (deletiondomain.TargetReceipt, error)
	FailDeletionTargetForClaim(context.Context, deletiondomain.TargetClaim, deletiondomain.TargetFailure) error
	FindCommittedDeletionTargetReceipt(context.Context, deletiondomain.TargetClaim) (deletiondomain.TargetReceipt, error)
}

type CoordinatorClock interface{ Now() time.Time }
type RetryDelay func(uint32, governance.Digest) time.Duration

type CoordinatorConfig struct {
	RuntimeTargetEnabled bool
	Claims               DeletionTargetClaimer
	Store                DeletionTargetStore
	Handlers             TargetHandlers
	Clock                CoordinatorClock
	Entropy              io.Reader
	RetryDelay           RetryDelay
	HeartbeatInterval    time.Duration
}

type Coordinator struct {
	runtimeTargetEnabled bool
	claims               DeletionTargetClaimer
	store                DeletionTargetStore
	handlers             TargetHandlers
	clock                CoordinatorClock
	entropy              io.Reader
	retryDelay           RetryDelay
	heartbeatInterval    time.Duration
}

func NewCoordinator(config CoordinatorConfig) (*Coordinator, error) {
	if config.Claims == nil || config.Store == nil {
		return nil, ErrInvalidCoordinator
	}
	if config.Clock == nil {
		config.Clock = systemCoordinatorClock{}
	}
	if config.Entropy == nil {
		config.Entropy = rand.Reader
	}
	if config.RetryDelay == nil {
		config.RetryDelay = defaultRetryDelay
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = DeletionTargetHeartbeatInterval
	}
	if config.HeartbeatInterval < 0 {
		return nil, ErrInvalidCoordinator
	}
	handlers := make(TargetHandlers, len(config.Handlers))
	for target, handler := range config.Handlers {
		if !deletiondomain.KnownTarget(target) || handler == nil {
			return nil, ErrInvalidCoordinator
		}
		handlers[target] = handler
	}
	return &Coordinator{
		runtimeTargetEnabled: config.RuntimeTargetEnabled,
		claims:               config.Claims, store: config.Store, handlers: handlers,
		clock: config.Clock, entropy: config.Entropy, retryDelay: config.RetryDelay,
		heartbeatInterval: config.HeartbeatInterval,
	}, nil
}

type RunOutcome string

const (
	RunNoWork         RunOutcome = "no_work"
	RunCompleted      RunOutcome = "completed"
	RunRetryScheduled RunOutcome = "retry_scheduled"
)

type RunFailureCode string

const (
	RunFailureInvalidCoordinator RunFailureCode = "invalid_coordinator"
	RunFailureClaim              RunFailureCode = "claim_failed"
	RunFailureInvalidClaim       RunFailureCode = "invalid_claim"
	RunFailureLoadRequest        RunFailureCode = "load_request_failed"
	RunFailureInvalidRequest     RunFailureCode = "invalid_request"
	RunFailureHandlerExecution   RunFailureCode = "handler_execution_failed"
	RunFailureRetryPersist       RunFailureCode = "retry_persist_failed"
	RunFailureCompletion         RunFailureCode = "completion_failed"
	RunFailureReceiptUnavailable RunFailureCode = "receipt_unavailable"
	RunFailureInvalidReceipt     RunFailureCode = "invalid_receipt"
)

type RunDiagnostic struct {
	Outcome     RunOutcome
	FailureCode RunFailureCode
	TaskID      string
	ProjectID   string
	SourceKind  governance.DeletionSelectionKind
	SourceID    string
	Target      governance.DeletionTarget
	Attempt     uint32
	Reason      governance.ReasonCode
	RetryAfter  time.Duration
}

func (coordinator *Coordinator) RunOnce(ctx context.Context) (RunOutcome, error) {
	diagnostic, err := coordinator.RunOnceDiagnostic(ctx)
	return diagnostic.Outcome, err
}

func (coordinator *Coordinator) RunOnceDiagnostic(ctx context.Context) (RunDiagnostic, error) {
	if coordinator == nil {
		return RunDiagnostic{FailureCode: RunFailureInvalidCoordinator}, ErrInvalidCoordinator
	}
	now := coordinator.clock.Now()
	ownerDigest, err := newLeaseOwnerDigest(coordinator.entropy)
	if err != nil {
		return RunDiagnostic{FailureCode: RunFailureInvalidCoordinator}, err
	}
	claimed, err := coordinator.claims.ClaimNextDeletionTargetTaskForWorker(ctx, ownerDigest, coordinator.runtimeTargetEnabled)
	if errors.Is(err, deletiondomain.ErrNoDeletionTarget) {
		return RunDiagnostic{Outcome: RunNoWork}, nil
	}
	if err != nil {
		return RunDiagnostic{FailureCode: RunFailureClaim}, err
	}
	if claimed.Validate(now) != nil || claimed.Claim.LeaseOwnerDigest != ownerDigest {
		return RunDiagnostic{FailureCode: RunFailureInvalidClaim}, deletiondomain.ErrInvalidTargetRequest
	}
	diagnostic := runDiagnosticForClaim(claimed.Claim, claimed.Attempt)
	request, err := coordinator.store.LoadDeletionTargetRequestForClaim(ctx, claimed.Claim)
	if err != nil {
		diagnostic.FailureCode = RunFailureLoadRequest
		return diagnostic, err
	}
	if request.Validate(now) != nil || request.Claim != claimed.Claim || request.Attempt != claimed.Attempt {
		diagnostic.FailureCode = RunFailureInvalidRequest
		return diagnostic, deletiondomain.ErrInvalidTargetRequest
	}
	diagnostic, err = runDiagnosticForRequest(request)
	if err != nil {
		diagnostic.FailureCode = RunFailureInvalidRequest
		return diagnostic, err
	}
	handler, ok := coordinator.handlers[request.Claim.Target]
	if !ok {
		return coordinator.scheduleRetry(ctx, request, governance.ReasonTargetUnavailable)
	}
	result, handlerErr, runErr := coordinator.runHandler(ctx, handler, request)
	if runErr != nil {
		diagnostic.FailureCode = RunFailureHandlerExecution
		return diagnostic, runErr
	}
	if handlerErr != nil || request.ValidateResult(result) != nil {
		return coordinator.scheduleRetry(ctx, request, governance.ReasonValidationFailed)
	}
	if result.Disposition == deletiondomain.DispositionRetry {
		return coordinator.scheduleRetry(ctx, request, result.Reason)
	}
	receipt, err := coordinator.store.CompleteDeletionTargetForClaim(ctx, request.Claim, result)
	if err != nil {
		receipt, lookupErr := coordinator.store.FindCommittedDeletionTargetReceipt(ctx, request.Claim)
		if lookupErr != nil {
			if errors.Is(lookupErr, deletiondomain.ErrDeletionReceiptUnavailable) {
				diagnostic.FailureCode = RunFailureReceiptUnavailable
				return diagnostic, deletiondomain.ErrDeletionReceiptUnavailable
			}
			diagnostic.FailureCode = RunFailureCompletion
			return diagnostic, err
		}
		if receipt.ValidateFor(request, result) != nil {
			diagnostic.FailureCode = RunFailureInvalidReceipt
			return diagnostic, deletiondomain.ErrInvalidTargetRequest
		}
		diagnostic.Outcome = RunCompleted
		diagnostic.Reason = result.Reason
		return diagnostic, nil
	}
	if receipt.ValidateFor(request, result) != nil {
		diagnostic.FailureCode = RunFailureInvalidReceipt
		return diagnostic, deletiondomain.ErrInvalidTargetRequest
	}
	diagnostic.Outcome = RunCompleted
	diagnostic.Reason = result.Reason
	return diagnostic, nil
}

func runDiagnosticForClaim(claim deletiondomain.TargetClaim, attempt uint32) RunDiagnostic {
	return RunDiagnostic{
		TaskID: claim.TombstoneID, ProjectID: claim.ProjectID,
		Target: claim.Target, Attempt: attempt,
	}
}

func runDiagnosticForRequest(request deletiondomain.TargetRequest) (RunDiagnostic, error) {
	diagnostic := runDiagnosticForClaim(request.Claim, request.Attempt)
	fields, err := request.Tombstone.Identity.SelectedVector.Fields()
	if err != nil {
		return diagnostic, deletiondomain.ErrInvalidTargetRequest
	}
	diagnostic.SourceKind = fields.Kind
	switch fields.Kind {
	case governance.DeletionSelectionProject:
		diagnostic.SourceID = fields.ProjectID
	case governance.DeletionSelectionBook:
		diagnostic.SourceID = fields.Source.BookID
	case governance.DeletionSelectionChapter:
		diagnostic.SourceID = fields.Source.ChapterID
	case governance.DeletionSelectionChat:
		diagnostic.SourceID = fields.ChatID
	default:
		return diagnostic, deletiondomain.ErrInvalidTargetRequest
	}
	return diagnostic, nil
}

func (coordinator *Coordinator) runHandler(ctx context.Context, handler TargetHandler, request deletiondomain.TargetRequest) (deletiondomain.TargetResult, error, error) {
	handlerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type response struct {
		result deletiondomain.TargetResult
		err    error
	}
	responses := make(chan response, 1)
	go func() {
		result, err := handler.Apply(handlerCtx, request)
		responses <- response{result: result, err: err}
	}()
	ticker := time.NewTicker(coordinator.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case response := <-responses:
			return response.result, response.err, nil
		case <-ticker.C:
			expiresAt, err := coordinator.store.HeartbeatDeletionTargetClaim(handlerCtx, request.Claim)
			if err != nil || !expiresAt.After(coordinator.clock.Now()) {
				cancel()
				<-responses
				if err != nil {
					return deletiondomain.TargetResult{}, nil, err
				}
				return deletiondomain.TargetResult{}, nil, deletiondomain.ErrLostTargetClaim
			}
		case <-ctx.Done():
			cancel()
			<-responses
			return deletiondomain.TargetResult{}, nil, ctx.Err()
		}
	}
}

func (coordinator *Coordinator) scheduleRetry(ctx context.Context, request deletiondomain.TargetRequest, reason governance.ReasonCode) (RunDiagnostic, error) {
	diagnostic, err := runDiagnosticForRequest(request)
	if err != nil {
		diagnostic.FailureCode = RunFailureInvalidRequest
		return diagnostic, err
	}
	delay := time.Second
	if reason != governance.ReasonStaleSource {
		delay = coordinator.retryDelay(request.Attempt, request.Claim.LeaseOwnerDigest)
	}
	if delay <= 0 || delay > deletiondomain.DeletionTargetMaxRetryDelay {
		diagnostic.FailureCode = RunFailureInvalidCoordinator
		return diagnostic, ErrInvalidCoordinator
	}
	if remainder := delay % time.Millisecond; remainder != 0 {
		delay += time.Millisecond - remainder
	}
	failure := deletiondomain.TargetFailure{Reason: reason, RetryAfter: delay}
	if failure.Validate() != nil {
		diagnostic.FailureCode = RunFailureInvalidRequest
		return diagnostic, deletiondomain.ErrInvalidTargetRequest
	}
	diagnostic.Reason = reason
	diagnostic.RetryAfter = delay
	if err := coordinator.store.FailDeletionTargetForClaim(ctx, request.Claim, failure); err != nil {
		diagnostic.FailureCode = RunFailureRetryPersist
		return diagnostic, err
	}
	diagnostic.Outcome = RunRetryScheduled
	return diagnostic, nil
}

func newLeaseOwnerDigest(entropy io.Reader) (governance.Digest, error) {
	var incarnation [16]byte
	if _, err := io.ReadFull(entropy, incarnation[:]); err != nil {
		return governance.Digest{}, err
	}
	digest := sha256.Sum256(incarnation[:])
	if digest == ([32]byte{}) {
		return governance.Digest{}, ErrInvalidCoordinator
	}
	return governance.Digest(digest), nil
}

func defaultRetryDelay(attempt uint32, owner governance.Digest) time.Duration {
	if attempt == 0 {
		attempt = 1
	}
	exponent := attempt - 1
	if exponent > 9 {
		exponent = 9
	}
	base := time.Second * time.Duration(uint64(1)<<exponent)
	jitterWindow := base / 5
	if jitterWindow == 0 {
		return base
	}
	return base + time.Duration(binary.BigEndian.Uint64(owner[:8])%uint64(jitterWindow+1))
}

type systemCoordinatorClock struct{}

func (systemCoordinatorClock) Now() time.Time { return time.Now().UTC() }
