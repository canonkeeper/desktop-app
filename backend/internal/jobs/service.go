package jobs

import (
	"context"
	"errors"
	"math"
	"strings"

	"canonkeeper/backend/internal/projects"
)

type Service struct {
	store        Store
	clock        Clock
	targetAccess TargetAccessResolver
}

func NewService(store Store, clock Clock, targetAccess ...TargetAccessResolver) *Service {
	if store == nil {
		panic("jobs: Store is required")
	}
	if clock == nil {
		panic("jobs: Clock is required")
	}
	if len(targetAccess) > 1 {
		panic("jobs: at most one TargetAccessResolver is allowed")
	}
	service := &Service{store: store, clock: clock}
	if len(targetAccess) == 1 {
		service.targetAccess = targetAccess[0]
	}
	return service
}

// ListProjectJobs returns the first page of Jobs in time order that the actor may read: each
// store page checks the read right only for its own candidates, so an unreadable candidate is
// skipped here and a short page means the list ended
// (specs/2026-10-28-fast-job-list-and-postgres.md, UC2).
func (s *Service) ListProjectJobs(ctx context.Context, actor Actor, projectID string, input ListProjectJobsInput) (JobPage, error) {
	role, err := s.projectReadRole(ctx, actor, projectID)
	if err != nil {
		return JobPage{}, err
	}
	limit, err := pageLimit(Page{Limit: input.Limit, Cursor: input.Cursor})
	if err != nil {
		return JobPage{}, err
	}
	cursor, err := decodeJobListCursor(input.Cursor)
	if err != nil {
		return JobPage{}, err
	}
	storeInput := ListProjectJobsStoreInput{
		ActorUserID: actor.UserID,
		ProjectID:   projectID,
		Kind:        input.Kind,
		ActiveOnly:  input.ActiveOnly,
		Limit:       limit + 1,
	}
	if cursor != nil {
		if cursor.ActorUserID != actor.UserID || cursor.ProjectID != projectID || cursor.Kind != string(input.Kind) ||
			cursor.Active != input.ActiveOnly {
			return JobPage{}, ErrValidation
		}
		storeInput.HasCursor = true
		storeInput.CursorAt = cursor.CreatedAt
		storeInput.CursorID = cursor.ID
	}
	visible := make([]Job, 0, limit+1)
	for len(visible) <= limit {
		candidates, err := s.store.ListProjectJobs(ctx, storeInput)
		if err != nil {
			return JobPage{}, err
		}
		for _, job := range candidates.Readable {
			readable, err := s.canReadJobTarget(ctx, actor, job)
			if err != nil {
				return JobPage{}, err
			}
			if readable {
				visible = append(visible, jobForActor(actor, role, job, true))
				if len(visible) > limit {
					break
				}
			}
		}
		if len(visible) > limit || candidates.Count < storeInput.Limit || candidates.Count == 0 {
			break
		}
		storeInput.HasCursor = true
		storeInput.CursorAt = candidates.LastCreatedAt
		storeInput.CursorID = candidates.LastID
	}
	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	meta := PageMeta{HasMore: hasMore}
	if hasMore && len(visible) > 0 {
		meta.NextCursor, err = encodeJobListCursor(actor.UserID, projectID, input.Kind, input.ActiveOnly, visible[len(visible)-1])
		if err != nil {
			return JobPage{}, err
		}
	}
	return JobPage{Jobs: visible, Meta: meta}, nil
}

func (s *Service) GetJob(ctx context.Context, actor Actor, jobID string) (Job, error) {
	job, role, err := s.getJobForActor(ctx, actor, jobID)
	if err != nil {
		return Job{}, err
	}
	readable, err := s.canReadJobTarget(ctx, actor, job)
	if err != nil {
		return Job{}, err
	}
	if !readable {
		return Job{}, ErrNotFound
	}
	return jobForActor(actor, role, job, true), nil
}

