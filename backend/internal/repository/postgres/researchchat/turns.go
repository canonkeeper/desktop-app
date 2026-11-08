package researchchat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"
	canonstore "canonkeeper/backend/internal/store/canon"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const storedTurnInputVersion = 1

type storedTurnInput struct {
	Version         int                                          `json:"version"`
	Question        string                                       `json:"question"`
	WorkshopLines   []string                                     `json:"workshop,omitempty"`
	ContextLocators []governance.CanonicalChapterSourceLocatorV1 `json:"contextLocators"`
	SelectedContext []researchdomain.ContextSelection            `json:"selectedContext,omitempty"`
	// Images are the gallery images attached to the question (specs/2026-10-29-saga-illustrations.md R15.9).
	Images []int `json:"images,omitempty"`
	// Button says a button of the workshop opened the turn, not the author's own words (specs/2026-11-04-writing-plain-path.md P5.6).
	Button bool `json:"button,omitempty"`
	// DrawAfter says «Нарисовать» sent the turn (specs/2026-11-05-drawing-one-screen.md D3.7).
	DrawAfter bool `json:"drawAfter,omitempty"`
}

func (repository *Repository) SubmitTurn(ctx context.Context, command service.SubmitTurnCommand) (jobs.Job, error) {
	var (
		privateInput  []byte
		privateDigest [32]byte
		result        jobstore.UserModelStartResult
	)
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		var locked writableSession
		started, err := repository.jobs.StartUserModelJobInTx(ctx, tx, jobstore.UserModelStartInput{
			ProjectID: command.Session.ProjectID, Kind: jobs.KindResearchChatTurn,
			ActorUserID: command.ActorUserID, SessionProof: command.SessionWriteProof,
			InputContractVersion: storedTurnInputVersion,
			Idempotency: jobstore.UserModelStartIdempotency{
				ScopeHash: command.ScopeHash, OperationID: command.OperationID,
				IdempotencyKeyHash: command.IdempotencyKeyHash, StatusCode: 202,
			},
			CreatedAt: command.CreatedAt,
		}, func(ctx context.Context, tx pgx.Tx) (jobstore.UserModelLockedTarget, error) {
			var err error
			locked, err = repository.lockWritableSessionInTx(
				ctx, tx, command.Session.ProjectID, command.Session.ID, command.ActorUserID,
			)
			if err != nil {
				return jobstore.UserModelLockedTarget{}, err
			}
			if locked.SavedAssignment != "" && locked.SavedAssignment != command.Question {
				return jobstore.UserModelLockedTarget{}, researchdomain.ErrConflict
			}
			refs, selected, err := repository.turnSourceRefs(ctx, tx, locked, command.ContextLocators)
			if err != nil {
				return jobstore.UserModelLockedTarget{}, err
			}
			if err := readyImagesInTx(ctx, tx, command.Session.ProjectID, command.Images); err != nil {
				return jobstore.UserModelLockedTarget{}, err
			}
			privateInput, err = json.Marshal(storedTurnInput{
				Version: storedTurnInputVersion, Question: command.Question, WorkshopLines: command.WorkshopLines,
				ContextLocators: command.ContextLocators, SelectedContext: selected, Images: command.Images, Button: command.Button, DrawAfter: command.DrawAfter,
			})
			if err != nil {
				return jobstore.UserModelLockedTarget{}, err
			}
			privateDigest = sha256.Sum256(privateInput)
			return jobstore.UserModelLockedTarget{
				Source:          jobs.JobSourceManifest{SourceView: locked.SourceViewArm, Refs: refs},
				ManifestVersion: 2, RequestHash: command.RequestHash,
			}, nil
		}, func(ctx context.Context, tx pgx.Tx, ids jobstore.UserModelStartIDs, target jobstore.UserModelLockedTarget) (jobstore.UserModelCreated, error) {
			created, err := repository.createTurnProductInTx(ctx, tx, ids, target, locked, command.ActorUserID, privateInput, privateDigest, command.CreatedAt)
			if err != nil || command.Attachment == nil {
				return created, err
			}
			// The chapter attached to the question goes in with its turn (specs/2026-11-07-chat-attach-chapter.md CA2.4).
			return created, createTurnAttachmentInTx(ctx, tx, locked, ids.TargetID, *command.Attachment, command.CreatedAt)
		})
		result = started
		return err
	})
	return result.Job, mapError(err)
}

