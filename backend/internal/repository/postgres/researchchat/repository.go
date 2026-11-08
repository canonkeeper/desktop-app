package researchchat

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"
	canonstore "canonkeeper/backend/internal/store/canon"
	storedb "canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
	jobstore "canonkeeper/backend/internal/store/jobs"
	quotastore "canonkeeper/backend/internal/store/quota"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type Repository struct {
	db      *storedb.DB
	canon   *canonstore.Store
	jobs    *jobstore.Store
	sources *governancestore.Store
}

var (
	_ service.CreateSessionRepository = (*Repository)(nil)
	_ service.ListSessionsRepository  = (*Repository)(nil)
	_ service.GetSessionRepository    = (*Repository)(nil)
	_ service.SubmitTurnRepository    = (*Repository)(nil)
	_ service.SteerRepository         = (*Repository)(nil)
	_ service.CancellationRepository  = (*Repository)(nil)
	_ service.DeletionRepository      = (*Repository)(nil)
	_ service.RunRepository           = (*Repository)(nil)
	_ service.ReadRepository          = (*Repository)(nil)
	_ service.FailureRepository       = (*Repository)(nil)
)

func New(db *storedb.DB) *Repository {
	if db == nil {
		panic("research chat repository: database is required")
	}
	return &Repository{
		db: db, canon: canonstore.New(db), jobs: jobstore.New(db),
		sources: governancestore.New(db),
	}
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows), errors.Is(err, jobs.ErrNotFound):
		return researchdomain.ErrNotFound
	case errors.Is(err, jobs.ErrForbidden):
		return researchdomain.ErrForbidden
	case errors.Is(err, jobs.ErrValidation):
		return researchdomain.ErrValidation
	case errors.Is(err, canonstore.ErrCanonicalSourceValidation):
		return researchdomain.ErrValidation
	case errors.Is(err, canonstore.ErrCanonicalSourceNotCurrent):
		return researchdomain.ErrConflict
	case errors.Is(err, jobs.ErrConflict):
		return researchdomain.ErrConflict
	default:
		return err
	}
}

func mapWriteError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23503", "23505", "23514", "23P01":
			return researchdomain.ErrConflict
		}
	}
	return err
}

type runtimeInputKey struct {
	ProjectID          string
	ChatID             string
	TurnID             string
	JobID              string
	ActorUserID        string
	ChatLifecycleEpoch governance.ChatLifecycleEpoch
	ClaimIncarnation   int64
}

type runtimeInputSnapshot struct {
	BodyJSON []byte
}

type turnProductIDs struct {
	project       pgtype.UUID
	chat          pgtype.UUID
	turn          pgtype.UUID
	job           pgtype.UUID
	workflowInput pgtype.UUID
}

