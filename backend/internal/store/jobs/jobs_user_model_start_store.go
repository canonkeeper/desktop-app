package jobstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	authstore "canonkeeper/backend/internal/store/auth"
	"canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type UserModelStartIdempotency struct {
	ScopeHash          string
	OperationID        string
	IdempotencyKeyHash string
	StatusCode         int
}

type UserModelStartInput struct {
	ProjectID            string
	Kind                 jobs.Kind
	ActorUserID          string
	SessionProof         auth.SessionWriteProof
	InputContractVersion uint32
	Idempotency          UserModelStartIdempotency
	CreatedAt            time.Time
}

type UserModelStartIDs struct {
	JobID           string
	WorkflowInputID string
	TargetID        string
}

type UserModelLockedTarget struct {
	Source          jobs.JobSourceManifest
	ManifestVersion uint32
	RequestHash     string
}

type UserModelCreated struct {
	Subject jobs.Subject
	Target  UserModelLockedTarget
}

type UserModelStartResult struct {
	Job             jobs.Job
	WorkflowInputID string
	TargetID        string
}

type LockUserModelTargetInTx func(context.Context, pgx.Tx) (UserModelLockedTarget, error)

type CreateUserModelProductInTx func(
	context.Context,
	pgx.Tx,
	UserModelStartIDs,
	UserModelLockedTarget,
) (UserModelCreated, error)

type userModelStartReplayEnvelope struct {
	StatusCode      int32           `json:"statusCode"`
	JobID           string          `json:"jobId"`
	WorkflowInputID string          `json:"workflowInputId"`
	TargetID        string          `json:"targetId"`
	Subject         json.RawMessage `json:"subject"`
	ReplayAuthority []byte          `json:"replayAuthority"`
}

// StartUserModelJobInTx сохраняет единый порядок блокировок для трёх пользовательских workflow.
func (s *Store) StartUserModelJobInTx(
	ctx context.Context,
	tx pgx.Tx,
	input UserModelStartInput,
	lockTarget LockUserModelTargetInTx,
	createProduct CreateUserModelProductInTx,
) (UserModelStartResult, error) {
	if s == nil || tx == nil || lockTarget == nil || createProduct == nil || validateUserModelStartInput(input) != nil {
		return UserModelStartResult{}, jobs.ErrValidation
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return UserModelStartResult{}, jobs.ErrNotFound
	}
	actorID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return UserModelStartResult{}, jobs.ErrNotFound
	}
	initiatingSessionID, err := authstore.LockVerifiedSessionWriteProof(ctx, tx, actorID, input.SessionProof)
	if err != nil {
		return UserModelStartResult{}, err
	}
	if err := lockActiveJobsProject(ctx, tx, projectID); err != nil {
		return UserModelStartResult{}, err
	}
	if _, err := lockJobsMutationMembership(ctx, tx, projectID, actorID); err != nil {
		return UserModelStartResult{}, err
	}

	lockedTarget, err := lockTarget(ctx, tx)
	if err != nil {
		return UserModelStartResult{}, err
	}
	if validateUserModelLockedTarget(input.ProjectID, lockedTarget) != nil {
		return UserModelStartResult{}, jobs.ErrConflict
	}
	if err := s.hitModelStart("after_target_lock"); err != nil {
		return UserModelStartResult{}, err
	}

	queries := sqlc.New(tx)
	record, newRecord, err := createOrLoadUserModelIdempotency(ctx, queries, input, lockedTarget.RequestHash)
	if err != nil {
		return UserModelStartResult{}, err
	}
	if err := s.hitModelStart("after_idempotency"); err != nil {
		return UserModelStartResult{}, err
	}
	replayAuthority := modelStartReplayAuthority(userModelJobInput(input, record, UserModelStartIDs{}, nil, lockedTarget.RequestHash), actorID, initiatingSessionID)
	if !newRecord {
		return replayUserModelStart(
			ctx, tx, queries, input, lockedTarget, record, replayAuthority, initiatingSessionID,
		)
	}

	ids, err := newUserModelStartIDs()
	if err != nil {
		return UserModelStartResult{}, err
	}
	created, err := createProduct(ctx, tx, ids, lockedTarget)
	if err != nil {
		return UserModelStartResult{}, err
	}
	if validateUserModelLockedTarget(input.ProjectID, created.Target) != nil ||
		created.Target.RequestHash != lockedTarget.RequestHash ||
		!userModelSourceContains(created.Target.Source, lockedTarget.Source) {
		return UserModelStartResult{}, jobs.ErrConflict
	}
	if err := s.hitModelStart("after_private_input"); err != nil {
		return UserModelStartResult{}, err
	}
	jobInput := userModelJobInput(input, record, ids, created.Subject, lockedTarget.RequestHash)
	policy, err := jobs.ModelPolicyFor(jobInput)
	if err != nil || created.Subject.Kind() != input.Kind {
		return UserModelStartResult{}, jobs.ErrValidation
	}
	if err := validateUserModelWorkflowInput(ctx, tx, queries, projectID, ids, input.Kind, created.Target, true); err != nil {
		return UserModelStartResult{}, err
	}

	job, err := s.createUserModelJob(
		ctx, tx, queries, jobInput, policy, projectID, actorID, initiatingSessionID, created.Target,
	)
	if err != nil {
		return UserModelStartResult{}, err
	}
	if err := closeUserModelStartIdempotency(ctx, queries, record, ids, created.Subject, replayAuthority); err != nil {
		return UserModelStartResult{}, err
	}
	return UserModelStartResult{Job: job, WorkflowInputID: ids.WorkflowInputID, TargetID: ids.TargetID}, nil
}