func (s *Service) CancelJob(ctx context.Context, actor Actor, jobID string) (CancelResult, error) {
	job, role, err := s.getJobForActor(ctx, actor, jobID)
	if err != nil {
		return CancelResult{}, err
	}
	readable, err := s.canReadJobTarget(ctx, actor, job)
	if err != nil {
		return CancelResult{}, err
	}
	if !readable {
		return s.cancelOpaqueChatTarget(ctx, actor, job)
	}
	if !canManageJob(actor, role, job) {
		return CancelResult{}, ErrForbidden
	}
	job = normalizeJob(job)
	if isTerminalStatus(job.Status) || job.Status == StatusCanceling {
		return CancelResult{Job: jobForActor(actor, role, job, true)}, nil
	}
	if !canCancelJob(actor, role, job) {
		return CancelResult{}, ErrForbidden
	}
	if requiresTargetCancelResolver(job.Kind) {
		resolver, ok := s.targetAccess.(TargetCancelResolver)
		if !ok {
			return CancelResult{}, ErrConflict
		}
		result, err := resolver.CancelJobTarget(ctx, TargetCancelInput{
			ActorUserID: actor.UserID, Job: job, TargetReadable: true, UpdatedAt: s.clock.Now(),
		})
		if err != nil {
			return CancelResult{}, err
		}
		if result.Opaque || result.Job.ID != job.ID || result.Job.ProjectID != job.ProjectID {
			return CancelResult{}, ErrConflict
		}
		result.Job = jobForActor(actor, role, result.Job, true)
		return result, nil
	}
	switch job.Status {
	case StatusQueued:
		updated, err := s.cancelTransition(ctx, actor.UserID, job.ProjectID, job.ID, StatusQueued, StatusCanceled, 0, job.Result, job.Error)
		if errors.Is(err, ErrConflict) {
			current, currentErr := s.currentJobAfterCancelConflict(ctx, actor, err, job.ID)
			return CancelResult{Job: current}, currentErr
		}
		if err != nil {
			return CancelResult{}, err
		}
		return CancelResult{Job: jobForActor(actor, role, updated, true)}, nil
	case StatusRunning:
		updated, err := s.cancelTransition(ctx, actor.UserID, job.ProjectID, job.ID, StatusRunning, StatusCanceling, progressValue(job.Progress), job.Result, job.Error)
		if errors.Is(err, ErrConflict) {
			current, currentErr := s.currentJobAfterCancelConflict(ctx, actor, err, job.ID)
			return CancelResult{Job: current}, currentErr
		}
		if err != nil {
			return CancelResult{}, err
		}
		return CancelResult{Job: jobForActor(actor, role, updated, true)}, nil
	default:
		return CancelResult{}, ErrConflict
	}
}

func (s *Service) cancelOpaqueChatTarget(ctx context.Context, actor Actor, job Job) (CancelResult, error) {
	job = normalizeJob(job)
	resolver, ok := s.targetAccess.(TargetCancelResolver)
	if !ok || job.Kind != KindResearchChatTurn || job.Status != StatusRunning || !job.CanCancel ||
		actor.UserID == "" || actor.UserID != job.CreatorUserID {
		return CancelResult{}, ErrNotFound
	}
	result, err := resolver.CancelJobTarget(ctx, TargetCancelInput{
		ActorUserID: actor.UserID, Job: job, TargetReadable: false, UpdatedAt: s.clock.Now(),
	})
	if err != nil {
		return CancelResult{}, err
	}
	if !result.Opaque || result.Job.ID != "" {
		return CancelResult{}, ErrNotFound
	}
	return result, nil
}

func (s *Service) cancelTransition(ctx context.Context, actorUserID, projectID, jobID string, from, to Status, progress float64, result Result, jobError *Error) (Job, error) {
	if strings.TrimSpace(jobID) == "" || projectID == "" || actorUserID == "" {
		return Job{}, ErrNotFound
	}
	job, err := s.store.CancelJobStatus(ctx, CancelJobStatusStoreInput{
		ActorUserID: actorUserID,
		ProjectID:   projectID,
		JobID:       jobID,
		From:        from,
		To:          to,
		Progress:    clampProgress(progress),
		Result:      result,
		Error:       jobError,
		UpdatedAt:   s.clock.Now(),
	})
	if err != nil {
		return Job{}, err
	}
	return normalizeJob(job), nil
}

func (s *Service) projectReadRole(ctx context.Context, actor Actor, projectID string) (projects.Role, error) {
	role, err := s.activeProjectRole(ctx, actor, projectID)
	if err != nil {
		return "", err
	}
	if !projects.CanReadProject(role) {
		return "", ErrForbidden
	}
	return role, nil
}

func (s *Service) activeProjectRole(ctx context.Context, actor Actor, projectID string) (projects.Role, error) {
	if actor.UserID == "" || projectID == "" {
		return "", ErrNotFound
	}
	return s.store.ActiveProjectRole(ctx, actor.UserID, projectID)
}

func (s *Service) getJobForActor(ctx context.Context, actor Actor, jobID string) (Job, projects.Role, error) {
	if actor.UserID == "" || strings.TrimSpace(jobID) == "" {
		return Job{}, "", ErrNotFound
	}
	resolved, err := s.store.ResolveJobAccessForActor(ctx, ResolveJobAccessForActorStoreInput{
		ActorUserID: actor.UserID,
		JobID:       jobID,
	})
	if err != nil {
		return Job{}, "", err
	}
	if resolved.JobID != jobID || resolved.ProjectID == "" ||
		resolved.Visibility != JobAccessVisibilityActive || !projects.CanReadProject(resolved.CurrentRole) {
		return Job{}, "", ErrNotFound
	}
	job, err := s.store.GetJob(ctx, GetJobStoreInput{
		ActorUserID: actor.UserID,
		ProjectID:   resolved.ProjectID,
		JobID:       resolved.JobID,
	})
	if err != nil {
		return Job{}, "", err
	}
	if job.ID != resolved.JobID || job.ProjectID != resolved.ProjectID {
		return Job{}, "", ErrNotFound
	}
	return job, resolved.CurrentRole, nil
}

