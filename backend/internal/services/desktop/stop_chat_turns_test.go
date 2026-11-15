// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	appworkdomain "canonkeeper/backend/internal/domain/appwork"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	appworkservice "canonkeeper/backend/internal/services/appwork"
	researchservice "canonkeeper/backend/internal/services/researchchat"
)

// siteOfTheStop answers the opens of permits and their closes by the rule of the site — a failure without an answer gives
// the unit back —; once silent it takes the connection of a close and never answers.
type siteOfTheStop struct {
	mu     sync.Mutex
	silent bool
	closes []string
}

func (*siteOfTheStop) LinkMark([]byte) string { return "mark-of-the-stop" }
func (*siteOfTheStop) OpenPermit(_ context.Context, kind appworkdomain.Kind, ref string) (appworkdomain.Permit, error) {
	return appworkdomain.Permit{ID: "permit-" + ref[:8], Kind: kind, State: appworkdomain.StateOpen}, nil
}
func (*siteOfTheStop) ChargeUnit(context.Context, string, string) error         { return nil }
func (*siteOfTheStop) ReturnUnit(context.Context, string, string) (bool, error) { return true, nil }
func (site *siteOfTheStop) ClosePermit(ctx context.Context, _ string, outcome appworkdomain.Outcome) (appworkdomain.Permit, error) {
	site.mu.Lock()
	silent := site.silent
	site.closes = append(site.closes, "close "+string(outcome))
	site.mu.Unlock()
	if silent {
		<-ctx.Done()
		return appworkdomain.Permit{}, ctx.Err()
	}
	return appworkdomain.Permit{State: appworkdomain.StateClosed, Outcome: outcome, Returned: outcome == appworkdomain.OutcomeFailed}, nil
}

func (site *siteOfTheStop) closed() []string {
	site.mu.Lock()
	defer site.mu.Unlock()
	return slices.Clone(site.closes)
}

type rowsOfTheStop struct {
	mu   sync.Mutex
	rows map[string]*appworkservice.Row
}

func (rows *rowsOfTheStop) Work(_ context.Context, jobID string) (appworkservice.Row, bool, error) {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	row, ok := rows.rows[jobID]
	if !ok {
		return appworkservice.Row{}, false, nil
	}
	return *row, true, nil
}
func (*rowsOfTheStop) KeepNumber(context.Context, string, int) error { return nil }
func (rows *rowsOfTheStop) KeepReturned(_ context.Context, jobID string, returned appworkdomain.Returned) error {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	rows.rows[jobID].Returned = returned
	return nil
}

func (*rowsOfTheStop) Pending(context.Context) ([]string, error) { return nil, nil }

func (*rowsOfTheStop) Answering(context.Context, string) (int64, error) { return 0, nil }

func (rows *rowsOfTheStop) returned(jobID string) appworkdomain.Returned {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	return rows.rows[jobID].Returned
}

// turnsOfTheStop are the chat turns of a saga in work, canceled the standard way: the cancel of a turn goes through the cancel
// of the chat (researchchat.CancelTurn), which ends the work of the turn once its cancel is written — a queued turn at once,
// a running one after the interrupt of its thread.
type turnsOfTheStop struct {
	mu       sync.Mutex
	turns    []jobs.Job
	canceler *researchservice.CancelTurn
	canceled []string
}

func (*turnsOfTheStop) ListProjects(ctx context.Context, _ string, _ projects.ListProjectsInput) (projects.ProjectPage, error) {
	if err := ctx.Err(); err != nil {
		return projects.ProjectPage{}, err
	}
	return projects.ProjectPage{Data: []projects.ProjectRecord{{ID: "saga-north"}}}, nil
}

func (turns *turnsOfTheStop) ListProjectJobs(ctx context.Context, _ jobs.Actor, _ string, _ jobs.ListProjectJobsInput) (jobs.JobPage, error) {
	if err := ctx.Err(); err != nil {
		return jobs.JobPage{}, err
	}
	turns.mu.Lock()
	defer turns.mu.Unlock()
	return jobs.JobPage{Jobs: slices.Clone(turns.turns)}, nil
}

func (turns *turnsOfTheStop) CancelJob(ctx context.Context, actor jobs.Actor, jobID string) (jobs.CancelResult, error) {
	if err := ctx.Err(); err != nil {
		return jobs.CancelResult{}, err
	}
	turns.mu.Lock()
	at := slices.IndexFunc(turns.turns, func(job jobs.Job) bool { return job.ID == jobID })
	job := turns.turns[at]
	turns.mu.Unlock()
	result, err := turns.canceler.CancelJobTarget(ctx, jobs.TargetCancelInput{ActorUserID: actor.UserID, Job: job, TargetReadable: true})
	if err == nil {
		turns.mu.Lock()
		turns.canceled = append(turns.canceled, jobID)
		turns.mu.Unlock()
	}
	return result, err
}

// cancelsOfTheChat are the cancels of the chat as its repository writes them: a queued turn is canceled at once, a running
// one is canceling until the interrupt of its thread, then canceled.
type cancelsOfTheChat struct{}

func (cancelsOfTheChat) BeginTargetCancel(_ context.Context, input jobs.TargetCancelInput) (researchdomain.CancelTarget, jobs.Job, error) {
	updated := input.Job
	target := researchdomain.CancelTarget{ProjectID: "saga-north", JobID: input.Job.ID, ChatID: "chat-" + input.Job.ID, TargetReadable: true}
	if input.Job.Status == jobs.StatusRunning {
		updated.Status, target.NeedsInterrupt, target.ClaimIncarnation = jobs.StatusCanceling, true, 1
	} else {
		updated.Status = jobs.StatusCanceled
	}
	return target, updated, nil
}

