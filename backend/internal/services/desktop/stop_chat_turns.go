// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"time"

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
	// Counted closes the permits of the turns a site counts as failed before they are canceled
	// (specs/2026-11-13-desk-account-ai.md AI2.7): the stop of the app is a failure, not the author's stop, and whether the
	// question comes back the site decides by what it saw. Nil — no site counts the turns.
	Counted CountedTurns
}

// CountedTurns are the turns a site counts.
type CountedTurns interface {
	// Stopping is the context the stop cancels the turns under: their end is the stop of the app, not the author's stop.
	Stopping(ctx context.Context) context.Context
	// StopRunning closes the turns that run, all at once within the term of ctx.
	StopRunning(ctx context.Context)
}

// closeShare bounds the closes of the counted turns when the stop has no term of its own.
const closeShare = 2 * time.Second

// Execute returns how many turns it canceled. The closes of the counted turns take at most half of the term left, so a
// site that takes the connection and never answers leaves the cancel its time; the cancels go under the context of the stop.
func (useCase StopChatTurns) Execute(ctx context.Context) (int, error) {
	if useCase.Counted != nil {
		ctx = useCase.Counted.Stopping(ctx)
		term := closeShare
		if deadline, ok := ctx.Deadline(); ok {
			term = time.Until(deadline) / 2
		}
		closeCtx, cancel := context.WithTimeout(ctx, term)
		useCase.Counted.StopRunning(closeCtx)
		cancel()
	}
	actor := jobs.Actor{UserID: useCase.LocalAuthorID}
	canceled := 0
	err := chatTurns(ctx, useCase.Projects, useCase.Jobs, useCase.LocalAuthorID, func(turn jobs.Job) error {
		_, err := useCase.Jobs.CancelJob(ctx, actor, turn.ID)
		switch {
		case err == nil:
			canceled++
		case errors.Is(err, jobs.ErrConflict), errors.Is(err, jobs.ErrNotFound):
		default:
			return err
		}
		return nil
	})
	return canceled, err
}

// TurnsInWork lists the Jobs of a saga still in work.
type TurnsInWork interface {
	ListProjectJobs(ctx context.Context, actor jobs.Actor, projectID string, input jobs.ListProjectJobsInput) (jobs.JobPage, error)
}

// CountChatTurns counts the chat turns of the local author still queued or running (specs/2026-11-13-desk-updates.md
// UB2.1): an update of the app waits until there is none — the same turns the stop would cancel.
type CountChatTurns struct {
	Projects      Projects
	Jobs          TurnsInWork
	LocalAuthorID string
}

func (useCase CountChatTurns) Execute(ctx context.Context) (int, error) {
	count := 0
	err := chatTurns(ctx, useCase.Projects, useCase.Jobs, useCase.LocalAuthorID, func(jobs.Job) error {
		count++
		return nil
	})
	return count, err
}

// chatTurns visits every chat turn of the local author still in work, in every saga; the failures are joined, and every
// turn is still visited.
func chatTurns(ctx context.Context, sagas Projects, work TurnsInWork, author string, visit func(jobs.Job) error) error {
	actor := jobs.Actor{UserID: author}
	var errs []error
	for cursor := ""; ; {
		page, err := sagas.ListProjects(ctx, author, projects.ListProjectsInput{Limit: listPage, Cursor: cursor})
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		for _, project := range page.Data {
			turns, err := work.ListProjectJobs(ctx, actor, project.ID, jobs.ListProjectJobsInput{
				Kind: jobs.KindResearchChatTurn, ActiveOnly: true, Limit: listPage,
			})
			if err != nil {
				errs = append(errs, err)
				continue
			}
			for _, turn := range turns.Jobs {
				if err := visit(turn); err != nil {
					errs = append(errs, err)
				}
			}
		}
		if !page.Meta.HasMore || page.Meta.NextCursor == nil {
			return errors.Join(errs...)
		}
		cursor = *page.Meta.NextCursor
	}
}
