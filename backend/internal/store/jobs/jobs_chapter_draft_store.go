package jobstore

import (
	"context"
	"time"

	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// ChapterDraftJobInput is the Job writing a plan or a chapter into a draft.
type ChapterDraftJobInput struct {
	JobID         string
	Subject       jobs.ChapterDraftSubject
	CreatorUserID string
	CreatedAt     time.Time
}

// CreateChapterDraftJobInTx queues the Job in the caller's transaction
// (specs/2026-11-01-writer-assistant-index.md, R7.3): an interactive Job of the author, who may stop it (W4.9).
// Its source owner names only the project, as that of a drawing: the fence of a book or a chapter selects
// owners by their references, so a deleted book leaves it, and deleting the project removes it.
func (s *Store) CreateChapterDraftJobInTx(ctx context.Context, tx pgx.Tx, input ChapterDraftJobInput) (jobs.Job, error) {
	if s == nil || tx == nil || input.CreatedAt.IsZero() || input.Subject.Type != jobs.KindChapterDraft {
		return jobs.Job{}, jobs.ErrValidation
	}
	subjectJSON, err := jobs.EncodeSubject(jobs.KindChapterDraft, input.Subject)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	return s.createProjectJobInTx(ctx, tx, input.Subject.ProjectID, input.JobID, input.CreatorUserID,
		func(queries *sqlc.Queries, row projectJobRow) (sqlc.Job, error) {
			return queries.CreateChapterDraftJob(ctx, sqlc.CreateChapterDraftJobParams{
				ID: row.jobID, SubjectJson: subjectJSON, CreatorUserID: row.creatorID,
				CreatedAt: storedb.Timestamptz(input.CreatedAt), ArtifactSourceOwnerID: row.ownerID, ProjectID: row.projectID,
			})
		})
}