func createOrLoadUserModelIdempotency(
	ctx context.Context,
	queries *sqlc.Queries,
	input UserModelStartInput,
	requestHash string,
) (sqlc.IdempotencyRecord, bool, error) {
	record, err := queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
		ScopeHash: input.Idempotency.ScopeHash, OperationID: input.Idempotency.OperationID,
		IdempotencyKeyHash: input.Idempotency.IdempotencyKeyHash, RequestHash: requestHash,
		StatusCode: int32(input.Idempotency.StatusCode), ResponseJson: []byte(idempotencyPendingResponseJSON),
		CreatedAt: storedb.Timestamptz(input.CreatedAt),
	})
	newRecord := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		record, err = queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
			ScopeHash: input.Idempotency.ScopeHash, OperationID: input.Idempotency.OperationID,
			IdempotencyKeyHash: input.Idempotency.IdempotencyKeyHash,
		})
	}
	if isUniqueViolation(err) {
		return sqlc.IdempotencyRecord{}, false, jobs.ErrConflict
	}
	return record, newRecord, err
}

func replayUserModelStart(
	ctx context.Context,
	tx pgx.Tx,
	queries *sqlc.Queries,
	input UserModelStartInput,
	lockedTarget UserModelLockedTarget,
	record sqlc.IdempotencyRecord,
	replayAuthority []byte,
	initiatingSessionID pgtype.UUID,
) (UserModelStartResult, error) {
	envelope, subject, err := decodeUserModelStartEnvelope(record, input, lockedTarget.RequestHash, replayAuthority)
	if err != nil {
		return UserModelStartResult{}, err
	}
	ids := UserModelStartIDs{
		JobID: envelope.JobID, WorkflowInputID: envelope.WorkflowInputID, TargetID: envelope.TargetID,
	}
	jobInput := userModelJobInput(input, record, ids, subject, lockedTarget.RequestHash)
	if _, err := jobs.ModelPolicyFor(jobInput); err != nil {
		return UserModelStartResult{}, jobs.ErrConflict
	}
	projectID, _ := storedb.ParseUUID(input.ProjectID)
	jobID, _ := storedb.ParseUUID(ids.JobID)
	preview, err := queries.PreviewModelJobForStart(ctx, sqlc.PreviewModelJobForStartParams{
		ProjectID: projectID, JobID: jobID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return UserModelStartResult{}, jobs.ErrConflict
	}
	if err != nil {
		return UserModelStartResult{}, err
	}
	// Replay повторно проверяет bound target, но не сравнивает сохранённый owner с новым optional context union.
	job, _, err := lockModelStartJobWithManifestVersion(
		ctx, queries, jobInput, initiatingSessionID, lockedTarget.Source,
		lockedTarget.ManifestVersion, preview, false,
	)
	if err != nil {
		return UserModelStartResult{}, err
	}
	if !terminalJobStatus(job.Status) {
		if err := validateUserModelWorkflowInput(ctx, tx, queries, projectID, ids, input.Kind, lockedTarget, false); err != nil {
			return UserModelStartResult{}, err
		}
	}
	return UserModelStartResult{Job: job, WorkflowInputID: ids.WorkflowInputID, TargetID: ids.TargetID}, nil
}

func decodeUserModelStartEnvelope(
	record sqlc.IdempotencyRecord,
	input UserModelStartInput,
	requestHash string,
	replayAuthority []byte,
) (userModelStartReplayEnvelope, jobs.Subject, error) {
	if record.RequestHash != requestHash || record.StatusCode != int32(input.Idempotency.StatusCode) {
		return userModelStartReplayEnvelope{}, nil, jobs.ErrConflict
	}
	decoder := json.NewDecoder(bytes.NewReader(record.ResponseJson))
	decoder.DisallowUnknownFields()
	var envelope userModelStartReplayEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return userModelStartReplayEnvelope{}, nil, jobs.ErrConflict
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) ||
		envelope.StatusCode != record.StatusCode || !bytes.Equal(envelope.ReplayAuthority, replayAuthority) {
		return userModelStartReplayEnvelope{}, nil, jobs.ErrConflict
	}
	if _, err := storedb.ParseUUID(envelope.JobID); err != nil {
		return userModelStartReplayEnvelope{}, nil, jobs.ErrConflict
	}
	if _, err := storedb.ParseUUID(envelope.WorkflowInputID); err != nil {
		return userModelStartReplayEnvelope{}, nil, jobs.ErrConflict
	}
	if _, err := storedb.ParseUUID(envelope.TargetID); err != nil {
		return userModelStartReplayEnvelope{}, nil, jobs.ErrConflict
	}
	subject, err := jobs.DecodeSubject(input.Kind, envelope.Subject)
	if err != nil || subject.Project() != input.ProjectID {
		return userModelStartReplayEnvelope{}, nil, jobs.ErrConflict
	}
	return envelope, subject, nil
}

