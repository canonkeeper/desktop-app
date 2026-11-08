package governance

import (
	"errors"
	"time"
)

type DeletionTarget string

const (
	TargetRuntime    DeletionTarget = "runtime"
	TargetFilesystem DeletionTarget = "filesystem"
	TargetPostgreSQL DeletionTarget = "postgresql"
)

func (target DeletionTarget) tag() (byte, bool) {
	switch target {
	case TargetRuntime:
		return 1, true
	case TargetFilesystem:
		return 2, true
	case TargetPostgreSQL:
		return 4, true
	default:
		return 0, false
	}
}

type ReceiptDisposition string

const (
	ReceiptPurged ReceiptDisposition = "purged"
	ReceiptNoData ReceiptDisposition = "no_data"
)

func (disposition ReceiptDisposition) tag() (byte, bool) {
	switch disposition {
	case ReceiptPurged:
		return 1, true
	case ReceiptNoData:
		return 2, true
	default:
		return 0, false
	}
}

type DeletionHandlerVersion string

const (
	HandlerRuntimeDirectSDKV1       DeletionHandlerVersion = "runtime_direct_sdk_v1"
	HandlerFilesystemOSRootV1       DeletionHandlerVersion = "filesystem_os_root_v1"
	HandlerPostgresExactInventoryV1 DeletionHandlerVersion = "postgres_exact_inventory_v1"
)

type DeletionTargetReceiptInput struct {
	Target               DeletionTarget
	TombstoneDigest      [32]byte
	SelectedVectorDigest [32]byte
	HandlerVersion       DeletionHandlerVersion
	Disposition          ReceiptDisposition
	CompletedAt          time.Time
	PermissionLoss       bool
}

func DeletionTargetReceiptProjection(input DeletionTargetReceiptInput) ([]byte, error) {
	targetTag, targetOK := input.Target.tag()
	dispositionTag, dispositionOK := input.Disposition.tag()
	if !targetOK || !dispositionOK || isZeroDigest(input.TombstoneDigest) ||
		isZeroDigest(input.SelectedVectorDigest) ||
		!handlerDispositionAllowed(input.Target, input.HandlerVersion, input.Disposition, input.PermissionLoss) {
		return nil, errors.New("governance: invalid deletion target receipt")
	}
	completedSeconds, err := unixSeconds(input.CompletedAt)
	if err != nil {
		return nil, err
	}
	projection := []byte{targetTag}
	projection = append(projection, input.TombstoneDigest[:]...)
	projection = append(projection, input.SelectedVectorDigest[:]...)
	projection, err = appendEncodedString(projection, string(input.HandlerVersion))
	if err != nil {
		return nil, err
	}
	projection = append(projection, dispositionTag)
	projection = appendU64(projection, completedSeconds)
	return projection, nil
}

func handlerDispositionAllowed(target DeletionTarget, handler DeletionHandlerVersion, disposition ReceiptDisposition, permissionLoss bool) bool {
	switch target {
	case TargetRuntime:
		return handler == HandlerRuntimeDirectSDKV1 &&
			(disposition == ReceiptPurged || disposition == ReceiptNoData)
	case TargetFilesystem:
		return handler == HandlerFilesystemOSRootV1 &&
			(disposition == ReceiptPurged || permissionLoss && disposition == ReceiptNoData)
	case TargetPostgreSQL:
		return handler == HandlerPostgresExactInventoryV1 &&
			(disposition == ReceiptPurged || permissionLoss && disposition == ReceiptNoData)
	default:
		return false
	}
}

func DeletionTargetReceiptDigest(input DeletionTargetReceiptInput) ([32]byte, error) {
	projection, err := DeletionTargetReceiptProjection(input)
	return hashProjection("CKDeletionTargetReceiptV1\x00", projection, err)
}

type DeletionTargetReceiptRow struct {
	Target DeletionTarget
	Digest [32]byte
}

func DeletionTargetReceiptsProjection(mode PurgeMode, tombstoneDigest, selectedVectorDigest [32]byte, rows []DeletionTargetReceiptRow) ([]byte, error) {
	// Порядок целей закреплён digest-контрактом и совпадает с порядком удаления.
	wantTargets := [...]DeletionTarget{TargetRuntime, TargetFilesystem, TargetPostgreSQL}
	if _, known := mode.tag(); !known || isZeroDigest(tombstoneDigest) || isZeroDigest(selectedVectorDigest) || len(rows) != len(wantTargets) {
		return nil, errors.New("governance: invalid deletion target receipt set")
	}
	projection := make([]byte, 0, 68+len(rows)*33)
	projection = append(projection, tombstoneDigest[:]...)
	projection = append(projection, selectedVectorDigest[:]...)
	projection = appendU32(projection, uint32(len(rows)))
	for index, row := range rows {
		if row.Target != wantTargets[index] || isZeroDigest(row.Digest) {
			return nil, errors.New("governance: deletion target receipt set is missing, duplicate, or reordered")
		}
		tag, _ := row.Target.tag()
		projection = append(projection, tag)
		projection = append(projection, row.Digest[:]...)
	}
	return projection, nil
}

func DeletionTargetReceiptsDigest(mode PurgeMode, tombstoneDigest, selectedVectorDigest [32]byte, rows []DeletionTargetReceiptRow) ([32]byte, error) {
	projection, err := DeletionTargetReceiptsProjection(mode, tombstoneDigest, selectedVectorDigest, rows)
	return hashProjection("CKDeletionTargetReceiptSetV1\x00", projection, err)
}
