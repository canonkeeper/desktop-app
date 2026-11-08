package jobstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type TerminalizeUserModelJobInput struct {
	Kind             jobs.Kind
	ProjectID        string
	JobID            string
	ClaimIncarnation int64
	From             jobs.Status
	To               jobs.Status
	Result           jobs.Result
	Error            *jobs.Error
}

func (s *Store) scrubAgentWorkflowInput(
	ctx context.Context,
	tx pgx.Tx,
	binding jobs.ModelBinding,
	lifecycleReadable bool,
	sourceDeleted bool,
) (pgtype.UUID, error) {
	if s == nil || tx == nil || binding.Validate() != nil || binding.Origin != jobs.ModelOriginUser ||
		!userModelKind(binding.Kind) {
		return pgtype.UUID{}, jobs.ErrConflict
	}
	projectID, err := storedb.ParseUUID(binding.ProjectID)
	if err != nil {
		return pgtype.UUID{}, jobs.ErrConflict
	}
	jobID, err := storedb.ParseUUID(binding.JobID)
	if err != nil {
		return pgtype.UUID{}, jobs.ErrConflict
	}
	var inputID, lockedProjectID, lockedJobID, ownerID pgtype.UUID
	var jobKind string
	var bodyJSON []byte
	inputRowPresent := true
	err = tx.QueryRow(ctx, `
		SELECT id, project_id, job_id, job_kind, artifact_source_owner_id, body_json
		FROM agent_workflow_inputs
		WHERE project_id = $1 AND job_id = $2 AND job_kind = $3
		FOR UPDATE
	`, projectID, jobID, string(binding.Kind)).Scan(
		&inputID, &lockedProjectID, &lockedJobID, &jobKind, &ownerID, &bodyJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if !sourceDeleted {
			return pgtype.UUID{}, jobs.ErrConflict
		}
		inputRowPresent = false
		inputID, ownerID, err = recoverPurgedAgentWorkflowInput(ctx, tx, binding, projectID)
	}
	if err != nil {
		return pgtype.UUID{}, err
	}
	if !inputID.Valid || !ownerID.Valid ||
		inputRowPresent && (lockedProjectID != projectID || lockedJobID != jobID || jobKind != string(binding.Kind)) {
		return pgtype.UUID{}, jobs.ErrConflict
	}
	owner, err := readModelPrivateInputOwnerWithLifecycle(
		ctx, tx, projectID, ownerID, lifecycleReadable, sourceDeleted,
	)
	if err != nil {
		return pgtype.UUID{}, err
	}
	validManifest := owner.manifest.Version() == 2
	if owner.artifactKind != governance.ArtifactAgentWorkflowInput || owner.artifactID != inputID.String() || !validManifest {
		return pgtype.UUID{}, jobs.ErrConflict
	}
	err = s.artifactSourcesStore(nil).DeleteArtifactWithSources(ctx, tx, governancestore.DeleteArtifactWithSourcesInput{
		OwnerID: ownerID.String(), ProjectID: binding.ProjectID, ArtifactID: inputID.String(),
		Kind: governance.ArtifactAgentWorkflowInput, ExpectedManifest: owner.manifest,
	}, func(ctx context.Context, tx pgx.Tx) error {
		deleted, err := sqlc.New(tx).DeleteAgentWorkflowInput(ctx, sqlc.DeleteAgentWorkflowInputParams{
			InputID: inputID, ProjectID: projectID, JobID: jobID,
		})
		if errors.Is(err, pgx.ErrNoRows) && sourceDeleted && !inputRowPresent {
			return nil
		}
		if err != nil {
			return err
		}
		if deleted != inputID {
			return jobs.ErrConflict
		}
		return nil
	})
	if errors.Is(err, governancestore.ErrArtifactSourcesValidation) ||
		errors.Is(err, governancestore.ErrArtifactSourcesConflict) {
		return pgtype.UUID{}, jobs.ErrConflict
	}
	if err != nil {
		return pgtype.UUID{}, err
	}
	return ownerID, nil
}

