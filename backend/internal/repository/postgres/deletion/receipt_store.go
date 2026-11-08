package deletion

import (
	"math"

	deletion "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

func deletionClaimUUIDs(claim deletion.TargetClaim) (pgtype.UUID, pgtype.UUID, error) {
	if claim.ValidateIdentity() != nil || claim.ClaimIncarnation > math.MaxInt64 {
		return pgtype.UUID{}, pgtype.UUID{}, deletion.ErrInvalidTargetRequest
	}
	projectID, err := storedb.ParseUUID(claim.ProjectID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, deletion.ErrInvalidTargetRequest
	}
	tombstoneID, err := storedb.ParseUUID(claim.TombstoneID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, deletion.ErrInvalidTargetRequest
	}
	return projectID, tombstoneID, nil
}

func deletionTaskForCompletionParams(
	claim deletion.TargetClaim,
	projectID pgtype.UUID,
	tombstoneID pgtype.UUID,
) sqlc.GetDeletionTargetTaskForCompletionParams {
	return sqlc.GetDeletionTargetTaskForCompletionParams{
		ProjectID:        projectID,
		TombstoneID:      tombstoneID,
		Target:           string(claim.Target),
		ClaimIncarnation: int64(claim.ClaimIncarnation),
		LeaseOwnerDigest: claim.LeaseOwnerDigest[:],
	}
}

func deletionRequestForPersistedTask(
	claim deletion.TargetClaim,
	tombstone deletion.Tombstone,
	task sqlc.GetDeletionTargetTaskForCompletionRow,
) (deletion.TargetRequest, error) {
	if task.Attempt <= 0 || uint64(task.Attempt) > math.MaxUint32 ||
		task.ClaimIncarnation != int64(claim.ClaimIncarnation) {
		return deletion.TargetRequest{}, deletion.ErrInvalidTargetRequest
	}
	owner, err := ParseDeletionDigest(task.LeaseOwnerDigest)
	if err != nil || owner != claim.LeaseOwnerDigest || !task.LeaseExpiresAt.Valid {
		return deletion.TargetRequest{}, deletion.ErrInvalidTargetRequest
	}
	request := deletion.TargetRequest{Claim: claim, Tombstone: tombstone, Attempt: uint32(task.Attempt)}
	if claim.ProjectID != tombstone.ProjectID || claim.TombstoneID != tombstone.Identity.TombstoneID {
		return deletion.TargetRequest{}, deletion.ErrInvalidTargetRequest
	}
	return request, nil
}

func persistedDeletionTargetReceipt(
	request deletion.TargetRequest,
	task sqlc.GetDeletionTargetTaskForCompletionRow,
) (deletion.TargetReceipt, deletion.TargetResult, error) {
	if !task.HandlerVersion.Valid || !task.CompletedAt.Valid {
		return deletion.TargetReceipt{}, deletion.TargetResult{}, deletion.ErrInvalidTargetRequest
	}
	persistedDigest, err := ParseDeletionDigest(task.ReceiptDigest)
	if err != nil {
		return deletion.TargetReceipt{}, deletion.TargetResult{}, deletion.ErrInvalidTargetRequest
	}
	result := deletion.TargetResult{
		HandlerVersion: governance.DeletionHandlerVersion(task.HandlerVersion.String),
	}
	switch deletion.TargetState(task.State) {
	case deletion.TargetSucceeded:
		result.Disposition = deletion.DispositionPurged
		result.Reason = governance.ReasonPurged
	case deletion.TargetNoData:
		result.Disposition = deletion.DispositionNoData
		result.Reason = governance.ReasonNoData
	default:
		return deletion.TargetReceipt{}, deletion.TargetResult{}, deletion.ErrInvalidTargetRequest
	}
	tombstoneDigest, vectorDigest, err := request.Tombstone.Digests()
	if err != nil {
		return deletion.TargetReceipt{}, deletion.TargetResult{}, deletion.ErrInvalidTargetRequest
	}
	var receiptDisposition governance.ReceiptDisposition
	switch result.Disposition {
	case deletion.DispositionPurged:
		receiptDisposition = governance.ReceiptPurged
	case deletion.DispositionNoData:
		receiptDisposition = governance.ReceiptNoData
	default:
		return deletion.TargetReceipt{}, deletion.TargetResult{}, deletion.ErrInvalidTargetRequest
	}
	completedAt := task.CompletedAt.Time.UTC()
	digest, err := governance.DeletionTargetReceiptDigest(governance.DeletionTargetReceiptInput{
		Target: request.Claim.Target, TombstoneDigest: [32]byte(tombstoneDigest),
		SelectedVectorDigest: [32]byte(vectorDigest), HandlerVersion: result.HandlerVersion,
		Disposition: receiptDisposition, CompletedAt: completedAt,
		PermissionLoss: request.Tombstone.Identity.PermissionLossActorUserID != "",
	})
	if err != nil || governance.Digest(digest) != persistedDigest {
		return deletion.TargetReceipt{}, deletion.TargetResult{}, deletion.ErrInvalidTargetRequest
	}
	receipt := deletion.TargetReceipt{
		Target: request.Claim.Target, TombstoneDigest: tombstoneDigest,
		SelectedVectorDigest: vectorDigest, HandlerVersion: result.HandlerVersion,
		Disposition: receiptDisposition, CompletedAt: completedAt, Digest: governance.Digest(digest),
	}
	return receipt, result, nil
}

func deletionTerminalState(disposition deletion.Disposition) (string, error) {
	switch disposition {
	case deletion.DispositionPurged:
		return string(deletion.TargetSucceeded), nil
	case deletion.DispositionNoData:
		return string(deletion.TargetNoData), nil
	default:
		return "", deletion.ErrInvalidTargetRequest
	}
}

func deletionReceiptSetDigests(
	tombstone deletion.Tombstone,
	rows []sqlc.ListDeletionTargetReceiptDigestsRow,
) (governance.Digest, governance.Digest, governance.Digest, error) {
	tombstoneDigest, vectorDigest, err := tombstone.Digests()
	if err != nil {
		return governance.Digest{}, governance.Digest{}, governance.Digest{}, err
	}
	receipts := make([]governance.DeletionTargetReceiptRow, 0, len(rows))
	for _, row := range rows {
		digest, err := ParseDeletionDigest(row.ReceiptDigest)
		if err != nil {
			return governance.Digest{}, governance.Digest{}, governance.Digest{}, deletion.ErrInvalidTargetRequest
		}
		receipts = append(receipts, governance.DeletionTargetReceiptRow{
			Target: governance.DeletionTarget(row.Target),
			Digest: [32]byte(digest),
		})
	}
	aggregate, err := governance.DeletionTargetReceiptsDigest(
		tombstone.Identity.Mode,
		[32]byte(tombstoneDigest),
		[32]byte(vectorDigest),
		receipts,
	)
	if err != nil {
		return governance.Digest{}, governance.Digest{}, governance.Digest{}, deletion.ErrInvalidTargetRequest
	}
	return tombstoneDigest, vectorDigest, governance.Digest(aggregate), nil
}

// ParseDeletionDigest проверяет контрольную сумму, прочитанную из PostgreSQL.
func ParseDeletionDigest(value []byte) (governance.Digest, error) {
	if len(value) != 32 {
		return governance.Digest{}, deletion.ErrInvalidTargetRequest
	}
	var digest governance.Digest
	copy(digest[:], value)
	if digest == (governance.Digest{}) {
		return governance.Digest{}, deletion.ErrInvalidTargetRequest
	}
	return digest, nil
}

type deletionTombstoneFields struct {
	TombstoneID               pgtype.UUID
	ProjectID                 pgtype.UUID
	Origin                    string
	EntityKind                string
	PurgeMode                 string
	PermissionLossActorUserID pgtype.UUID
	ProjectGeneration         pgtype.Int8
	BookID                    pgtype.UUID
	BookGeneration            pgtype.Int8
	ChapterID                 pgtype.UUID
	ChapterGeneration         pgtype.Int8
	ChatSessionID             pgtype.UUID
	ChatLifecycleEpoch        pgtype.Int8
	TombstoneStatus           string
	FencedAt                  pgtype.Timestamptz
	PurgeDeadline             pgtype.Timestamptz
}

func deletionTombstoneFieldsFromLoad(row sqlc.LoadDeletionTargetRequestForClaimRow) deletionTombstoneFields {
	return deletionTombstoneFields{
		TombstoneID: row.TombstoneID, ProjectID: row.ProjectID, Origin: row.Origin,
		EntityKind: row.EntityKind, PurgeMode: row.PurgeMode,
		PermissionLossActorUserID: row.PermissionLossActorUserID, ProjectGeneration: row.ProjectGeneration,
		BookID: row.BookID, BookGeneration: row.BookGeneration,
		ChapterID: row.ChapterID, ChapterGeneration: row.ChapterGeneration,
		ChatSessionID: row.ChatSessionID, ChatLifecycleEpoch: row.ChatLifecycleEpoch,
		TombstoneStatus: row.TombstoneStatus, FencedAt: row.FencedAt, PurgeDeadline: row.PurgeDeadline,
	}
}

func deletionTombstoneFieldsFromTarget(row sqlc.GetDeletionTombstoneForTargetRow) deletionTombstoneFields {
	return deletionTombstoneFields{
		TombstoneID: row.TombstoneID, ProjectID: row.ProjectID, Origin: row.Origin,
		EntityKind: row.EntityKind, PurgeMode: row.PurgeMode,
		PermissionLossActorUserID: row.PermissionLossActorUserID, ProjectGeneration: row.ProjectGeneration,
		BookID: row.BookID, BookGeneration: row.BookGeneration,
		ChapterID: row.ChapterID, ChapterGeneration: row.ChapterGeneration,
		ChatSessionID: row.ChatSessionID, ChatLifecycleEpoch: row.ChatLifecycleEpoch,
		TombstoneStatus: row.TombstoneStatus, FencedAt: row.FencedAt, PurgeDeadline: row.PurgeDeadline,
	}
}

func deletionTombstoneFromFields(fields deletionTombstoneFields) (deletion.Tombstone, error) {
	if !fields.TombstoneID.Valid || !fields.ProjectID.Valid || !fields.FencedAt.Valid {
		return deletion.Tombstone{}, deletion.ErrInvalidTargetRequest
	}
	projectID := fields.ProjectID.String()
	permissionLossActorUserID := ""
	if fields.PermissionLossActorUserID.Valid {
		permissionLossActorUserID = fields.PermissionLossActorUserID.String()
	}
	var selected governance.DeletionSelectedVector
	var err error
	switch governance.DeletionSelectionKind(fields.EntityKind) {
	case governance.DeletionSelectionProject:
		if !fields.ProjectGeneration.Valid || fields.ProjectGeneration.Int64 <= 0 {
			return deletion.Tombstone{}, deletion.ErrInvalidTargetRequest
		}
		selected, err = governance.ProjectDeletionVector(governance.SourceGenerationVector{
			ProjectID: projectID, ProjectGeneration: governance.LifecycleGeneration(fields.ProjectGeneration.Int64),
		})
	case governance.DeletionSelectionBook:
		if !fields.ProjectGeneration.Valid || fields.ProjectGeneration.Int64 <= 0 ||
			!fields.BookID.Valid || !fields.BookGeneration.Valid || fields.BookGeneration.Int64 <= 0 {
			return deletion.Tombstone{}, deletion.ErrInvalidTargetRequest
		}
		selected, err = governance.BookDeletionVector(governance.SourceGenerationVector{
			ProjectID: projectID, ProjectGeneration: governance.LifecycleGeneration(fields.ProjectGeneration.Int64),
			BookID: fields.BookID.String(), BookGeneration: governance.LifecycleGeneration(fields.BookGeneration.Int64),
		})
	case governance.DeletionSelectionChapter:
		if !fields.ProjectGeneration.Valid || fields.ProjectGeneration.Int64 <= 0 ||
			!fields.BookID.Valid || !fields.BookGeneration.Valid || fields.BookGeneration.Int64 <= 0 ||
			!fields.ChapterID.Valid || !fields.ChapterGeneration.Valid || fields.ChapterGeneration.Int64 <= 0 {
			return deletion.Tombstone{}, deletion.ErrInvalidTargetRequest
		}
		selected, err = governance.ChapterDeletionVector(governance.SourceGenerationVector{
			ProjectID: projectID, ProjectGeneration: governance.LifecycleGeneration(fields.ProjectGeneration.Int64),
			BookID: fields.BookID.String(), BookGeneration: governance.LifecycleGeneration(fields.BookGeneration.Int64),
			ChapterID: fields.ChapterID.String(), ChapterGeneration: governance.LifecycleGeneration(fields.ChapterGeneration.Int64),
		})
	case governance.DeletionSelectionChat:
		if !fields.ChatSessionID.Valid || !fields.ChatLifecycleEpoch.Valid || fields.ChatLifecycleEpoch.Int64 <= 0 {
			return deletion.Tombstone{}, deletion.ErrInvalidTargetRequest
		}
		selected, err = governance.ChatDeletionVector(
			projectID,
			fields.ChatSessionID.String(),
			governance.ChatLifecycleEpoch(fields.ChatLifecycleEpoch.Int64),
		)
	default:
		return deletion.Tombstone{}, deletion.ErrInvalidTargetRequest
	}
	if err != nil {
		return deletion.Tombstone{}, deletion.ErrInvalidTargetRequest
	}
	deadline := governance.AbsentPurgeDeadline()
	if fields.PurgeDeadline.Valid {
		deadline, err = governance.PresentPurgeDeadline(fields.PurgeDeadline.Time.UTC())
		if err != nil {
			return deletion.Tombstone{}, deletion.ErrInvalidTargetRequest
		}
	}
	tombstone := deletion.Tombstone{
		ProjectID: projectID,
		Identity: governance.DeletionTombstoneInput{
			TombstoneID:               fields.TombstoneID.String(),
			Origin:                    governance.DeletionOrigin(fields.Origin),
			Mode:                      governance.PurgeMode(fields.PurgeMode),
			SelectedVector:            selected,
			FencedAt:                  fields.FencedAt.Time.UTC(),
			Deadline:                  deadline,
			PermissionLossActorUserID: permissionLossActorUserID,
		},
		Status: deletion.TombstoneStatus(fields.TombstoneStatus),
	}
	if tombstone.Validate() != nil {
		return deletion.Tombstone{}, deletion.ErrInvalidTargetRequest
	}
	return tombstone, nil
}
