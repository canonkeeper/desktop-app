package deletion

import (
	"errors"
	"time"

	"canonkeeper/backend/internal/governance"
)

type TargetReceipt struct {
	Target               governance.DeletionTarget
	TombstoneDigest      governance.Digest
	SelectedVectorDigest governance.Digest
	HandlerVersion       governance.DeletionHandlerVersion
	Disposition          governance.ReceiptDisposition
	CompletedAt          time.Time
	Digest               governance.Digest
}

func NewTargetReceipt(request TargetRequest, result TargetResult, completedAt time.Time) (TargetReceipt, error) {
	if request.ValidateShape() != nil || request.ValidateResult(result) != nil {
		return TargetReceipt{}, ErrInvalidTargetRequest
	}
	disposition, err := receiptDisposition(result.Disposition)
	if err != nil {
		return TargetReceipt{}, err
	}
	tombstoneDigest, vectorDigest, err := request.Tombstone.Digests()
	if err != nil {
		return TargetReceipt{}, err
	}
	digest, err := governance.DeletionTargetReceiptDigest(governance.DeletionTargetReceiptInput{
		Target: request.Claim.Target, TombstoneDigest: [32]byte(tombstoneDigest),
		SelectedVectorDigest: [32]byte(vectorDigest), HandlerVersion: result.HandlerVersion,
		Disposition: disposition, CompletedAt: completedAt,
		PermissionLoss: request.Tombstone.Identity.PermissionLossActorUserID != "",
	})
	if err != nil {
		return TargetReceipt{}, err
	}
	return TargetReceipt{
		Target: request.Claim.Target, TombstoneDigest: tombstoneDigest, SelectedVectorDigest: vectorDigest,
		HandlerVersion: result.HandlerVersion, Disposition: disposition, CompletedAt: completedAt,
		Digest: governance.Digest(digest),
	}, nil
}

func (receipt TargetReceipt) ValidateFor(request TargetRequest, result TargetResult) error {
	want, err := NewTargetReceipt(request, result, receipt.CompletedAt)
	if err != nil {
		return err
	}
	if receipt != want {
		return errors.New("deletion: target receipt does not match request")
	}
	return nil
}

func receiptDisposition(disposition Disposition) (governance.ReceiptDisposition, error) {
	switch disposition {
	case DispositionPurged:
		return governance.ReceiptPurged, nil
	case DispositionNoData:
		return governance.ReceiptNoData, nil
	default:
		return "", ErrInvalidTargetRequest
	}
}
