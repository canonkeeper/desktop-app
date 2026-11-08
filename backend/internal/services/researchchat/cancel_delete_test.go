package researchchat

import (
	"context"
	"testing"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
)

type cancellationRepositoryStub struct {
	target    researchdomain.CancelTarget
	completed int
	job       jobs.Job
}

func (stub *cancellationRepositoryStub) CompleteCancel(context.Context, researchdomain.CancelTarget) (jobs.Job, error) {
	stub.completed++
	stub.job.Status = jobs.StatusCanceled
	return stub.job, nil
}

func (stub *cancellationRepositoryStub) BeginTargetCancel(context.Context, jobs.TargetCancelInput) (researchdomain.CancelTarget, jobs.Job, error) {
	return stub.target, stub.job, nil
}

func (stub *cancellationRepositoryStub) CancelTargetForJob(context.Context, jobs.Job) (researchdomain.CancelTarget, error) {
	return stub.target, nil
}

func (stub *cancellationRepositoryStub) CanReadJobTarget(context.Context, jobs.Actor, jobs.Subject) (bool, error) {
	return true, nil
}

func TestCancelQueuedTurnDoesNotInterruptReusableSessionRoot(t *testing.T) {
	repository := &cancellationRepositoryStub{target: researchdomain.CancelTarget{
		ProjectID: "project-1", JobID: "job-1", ChatID: "chat-1", RootThreadID: "root-1",
	}, job: jobs.Job{ID: "job-1", ProjectID: "project-1", Kind: jobs.KindResearchChatTurn, Status: jobs.StatusCanceled}}
	interrupted := false
	interrupt := func(context.Context, RootSelector) error {
		interrupted = true
		return nil
	}
	result, err := NewCancelTurn(repository, interrupt).CancelJobTarget(context.Background(), jobs.TargetCancelInput{
		ActorUserID: "user-1", Job: repository.job, TargetReadable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if interrupted || repository.completed != 0 || result.Job.Status != jobs.StatusCanceled {
		t.Fatalf("queued cancel = (interrupted=%v, completed=%d, result=%+v)", interrupted, repository.completed, result)
	}
}

func TestCancelJobTargetReturnsCanceledAfterExactNativeStop(t *testing.T) {
	target := researchdomain.CancelTarget{
		ProjectID: "project-1", JobID: "job-1", ChatID: "chat-1", RootThreadID: "root-1",
		ClaimIncarnation: 4, NeedsInterrupt: true,
	}
	repository := &cancellationRepositoryStub{
		target: target,
		job:    jobs.Job{ID: target.JobID, ProjectID: target.ProjectID, Kind: jobs.KindResearchChatTurn, Status: jobs.StatusCanceling},
	}
	var selector RootSelector
	useCase := NewCancelTurn(repository, func(_ context.Context, value RootSelector) error {
		selector = value
		return nil
	})
	result, err := useCase.CancelJobTarget(context.Background(), jobs.TargetCancelInput{
		ActorUserID: "user-1", Job: repository.job, TargetReadable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Opaque || result.Job.Status != jobs.StatusCanceled || selector.ThreadID != target.RootThreadID ||
		repository.completed != 1 {
		t.Fatalf("target cancel = (%+v, selector=%+v, completed=%d)", result, selector, repository.completed)
	}
}

func TestCancelJobTargetPreservesOpaqueUnreadableResult(t *testing.T) {
	repository := &cancellationRepositoryStub{
		target: researchdomain.CancelTarget{ProjectID: "project-1", JobID: "job-1", ChatID: "chat-1"},
		job:    jobs.Job{ID: "job-1", ProjectID: "project-1", Kind: jobs.KindResearchChatTurn, Status: jobs.StatusCanceled},
	}
	result, err := NewCancelTurn(repository, func(context.Context, RootSelector) error { return nil }).
		CancelJobTarget(context.Background(), jobs.TargetCancelInput{
			ActorUserID: "user-1", Job: repository.job, TargetReadable: false,
		})
	if err != nil || !result.Opaque || result.Job.ID != "" {
		t.Fatalf("opaque cancel = (%+v, %v)", result, err)
	}
}

type deletionRepositoryStub struct {
	target researchdomain.DeleteTarget
	events *[]string
}

func (stub *deletionRepositoryStub) MarkDeleted(context.Context, researchdomain.Actor, string, string) (researchdomain.DeleteTarget, error) {
	*stub.events = append(*stub.events, "mark")
	return stub.target, nil
}

func TestDeleteSessionDelegatesGovernedRootDeletionAfterTombstone(t *testing.T) {
	events := []string{}
	target := researchdomain.DeleteTarget{ProjectID: "project-1", ChatID: "chat-1", RootThreadID: "root-1"}
	repository := &deletionRepositoryStub{target: target, events: &events}
	governedDelete := func(_ context.Context, value researchdomain.DeleteTarget) error {
		events = append(events, "native-delete")
		if value != target {
			t.Fatalf("delete target = %+v", value)
		}
		return nil
	}

	if err := NewDeleteSession(repository, governedDelete).Execute(context.Background(), researchdomain.Actor{UserID: "user-1"}, target.ChatID); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0] != "mark" || events[1] != "native-delete" {
		t.Fatalf("delete events = %v", events)
	}
}