func (s *Service) currentJobAfterCancelConflict(ctx context.Context, actor Actor, conflictErr error, jobID string) (Job, error) {
	job, role, err := s.getJobForActor(ctx, actor, jobID)
	if err != nil {
		return Job{}, conflictErr
	}
	job = normalizeJob(job)
	if isTerminalStatus(job.Status) || job.Status == StatusCanceling {
		return jobForActor(actor, role, job, true), nil
	}
	if job.Status == StatusRunning && canCancelJob(actor, role, job) {
		updated, err := s.cancelTransition(ctx, actor.UserID, job.ProjectID, job.ID, StatusRunning, StatusCanceling, progressValue(job.Progress), job.Result, job.Error)
		if errors.Is(err, ErrConflict) {
			job, currentRole, readErr := s.getJobForActor(ctx, actor, jobID)
			if readErr != nil {
				return Job{}, conflictErr
			}
			job = normalizeJob(job)
			if isTerminalStatus(job.Status) || job.Status == StatusCanceling {
				return jobForActor(actor, currentRole, job, true), nil
			}
			return Job{}, conflictErr
		}
		if err != nil {
			return Job{}, err
		}
		return jobForActor(actor, role, updated, true), nil
	}
	return Job{}, conflictErr
}

func canCancelJob(actor Actor, role projects.Role, job Job) bool {
	if !canManageJob(actor, role, job) {
		return false
	}
	if job.CanCancel {
		return true
	}
	return systemIndexingJobCanBeCanceled(role, job)
}

func systemIndexingJobCanBeCanceled(role projects.Role, job Job) bool {
	if job.CreatorUserID != "" || !projects.CanManageMembers(role) ||
		(job.Status != StatusQueued && job.Status != StatusRunning) {
		return false
	}
	switch job.Kind {
	case KindCanonTextIndex, KindChapterCards, KindSagaLayer:
		return true
	default:
		return false
	}
}

func canManageJob(actor Actor, role projects.Role, job Job) bool {
	if actor.UserID != "" && job.CreatorUserID != "" && actor.UserID == job.CreatorUserID {
		return true
	}
	return projects.CanManageMembers(role)
}

func canViewJobResult(actor Actor, role projects.Role, job Job, targetReadable bool) bool {
	if !isTerminalStatus(job.Status) {
		return false
	}
	if requiresTargetAccess(job.Kind) {
		return targetReadable
	}
	if role == projects.RoleAdmin || role == projects.RoleOwner {
		return true
	}
	if job.Result == nil {
		return false
	}
	switch job.Result.Kind() {
	case KindImport:
		if job.Kind != KindImport {
			return false
		}
		if actor.UserID != "" && job.CreatorUserID != "" && actor.UserID == job.CreatorUserID {
			return true
		}
		return projects.CanMutateProject(role)
	default:
		return false
	}
}

func jobForActor(actor Actor, role projects.Role, job Job, targetReadable bool) Job {
	job = normalizeJob(job)
	if !canViewJobResult(actor, role, job, targetReadable) {
		job.Result = nil
	}
	job.CanCancel = canCancelJob(actor, role, job)
	return job
}

func (s *Service) canReadJobTarget(ctx context.Context, actor Actor, job Job) (bool, error) {
	if !requiresTargetAccess(job.Kind) {
		return true, nil
	}
	if s.targetAccess == nil {
		return false, nil
	}
	return s.targetAccess.CanReadJobTarget(ctx, actor, job.Subject)
}

func requiresTargetAccess(kind Kind) bool {
	switch kind {
	case KindCanonTextIndex, KindResearchChatTurn, KindChapterCards, KindSagaLayer:
		return true
	default:
		return false
	}
}

func requiresTargetCancelResolver(kind Kind) bool {
	return kind == KindResearchChatTurn
}

func normalizeJob(job Job) Job {
	job.Progress = clampProgressPointer(job.Progress)
	if isTerminalStatus(job.Status) || job.Status == StatusCanceling {
		job.CanCancel = false
	}
	return job
}

func isTerminalStatus(status Status) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusCanceled, StatusExpired:
		return true
	default:
		return false
	}
}

func clampProgress(progress float64) float64 {
	if math.IsNaN(progress) || math.IsInf(progress, 0) || progress < 0 {
		return 0
	}
	if progress > 1 {
		if progress <= 100 {
			return progress / 100
		}
		return 1
	}
	return progress
}

func clampProgressPointer(progress *float64) *float64 {
	if progress == nil {
		return nil
	}
	value := clampProgress(*progress)
	return &value
}

func progressValue(progress *float64) float64 {
	if progress == nil {
		return 0
	}
	return *progress
}