func userModelJobInput(
	input UserModelStartInput,
	record sqlc.IdempotencyRecord,
	ids UserModelStartIDs,
	subject jobs.Subject,
	requestHash string,
) jobs.CreateModelJobInput {
	return jobs.CreateModelJobInput{
		JobID: ids.JobID, ProjectID: input.ProjectID, Subject: subject, Origin: jobs.ModelOriginUser,
		ActorUserID: input.ActorUserID, SessionProof: input.SessionProof,
		InputContractVersion: input.InputContractVersion, CreatedAt: input.CreatedAt,
		Idempotency: jobs.IdempotentJobStoreInput{
			RecordID: record.ID.String(), ScopeHash: input.Idempotency.ScopeHash,
			OperationID: input.Idempotency.OperationID, IdempotencyKeyHash: input.Idempotency.IdempotencyKeyHash,
			RequestHash: requestHash, StatusCode: input.Idempotency.StatusCode,
		},
	}
}

func newUserModelStartIDs() (UserModelStartIDs, error) {
	jobID, err := newUUIDValue()
	if err != nil {
		return UserModelStartIDs{}, err
	}
	workflowInputID, err := newUUIDValue()
	if err != nil {
		return UserModelStartIDs{}, err
	}
	targetID, err := newUUIDValue()
	if err != nil {
		return UserModelStartIDs{}, err
	}
	return UserModelStartIDs{
		JobID: jobID.String(), WorkflowInputID: workflowInputID.String(), TargetID: targetID.String(),
	}, nil
}

