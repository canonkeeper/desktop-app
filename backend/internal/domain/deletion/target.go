package deletion

import (
	"errors"
	"time"

	agentdomain "canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/governance"
)

const (
	DeletionTargetLeaseTTL          = 5 * time.Minute
	DeletionTargetHeartbeatInterval = time.Minute
	DeletionTargetMaxRetryDelay     = 15 * time.Minute
)

var (
	ErrNoDeletionTarget           = errors.New("deletion: no target work available")
	ErrLostTargetClaim            = errors.New("deletion: target claim lost")
	ErrDeletionReceiptUnavailable = errors.New("deletion: receipt unavailable")
	ErrInvalidTargetRequest       = errors.New("deletion: invalid target request")
	ErrRootBindingConflict        = errors.New("deletion: root binding conflict")
)

type RootSelector struct {
	Flow          agentdomain.Flow
	CorrelationID string
	ThreadID      string
}

func (selector RootSelector) Validate() error {
	if !selector.Flow.Valid() || selector.CorrelationID == "" && selector.ThreadID == "" {
		return ErrInvalidTargetRequest
	}
	return nil
}

// RootCandidate is the native root thread of a chat session; its correlation is the chat ID.
type RootCandidate struct {
	ProjectID string
	Selector  RootSelector
}

func (candidate RootCandidate) Validate() error {
	if !canonicalUUID(candidate.ProjectID) || candidate.Selector.Validate() != nil || candidate.Selector.CorrelationID == "" {
		return ErrInvalidTargetRequest
	}
	return nil
}

type TargetState string

const (
	TargetPending    TargetState = "pending"
	TargetProcessing TargetState = "processing"
	TargetSucceeded  TargetState = "succeeded"
	TargetNoData     TargetState = "no_data"
	TargetFailed     TargetState = "failed"
)

type TombstoneStatus string

const (
	TombstonePending    TombstoneStatus = "pending"
	TombstoneProcessing TombstoneStatus = "processing"
	TombstonePurged     TombstoneStatus = "purged"
	TombstoneFailed     TombstoneStatus = "failed"
)

type TargetClaim struct {
	ProjectID           string
	TombstoneID         string
	Target              governance.DeletionTarget
	ClaimIncarnation    uint64
	LeaseOwnerDigest    governance.Digest
	ObservedLeaseExpiry time.Time
}

func (claim TargetClaim) ValidateIdentity() error { return claim.validateShape() }

func (claim TargetClaim) validateShape() error {
	if !canonicalUUID(claim.ProjectID) || !canonicalUUID(claim.TombstoneID) ||
		!KnownTarget(claim.Target) || claim.ClaimIncarnation == 0 ||
		claim.LeaseOwnerDigest == (governance.Digest{}) || claim.ObservedLeaseExpiry.IsZero() {
		return ErrInvalidTargetRequest
	}
	return nil
}

func (claim TargetClaim) Validate(now time.Time) error {
	if claim.validateShape() != nil || now.IsZero() || !claim.ObservedLeaseExpiry.After(now) {
		return ErrInvalidTargetRequest
	}
	return nil
}

type ClaimedTarget struct {
	Claim   TargetClaim
	Attempt uint32
	State   TargetState
}

func (claimed ClaimedTarget) Validate(now time.Time) error {
	if claimed.State != TargetProcessing || claimed.Attempt == 0 || claimed.Claim.Validate(now) != nil {
		return ErrInvalidTargetRequest
	}
	return nil
}

type Tombstone struct {
	ProjectID string
	Identity  governance.DeletionTombstoneInput
	Status    TombstoneStatus
}

func (tombstone Tombstone) Validate() error {
	if !canonicalUUID(tombstone.ProjectID) ||
		(tombstone.Status != TombstonePending && tombstone.Status != TombstoneProcessing &&
			tombstone.Status != TombstonePurged && tombstone.Status != TombstoneFailed) {
		return ErrInvalidTargetRequest
	}
	if _, err := governance.DeletionTombstoneDigest(tombstone.Identity); err != nil {
		return ErrInvalidTargetRequest
	}
	fields, err := tombstone.Identity.SelectedVector.Fields()
	if err != nil || fields.ProjectID != tombstone.ProjectID {
		return ErrInvalidTargetRequest
	}
	return nil
}

