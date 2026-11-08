package agenttimeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/agenttimeline"
	"canonkeeper/backend/internal/jobs"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
)

const timelineTestOrigin = "https://canon.test"

type currentUserStub struct {
	user auth.User
	err  error
}

func (stub currentUserStub) CurrentUser(context.Context, string) (auth.User, error) {
	return stub.user, stub.err
}

type jobAccessStub struct {
	job jobs.Job
	err error
}

func (stub jobAccessStub) GetJob(context.Context, jobs.Actor, string) (jobs.Job, error) {
	return stub.job, stub.err
}

type liveTimelineStub struct {
	mu                        sync.Mutex
	calls                     int
	snapshots                 []domain.Snapshot
	updates                   chan domain.Update
	subscribed                bool
	updateDuringFirstSnapshot bool
}

func (stub *liveTimelineStub) Snapshot(context.Context, string, string) (domain.Snapshot, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	index := stub.calls
	if index >= len(stub.snapshots) {
		index = len(stub.snapshots) - 1
	}
	if stub.calls == 0 && stub.updateDuringFirstSnapshot && stub.subscribed {
		stub.updates <- domain.Update{}
	}
	stub.calls++
	return stub.snapshots[index], nil
}

func (stub *liveTimelineStub) Updates(context.Context, string, string) (<-chan domain.Update, error) {
	stub.mu.Lock()
	stub.subscribed = true
	stub.mu.Unlock()
	return stub.updates, nil
}

func TestMountedSnapshotAuthorizesExactProject(t *testing.T) {
	timeline := &liveTimelineStub{snapshots: []domain.Snapshot{
		{Events: []domain.Event{{
			ID: "assistant-1:0", Kind: domain.EventAssistant,
			JobID: "job-1", TurnID: "turn-1", ItemID: "assistant-1", Text: "Ответ",
		}}},
	}}
	router := mountedTimeline(timeline, currentUserStub{user: auth.User{ID: "user-1"}}, jobAccessStub{job: jobs.Job{ID: "job-1", ProjectID: "project-1"}})
	response := snapshotRequest(router, timelineTestOrigin)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var snapshot SnapshotDTO
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil || len(snapshot.Events) != 1 || snapshot.Events[0].Text != "Ответ" {
		t.Fatalf("snapshot = %#v, %v", snapshot, err)
	}
}