func recoverPurgedAgentWorkflowInput(
	ctx context.Context,
	tx pgx.Tx,
	binding jobs.ModelBinding,
	projectID pgtype.UUID,
) (pgtype.UUID, pgtype.UUID, error) {
	actorID, actorErr := storedb.ParseUUID(binding.ActorUserID)
	sessionID, sessionErr := storedb.ParseUUID(binding.InitiatingAuthSessionID)
	if actorErr != nil || sessionErr != nil {
		return pgtype.UUID{}, pgtype.UUID{}, jobs.ErrConflict
	}
	wantAuthority := modelStartReplayAuthority(jobs.CreateModelJobInput{
		Origin: binding.Origin, InputContractVersion: binding.InputContractVersion,
	}, actorID, sessionID)
	rows, err := tx.Query(ctx, `
		SELECT status_code, response_json
		FROM idempotency_records
		WHERE response_json->>'jobId' = $1
		ORDER BY id
		FOR SHARE
	`, binding.JobID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	defer rows.Close()
	workflowInputID := ""
	for rows.Next() {
		var statusCode int32
		var responseJSON []byte
		if err := rows.Scan(&statusCode, &responseJSON); err != nil {
			return pgtype.UUID{}, pgtype.UUID{}, err
		}
		decoder := json.NewDecoder(bytes.NewReader(responseJSON))
		decoder.DisallowUnknownFields()
		var envelope userModelStartReplayEnvelope
		if decoder.Decode(&envelope) != nil ||
			!errors.Is(decoder.Decode(&struct{}{}), io.EOF) ||
			envelope.StatusCode != statusCode || envelope.JobID != binding.JobID ||
			!bytes.Equal(envelope.ReplayAuthority, wantAuthority) ||
			envelope.WorkflowInputID == "" || workflowInputID != "" && workflowInputID != envelope.WorkflowInputID {
			return pgtype.UUID{}, pgtype.UUID{}, jobs.ErrConflict
		}
		workflowInputID = envelope.WorkflowInputID
	}
	if err := rows.Err(); err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	rows.Close()
	if workflowInputID == "" {
		return pgtype.UUID{}, pgtype.UUID{}, jobs.ErrConflict
	}
	inputID, err := storedb.ParseUUID(workflowInputID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, jobs.ErrConflict
	}
	var ownerID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM artifact_source_owners
		WHERE project_id = $1
		  AND artifact_kind = 'agent_workflow_input'
		  AND artifact_id = $2
		FOR UPDATE
	`, projectID, inputID).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, pgtype.UUID{}, jobs.ErrConflict
	}
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	if !ownerID.Valid {
		return pgtype.UUID{}, pgtype.UUID{}, jobs.ErrConflict
	}
	return inputID, ownerID, nil
}

func (s *Store) TerminalizeUserModelJobInTx(
	ctx context.Context,
	tx pgx.Tx,
	input TerminalizeUserModelJobInput,
) error {
	projectID, jobID, resultJSON, errorJSON, err := userModelTerminalValues(input)
	if s == nil || tx == nil || err != nil {
		if err != nil {
			return err
		}
		return jobs.ErrValidation
	}
	return terminalizeModelJobInTx(ctx, tx, modelTerminalJobInTxInput{
		projectID: projectID, jobID: jobID, claimIncarnation: input.ClaimIncarnation,
		from: input.From, to: input.To, resultJSON: resultJSON, errorJSON: errorJSON,
		queuedCancel: input.From == jobs.StatusQueued,
	}, func(ctx context.Context, tx pgx.Tx, binding jobs.ModelBinding) (pgtype.UUID, error) {
		if binding.Kind != input.Kind || binding.Origin != jobs.ModelOriginUser {
			return pgtype.UUID{}, jobs.ErrConflict
		}
		return s.scrubAgentWorkflowInput(ctx, tx, binding, input.To == jobs.StatusCanceled, false)
	})
}

func userModelTerminalValues(
	input TerminalizeUserModelJobInput,
) (pgtype.UUID, pgtype.UUID, []byte, []byte, error) {
	if !userModelKind(input.Kind) || input.ClaimIncarnation < 0 {
		return pgtype.UUID{}, pgtype.UUID{}, nil, nil, jobs.ErrValidation
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, nil, nil, jobs.ErrValidation
	}
	jobID, err := storedb.ParseUUID(input.JobID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, nil, nil, jobs.ErrValidation
	}
	switch {
	case input.ClaimIncarnation == 0 && input.From == jobs.StatusQueued && input.To == jobs.StatusCanceled &&
		input.Result == nil && input.Error == nil:
		return projectID, jobID, nil, nil, nil
	case input.ClaimIncarnation > 0 && input.From == jobs.StatusRunning && input.To == jobs.StatusSucceeded && input.Result != nil && input.Error == nil:
		resultJSON, err := jobs.EncodeResult(input.Kind, input.Result)
		return projectID, jobID, resultJSON, nil, err
	case input.ClaimIncarnation > 0 && input.From == jobs.StatusRunning && input.To == jobs.StatusFailed && input.Result == nil && input.Error != nil:
		errorJSON, err := jobs.EncodeError(*input.Error)
		return projectID, jobID, nil, errorJSON, err
	case input.ClaimIncarnation > 0 && input.From == jobs.StatusCanceling && input.To == jobs.StatusCanceled && input.Result == nil && input.Error == nil:
		return projectID, jobID, nil, nil, nil
	default:
		return pgtype.UUID{}, pgtype.UUID{}, nil, nil, jobs.ErrValidation
	}
}

func userModelKind(kind jobs.Kind) bool {
	switch kind {
	case jobs.KindResearchChatTurn:
		return true
	default:
		return false
	}
}