func (cancelsOfTheChat) CancelTargetForJob(context.Context, jobs.Job) (researchdomain.CancelTarget, error) {
	return researchdomain.CancelTarget{}, researchdomain.ErrConflict
}

func (cancelsOfTheChat) CompleteCancel(_ context.Context, target researchdomain.CancelTarget) (jobs.Job, error) {
	return jobs.Job{ID: target.JobID, ProjectID: target.ProjectID, Kind: jobs.KindResearchChatTurn, Status: jobs.StatusCanceled}, nil
}

func (cancelsOfTheChat) CanReadJobTarget(context.Context, jobs.Actor, jobs.Subject) (bool, error) {
	return true, nil
}

// theCountOfTheStop is the count of the work of the account over the site of the test, the turns opened at their start and
// those running taken by a worker, and the cancel of the chat that ends their work.
func theCountOfTheStop(t *testing.T, site *siteOfTheStop, queued, running []string) (*appworkservice.Work, *rowsOfTheStop, *turnsOfTheStop) {
	t.Helper()
	rows := &rowsOfTheStop{rows: map[string]*appworkservice.Row{}}
	work := appworkservice.New(site, rows, []byte("salt-of-the-stop"), nil, nil)
	turns := &turnsOfTheStop{}
	turns.canceler = researchservice.NewCancelTurn(cancelsOfTheChat{}, func(context.Context, researchservice.RootSelector) error { return nil }).WithWork(work)
	for _, job := range append(slices.Clone(queued), running...) {
		rows.rows[job] = &appworkservice.Row{Kind: appworkdomain.KindTurn, Source: appworkdomain.SourceAccount, Link: "mark-of-the-stop"}
		if err := work.Open(context.Background(), quota.Work{Kind: quota.WorkTurn, JobID: job, UserID: "author", ChatID: "chat-" + job}); err != nil {
			t.Fatal(err)
		}
		status := jobs.StatusQueued
		if slices.Contains(running, job) {
			status = jobs.StatusRunning
			if code, err := work.Begin(context.Background(), researchdomain.TurnRun{JobID: job, ChatID: "chat-" + job}); code != "" || err != nil {
				t.Fatal(code, err)
			}
		}
		turns.turns = append(turns.turns, jobs.Job{ID: job, ProjectID: "saga-north", Kind: jobs.KindResearchChatTurn, Status: status})
	}
	return work, rows, turns
}

// specs/2026-11-13-desk-account-ai.md AI2.7, DT3.8, Decision log «остановка приложения закрывает ход как срыв»: the stop of the
// app cancels its turns the standard way, and the cancel carries the stop to the count of the work. A turn no worker took
// asked no model: no close goes to the site — its lease gives the unit back outside the ceiling of the returns of the app —
// and its question is pending; a turn that ran is closed failed, never canceled. The author's own stop of a turn is canceled.
func TestTheStopOfTheAppEndsItsTurnsAsAStopNotAsTheAuthor(t *testing.T) {
	site := &siteOfTheStop{}
	work, rows, turns := theCountOfTheStop(t, site, []string{"turn-queued"}, []string{"turn-running"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	canceled, err := StopChatTurns{Projects: turns, Jobs: turns, LocalAuthorID: "author", Counted: work}.Execute(ctx)
	if err != nil || canceled != 2 {
		t.Fatalf("the stop canceled %d %v", canceled, err)
	}
	if closes := site.closed(); !slices.Equal(closes, []string{"close failed"}) {
		t.Fatalf("the stop sent %v; want one close failed of the running turn", closes)
	}
	if rows.returned("turn-queued") != appworkdomain.ReturnedPending || rows.returned("turn-running") != appworkdomain.ReturnedPending {
		t.Fatalf("the questions: queued %q, running %q", rows.returned("turn-queued"), rows.returned("turn-running"))
	}

	_, _, ofTheAuthor := theCountOfTheStop(t, site, nil, []string{"turn-of-the-author"})
	before := len(site.closed())
	if _, err := ofTheAuthor.CancelJob(context.Background(), jobs.Actor{UserID: "author"}, "turn-of-the-author"); err != nil {
		t.Fatal(err)
	}
	if closes := site.closed()[before:]; !slices.Equal(closes, []string{"close canceled"}) {
		t.Fatalf("the author's stop sent %v", closes)
	}
}

// AI2.7, DA1.6: with two running turns and a site that takes the connection and never answers, the closes of the stop go at
// once within half of its term and the cancels under the stop wait for the site no longer: both turns are canceled — none
// comes back after the next start — within the term of the stop, and their questions are pending.
func TestTheStopWithTwoTurnsAndASilentSiteCancelsBoth(t *testing.T) {
	site := &siteOfTheStop{}
	work, rows, turns := theCountOfTheStop(t, site, nil, []string{"turn-one", "turn-two"})
	site.mu.Lock()
	site.silent = true
	site.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // run.go stopTurnsTimeout
	defer cancel()
	began := time.Now()
	canceled, err := StopChatTurns{Projects: turns, Jobs: turns, LocalAuthorID: "author", Counted: work}.Execute(ctx)
	took := time.Since(began)
	if err != nil || canceled != 2 || took >= 5*time.Second {
		t.Fatalf("the stop canceled %d %v (%v) in %s", canceled, err, turns.canceled, took.Round(100*time.Millisecond))
	}
	if rows.returned("turn-one") != appworkdomain.ReturnedPending || rows.returned("turn-two") != appworkdomain.ReturnedPending {
		t.Fatalf("the questions: %q %q", rows.returned("turn-one"), rows.returned("turn-two"))
	}
}
