package agenttimeline

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	agentdomain "canonkeeper/backend/internal/domain/agent"
	domain "canonkeeper/backend/internal/domain/agenttimeline"
)

type nativeThreadStub struct {
	items      []domain.RawItem
	readCalls  int
	readFlow   agentdomain.Flow
	readThread string
	waitFlow   agentdomain.Flow
	waitThread string
}

func (stub *nativeThreadStub) ThreadItems(_ context.Context, flow agentdomain.Flow, threadID string) ([]domain.RawItem, error) {
	stub.readCalls++
	stub.readFlow = flow
	stub.readThread = threadID
	return append([]domain.RawItem(nil), stub.items...), nil
}

func (stub *nativeThreadStub) ThreadUpdates(_ context.Context, flow agentdomain.Flow, threadID string) (<-chan domain.Update, error) {
	stub.waitFlow = flow
	stub.waitThread = threadID
	updates := make(chan domain.Update, 1)
	updates <- domain.Update{}
	return updates, nil
}

func TestSnapshotReadsExactBoundNativeThread(t *testing.T) {
	var resolvedProject, resolvedJob string
	native := &nativeThreadStub{items: []domain.RawItem{{
		Role:       domain.RoleMain,
		TurnID:     "turn-1",
		TurnStatus: domain.TurnStatusInProgress,
		Item:       json.RawMessage(`{"type":"agentMessage","id":"assistant-1","text":"Ответ","phase":"commentary"}`),
	}}}
	service := New(
		func(_ context.Context, projectID, jobID string) (ThreadBinding, error) {
			resolvedProject, resolvedJob = projectID, jobID
			return ThreadBinding{Flow: agentdomain.FlowResearchChat, RootThreadID: "thread-root"}, nil
		},
		native,
	)

	snapshot, err := service.Snapshot(t.Context(), "project-1", "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if resolvedProject != "project-1" || resolvedJob != "job-1" || native.readFlow != agentdomain.FlowResearchChat || native.readThread != "thread-root" {
		t.Fatalf("ports = project:%q job:%q thread:%q", resolvedProject, resolvedJob, native.readThread)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].Text != "Ответ" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestSnapshotIsEmptyBeforeNativeRootIsBound(t *testing.T) {
	native := &nativeThreadStub{}
	service := New(
		func(context.Context, string, string) (ThreadBinding, error) {
			return ThreadBinding{Flow: agentdomain.FlowResearchChat}, nil
		},
		native,
	)

	snapshot, err := service.Snapshot(t.Context(), "project-1", "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events) != 0 || native.readCalls != 0 {
		t.Fatalf("snapshot = %#v, native reads = %d", snapshot, native.readCalls)
	}
}

func TestUpdatesUseExactBoundNativeThread(t *testing.T) {
	native := &nativeThreadStub{}
	service := New(func(context.Context, string, string) (ThreadBinding, error) {
		return ThreadBinding{Flow: agentdomain.FlowResearchChat, RootThreadID: "thread-root", TurnID: "turn-bound"}, nil
	}, native)

	updates, err := service.Updates(t.Context(), "project-1", "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if update := <-updates; update.Err != nil {
		t.Fatal(update.Err)
	}
	if native.waitFlow != agentdomain.FlowResearchChat || native.waitThread != "thread-root" {
		t.Fatalf("wait binding = thread:%q", native.waitThread)
	}
}

func TestSnapshotProjectsCanonicalRootThreadForEveryResearchJob(t *testing.T) {
	native := &nativeThreadStub{items: []domain.RawItem{
		{Role: domain.RoleMain, TurnID: "turn-1", TurnStatus: domain.TurnStatusInProgress, Item: json.RawMessage(`{"type":"agentMessage","id":"assistant-1","text":"Первый ответ"}`)},
		{Role: domain.RoleMain, TurnID: "turn-2", TurnStatus: domain.TurnStatusInProgress, Item: json.RawMessage(`{"type":"agentMessage","id":"assistant-2","text":"Второй ответ"}`)},
	}}
	service := New(func(_ context.Context, _, jobID string) (ThreadBinding, error) {
		return ThreadBinding{Flow: agentdomain.FlowResearchChat, RootThreadID: "thread-shared", TurnID: map[string]string{"job-1": "turn-1", "job-2": "turn-2"}[jobID]}, nil
	}, native)

	first, err := service.Snapshot(t.Context(), "project-1", "job-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Snapshot(t.Context(), "project-1", "job-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 2 || first.Events[0].JobID != "job-1" || first.Events[0].Text != "Первый ответ" || first.Events[1].Text != "Второй ответ" ||
		len(second.Events) != 2 || second.Events[0].JobID != "job-2" || second.Events[0].Text != "Первый ответ" || second.Events[1].Text != "Второй ответ" {
		t.Fatalf("first = %#v, second = %#v", first.Events, second.Events)
	}
}

func TestSnapshotKeepsHistoricalPublicQuestionsInNativeOrder(t *testing.T) {
	native := &nativeThreadStub{items: []domain.RawItem{
		nativeQuestionItem(t, "turn-1", "user-1", "Первый вопрос?"),
		{Role: domain.RoleMain, TurnID: "turn-1", TurnStatus: domain.TurnStatusCompleted, Item: json.RawMessage(`{"type":"agentMessage","id":"answer-1","text":"Первый ответ"}`)},
		nativeQuestionItem(t, "turn-2", "user-2", "Второй вопрос?"),
		{Role: domain.RoleMain, TurnID: "turn-2", TurnStatus: domain.TurnStatusCompleted, Item: json.RawMessage(`{"type":"agentMessage","id":"answer-2","text":"Второй ответ"}`)},
	}}
	service := New(func(context.Context, string, string) (ThreadBinding, error) {
		return ThreadBinding{Flow: agentdomain.FlowResearchChat, RootThreadID: "thread-root", TurnID: "turn-2"}, nil
	}, native)

	for range 2 {
		snapshot, err := service.Snapshot(t.Context(), "project-1", "job-2")
		if err != nil {
			t.Fatal(err)
		}
		var ids, texts, turns []string
		for _, event := range snapshot.Events {
			if event.Kind == domain.EventTurnOutcome {
				continue
			}
			if event.Kind != domain.EventUser && event.Kind != domain.EventAssistant || event.Role != domain.RoleMain {
				t.Fatalf("unexpected public event = %#v", event)
			}
			ids = append(ids, event.ID)
			texts = append(texts, event.Text)
			turns = append(turns, event.TurnID)
		}
		if !reflect.DeepEqual(ids, []string{"user-1:0", "answer-1:0", "user-2:0", "answer-2:0"}) ||
			!reflect.DeepEqual(texts, []string{"Первый вопрос?", "Первый ответ", "Второй вопрос?", "Второй ответ"}) ||
			!reflect.DeepEqual(turns, []string{"turn-1", "turn-1", "turn-2", "turn-2"}) {
			t.Fatalf("public conversation = ids:%v texts:%v turns:%v", ids, texts, turns)
		}
	}
}

func TestSnapshotDoesNotExposeOtherNativeUserInputs(t *testing.T) {
	for _, test := range []struct {
		name string
		flow agentdomain.Flow
		role domain.Role
		item json.RawMessage
	}{
		{name: "reviewer", flow: agentdomain.FlowResearchChat, role: domain.RoleSourceEvidence},
		{name: "internal continuation", flow: agentdomain.FlowResearchChat, role: domain.RoleMain, item: json.RawMessage(`{"type":"userMessage","id":"internal","content":[{"type":"text","text":"Continue the assigned operation after the previous provider turn."}]}`)},
		{name: "malformed assignment", flow: agentdomain.FlowResearchChat, role: domain.RoleMain, item: json.RawMessage(`{"type":"userMessage","id":"malformed","content":[{"type":"text","text":"{\"question\":\"private wrapper\"}"}]}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := nativeQuestionItem(t, "turn-1", "user-1", "Публичный вопрос?")
			item.Role = test.role
			if len(test.item) > 0 {
				item.Item = test.item
			}
			service := New(func(context.Context, string, string) (ThreadBinding, error) {
				return ThreadBinding{Flow: test.flow, RootThreadID: "thread-root"}, nil
			}, &nativeThreadStub{items: []domain.RawItem{item}})
			snapshot, err := service.Snapshot(t.Context(), "project-1", "job-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Events) != 1 || snapshot.Events[0].Kind != domain.EventTurnOutcome {
				t.Fatalf("non-public input exposed = %#v", snapshot.Events)
			}
		})
	}
}

func nativeQuestionItem(t *testing.T, turnID, itemID, question string) domain.RawItem {
	t.Helper()
	input, err := json.Marshal(map[string]any{
		"question": question, "projectRef": "project", "sourceView": "published", "scope": "project",
		"selectedContext": []map[string]any{{"bookTitle": "Internal context", "chapterPosition": 1, "chapterTitle": "Not a public question"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err := json.Marshal(map[string]any{
		"type": "userMessage", "id": itemID, "clientId": "product-" + turnID,
		"content": []map[string]string{{"type": "text", "text": string(input)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return domain.RawItem{Role: domain.RoleMain, TurnID: turnID, TurnStatus: domain.TurnStatusCompleted, Item: item}
}

// R2.1: before the root thread is bound the stream waits, it is not an outage.
func TestUpdatesBeforeNativeRootIsBoundArePending(t *testing.T) {
	native := &nativeThreadStub{}
	service := New(func(context.Context, string, string) (ThreadBinding, error) {
		return ThreadBinding{Flow: agentdomain.FlowResearchChat}, nil
	}, native)

	if _, err := service.Updates(t.Context(), "project-1", "job-1"); !errors.Is(err, domain.ErrThreadPending) {
		t.Fatalf("Updates() error = %v, want ErrThreadPending", err)
	}
	if native.waitThread != "" {
		t.Fatalf("subscribed to native thread %q before binding", native.waitThread)
	}
}