func TestMountedSnapshotRequiresAuthentication(t *testing.T) {
	router := mountedTimeline(&liveTimelineStub{}, currentUserStub{err: auth.ErrUnauthenticated}, jobAccessStub{})
	if response := snapshotRequest(router, timelineTestOrigin); response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestMountedSnapshotHidesCrossProjectJob(t *testing.T) {
	router := mountedTimeline(&liveTimelineStub{}, currentUserStub{user: auth.User{ID: "user-1"}}, jobAccessStub{job: jobs.Job{ID: "job-1", ProjectID: "project-other"}})
	if response := snapshotRequest(router, timelineTestOrigin); response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestMountedSnapshotRejectsInvalidOrigin(t *testing.T) {
	router := mountedTimeline(&liveTimelineStub{}, currentUserStub{user: auth.User{ID: "user-1"}}, jobAccessStub{job: jobs.Job{ID: "job-1", ProjectID: "project-1"}})
	if response := snapshotRequest(router, "https://evil.test"); response.Code != http.StatusForbidden {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestLiveStreamReprojectsSameCursorAfterNativeUpdate(t *testing.T) {
	timeline := &liveTimelineStub{
		snapshots: []domain.Snapshot{
			{Events: []domain.Event{{ID: "plan-1:0", Kind: domain.EventPlan, JobID: "job-1", TurnID: "turn-1", ItemID: "plan-1", Text: "Первый план"}}},
			{Events: []domain.Event{{ID: "plan-1:0", Kind: domain.EventPlan, JobID: "job-1", TurnID: "turn-1", ItemID: "plan-1", Text: "Обновлённый план"}}},
		},
		updates:                   make(chan domain.Update, 1),
		updateDuringFirstSnapshot: true,
	}
	router := mountedTimeline(timeline, currentUserStub{user: auth.User{ID: "user-1"}}, jobAccessStub{job: jobs.Job{ID: "job-1", ProjectID: "project-1"}})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	header := http.Header{"Origin": []string{timelineTestOrigin}}
	connection, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/projects/project-1/jobs/job-1/agent-timeline/stream", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "")

	var first EnvelopeDTO
	if err := wsjson.Read(ctx, connection, &first); err != nil || first.Snapshot == nil || first.Snapshot.Events[0].Text != "Первый план" {
		t.Fatalf("first envelope = %#v, %v", first, err)
	}
	var updated EnvelopeDTO
	if err := wsjson.Read(ctx, connection, &updated); err != nil || updated.Snapshot == nil ||
		updated.Snapshot.Events[0].Cursor != first.Snapshot.Events[0].Cursor || updated.Snapshot.Events[0].Text != "Обновлённый план" {
		t.Fatalf("updated envelope = %#v, %v", updated, err)
	}
}

func TestLiveStreamCoalescesNativeBoundaryBurst(t *testing.T) {
	updates := make(chan domain.Update, 32)
	for range 32 {
		updates <- domain.Update{}
	}
	close(updates)
	timeline := &liveTimelineStub{
		snapshots: []domain.Snapshot{
			{Events: []domain.Event{{ID: "tool-1:0", Kind: domain.EventToolRequest, JobID: "job-1", TurnID: "turn-1", ItemID: "tool-1", Outcome: domain.OutcomeInProgress}}},
			{Events: []domain.Event{{ID: "tool-1:0", Kind: domain.EventToolRequest, JobID: "job-1", TurnID: "turn-1", ItemID: "tool-1", Outcome: domain.OutcomeCompleted}}},
		},
		updates: updates,
	}
	router := mountedTimeline(timeline, currentUserStub{user: auth.User{ID: "user-1"}}, jobAccessStub{job: jobs.Job{ID: "job-1", ProjectID: "project-1"}})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/projects/project-1/jobs/job-1/agent-timeline/stream", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{timelineTestOrigin}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "")

	var envelopes []EnvelopeDTO
	for {
		var envelope EnvelopeDTO
		if err := wsjson.Read(ctx, connection, &envelope); err != nil {
			break
		}
		envelopes = append(envelopes, envelope)
	}
	if len(envelopes) != 2 || envelopes[1].Snapshot == nil ||
		envelopes[1].Snapshot.Events[0].Outcome != domain.OutcomeCompleted || timeline.calls != 2 {
		t.Fatalf("envelopes = %d, snapshot calls = %d, latest = %#v", len(envelopes), timeline.calls, envelopes[len(envelopes)-1])
	}
}

func TestLiveStreamWritesTextDeltaWithoutSnapshotRead(t *testing.T) {
	delta := &domain.Delta{
		Kind: domain.EventReasoning, Role: domain.RoleMain, TurnID: "turn-1",
		ItemID: "reasoning-1", LogicalSection: 2, Text: " chunk", RawReasoning: true,
	}
	timeline := &liveTimelineStub{
		snapshots: []domain.Snapshot{{Events: []domain.Event{{
			ID: "assistant-1:0", Kind: domain.EventAssistant, JobID: "job-1", TurnID: "turn-1",
		}}}},
		updates: make(chan domain.Update, 1),
	}
	timeline.updates <- domain.Update{Delta: delta}
	router := mountedTimeline(timeline, currentUserStub{user: auth.User{ID: "user-1"}}, jobAccessStub{job: jobs.Job{ID: "job-1", ProjectID: "project-1"}})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/projects/project-1/jobs/job-1/agent-timeline/stream", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{timelineTestOrigin}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "")

	var initial EnvelopeDTO
	if err := wsjson.Read(ctx, connection, &initial); err != nil {
		t.Fatal(err)
	}
	var live EnvelopeDTO
	if err := wsjson.Read(ctx, connection, &live); err != nil {
		t.Fatal(err)
	}
	wantCursor := eventCursor(delta.Event("job-1"))
	if live.Type != "delta" || live.Cursor != wantCursor || live.Snapshot != nil ||
		live.Delta == nil || live.Delta.ID != "reasoning-1:raw:2" || live.Delta.Kind != domain.EventReasoning ||
		live.Delta.JobID != "job-1" || live.Delta.Role != "main" || live.Delta.ProviderTurnID != "turn-1" ||
		live.Delta.ProviderItemID != "reasoning-1" || live.Delta.LogicalSection != 2 || live.Delta.Text != " chunk" {
		t.Fatalf("delta envelope = %#v, cursor = %q", live, wantCursor)
	}
	if timeline.calls != 1 {
		t.Fatalf("snapshot reads = %d", timeline.calls)
	}
}

func TestLiveStreamWritesContextUsageWithoutSnapshotRead(t *testing.T) {
	usedTokens, contextWindow := int64(96_000), int64(128_000)
	timeline := &liveTimelineStub{
		snapshots: []domain.Snapshot{{Events: []domain.Event{{
			ID: "assistant-1:0", Kind: domain.EventAssistant, JobID: "job-1", TurnID: "turn-1",
		}}}},
		updates: make(chan domain.Update, 1),
	}
	timeline.updates <- domain.Update{Context: &domain.ContextUpdate{
		Role: domain.RoleMain, TurnID: "turn-1", UsedTokens: &usedTokens, ContextWindow: &contextWindow,
	}}
	router := mountedTimeline(timeline, currentUserStub{user: auth.User{ID: "user-1"}}, jobAccessStub{job: jobs.Job{ID: "job-1", ProjectID: "project-1"}})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/projects/project-1/jobs/job-1/agent-timeline/stream", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{timelineTestOrigin}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "")

	var initial EnvelopeDTO
	if err := wsjson.Read(ctx, connection, &initial); err != nil {
		t.Fatal(err)
	}
	var live EnvelopeDTO
	if err := wsjson.Read(ctx, connection, &live); err != nil {
		t.Fatal(err)
	}
	if live.Type != "context" || live.Cursor != "" || live.Context == nil ||
		live.Context.Role != "main" || live.Context.ProviderTurnID != "turn-1" ||
		live.Context.UsedTokens == nil || *live.Context.UsedTokens != usedTokens ||
		live.Context.ContextWindow == nil || *live.Context.ContextWindow != contextWindow || live.Context.Compacted {
		t.Fatalf("context envelope = %#v", live)
	}
	if timeline.calls != 1 {
		t.Fatalf("snapshot reads = %d", timeline.calls)
	}
}

func mountedTimeline(timeline Timeline, current CurrentUserService, access JobAccessService) http.Handler {
	router := chi.NewRouter()
	NewHandlers(timeline, current, access, []string{timelineTestOrigin}).Mount(router)
	return router
}

func snapshotRequest(handler http.Handler, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/projects/project-1/jobs/job-1/agent-timeline", nil)
	request.Header.Set("Origin", origin)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// pendingTimelineStub has no root thread for its first pendingUpdates subscriptions.
type pendingTimelineStub struct {
	mu             sync.Mutex
	pendingUpdates int
	subscriptions  int
	updates        chan domain.Update
	bound          domain.Snapshot
}

func (stub *pendingTimelineStub) Snapshot(context.Context, string, string) (domain.Snapshot, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.subscriptions <= stub.pendingUpdates {
		return domain.Snapshot{Events: []domain.Event{}}, nil
	}
	return stub.bound, nil
}

func (stub *pendingTimelineStub) Updates(context.Context, string, string) (<-chan domain.Update, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.subscriptions++
	if stub.subscriptions <= stub.pendingUpdates {
		return nil, domain.ErrThreadPending
	}
	return stub.updates, nil
}

func dialTimelineStream(t *testing.T, ctx context.Context, handlers Handlers) *websocket.Conn {
	t.Helper()
	router := chi.NewRouter()
	handlers.Mount(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	header := http.Header{"Origin": []string{timelineTestOrigin}}
	connection, response, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/projects/project-1/jobs/job-1/agent-timeline/stream", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial stream before the thread is bound: status %d, %v", status, err)
	}
	t.Cleanup(func() { _ = connection.Close(websocket.StatusNormalClosure, "") })
	return connection
}

// R2.1: before the turn has a root thread the stream opens, sends an empty snapshot and waits.
func TestLiveStreamWaitsForRootThread(t *testing.T) {
	timeline := &pendingTimelineStub{
		pendingUpdates: 3,
		updates:        make(chan domain.Update, 1),
		bound: domain.Snapshot{Events: []domain.Event{{
			ID: "assistant-1:0", Kind: domain.EventAssistant, JobID: "job-1", TurnID: "turn-1", ItemID: "assistant-1", Text: "Ответ",
		}}},
	}
	handlers := NewHandlers(timeline, currentUserStub{user: auth.User{ID: "user-1"}},
		jobAccessStub{job: jobs.Job{ID: "job-1", ProjectID: "project-1", Status: jobs.StatusRunning}}, []string{timelineTestOrigin})
	handlers.bindingPollInterval = time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	connection := dialTimelineStream(t, ctx, handlers)

	var empty EnvelopeDTO
	if err := wsjson.Read(ctx, connection, &empty); err != nil || empty.Snapshot == nil || len(empty.Snapshot.Events) != 0 {
		t.Fatalf("first envelope = %#v, %v, want an empty snapshot", empty, err)
	}
	var bound EnvelopeDTO
	if err := wsjson.Read(ctx, connection, &bound); err != nil || bound.Snapshot == nil ||
		len(bound.Snapshot.Events) != 1 || bound.Snapshot.Events[0].Text != "Ответ" {
		t.Fatalf("envelope after binding = %#v, %v", bound, err)
	}
	timeline.updates <- domain.Update{Delta: &domain.Delta{
		Kind: domain.EventAssistant, Role: domain.RoleMain, TurnID: "turn-1", ItemID: "assistant-1", Text: " дальше",
	}}
	var delta EnvelopeDTO
	if err := wsjson.Read(ctx, connection, &delta); err != nil || delta.Delta == nil || delta.Delta.Text != " дальше" {
		t.Fatalf("delta after binding = %#v, %v", delta, err)
	}
}

// R2.2: a Job that ended without a root thread keeps the stream open without polling the
// binding, like an ended Job with a thread, and stops the stream once access is lost.
func TestLiveStreamHoldsJobEndedWithoutThread(t *testing.T) {
	timeline := &pendingTimelineStub{pendingUpdates: 1 << 30}
	current := &switchableUserStub{user: auth.User{ID: "user-1"}}
	handlers := NewHandlers(timeline, current,
		jobAccessStub{job: jobs.Job{ID: "job-1", ProjectID: "project-1", Status: jobs.StatusFailed}}, []string{timelineTestOrigin})
	handlers.bindingPollInterval = time.Millisecond
	handlers.authorizationLeaseDuration = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	connection := dialTimelineStream(t, ctx, handlers)

	var empty EnvelopeDTO
	if err := wsjson.Read(ctx, connection, &empty); err != nil || empty.Snapshot == nil {
		t.Fatalf("first envelope = %#v, %v", empty, err)
	}
	time.Sleep(20 * time.Millisecond)
	timeline.mu.Lock()
	polled := timeline.subscriptions
	timeline.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	timeline.mu.Lock()
	later := timeline.subscriptions
	timeline.mu.Unlock()
	if later != polled {
		t.Fatalf("binding polls continued for an ended Job: %d then %d", polled, later)
	}
	current.revoke()
	_, _, err := connection.Read(ctx)
	if status := websocket.CloseStatus(err); status != websocket.StatusPolicyViolation {
		t.Fatalf("close = %v (status %d), want policy violation after access loss", err, status)
	}
}

type switchableUserStub struct {
	mu      sync.Mutex
	user    auth.User
	revoked bool
}

func (stub *switchableUserStub) CurrentUser(context.Context, string) (auth.User, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.revoked {
		return auth.User{}, auth.ErrUnauthenticated
	}
	return stub.user, nil
}

func (stub *switchableUserStub) revoke() {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.revoked = true
}

// R2.3: a missing binding is 404, not an outage.
func TestLiveStreamMissingBindingIsNotFound(t *testing.T) {
	timeline := &failingTimelineStub{err: domain.ErrNotFound}
	router := mountedTimeline(timeline, currentUserStub{user: auth.User{ID: "user-1"}}, jobAccessStub{job: jobs.Job{ID: "job-1", ProjectID: "project-1"}})
	request := httptest.NewRequest(http.MethodGet, "/projects/project-1/jobs/job-1/agent-timeline/stream", nil)
	request.Header.Set("Origin", timelineTestOrigin)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s, want 404", response.Code, response.Body.String())
	}
}

type failingTimelineStub struct{ err error }

func (stub failingTimelineStub) Snapshot(context.Context, string, string) (domain.Snapshot, error) {
	return domain.Snapshot{}, stub.err
}

func (stub failingTimelineStub) Updates(context.Context, string, string) (<-chan domain.Update, error) {
	return nil, stub.err
}