func validateUserModelWorkflowInput(
	ctx context.Context,
	tx pgx.Tx,
	queries *sqlc.Queries,
	projectID pgtype.UUID,
	ids UserModelStartIDs,
	kind jobs.Kind,
	target UserModelLockedTarget,
	requireExactSource bool,
) error {
	jobID, _ := storedb.ParseUUID(ids.JobID)
	workflowInputID, _ := storedb.ParseUUID(ids.WorkflowInputID)
	targetID, _ := storedb.ParseUUID(ids.TargetID)
	row, err := queries.GetAgentWorkflowInputByJob(ctx, sqlc.GetAgentWorkflowInputByJobParams{
		JobID: jobID, ProjectID: projectID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	}
	if err != nil {
		return err
	}
	view, _ := target.Source.SourceView.View()
	if row.ID != workflowInputID || row.ProjectID != projectID || row.JobID != jobID ||
		row.JobKind != string(kind) || row.TargetID != targetID || row.SourceView != string(view) ||
		!row.ArtifactSourceOwnerID.Valid {
		return jobs.ErrConflict
	}
	owner, err := readModelPrivateInputOwner(ctx, tx, projectID, row.ArtifactSourceOwnerID)
	if err != nil {
		return err
	}
	if !userModelWorkflowInputOwnerMatches(owner, ids, target, requireExactSource) {
		return jobs.ErrConflict
	}
	return nil
}

func userModelWorkflowInputOwnerMatches(
	owner modelPrivateInputOwner,
	ids UserModelStartIDs,
	target UserModelLockedTarget,
	requireExactSource bool,
) bool {
	ownerView, ownerPresent := owner.source.SourceView.View()
	targetView, targetPresent := target.Source.SourceView.View()
	if owner.artifactKind != governance.ArtifactAgentWorkflowInput || owner.artifactID != ids.WorkflowInputID ||
		!ownerPresent || !targetPresent || ownerView != targetView {
		return false
	}
	return !requireExactSource || owner.manifest.Version() == target.ManifestVersion &&
		sameModelSourceManifest(owner.source, target.Source)
}

func (s *Store) createUserModelJob(
	ctx context.Context,
	tx pgx.Tx,
	queries *sqlc.Queries,
	input jobs.CreateModelJobInput,
	policy jobs.ModelPolicy,
	projectID, actorID, initiatingSessionID pgtype.UUID,
	target UserModelLockedTarget,
) (jobs.Job, error) {
	manifest, err := newModelArtifactManifest(target.ManifestVersion, governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: input.ProjectID, ArtifactID: input.JobID,
		OwnerSourceView: target.Source.SourceView, Refs: target.Source.Refs,
	})
	if err != nil {
		return jobs.Job{}, jobs.ErrConflict
	}
	ownerIDValue, err := governancestore.NewArtifactSourceOwnerID(ctx, tx)
	if err != nil {
		return jobs.Job{}, err
	}
	ownerID, err := storedb.ParseUUID(ownerIDValue)
	if err != nil {
		return jobs.Job{}, err
	}
	var created jobs.Job
	artifactSources := s.artifactSourcesStore(func(point string) error {
		if point == "before_artifact" {
			return s.hitModelStart("after_job_owner")
		}
		return nil
	})
	err = artifactSources.CreateArtifactWithSources(ctx, tx, governancestore.SealArtifactSourcesInput{
		OwnerID: ownerID.String(), ProjectID: input.ProjectID, ArtifactID: input.JobID,
		Kind: governance.ArtifactJob, SourceView: target.Source.SourceView,
		Refs: target.Source.Refs, Manifest: manifest,
	}, func(ctx context.Context, tx pgx.Tx, sealedOwnerID pgtype.UUID) error {
		created, err = createJobWithQueries(ctx, sqlc.New(tx), jobs.CreateJobStoreInput{
			ID: input.JobID, ProjectID: input.ProjectID, Kind: input.Subject.Kind(),
			Status: jobs.StatusQueued, Subject: input.Subject, CanCancel: true,
			CreatorUserID: input.ActorUserID, QueueClass: policy.QueueClass,
			MaxAttempts: policy.MaxAttempts, NextAttemptAt: input.CreatedAt,
			CreatedAt: input.CreatedAt, UpdatedAt: input.CreatedAt,
		}, sealedOwnerID)
		if err != nil {
			return err
		}
		return s.hitModelStart("after_job_insert")
	})
	if err != nil {
		return jobs.Job{}, mapModelStartWriteError(err)
	}
	binding, err := modelStartBinding(input, created.ID, initiatingSessionID)
	if err != nil {
		return jobs.Job{}, err
	}
	jobID, _ := storedb.ParseUUID(input.JobID)
	if _, err := queries.CreateJobModelBinding(ctx, sqlc.CreateJobModelBindingParams{
		JobID: jobID, ProjectID: projectID, ArtifactSourceOwnerID: ownerID,
		JobKind: string(binding.Kind), Origin: string(binding.Origin), ActorUserID: actorID,
		InitiatingAuthSessionID: initiatingSessionID, InputContractVersion: int32(binding.InputContractVersion),
		CreatedAt: storedb.Timestamptz(input.CreatedAt),
	}); err != nil {
		return jobs.Job{}, mapModelStartWriteError(err)
	}
	if err := s.hitModelStart("after_binding_insert"); err != nil {
		return jobs.Job{}, err
	}
	return created, nil
}

func closeUserModelStartIdempotency(
	ctx context.Context,
	queries *sqlc.Queries,
	record sqlc.IdempotencyRecord,
	ids UserModelStartIDs,
	subject jobs.Subject,
	replayAuthority []byte,
) error {
	encodedSubject, err := jobs.EncodeSubject(subject.Kind(), subject)
	if err != nil {
		return jobs.ErrConflict
	}
	response, err := json.Marshal(userModelStartReplayEnvelope{
		StatusCode: record.StatusCode, JobID: ids.JobID, WorkflowInputID: ids.WorkflowInputID,
		TargetID: ids.TargetID, Subject: encodedSubject, ReplayAuthority: replayAuthority,
	})
	if err != nil {
		return err
	}
	_, err = queries.UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{
		StatusCode: record.StatusCode, ResponseJson: response, ID: record.ID,
	})
	return err
}

func newModelArtifactManifest(
	version uint32,
	input governance.ArtifactManifestInput,
) (governance.ArtifactManifest, error) {
	if version == 1 {
		return governance.NewArtifactManifest(input)
	}
	if version == 2 {
		return governance.NewArtifactManifestV2(input)
	}
	return governance.ArtifactManifest{}, jobs.ErrConflict
}

func (s *Store) hitModelStart(point string) error {
	if s.modelStartFailpoint == nil {
		return nil
	}
	return s.modelStartFailpoint(point)
}

func mapModelStartWriteError(err error) error {
	if errors.Is(err, governancestore.ErrArtifactSourcesValidation) {
		return jobs.ErrValidation
	}
	if errors.Is(err, governancestore.ErrArtifactSourcesConflict) || isUniqueViolation(err) || errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	}
	return err
}