func (repository *Repository) createTurnProductInTx(
	ctx context.Context,
	tx pgx.Tx,
	ids jobstore.UserModelStartIDs,
	target jobstore.UserModelLockedTarget,
	session writableSession,
	actorUserID string,
	privateInput []byte,
	privateDigest [32]byte,
	createdAt time.Time,
) (jobstore.UserModelCreated, error) {
	if target.ManifestVersion != 2 || ids.JobID == "" || ids.WorkflowInputID == "" || ids.TargetID == "" ||
		session.ID == "" || session.ProjectID == "" || session.LifecycleEpoch < 1 ||
		!session.SourceViewArm.IsPresent() || session.SourceViewArm.Validate() != nil ||
		!validSessionShape(session.SourceView, session.Scope, session.BookID) || len(target.Source.Refs) == 0 ||
		target.Source.SourceView != session.SourceViewArm || createdAt.IsZero() || len(privateInput) < 2 ||
		sha256.Sum256(privateInput) != privateDigest {
		return jobstore.UserModelCreated{}, researchdomain.ErrValidation
	}
	parsed, err := turnProductUUIDs(ids, session)
	if err != nil {
		return jobstore.UserModelCreated{}, researchdomain.ErrValidation
	}
	// Only a new idempotency key reaches this callback, so a replay never counts (R1.3). A turn of the helper of a
	// drawing counts drawing_turns_per_day, not the questions of the chat (specs/2026-11-02-image-workshop.md IW8.2);
	// a turn of the helper of a chapter draft counts draft_turns_per_day (specs/2026-11-02-writer-workshop.md W1.5).
	reserve := quotastore.ReserveChatTurnInTx
	switch session.Kind {
	case agent.SessionDrawing:
		reserve = quotastore.ReserveDrawingTurnInTx
	case agent.SessionDraft:
		reserve = quotastore.ReserveDraftTurnInTx
	}
	if err := reserve(ctx, tx, actorUserID, createdAt); err != nil {
		return jobstore.UserModelCreated{}, err
	}
	refs, err := repository.normalizeChatRefsInTx(ctx, tx, session, target.Source.Refs)
	if err != nil {
		return jobstore.UserModelCreated{}, err
	}
	err = repository.createOwnedArtifact(ctx, tx, ids.WorkflowInputID, governance.ArtifactAgentWorkflowInput, session.SourceViewArm, refs,
		func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
			_, err := sqlc.New(tx).CreateAgentWorkflowInput(ctx, sqlc.CreateAgentWorkflowInputParams{
				ID: parsed.workflowInput, ProjectID: parsed.project, JobID: parsed.job,
				JobKind: string(jobs.KindResearchChatTurn), TargetID: parsed.turn, SourceView: string(session.SourceView),
				BodyJson: privateInput, BodyDigest: privateDigest[:], ArtifactSourceOwnerID: ownerID,
				CreatedAt: storedb.Timestamptz(createdAt),
			})
			return err
		})
	if err != nil {
		return jobstore.UserModelCreated{}, mapWriteError(err)
	}
	err = repository.createOwnedArtifact(ctx, tx, ids.TargetID, governance.ArtifactChatTurn, session.SourceViewArm, refs,
		func(ctx context.Context, tx pgx.Tx, ownerID pgtype.UUID) error {
			_, err := sqlc.New(tx).CreateChatTurn(ctx, sqlc.CreateChatTurnParams{
				ID: parsed.turn, ChatID: parsed.chat, ProjectID: parsed.project, SourceView: string(session.SourceView),
				JobID: parsed.job, WorkflowInputID: parsed.workflowInput, ArtifactSourceOwnerID: ownerID,
				CreatedAt: storedb.Timestamptz(createdAt),
			})
			return err
		})
	if err != nil {
		return jobstore.UserModelCreated{}, mapWriteError(err)
	}
	if session.SavedAssignment != "" {
		_, err = sqlc.New(tx).ClearChatSessionSavedAssignment(ctx, sqlc.ClearChatSessionSavedAssignmentParams{
			ChatID: parsed.chat, ProjectID: parsed.project, LifecycleEpoch: int64(session.LifecycleEpoch),
			SavedAssignment: pgtype.Text{String: session.SavedAssignment, Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return jobstore.UserModelCreated{}, researchdomain.ErrConflict
		}
		if err != nil {
			return jobstore.UserModelCreated{}, mapWriteError(err)
		}
	}
	var bookID *string
	if session.BookID != "" {
		value := session.BookID
		bookID = &value
	}
	return jobstore.UserModelCreated{Subject: jobs.ResearchChatTurnSubject{
		Type: jobs.KindResearchChatTurn, ProjectID: session.ProjectID, ChatID: session.ID,
		TurnID: ids.TargetID, SourceView: session.SourceView, Scope: string(session.Scope), BookID: bookID,
	}, Target: target}, nil
}

func (repository *Repository) createOwnedArtifact(
	ctx context.Context,
	tx pgx.Tx,
	artifactID string,
	kind governance.ArtifactKind,
	view governance.SourceViewArm,
	refs []governance.ArtifactSourceRef,
	mutate governancestore.ArtifactSourceCreateMutation,
) error {
	projectID, err := refProjectID(refs)
	if err != nil {
		return err
	}
	manifest, err := governance.NewArtifactManifestV2(governance.ArtifactManifestInput{
		Kind: kind, ProjectID: projectID, ArtifactID: artifactID, OwnerSourceView: view, Refs: refs,
	})
	if err != nil {
		return researchdomain.ErrValidation
	}
	ownerID, err := governancestore.NewArtifactSourceOwnerID(ctx, tx)
	if err != nil {
		return err
	}
	return repository.sources.CreateArtifactWithSources(ctx, tx, governancestore.SealArtifactSourcesInput{
		OwnerID: ownerID, ProjectID: projectID, ArtifactID: artifactID, Kind: kind,
		SourceView: view, Refs: refs, Manifest: manifest,
	}, mutate)
}

func (repository *Repository) normalizeChatRefsInTx(
	ctx context.Context,
	tx pgx.Tx,
	session writableSession,
	refs []governance.ArtifactSourceRef,
) ([]governance.ArtifactSourceRef, error) {
	transfer, err := repository.canon.NormalizeArtifactSourceTransferInTx(ctx, tx, session.ProjectID, session.SourceViewArm, refs)
	if err != nil || !transfer.ContentAllowed {
		if err != nil {
			return nil, err
		}
		return nil, researchdomain.ErrConflict
	}
	if !sameArtifactRefSet(refs, transfer.Refs) {
		return nil, researchdomain.ErrConflict
	}
	for _, ref := range transfer.Refs {
		fields, err := ref.Fields()
		if err != nil || fields.ChatID != session.ID || fields.ChatLifecycleEpoch != session.LifecycleEpoch {
			return nil, researchdomain.ErrConflict
		}
	}
	return transfer.Refs, nil
}

func (repository *Repository) lockRuntimeInputInTx(
	ctx context.Context,
	tx pgx.Tx,
	key runtimeInputKey,
) (runtimeInputSnapshot, error) {
	project, projectErr := storedb.ParseUUID(key.ProjectID)
	turnID, turnErr := storedb.ParseUUID(key.TurnID)
	jobID, jobErr := storedb.ParseUUID(key.JobID)
	actor, actorErr := storedb.ParseUUID(key.ActorUserID)
	if projectErr != nil || turnErr != nil || jobErr != nil || actorErr != nil ||
		key.ClaimIncarnation < 1 || key.ChatLifecycleEpoch < 1 {
		return runtimeInputSnapshot{}, researchdomain.ErrValidation
	}
	session, err := repository.lockWritableSessionInTx(ctx, tx, key.ProjectID, key.ChatID, key.ActorUserID)
	if err != nil {
		return runtimeInputSnapshot{}, err
	}
	if session.LifecycleEpoch != key.ChatLifecycleEpoch {
		return runtimeInputSnapshot{}, researchdomain.ErrConflict
	}
	var locked bool
	if err := tx.QueryRow(ctx, `
		SELECT true
		FROM jobs
		WHERE project_id=$1 AND id=$2 AND kind='research_chat_turn' AND status='running'
		  AND cancel_requested_at IS NULL AND claim_incarnation=$3 AND creator_user_id=$4
		  AND governance_payload_state='active'
		FOR UPDATE
	`, project, jobID, key.ClaimIncarnation, actor).Scan(&locked); err != nil || !locked {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return runtimeInputSnapshot{}, err
		}
		return runtimeInputSnapshot{}, researchdomain.ErrConflict
	}
	queries := sqlc.New(tx)
	turn, err := queries.LockChatTurnForSubmit(ctx, sqlc.LockChatTurnForSubmitParams{
		TurnID: turnID, ProjectID: project, JobID: jobID,
	})
	if err != nil || storedb.UUIDString(turn.ChatID) != key.ChatID || turn.JobKind != string(jobs.KindResearchChatTurn) ||
		turn.SourceView != string(session.SourceView) || !turn.WorkflowInputID.Valid {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return runtimeInputSnapshot{}, err
		}
		return runtimeInputSnapshot{}, researchdomain.ErrConflict
	}
	input, err := queries.LockAgentWorkflowInputForSeal(ctx, sqlc.LockAgentWorkflowInputForSealParams{
		InputID: turn.WorkflowInputID, ProjectID: project, JobID: jobID,
	})
	if err != nil || input.JobKind != string(jobs.KindResearchChatTurn) || input.TargetID != turnID ||
		input.SourceView != string(session.SourceView) || len(input.BodyDigest) != sha256.Size {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return runtimeInputSnapshot{}, err
		}
		return runtimeInputSnapshot{}, researchdomain.ErrConflict
	}
	transfer, err := repository.canon.LoadArtifactSourceTransferInTx(ctx, tx, canonstore.LoadArtifactSourceTransferInput{
		OwnerID: storedb.UUIDString(input.ArtifactSourceOwnerID), ProjectID: key.ProjectID,
		ArtifactID: storedb.UUIDString(input.ID), Kind: governance.ArtifactAgentWorkflowInput, SourceView: session.SourceViewArm,
	})
	if err != nil || !transfer.ContentAllowed {
		if err != nil {
			return runtimeInputSnapshot{}, err
		}
		return runtimeInputSnapshot{}, researchdomain.ErrConflict
	}
	refs, err := repository.normalizeChatRefsInTx(ctx, tx, session, transfer.Refs)
	if err != nil || len(refs) == 0 {
		if err != nil {
			return runtimeInputSnapshot{}, err
		}
		return runtimeInputSnapshot{}, researchdomain.ErrConflict
	}
	return runtimeInputSnapshot{BodyJSON: append([]byte(nil), input.BodyJson...)}, nil
}

