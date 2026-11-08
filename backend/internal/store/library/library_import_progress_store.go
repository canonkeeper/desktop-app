package librarystore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

func (s *Store) MarkImportParsingForClaim(ctx context.Context, claim jobs.WorkerClaim) error {
	return s.advanceImportProgressForClaim(ctx, claim, jobs.ImportPhaseParsing, 0)
}

func (s *Store) MarkImportSavingForClaim(ctx context.Context, claim jobs.WorkerClaim, chapters int) error {
	if chapters < 1 {
		return library.ErrValidation
	}
	return s.advanceImportProgressForClaim(ctx, claim, jobs.ImportPhaseSaving, chapters)
}

// advanceImportProgressForClaim меняет только две быстрые детерминированные фазы.
// Точный claim и текущее JSON-состояние проверяются в одной транзакции.
func (s *Store) advanceImportProgressForClaim(
	ctx context.Context,
	claim jobs.WorkerClaim,
	phase jobs.ImportPhase,
	chapters int,
) error {
	if phase != jobs.ImportPhaseParsing && phase != jobs.ImportPhaseSaving {
		return library.ErrValidation
	}
	projectID, jobID, incarnation, err := jobstore.WorkerClaimSQLValues(claim)
	if err != nil {
		return library.ErrConflict
	}
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		locked, err := queries.LockImportProgressForClaim(ctx, sqlc.LockImportProgressForClaimParams{
			ProjectID: projectID, JobID: jobID, ClaimIncarnation: incarnation,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return library.ErrConflict
		}
		if err != nil {
			return err
		}
		current, err := jobstore.ParseImportProgress(locked.ProgressJson, jobs.StatusRunning)
		if err != nil || !locked.TransitionedAt.Valid {
			return library.ErrConflict
		}
		next := *current
		transitionedAt := locked.TransitionedAt.Time
		switch phase {
		case jobs.ImportPhaseParsing:
			if current.Phase == jobs.ImportPhaseParsing {
				return nil
			}
			if current.Phase != jobs.ImportPhaseQueued {
				return library.ErrConflict
			}
			next.Phase = phase
			next.ParsingStartedAt = &transitionedAt
		case jobs.ImportPhaseSaving:
			if current.Phase == jobs.ImportPhaseSaving {
				if current.TotalChapters != nil && *current.TotalChapters == chapters {
					return nil
				}
				return library.ErrConflict
			}
			if current.Phase != jobs.ImportPhaseParsing {
				return library.ErrConflict
			}
			noneSaved := 0
			next.Phase = phase
			next.ParsedChapters = &chapters
			next.TotalChapters = &chapters
			next.ParsedAt = &transitionedAt
			next.SavingStartedAt = &transitionedAt
			next.SavedChapters = &noneSaved
		}
		nextJSON, err := jobstore.MarshalImportProgress(next, jobs.StatusRunning)
		if err != nil {
			return library.ErrConflict
		}
		updated, err := queries.UpdateImportProgressForClaim(ctx, sqlc.UpdateImportProgressForClaimParams{
			ProgressJson: nextJSON, TransitionedAt: locked.TransitionedAt,
			ProjectID: projectID, JobID: jobID, ClaimIncarnation: incarnation,
			ExpectedProgressJson: locked.ProgressJson,
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return library.ErrConflict
		}
		return nil
	})
	return err
}
