package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"

	"github.com/go-chi/chi/v5"
)

func TestCancelHandlerAllowsProjectOwnerToCancelQueuedSystemTextIndex(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("owner-1", "project-1")] = projects.RoleOwner
	store.jobs["job-1"] = Job{
		ID: "job-1", ProjectID: "project-1", Kind: KindCanonTextIndex,
		Status: StatusQueued, CanCancel: false,
		Subject: CanonTextIndexSubject{
			Type: KindCanonTextIndex, ProjectID: "project-1",
			SourceView: governance.SourceViewPublished, CatalogRevision: 2,
		},
		CreatedAt: jobTestNow(), UpdatedAt: jobTestNow(),
	}
	resolver := &targetCancelResolverForTest{readable: true}
	service := NewService(store, fixedJobClock{now: jobTestNow()}, resolver)
	handlers := NewHandlers(service, fakeJobHandlerCurrentUser{user: auth.User{ID: "owner-1"}}, []string{"https://app.example.test"})
	router := chi.NewRouter()
	handlers.Mount(router)
	req := httptest.NewRequest(http.MethodPost, "/jobs/job-1/cancel", nil)
	authorizeJobRequest(req)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response struct {
		Status    Status `json:"status"`
		CanCancel bool   `json:"canCancel"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Status != StatusCanceled || response.CanCancel {
		t.Fatalf("response = %+v, want canceled and not cancellable", response)
	}
	second := httptest.NewRequest(http.MethodPost, "/jobs/job-1/cancel", nil)
	authorizeJobRequest(second)
	secondRecorder := httptest.NewRecorder()
	router.ServeHTTP(secondRecorder, second)
	if secondRecorder.Code != http.StatusOK {
		t.Fatalf("second cancel status = %d, want %d", secondRecorder.Code, http.StatusOK)
	}
}

func TestServiceDelegatesVisibleUserModelCancel(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-1", "project-1")] = projects.RoleEditor
	store.jobs["job-1"] = researchChatJobForCancel(StatusQueued, "user-1")
	genericCancelCalls := 0
	store.beforeCancelJobStatus = func(CancelJobStatusStoreInput) { genericCancelCalls++ }
	resolver := &targetCancelResolverForTest{readable: true}
	updated := store.jobs["job-1"]
	updated.Status = StatusCanceled
	updated.CanCancel = false
	resolver.result = CancelResult{Job: updated}
	service := NewService(store, fixedJobClock{now: jobTestNow()}, resolver)

	result, err := service.CancelJob(context.Background(), Actor{UserID: "user-1"}, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Opaque || result.Status != StatusCanceled || resolver.calls != 1 ||
		!resolver.input.TargetReadable || genericCancelCalls != 0 {
		t.Fatalf("result = %+v, resolver = %+v, generic calls = %d", result, resolver, genericCancelCalls)
	}
}

func TestServiceReturnsCancelingVisibleTargetCancel(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-1", "project-1")] = projects.RoleEditor
	store.jobs["job-1"] = researchChatJobForCancel(StatusRunning, "user-1")
	resolver := &targetCancelResolverForTest{readable: true}
	updated := store.jobs["job-1"]
	updated.Status = StatusCanceling
	updated.CanCancel = false
	resolver.result = CancelResult{Job: updated}
	service := NewService(store, fixedJobClock{now: jobTestNow()}, resolver)

	result, err := service.CancelJob(context.Background(), Actor{UserID: "user-1"}, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCanceling {
		t.Fatalf("result = %+v, want canceling", result)
	}
}

func TestServiceReturnsOpaqueCancelForDowngradedChatCreator(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-1", "project-1")] = projects.RoleViewer
	store.jobs["job-1"] = researchChatJobForCancel(StatusRunning, "user-1")
	resolver := &targetCancelResolverForTest{readable: false, result: CancelResult{Opaque: true}}
	service := NewService(store, fixedJobClock{now: jobTestNow()}, resolver)

	result, err := service.CancelJob(context.Background(), Actor{UserID: "user-1"}, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Opaque || result.Job.ID != "" || resolver.calls != 1 || resolver.input.TargetReadable ||
		resolver.input.ActorUserID != "user-1" || !resolver.input.UpdatedAt.Equal(jobTestNow()) {
		t.Fatalf("result = %+v, resolver = %+v", result, resolver)
	}
}

func TestServiceConcealsOpaqueCancelFromNonCreator(t *testing.T) {
	store := newJobStoreForTest()
	store.roles[projectActorKey("user-2", "project-1")] = projects.RoleViewer
	store.jobs["job-1"] = researchChatJobForCancel(StatusRunning, "user-1")
	resolver := &targetCancelResolverForTest{readable: false, result: CancelResult{Opaque: true}}
	service := NewService(store, fixedJobClock{now: jobTestNow()}, resolver)

	_, err := service.CancelJob(context.Background(), Actor{UserID: "user-2"}, "job-1")
	if !errors.Is(err, ErrNotFound) || resolver.calls != 0 {
		t.Fatalf("error = %v, resolver calls = %d", err, resolver.calls)
	}
}

func TestCancelHandlerWritesEmptyOpaqueResponse(t *testing.T) {
	service := &fakeJobHandlerService{cancelResult: CancelResult{Opaque: true}}
	handlers := NewHandlers(service, fakeJobHandlerCurrentUser{user: auth.User{ID: "user-1"}}, []string{"https://app.example.test"})
	router := chi.NewRouter()
	handlers.Mount(router)
	req := httptest.NewRequest(http.MethodPost, "/jobs/job-1/cancel", nil)
	authorizeJobRequest(req)
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status = %d, body = %q, cache = %q", recorder.Code, recorder.Body.String(), recorder.Header().Get("Cache-Control"))
	}
}

func researchChatJobForCancel(status Status, creator string) Job {
	return Job{
		ID: "job-1", ProjectID: "project-1", Kind: KindResearchChatTurn, Status: status,
		Subject: ResearchChatTurnSubject{
			Type: KindResearchChatTurn, ProjectID: "project-1", ChatID: "chat-1", TurnID: "turn-1",
			SourceView: governance.SourceViewDraft, Scope: "project",
		},
		CanCancel: status == StatusQueued || status == StatusRunning, CreatorUserID: creator,
		CreatedAt: jobTestNow(), UpdatedAt: jobTestNow(),
	}
}

type targetCancelResolverForTest struct {
	readable  bool
	result    CancelResult
	err       error
	calls     int
	readCalls int
	input     TargetCancelInput
}

func (r *targetCancelResolverForTest) CanReadJobTarget(context.Context, Actor, Subject) (bool, error) {
	r.readCalls++
	return r.readable, nil
}

func (r *targetCancelResolverForTest) CancelJobTarget(_ context.Context, input TargetCancelInput) (CancelResult, error) {
	r.calls++
	r.input = input
	return r.result, r.err
}
