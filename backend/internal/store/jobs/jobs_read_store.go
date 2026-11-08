package jobstore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

func (s *Store) ActiveProjectRole(ctx context.Context, actorUserID, projectID string) (projects.Role, error) {
	actorUserUUID, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return "", jobs.ErrNotFound
	}
	projectUUID, err := storedb.ParseUUID(projectID)
	if err != nil {
		return "", jobs.ErrNotFound
	}
	role, err := sqlc.New(s.Pool).GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{
		ProjectID:   projectUUID,
		ActorUserID: actorUserUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", jobs.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return projects.Role(role), nil
}

// ListProjectJobs reads one page of candidates whose read right SQL checks only for that page
// (specs/2026-10-28-fast-job-list-and-postgres.md, UC2). An unreadable candidate is only a
// position in the list: it is not decoded, its payload may be a tombstone.
func (s *Store) ListProjectJobs(ctx context.Context, input jobs.ListProjectJobsStoreInput) (jobs.ProjectJobCandidates, error) {
	actorUserUUID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return jobs.ProjectJobCandidates{}, jobs.ErrNotFound
	}
	projectUUID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return jobs.ProjectJobCandidates{}, jobs.ErrNotFound
	}
	params := sqlc.ListProjectJobsParams{
		ProjectID:   projectUUID,
		ActorUserID: actorUserUUID,
		Kind:        string(input.Kind),
		ActiveOnly:  input.ActiveOnly,
		LimitCount:  jobListLimit(input.Limit),
	}
	if input.HasCursor {
		cursorID, err := storedb.ParseUUID(input.CursorID)
		if err != nil {
			return jobs.ProjectJobCandidates{}, jobs.ErrValidation
		}
		params.HasCursor = true
		params.CursorCreatedAt = storedb.Timestamptz(input.CursorAt)
		params.CursorID = cursorID
	}
	rows, err := sqlc.New(s.Pool).ListProjectJobs(ctx, params)
	if err != nil {
		return jobs.ProjectJobCandidates{}, err
	}
	page := jobs.ProjectJobCandidates{Count: len(rows)}
	for _, row := range rows {
		if !row.Readable {
			continue
		}
		job, err := JobFromSQLJob(row.Job)
		if err != nil {
			return jobs.ProjectJobCandidates{}, err
		}
		page.Readable = append(page.Readable, job)
	}
	if len(rows) > 0 {
		last := rows[len(rows)-1].Job
		page.LastCreatedAt = last.CreatedAt.Time
		page.LastID = storedb.UUIDString(last.ID)
	}
	return page, nil
}

func (s *Store) ResolveJobAccessForActor(ctx context.Context, input jobs.ResolveJobAccessForActorStoreInput) (jobs.ResolvedJobAccess, error) {
	jobID, err := storedb.ParseUUID(input.JobID)
	if err != nil {
		return jobs.ResolvedJobAccess{}, jobs.ErrNotFound
	}
	actorUserUUID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return jobs.ResolvedJobAccess{}, jobs.ErrNotFound
	}
	row, err := sqlc.New(s.Pool).ResolveJobAccessForActor(ctx, sqlc.ResolveJobAccessForActorParams{
		JobID:       jobID,
		ActorUserID: actorUserUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.ResolvedJobAccess{}, jobs.ErrNotFound
	}
	if err != nil {
		return jobs.ResolvedJobAccess{}, err
	}
	role := projects.Role(row.CurrentRole)
	if !projects.CanReadProject(role) || row.Visibility != string(jobs.JobAccessVisibilityActive) {
		return jobs.ResolvedJobAccess{}, jobs.ErrNotFound
	}
	return jobs.ResolvedJobAccess{
		ProjectID:   storedb.UUIDString(row.ProjectID),
		JobID:       storedb.UUIDString(row.JobID),
		CurrentRole: role,
		Visibility:  jobs.JobAccessVisibility(row.Visibility),
	}, nil
}

func (s *Store) GetJob(ctx context.Context, input jobs.GetJobStoreInput) (jobs.Job, error) {
	actorUserUUID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return jobs.Job{}, jobs.ErrNotFound
	}
	projectUUID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return jobs.Job{}, jobs.ErrNotFound
	}
	jobUUID, err := storedb.ParseUUID(input.JobID)
	if err != nil {
		return jobs.Job{}, jobs.ErrNotFound
	}
	row, err := sqlc.New(s.Pool).GetJob(ctx, sqlc.GetJobParams{
		JobID: jobUUID, ProjectID: projectUUID, ActorUserID: actorUserUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrNotFound
	}
	if err != nil {
		return jobs.Job{}, err
	}
	job, err := JobFromSQLJob(row)
	if err != nil || job.Kind != jobs.KindResearchChatTurn || job.Status != jobs.StatusQueued {
		return job, err
	}
	// A waiting question shows its place in the fair queue (quota spec R10.3).
	position, err := sqlc.New(s.Pool).ChatTurnQueuePosition(ctx, jobUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return job, nil
	}
	if err != nil {
		return jobs.Job{}, err
	}
	job.QueuePosition = &position
	return job, nil
}

func jobListLimit(limit int) int32 {
	if limit <= 0 {
		return 50
	}
	if limit > 101 {
		return 101
	}
	return int32(limit)
}
