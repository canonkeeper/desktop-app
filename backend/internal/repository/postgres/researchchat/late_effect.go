package researchchat

import (
	"bytes"
	"context"
	"errors"

	"canonkeeper/backend/internal/auth"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	canonstore "canonkeeper/backend/internal/store/canon"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type lateEffectKey struct {
	ProjectID        string
	JobID            string
	ChatID           string
	TurnID           string
	ActorUserID      string
	ClaimIncarnation int64
}

func (repository *Repository) validateLateEffectInTx(
	ctx context.Context,
	tx pgx.Tx,
	key lateEffectKey,
	lockTarget func(context.Context, pgx.Tx, jobs.ModelClaim) error,
) error {
	var (
		incarnation     int64
		subjectJSON     []byte
		origin          string
		actorID         pgtype.UUID
		sessionID       pgtype.UUID
		contractVersion int32
		ownerID         pgtype.UUID
	)
	err := tx.QueryRow(ctx, `
		SELECT job.claim_incarnation, job.subject_json, binding.origin,
		       binding.actor_user_id, binding.initiating_auth_session_id,
		       binding.input_contract_version, job.artifact_source_owner_id
		FROM jobs AS job
		JOIN job_model_bindings AS binding
		  ON binding.job_id=job.id AND binding.project_id=job.project_id
		 AND binding.job_kind=job.kind
		 AND binding.artifact_source_owner_id=job.artifact_source_owner_id
		WHERE job.project_id=$1::uuid AND job.id=$2::uuid
		  AND job.kind='research_chat_turn'
	`, key.ProjectID, key.JobID).Scan(
		&incarnation, &subjectJSON, &origin, &actorID, &sessionID, &contractVersion, &ownerID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return researchdomain.ErrConflict
	}
	if err != nil {
		return err
	}
	decoded, err := jobs.DecodeSubject(jobs.KindResearchChatTurn, subjectJSON)
	subject, ok := decoded.(jobs.ResearchChatTurnSubject)
	actor := storedb.UUIDString(actorID)
	if err != nil || !ok || contractVersion < 1 || jobs.ModelOrigin(origin) != jobs.ModelOriginUser ||
		subject.ProjectID != key.ProjectID || subject.ChatID != key.ChatID || subject.TurnID != key.TurnID ||
		key.ActorUserID != "" && actor != key.ActorUserID ||
		key.ClaimIncarnation > 0 && incarnation != key.ClaimIncarnation {
		return researchdomain.ErrConflict
	}
	claim := jobs.ModelClaim{
		Claim: jobs.WorkerClaim{ProjectID: key.ProjectID, JobID: key.JobID, ClaimIncarnation: incarnation},
		Kind:  jobs.KindResearchChatTurn, Subject: subject, Origin: jobs.ModelOriginUser,
		ActorUserID: actor, InitiatingAuthSessionID: storedb.UUIDString(sessionID),
		InputContractVersion: uint32(contractVersion),
	}
	err = repository.jobs.ValidateModelLateEffect(ctx, tx, claim,
		func(ctx context.Context, tx pgx.Tx, _ jobs.Subject) (jobs.JobSourceManifest, error) {
			if err := lockTarget(ctx, tx, claim); err != nil {
				return jobs.JobSourceManifest{}, err
			}
			view, err := governance.PresentSourceView(subject.SourceView)
			if err != nil {
				return jobs.JobSourceManifest{}, jobs.ErrConflict
			}
			transfer, err := repository.canon.LoadArtifactSourceTransferInTx(ctx, tx, canonstore.LoadArtifactSourceTransferInput{
				OwnerID: storedb.UUIDString(ownerID), ProjectID: key.ProjectID, ArtifactID: key.JobID,
				Kind: governance.ArtifactJob, SourceView: view,
			})
			if err != nil || !transfer.ContentAllowed {
				if err != nil {
					return jobs.JobSourceManifest{}, err
				}
				return jobs.JobSourceManifest{}, jobs.ErrConflict
			}
			return jobs.JobSourceManifest{SourceView: view, Refs: transfer.Refs}, nil
		})
	if errors.Is(err, auth.ErrUnauthenticated) || errors.Is(err, auth.ErrSessionNotFound) {
		return researchdomain.ErrForbidden
	}
	return err
}

func resolveNativeAuthorityInTx(
	ctx context.Context,
	tx pgx.Tx,
	rootThreadID string,
	providerTurnID string,
) (sqlc.ResolveNativeRootAuthorityRow, error) {
	rows, err := sqlc.New(tx).ResolveNativeRootAuthority(ctx, sqlc.ResolveNativeRootAuthorityParams{
		RootThreadID: rootThreadID, TurnID: providerTurnID,
	})
	if err != nil {
		return sqlc.ResolveNativeRootAuthorityRow{}, err
	}
	if len(rows) == 0 {
		return sqlc.ResolveNativeRootAuthorityRow{}, researchdomain.ErrForbidden
	}
	if len(rows) != 1 {
		return sqlc.ResolveNativeRootAuthorityRow{}, researchdomain.ErrConflict
	}
	return rows[0], nil
}

func sameNativeAuthority(left, right sqlc.ResolveNativeRootAuthorityRow) bool {
	return left.ProjectID == right.ProjectID && left.JobID == right.JobID &&
		left.ChatSessionID == right.ChatSessionID && left.Kind == right.Kind &&
		left.RootThreadID == right.RootThreadID && left.TurnID == right.TurnID &&
		left.Origin == right.Origin && left.ActorUserID == right.ActorUserID &&
		left.InitiatingAuthSessionID == right.InitiatingAuthSessionID &&
		bytes.Equal(left.SubjectJson, right.SubjectJson)
}