// readyImagesInTx checks that the images attached to a question are ready images of the project's
// gallery (R15.9), not variants waiting for the choice (R21.8); another number is not found.
func readyImagesInTx(ctx context.Context, tx pgx.Tx, projectID string, numbers []int) error {
	if len(numbers) == 0 {
		return nil
	}
	var ready int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM saga_images
		WHERE project_id = $1::uuid AND number = ANY($2::integer[]) AND status = 'ready' AND variant_group IS NULL
	`, projectID, numbers).Scan(&ready); err != nil {
		return err
	}
	if ready != len(numbers) {
		return researchdomain.ErrNotFound
	}
	return nil
}

func (repository *Repository) turnSourceRefs(
	ctx context.Context,
	tx pgx.Tx,
	session writableSession,
	locators []governance.CanonicalChapterSourceLocatorV1,
) ([]governance.ArtifactSourceRef, []researchdomain.ContextSelection, error) {
	baseline, err := repository.canon.LoadCurrentCanonicalTextBaselineInTx(ctx, tx, canonstore.CanonicalTextSelectionInput{
		ProjectID: session.ProjectID, SourceView: session.SourceView, BookID: session.BookID,
	})
	if err != nil {
		return nil, nil, err
	}
	refs := append([]governance.ArtifactSourceRef(nil), baseline.SourceRefs...)
	selected := make([]researchdomain.ContextSelection, 0, len(locators))
	for _, locator := range locators {
		resolved, err := repository.canon.ResolveCanonicalChapterSourceInTx(
			ctx, tx, locator.ProjectID, locator.SourceView, locator.BookID, locator.ChapterID,
		)
		if err != nil {
			return nil, nil, err
		}
		if resolved.SourceRevision != locator.SourceRevision || resolved.SourceGeneration != locator.SourceGeneration ||
			resolved.ContentHash != locator.ContentHash {
			return nil, nil, researchdomain.ErrConflict
		}
		refs = append(refs, resolved.ExactRef)
		selected = append(selected, researchdomain.ContextSelection{
			BookTitle: resolved.BookTitle, ChapterPosition: resolved.ChapterPosition, ChapterTitle: resolved.ChapterTitle,
		})
	}
	if err := bindTurnRefsToChat(refs, session.ChatArm); err != nil {
		return nil, nil, err
	}
	refs, err = uniqueRefs(refs)
	return refs, selected, err
}

func bindTurnRefsToChat(refs []governance.ArtifactSourceRef, chat governance.ChatArm) error {
	for index, ref := range refs {
		fields, err := ref.Fields()
		if err != nil || fields.RefKind != governance.ArtifactSourceRefChapter {
			return researchdomain.ErrConflict
		}
		refs[index], err = governance.NewExactChapterArtifactRef(governance.ChapterSourceRefInput{
			Source: governance.SourceGenerationVector{
				ProjectID: fields.ProjectID, ProjectGeneration: fields.ProjectGeneration,
				BookID: fields.BookID, BookGeneration: fields.BookGeneration,
				ChapterID: fields.ChapterID, ChapterGeneration: fields.ChapterGeneration,
			},
			SourceRevision: fields.SourceRevision, SourceView: fields.SourceView,
			SourceGenerationDigest: governance.Digest(fields.LocatorSourceGenerationDigest),
			ContentHash:            fields.ContentHash, Chat: chat,
		})
		if err != nil {
			return researchdomain.ErrConflict
		}
	}
	return nil
}

func uniqueRefs(refs []governance.ArtifactSourceRef) ([]governance.ArtifactSourceRef, error) {
	result := make([]governance.ArtifactSourceRef, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		projection, err := governance.ArtifactSourceRefProjection(ref)
		if err != nil {
			return nil, err
		}
		key := string(projection)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, ref)
	}
	if len(result) == 0 {
		return nil, researchdomain.ErrConflict
	}
	return result, nil
}

func (repository *Repository) LoadRun(ctx context.Context, claim researchdomain.WorkerClaim) (researchdomain.TurnRun, error) {
	job, err := repository.jobs.LoadJobForWorkerClaim(ctx, jobs.WorkerClaim{
		ProjectID: claim.ProjectID, JobID: claim.JobID, ClaimIncarnation: claim.ClaimIncarnation,
	})
	if err != nil {
		return researchdomain.TurnRun{}, mapError(err)
	}
	subject, ok := job.Subject.(jobs.ResearchChatTurnSubject)
	if !ok || job.Kind != jobs.KindResearchChatTurn || job.Status != jobs.StatusRunning || job.CreatorUserID == "" {
		return researchdomain.TurnRun{}, researchdomain.ErrConflict
	}
	current, err := repository.getSession(ctx, job.CreatorUserID, subject.ChatID)
	if err != nil {
		return researchdomain.TurnRun{}, mapError(err)
	}
	var snapshot runtimeInputSnapshot
	err = repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		row, err := repository.lockRuntimeInputInTx(ctx, tx, runtimeInputKey{
			ProjectID: job.ProjectID, ChatID: subject.ChatID, TurnID: subject.TurnID, JobID: job.ID,
			ActorUserID: job.CreatorUserID, ChatLifecycleEpoch: current.LifecycleEpoch,
			ClaimIncarnation: claim.ClaimIncarnation,
		})
		snapshot = row
		return err
	})
	if err != nil {
		return researchdomain.TurnRun{}, mapError(err)
	}
	var input storedTurnInput
	if json.Unmarshal(snapshot.BodyJSON, &input) != nil || input.Version != storedTurnInputVersion || strings.TrimSpace(input.Question) == "" {
		return researchdomain.TurnRun{}, researchdomain.ErrConflict
	}
	providerTurnID, err := repository.providerTurnID(ctx, job.ProjectID, subject.ChatID, subject.TurnID, job.ID)
	if err != nil {
		return researchdomain.TurnRun{}, mapError(err)
	}
	bookID := ""
	if subject.BookID != nil {
		bookID = *subject.BookID
	}
	drawingID := ""
	if current.Kind == agent.SessionDrawing {
		// The session of a helper belongs to its drawing (specs/2026-11-02-image-workshop.md IW4.2).
		if err := repository.db.Pool.QueryRow(ctx, `
			SELECT id::text FROM saga_drawings WHERE project_id = $1::uuid AND chat_id = $2::uuid
		`, job.ProjectID, subject.ChatID).Scan(&drawingID); err != nil {
			return researchdomain.TurnRun{}, mapError(err)
		}
	}
	titles, err := repository.imageTitles(ctx, job.ProjectID, input.Images)
	if err != nil {
		return researchdomain.TurnRun{}, err
	}
	attachment, err := repository.turnAttachmentHeader(ctx, job.ProjectID, subject.TurnID)
	if err != nil {
		return researchdomain.TurnRun{}, err
	}
	return researchdomain.TurnRun{
		ProjectID: job.ProjectID, ChatID: subject.ChatID, TurnID: subject.TurnID, JobID: job.ID,
		ActorUserID: job.CreatorUserID, ClaimIncarnation: claim.ClaimIncarnation,
		SourceView: string(subject.SourceView), Scope: researchdomain.Scope(subject.Scope), BookID: bookID,
		Question: input.Question, WorkshopLines: append([]string(nil), input.WorkshopLines...), SelectedContext: append([]researchdomain.ContextSelection(nil), input.SelectedContext...),
		Images: append([]int(nil), input.Images...), ImageTitles: titles, DrawAfter: input.DrawAfter,
		RootThreadID: current.ThreadID, ProviderTurnID: providerTurnID, Kind: current.Kind, DrawingID: drawingID,
		Attachment: attachment,
	}, nil
}

// imageTitles reads the captions of the images attached to a question, for its anchors (R15.19).
func (repository *Repository) imageTitles(ctx context.Context, projectID string, numbers []int) (map[int]string, error) {
	if len(numbers) == 0 {
		return nil, nil
	}
	rows, err := repository.db.Pool.Query(ctx, `
		SELECT number, COALESCE(title, '') FROM saga_images WHERE project_id = $1::uuid AND number = ANY($2::integer[])
	`, projectID, numbers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	titles := make(map[int]string, len(numbers))
	for rows.Next() {
		var number int
		var title string
		if err := rows.Scan(&number, &title); err != nil {
			return nil, err
		}
		titles[number] = title
	}
	return titles, rows.Err()
}

func (repository *Repository) providerTurnID(ctx context.Context, projectID, chatID, turnID, jobID string) (string, error) {
	var providerTurnID *string
	err := repository.db.Pool.QueryRow(ctx, `
		SELECT turn_row.provider_turn_id
		FROM chat_turns AS turn_row
		JOIN chat_sessions AS chat ON chat.id=turn_row.chat_id AND chat.project_id=turn_row.project_id
		WHERE turn_row.project_id=$1::uuid AND turn_row.chat_id=$2::uuid
		  AND turn_row.id=$3::uuid AND turn_row.job_id=$4::uuid AND chat.deleted_at IS NULL
	`, projectID, chatID, turnID, jobID).Scan(&providerTurnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", researchdomain.ErrNotFound
	}
	if err != nil || providerTurnID == nil {
		return "", err
	}
	return *providerTurnID, nil
}

func (repository *Repository) BindRoot(ctx context.Context, run researchdomain.TurnRun, rootThreadID string) error {
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		return repository.bindChatAuthorityInTx(ctx, tx, run, rootThreadID)
	})
	return mapError(err)
}

func (repository *Repository) BindTurn(ctx context.Context, run researchdomain.TurnRun, rootThreadID, turnID string) error {
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := repository.bindChatAuthorityInTx(ctx, tx, run, rootThreadID); err != nil {
			return err
		}
		projectID, projectErr := storedb.ParseUUID(run.ProjectID)
		chatID, chatErr := storedb.ParseUUID(run.ChatID)
		chatTurnID, turnErr := storedb.ParseUUID(run.TurnID)
		jobID, jobErr := storedb.ParseUUID(run.JobID)
		if projectErr != nil || chatErr != nil || turnErr != nil || jobErr != nil || turnID == "" {
			return researchdomain.ErrValidation
		}
		rows, err := sqlc.New(tx).BindChatProviderTurn(ctx, sqlc.BindChatProviderTurnParams{
			ProviderTurnID: pgtype.Text{String: turnID, Valid: true},
			ProjectID:      projectID, ChatID: chatID, TurnID: chatTurnID, JobID: jobID,
			ThreadID: pgtype.Text{String: rootThreadID, Valid: true},
		})
		if err != nil || rows != 1 {
			return errors.Join(researchdomain.ErrConflict, err)
		}
		return nil
	})
	return mapError(err)
}

func (repository *Repository) bindChatAuthorityInTx(
	ctx context.Context,
	tx pgx.Tx,
	run researchdomain.TurnRun,
	rootThreadID string,
) error {
	projectID, projectErr := storedb.ParseUUID(run.ProjectID)
	chatID, chatErr := storedb.ParseUUID(run.ChatID)
	turnID, turnErr := storedb.ParseUUID(run.TurnID)
	actorID, actorErr := storedb.ParseUUID(run.ActorUserID)
	if projectErr != nil || chatErr != nil || turnErr != nil || actorErr != nil || rootThreadID == "" || run.ClaimIncarnation < 1 {
		return researchdomain.ErrValidation
	}
	queries := sqlc.New(tx)
	params := sqlc.BindChatRootThreadParams{
		ProjectID: projectID, ChatID: chatID, TurnID: turnID, ActorUserID: actorID,
		ThreadID: pgtype.Text{String: rootThreadID, Valid: true},
	}
	if _, err := queries.BindChatRootThread(ctx, params); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return researchdomain.ErrConflict
		}
		return err
	}
	var locked bool
	err := tx.QueryRow(ctx, `
		SELECT true
		FROM jobs AS job
		JOIN chat_turns AS turn_row ON turn_row.job_id=job.id AND turn_row.project_id=job.project_id
		JOIN chat_sessions AS chat ON chat.id=turn_row.chat_id AND chat.project_id=turn_row.project_id
		WHERE job.project_id=$1::uuid AND job.id=$2::uuid AND job.kind='research_chat_turn'
		  AND job.status='running' AND job.cancel_requested_at IS NULL
		  AND job.claim_incarnation=$3::bigint AND job.creator_user_id=$4::uuid
		  AND job.governance_payload_state='active' AND turn_row.id=$5::uuid
		  AND chat.id=$6::uuid AND chat.thread_id=$7::text AND chat.deleted_at IS NULL
		FOR UPDATE OF job, turn_row, chat
	`, run.ProjectID, run.JobID, run.ClaimIncarnation, run.ActorUserID,
		run.TurnID, run.ChatID, rootThreadID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !locked {
		return researchdomain.ErrConflict
	}
	if err != nil {
		return err
	}
	if err := repository.validateLateEffectInTx(ctx, tx, lateEffectKey{
		ProjectID: run.ProjectID, JobID: run.JobID, ChatID: run.ChatID, TurnID: run.TurnID,
		ActorUserID: run.ActorUserID, ClaimIncarnation: run.ClaimIncarnation,
	}, func(ctx context.Context, tx pgx.Tx, _ jobs.ModelClaim) error {
		_, err := sqlc.New(tx).BindChatRootThread(ctx, params)
		if errors.Is(err, pgx.ErrNoRows) {
			return researchdomain.ErrConflict
		}
		return err
	}); err != nil {
		return err
	}
	if _, err := queries.BindChatRootThread(ctx, params); errors.Is(err, pgx.ErrNoRows) {
		return researchdomain.ErrConflict
	} else {
		return err
	}
}

func (repository *Repository) FinishResponse(ctx context.Context, input researchdomain.FinishResponse) error {
	if strings.TrimSpace(input.Response) == "" || input.ClaimIncarnation < 1 {
		return researchdomain.ErrValidation
	}
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		var locked bool
		if err := tx.QueryRow(ctx, `
			SELECT true
			FROM jobs AS job
			JOIN chat_turns AS turn_row ON turn_row.job_id=job.id AND turn_row.project_id=job.project_id
			JOIN chat_sessions AS chat ON chat.id=turn_row.chat_id AND chat.project_id=turn_row.project_id
			WHERE job.project_id=$1::uuid AND job.id=$2::uuid AND job.kind='research_chat_turn'
			  AND job.status='running' AND job.cancel_requested_at IS NULL
			  AND job.claim_incarnation=$3::bigint AND job.governance_payload_state='active'
			  AND turn_row.id=$4::uuid AND chat.id=$5::uuid AND chat.deleted_at IS NULL
			  AND chat.thread_id=$6::text AND turn_row.provider_turn_id=$7::text
			FOR UPDATE OF job, turn_row, chat
		`, input.ProjectID, input.JobID, input.ClaimIncarnation, input.TurnID, input.ChatID,
			input.RootThreadID, input.ProviderTurnID).Scan(&locked); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return researchdomain.ErrConflict
			}
			return err
		}
		if !locked {
			return researchdomain.ErrConflict
		}
		row, err := resolveNativeAuthorityInTx(ctx, tx, input.RootThreadID, input.ProviderTurnID)
		if err != nil {
			return err
		}
		if storedb.UUIDString(row.ProjectID) != input.ProjectID || storedb.UUIDString(row.JobID) != input.JobID ||
			storedb.UUIDString(row.ChatSessionID) != input.ChatID {
			return researchdomain.ErrConflict
		}
		if err := repository.validateLateEffectInTx(ctx, tx, lateEffectKey{
			ProjectID: input.ProjectID, JobID: input.JobID, ChatID: input.ChatID, TurnID: input.TurnID,
			ActorUserID: storedb.UUIDString(row.ActorUserID), ClaimIncarnation: input.ClaimIncarnation,
		}, func(ctx context.Context, tx pgx.Tx, _ jobs.ModelClaim) error {
			current, err := resolveNativeAuthorityInTx(ctx, tx, input.RootThreadID, input.ProviderTurnID)
			if err != nil {
				return err
			}
			if !sameNativeAuthority(row, current) {
				return researchdomain.ErrConflict
			}
			return nil
		}); err != nil {
			return err
		}
		if err := recordChatUsageInTx(ctx, tx, input.ProjectID, input.ChatID, input.JobID, input.Usage); err != nil {
			return err
		}
		if err := repository.jobs.TerminalizeUserModelJobInTx(ctx, tx, jobstore.TerminalizeUserModelJobInput{
			Kind: jobs.KindResearchChatTurn, ProjectID: input.ProjectID, JobID: input.JobID,
			ClaimIncarnation: input.ClaimIncarnation, From: jobs.StatusRunning, To: jobs.StatusSucceeded,
			Result: jobs.ResearchChatTurnResult{
				Type: jobs.KindResearchChatTurn, ChatID: input.ChatID, TurnID: input.TurnID, Response: input.Response,
			},
		}); err != nil {
			return err
		}
		return nil
	})
	return mapError(err)
}

func (repository *Repository) FinishFailure(ctx context.Context, input researchdomain.FinishFailure) error {
	message, known := researchdomain.TurnFailureCodes[input.Code]
	if input.ClaimIncarnation < 1 || !known {
		return researchdomain.ErrValidation
	}
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		var rootThreadID, providerTurnID string
		if err := tx.QueryRow(ctx, `
			SELECT chat.thread_id, turn_row.provider_turn_id
			FROM jobs AS job
			JOIN chat_turns AS turn_row ON turn_row.job_id=job.id AND turn_row.project_id=job.project_id
			JOIN chat_sessions AS chat ON chat.id=turn_row.chat_id AND chat.project_id=turn_row.project_id
			WHERE job.project_id=$1::uuid AND job.id=$2::uuid AND job.kind='research_chat_turn'
			  AND job.status='running' AND job.cancel_requested_at IS NULL
			  AND job.claim_incarnation=$3::bigint AND job.governance_payload_state='active'
			  AND turn_row.id=$4::uuid AND chat.id=$5::uuid AND chat.deleted_at IS NULL
			  AND chat.thread_id IS NOT NULL AND turn_row.provider_turn_id IS NOT NULL
			FOR UPDATE OF job, turn_row, chat
		`, input.ProjectID, input.JobID, input.ClaimIncarnation, input.TurnID, input.ChatID).
			Scan(&rootThreadID, &providerTurnID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return researchdomain.ErrConflict
			}
			return err
		}
		if err := recordChatUsageInTx(ctx, tx, input.ProjectID, input.ChatID, input.JobID, input.Usage); err != nil {
			return err
		}
		if err := repository.jobs.TerminalizeUserModelJobInTx(ctx, tx, jobstore.TerminalizeUserModelJobInput{
			Kind: jobs.KindResearchChatTurn, ProjectID: input.ProjectID, JobID: input.JobID,
			ClaimIncarnation: input.ClaimIncarnation, From: jobs.StatusRunning, To: jobs.StatusFailed,
			Error: &jobs.Error{Code: input.Code, Message: message},
		}); err != nil {
			return err
		}
		return nil
	})
	return mapError(err)
}
