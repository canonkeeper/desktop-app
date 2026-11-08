package deletion

import (
	"context"

	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
)

type NativeRootLookup interface {
	ListDeletionRoots(context.Context, deletiondomain.Tombstone) ([]deletiondomain.RootCandidate, error)
	ListPermissionLossRoots(context.Context, deletiondomain.PermissionLossScope) ([]deletiondomain.RootCandidate, error)
	ReleaseDeletedRoot(context.Context, deletiondomain.RootCandidate, string) error
}

type NativeTreeDeleter interface {
	DeleteTree(context.Context, deletiondomain.RootSelector) (string, error)
}

type NativeTreeDeleterFunc func(context.Context, deletiondomain.RootSelector) (string, error)

func (deleteTree NativeTreeDeleterFunc) DeleteTree(ctx context.Context, selector deletiondomain.RootSelector) (string, error) {
	return deleteTree(ctx, selector)
}

type NativeTarget struct {
	roots   NativeRootLookup
	deleter NativeTreeDeleter
}

func NewNativeTarget(roots NativeRootLookup, deleter NativeTreeDeleter) (*NativeTarget, error) {
	if roots == nil || deleter == nil {
		return nil, ErrInvalidCoordinator
	}
	return &NativeTarget{roots: roots, deleter: deleter}, nil
}

func (target *NativeTarget) Apply(ctx context.Context, request deletiondomain.TargetRequest) (deletiondomain.TargetResult, error) {
	if target == nil || ctx == nil || request.ValidateShape() != nil || request.Claim.Target != governance.TargetRuntime {
		return deletiondomain.TargetResult{}, deletiondomain.ErrInvalidTargetRequest
	}
	var candidates []deletiondomain.RootCandidate
	var err error
	if actorUserID := request.Tombstone.Identity.PermissionLossActorUserID; actorUserID != "" {
		candidates, err = target.roots.ListPermissionLossRoots(ctx, deletiondomain.PermissionLossScope{
			ProjectID: request.Claim.ProjectID, ActorUserID: actorUserID,
		})
	} else {
		candidates, err = target.roots.ListDeletionRoots(ctx, request.Tombstone)
	}
	if err != nil {
		return deletiondomain.TargetResult{}, err
	}
	deleted, err := target.deleteCandidates(ctx, candidates)
	if err != nil {
		return deletiondomain.TargetResult{}, err
	}
	if deleted {
		return deletiondomain.TargetResult{
			Disposition: deletiondomain.DispositionPurged, HandlerVersion: governance.HandlerRuntimeDirectSDKV1,
			Reason: governance.ReasonPurged,
		}, nil
	}
	return deletiondomain.TargetResult{
		Disposition: deletiondomain.DispositionNoData, HandlerVersion: governance.HandlerRuntimeDirectSDKV1,
		Reason: governance.ReasonNoData,
	}, nil
}

func (target *NativeTarget) deleteCandidates(ctx context.Context, candidates []deletiondomain.RootCandidate) (bool, error) {
	deleted := false
	for _, candidate := range candidates {
		wasDeleted, err := target.deleteCandidate(ctx, candidate)
		if err != nil {
			return false, err
		}
		deleted = deleted || wasDeleted
	}
	return deleted, nil
}

func (target *NativeTarget) deleteCandidate(ctx context.Context, candidate deletiondomain.RootCandidate) (bool, error) {
	if candidate.Validate() != nil {
		return false, deletiondomain.ErrInvalidTargetRequest
	}
	rootID, err := target.deleter.DeleteTree(ctx, candidate.Selector)
	if err != nil {
		return false, err
	}
	confirmedRootID := rootID
	if candidate.Selector.ThreadID != "" {
		if rootID != "" && rootID != candidate.Selector.ThreadID {
			return false, deletiondomain.ErrRootBindingConflict
		}
		confirmedRootID = candidate.Selector.ThreadID
	}
	if err := target.roots.ReleaseDeletedRoot(ctx, candidate, confirmedRootID); err != nil {
		return false, err
	}
	return rootID != "", nil
}

var _ TargetHandler = (*NativeTarget)(nil)
