package governance

import (
	"bytes"
	"errors"
	"math"
)

type DeletionPredecessorStatus string

const DeletionPredecessorPurged DeletionPredecessorStatus = "purged"

type DeletionPredecessor struct {
	Tombstone            DeletionTombstoneInput
	Status               DeletionPredecessorStatus
	ReceiptVectorDigest  [32]byte
	TargetReceiptsDigest [32]byte
}

func DeletionPredecessorSetProjection(hardPurgeSelectedVector DeletionSelectedVector, rows []DeletionPredecessor) ([]byte, error) {
	hardPurgeSelectedVectorDigest, err := SelectedVectorDigest(hardPurgeSelectedVector)
	if err != nil || hardPurgeSelectedVector.arm == deletionVectorChat || len(rows) == 0 || uint64(len(rows)) > math.MaxUint32 {
		return nil, errors.New("governance: invalid deletion predecessor set")
	}
	projection := make([]byte, 0, 40+len(rows)*80)
	projection = append(projection, hardPurgeSelectedVectorDigest[:]...)
	projection = appendU32(projection, uint32(len(rows)))
	// Порядок по tombstone_id входит в подписываемое представление набора.
	var previous [16]byte
	for index, row := range rows {
		tombstoneID, err := parseCanonicalUUID(row.Tombstone.TombstoneID)
		selectedVectorDigest, selectedErr := SelectedVectorDigest(row.Tombstone.SelectedVector)
		tombstoneDigest, tombstoneErr := DeletionTombstoneDigest(row.Tombstone)
		if err != nil || selectedErr != nil || tombstoneErr != nil ||
			row.Tombstone.Mode != PurgeModeDerivedGeneration || row.Status != DeletionPredecessorPurged ||
			row.ReceiptVectorDigest != selectedVectorDigest ||
			isZeroDigest(row.TargetReceiptsDigest) ||
			!deletionSelectedVectorContains(hardPurgeSelectedVector, row.Tombstone.SelectedVector) {
			return nil, errors.New("governance: invalid deletion predecessor")
		}
		if index > 0 && bytes.Compare(previous[:], tombstoneID[:]) >= 0 {
			return nil, errors.New("governance: deletion predecessors are duplicate or unordered")
		}
		previous = tombstoneID
		projection = append(projection, tombstoneID[:]...)
		projection = append(projection, tombstoneDigest[:]...)
		projection = append(projection, row.TargetReceiptsDigest[:]...)
	}
	return projection, nil
}

func DeletionPredecessorSetDigest(hardPurgeSelectedVector DeletionSelectedVector, rows []DeletionPredecessor) ([32]byte, error) {
	projection, err := DeletionPredecessorSetProjection(hardPurgeSelectedVector, rows)
	return hashProjection("CKDeletionPredecessorSetV1\x00", projection, err)
}

func deletionSelectedVectorContains(hardPurge, predecessor DeletionSelectedVector) bool {
	switch hardPurge.arm {
	case deletionVectorProject:
		return predecessor.arm != deletionVectorChat &&
			hardPurge.source.ProjectID == predecessor.source.ProjectID
	case deletionVectorBook:
		return (predecessor.arm == deletionVectorBook || predecessor.arm == deletionVectorChapter) &&
			hardPurge.source.ProjectID == predecessor.source.ProjectID &&
			hardPurge.source.BookID == predecessor.source.BookID
	case deletionVectorChapter:
		return predecessor.arm == deletionVectorChapter &&
			hardPurge.source.ProjectID == predecessor.source.ProjectID &&
			hardPurge.source.BookID == predecessor.source.BookID &&
			hardPurge.source.ChapterID == predecessor.source.ChapterID
	default:
		return false
	}
}
