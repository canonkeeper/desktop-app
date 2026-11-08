// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
)

// Projects lists the sagas of the local author, a page at a time.
type Projects interface {
	ListProjects(ctx context.Context, actorUserID string, input projects.ListProjectsInput) (projects.ProjectPage, error)
}

// Jobs lists the Jobs of a saga still in work and cancels one the standard way (cancelJob).
type Jobs interface {
	ListProjectJobs(ctx context.Context, actor jobs.Actor, projectID string, input jobs.ListProjectJobsInput) (jobs.JobPage, error)
	CancelJob(ctx context.Context, actor jobs.Actor, jobID string) (jobs.CancelResult, error)
}

const listPage = 100

// StopChatTurns is the first step of the stop of the backend (specs/2026-11-07-desktop-app.md DA1.6): every chat turn
// of the local author still queued or running is canceled by the standard cancelJob — the author closed the window,
// a turn must not come back after the next start the way an interrupted import or index does. A Job that ended
// meanwhile is no error; the other failures are joined, and every turn is still tried.
type StopChatTurns struct {
	Projects      Projects
	Jobs          Jobs
	LocalAuthorID string
}

// Execute returns how many turns it canceled.
func (useCase StopChatTurns) Execute(ctx context.Context) (int, error) {
	actor := jobs.Actor{UserID: useCase.LocalAuthorID}
	canceled := 0
	var errs []error
	for cursor := ""; ; {
		page, err := useCase.Projects.ListProjects(ctx, useCase.LocalAuthorID, projects.ListProjectsInput{Limit: listPage, Cursor: cursor})
		if err != nil {
			return canceled, errors.Join(append(errs, err)...)
		}
		for _, project := range page.Data {
			turns, err := useCase.Jobs.ListProjectJobs(ctx, actor, project.ID, jobs.ListProjectJobsInput{
				Kind: jobs.KindResearchChatTurn, ActiveOnly: true, Limit: listPage,
			})
			if err != nil {
				errs = append(errs, err)
				continue
			}
			for _, turn := range turns.Jobs {
				_, err := useCase.Jobs.CancelJob(ctx, actor, turn.ID)
				switch {
				case err == nil:
					canceled++
				case errors.Is(err, jobs.ErrConflict), errors.Is(err, jobs.ErrNotFound):
				default:
					errs = append(errs, err)
				}
			}
		}
		if !page.Meta.HasMore || page.Meta.NextCursor == nil {
			return canceled, errors.Join(errs...)
		}
		cursor = *page.Meta.NextCursor
	}
}