func (tombstone Tombstone) Digests() (governance.Digest, governance.Digest, error) {
	if err := tombstone.Validate(); err != nil {
		return governance.Digest{}, governance.Digest{}, err
	}
	tombstoneDigest, err := governance.DeletionTombstoneDigest(tombstone.Identity)
	if err != nil {
		return governance.Digest{}, governance.Digest{}, ErrInvalidTargetRequest
	}
	vectorDigest, err := governance.SelectedVectorDigest(tombstone.Identity.SelectedVector)
	if err != nil {
		return governance.Digest{}, governance.Digest{}, ErrInvalidTargetRequest
	}
	return governance.Digest(tombstoneDigest), governance.Digest(vectorDigest), nil
}

type TargetRequest struct {
	Claim     TargetClaim
	Tombstone Tombstone
	Attempt   uint32
}

func (request TargetRequest) ValidateShape() error {
	if request.Claim.validateShape() != nil || request.Tombstone.Validate() != nil || request.Attempt == 0 ||
		request.Claim.ProjectID != request.Tombstone.ProjectID ||
		request.Claim.TombstoneID != request.Tombstone.Identity.TombstoneID {
		return ErrInvalidTargetRequest
	}
	return nil
}

func (request TargetRequest) Validate(now time.Time) error {
	if request.ValidateShape() != nil || request.Tombstone.Status != TombstoneProcessing || request.Claim.Validate(now) != nil {
		return ErrInvalidTargetRequest
	}
	return nil
}

func (request TargetRequest) ValidateResult(result TargetResult) error {
	return result.validate(request.Claim.Target, request.Tombstone.Identity.PermissionLossActorUserID != "")
}

type Disposition string

const (
	DispositionPurged Disposition = "purged"
	DispositionNoData Disposition = "no_data"
	DispositionRetry  Disposition = "retry"
)

type TargetResult struct {
	Disposition    Disposition
	HandlerVersion governance.DeletionHandlerVersion
	Reason         governance.ReasonCode
}

func (result TargetResult) validate(target governance.DeletionTarget, permissionLoss bool) error {
	if !KnownTarget(target) {
		return ErrInvalidTargetRequest
	}
	if result.Disposition == DispositionRetry {
		if result.HandlerVersion != "" || !RetryReason(result.Reason) {
			return ErrInvalidTargetRequest
		}
		return nil
	}
	var disposition governance.ReceiptDisposition
	switch result.Disposition {
	case DispositionPurged:
		disposition = governance.ReceiptPurged
		if result.Reason != governance.ReasonPurged {
			return ErrInvalidTargetRequest
		}
	case DispositionNoData:
		disposition = governance.ReceiptNoData
		if result.Reason != governance.ReasonNoData {
			return ErrInvalidTargetRequest
		}
	default:
		return ErrInvalidTargetRequest
	}
	if result.HandlerVersion == "" {
		return ErrInvalidTargetRequest
	}
	var tombstoneDigest, vectorDigest [32]byte
	tombstoneDigest[0], vectorDigest[0] = 1, 2
	if _, err := governance.DeletionTargetReceiptDigest(governance.DeletionTargetReceiptInput{
		Target: target, TombstoneDigest: tombstoneDigest, SelectedVectorDigest: vectorDigest,
		HandlerVersion: result.HandlerVersion, Disposition: disposition, CompletedAt: time.Unix(1, 0).UTC(),
		PermissionLoss: permissionLoss,
	}); err != nil {
		return ErrInvalidTargetRequest
	}
	return nil
}

type TargetFailure struct {
	Reason     governance.ReasonCode
	RetryAfter time.Duration
}

func (failure TargetFailure) Validate() error {
	if !RetryReason(failure.Reason) || failure.RetryAfter < time.Millisecond ||
		failure.RetryAfter > DeletionTargetMaxRetryDelay || failure.RetryAfter%time.Millisecond != 0 {
		return ErrInvalidTargetRequest
	}
	return nil
}

func KnownTarget(target governance.DeletionTarget) bool {
	switch target {
	case governance.TargetRuntime, governance.TargetFilesystem, governance.TargetPostgreSQL:
		return true
	default:
		return false
	}
}

func RetryReason(reason governance.ReasonCode) bool {
	switch reason {
	case governance.ReasonTargetUnavailable, governance.ReasonStaleSource,
		governance.ReasonExpiredLease, governance.ReasonValidationFailed:
		return true
	default:
		return false
	}
}