func turnProductUUIDs(ids jobstore.UserModelStartIDs, session writableSession) (turnProductIDs, error) {
	values := []string{session.ProjectID, session.ID, ids.TargetID, ids.JobID, ids.WorkflowInputID}
	parsed := make([]pgtype.UUID, len(values))
	for index, value := range values {
		id, err := storedb.ParseUUID(value)
		if err != nil {
			return turnProductIDs{}, err
		}
		parsed[index] = id
	}
	return turnProductIDs{parsed[0], parsed[1], parsed[2], parsed[3], parsed[4]}, nil
}

func sameArtifactRefSet(left, right []governance.ArtifactSourceRef) bool {
	projected := make(map[string]struct{}, len(left))
	for _, ref := range left {
		value, err := governance.ArtifactSourceRefProjection(ref)
		if err != nil {
			return false
		}
		projected[string(value)] = struct{}{}
	}
	if len(projected) != len(right) {
		return false
	}
	for _, ref := range right {
		value, err := governance.ArtifactSourceRefProjection(ref)
		if err != nil {
			return false
		}
		if _, present := projected[string(value)]; !present {
			return false
		}
	}
	return true
}

func refProjectID(refs []governance.ArtifactSourceRef) (string, error) {
	if len(refs) == 0 {
		return "", researchdomain.ErrValidation
	}
	fields, err := refs[0].Fields()
	if err != nil || fields.ProjectID == "" {
		return "", researchdomain.ErrValidation
	}
	return fields.ProjectID, nil
}
