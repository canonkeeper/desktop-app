package agenttimeline

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestProjectPreservesNativeItemsInProjectionOrder(t *testing.T) {
	items := []RawItem{
		rawItem("turn-1", TurnStatusCompleted, `{"type":"reasoning","id":"reasoning-1","summary":["Сначала","Затем"]}`),
		rawItem("turn-1", TurnStatusCompleted, `{"type":"agentMessage","id":"assistant-1","text":"Проверяю факт","phase":"commentary"}`),
		rawItem("turn-1", TurnStatusCompleted, `{"type":"dynamicToolCall","id":"tool-1","namespace":"source","tool":"read","arguments":{"sourceRef":"s1"},"status":"completed","success":true,"contentItems":[{"type":"inputText","text":"Источник прочитан"}]}`),
		rawItem("turn-1", TurnStatusCompleted, `{"type":"plan","id":"plan-1","text":"1. Проверить\n2. Завершить"}`),
		rawItem("turn-1", TurnStatusCompleted, `{"type":"subAgentActivity","id":"subagent-1","agentPath":"reviewer-sources","agentThreadId":"thread-child","kind":"started"}`),
		rawItem("turn-1", TurnStatusCompleted, `{"type":"collabAgentToolCall","id":"collab-1","senderThreadId":"thread-root","receiverThreadIds":["thread-child"],"tool":"wait","status":"completed","agentsStates":{"thread-child":{"status":"completed","message":"accepted"}}}`),
		rawItem("turn-1", TurnStatusCompleted, `{"type":"agentMessage","id":"final-1","text":"Готово","phase":"final_answer"}`),
	}

	snapshot, err := Project("job-1", items)
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []EventKind{
		EventReasoning,
		EventReasoning,
		EventAssistant,
		EventToolResult,
		EventPlan,
		EventSubagent,
		EventSubagent,
		EventAssistant,
		EventKind("turn_outcome"),
	}
	gotKinds := make([]EventKind, len(snapshot.Events))
	for index, event := range snapshot.Events {
		gotKinds[index] = event.Kind
		if event.JobID != "job-1" || event.TurnID != "turn-1" {
			t.Fatalf("event %d identity = %#v", index, event)
		}
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("kinds = %v, want %v", gotKinds, wantKinds)
	}
	if got := snapshot.Events[1].Text; got != "Затем" {
		t.Fatalf("second reasoning = %q", got)
	}
	if got := snapshot.Events[3]; got.Outcome != OutcomeCompleted || got.Tool == nil || got.Tool.Name != "read" || got.Tool.Success == nil || !*got.Tool.Success || got.Tool.Content[0].Text != "Источник прочитан" {
		t.Fatalf("tool result = %#v", got)
	}
	if got := snapshot.Events[5]; got.Outcome != OutcomeInProgress || got.Subagent == nil || got.Subagent.ThreadID != "thread-child" || got.Subagent.Path != "reviewer-sources" || got.Subagent.Status != "started" {
		t.Fatalf("subagent = %#v", got)
	}
	if got := snapshot.Events[6]; got.Text != "accepted" || got.Outcome != OutcomeCompleted || got.Subagent == nil || got.Subagent.ThreadID != "thread-child" {
		t.Fatalf("subagent terminal = %#v", got)
	}
	if got := snapshot.Events[7]; got.Text != "Готово" || got.Phase != "final_answer" || got.Kind != EventAssistant {
		t.Fatalf("final assistant = %#v", got)
	}
	if got := snapshot.Events[8]; got.ID != "turn-1:turn_outcome" || got.TurnID != "turn-1" || got.Outcome != OutcomeCompleted || got.ItemID != "" {
		t.Fatalf("turn outcome = %#v", got)
	}
}

func TestProjectKeepsOneStableToolEventAcrossStates(t *testing.T) {
	inProgress, err := Project("job-1", []RawItem{
		rawItem("turn-1", TurnStatusInProgress, `{"type":"dynamicToolCall","id":"tool-1","namespace":"source","tool":"read","arguments":{"source_number":77},"status":"inProgress"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := Project("job-1", []RawItem{
		rawItem("turn-1", TurnStatusInProgress, `{"type":"dynamicToolCall","id":"tool-1","namespace":"source","tool":"read","arguments":{"source_number":77},"status":"completed","success":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inProgress.Events) != 1 || len(completed.Events) != 1 {
		t.Fatalf("tool events = %d running, %d completed", len(inProgress.Events), len(completed.Events))
	}
	if inProgress.Events[0].ID != completed.Events[0].ID || inProgress.Events[0].Kind != EventToolRequest ||
		completed.Events[0].Kind != EventToolResult || completed.Events[0].Outcome != OutcomeCompleted {
		t.Fatalf("tool transition = running %#v, completed %#v", inProgress.Events[0], completed.Events[0])
	}
}

func TestProjectNormalizesNativeFailureStatusesWithoutSuccessFlag(t *testing.T) {
	items := []RawItem{
		rawItem("turn-1", TurnStatusInProgress, `{"type":"dynamicToolCall","id":"tool-1","tool":"read","arguments":{},"status":"failed"}`),
		rawItem("turn-1", TurnStatusInProgress, `{"type":"collabAgentToolCall","id":"collab-1","senderThreadId":"thread-root","receiverThreadIds":["running","errored"],"tool":"wait","status":"failed","agentsStates":{"running":{"status":"running"},"errored":{"status":"errored"}}}`),
	}

	snapshot, err := Project("job-1", items)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events) != 3 || snapshot.Events[0].Outcome != OutcomeFailed ||
		snapshot.Events[1].Outcome != OutcomeInProgress || snapshot.Events[2].Outcome != OutcomeFailed {
		t.Fatalf("normalized outcomes = %#v", snapshot.Events)
	}
}

func TestProjectAcceptsCollabActivityBeforeReceiverExists(t *testing.T) {
	snapshot, err := Project("job-1", []RawItem{
		rawItem("turn-1", TurnStatusInProgress, `{"type":"collabAgentToolCall","id":"wait-1","senderThreadId":"thread-root","receiverThreadIds":[],"tool":"wait","status":"inProgress","agentsStates":{}}`),
	})
	if err != nil || len(snapshot.Events) != 0 {
		t.Fatalf("snapshot = %#v, err = %v", snapshot, err)
	}
}

func TestProjectMapsCompletedSubagentActivity(t *testing.T) {
	snapshot, err := Project("job-1", []RawItem{
		rawItem("turn-1", TurnStatusInProgress, `{"type":"subAgentActivity","id":"subagent-1","agentPath":"/root/reviewer","agentThreadId":"thread-child","kind":"completed"}`),
	})
	if err != nil || len(snapshot.Events) != 1 || snapshot.Events[0].Outcome != OutcomeCompleted {
		t.Fatalf("snapshot = %#v, err = %v", snapshot, err)
	}
}

func TestProjectMapsFailedAndInterruptedTurnsToContractOutcomes(t *testing.T) {
	for _, test := range []struct {
		status  TurnStatus
		outcome Outcome
	}{
		{status: TurnStatusFailed, outcome: OutcomeFailed},
		{status: TurnStatusInterrupted, outcome: OutcomeInterrupted},
	} {
		snapshot, err := Project("job-1", []RawItem{{Role: RoleMain, TurnID: "turn-1", TurnStatus: test.status}})
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Events) != 1 || snapshot.Events[0].Kind != EventKind("turn_outcome") || snapshot.Events[0].Outcome != test.outcome {
			t.Fatalf("status %q = %#v", test.status, snapshot.Events)
		}
	}
}

func TestProjectPreservesNativeThreadRole(t *testing.T) {
	snapshot, err := Project("job-1", []RawItem{
		rawItem("turn-main", TurnStatusInProgress, `{"type":"agentMessage","id":"main","text":"root"}`),
		{Role: RoleSagaHistory, TurnID: "turn-review", TurnStatus: TurnStatusCompleted, Item: json.RawMessage(`{"type":"agentMessage","id":"review","text":"child"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events) != 3 || snapshot.Events[0].Role != RoleMain ||
		snapshot.Events[1].Role != RoleSagaHistory || snapshot.Events[2].Role != RoleSagaHistory {
		t.Fatalf("roles = %#v", snapshot.Events)
	}
}

func TestDeltaEventUsesCanonicalItemSection(t *testing.T) {
	event := Delta{
		Kind: EventAssistant, Role: RoleMain, TurnID: "turn-1", ItemID: "item-1",
		LogicalSection: 2, Text: " chunk",
	}.Event("job-1")

	if event.ID != "item-1:2" || event.JobID != "job-1" || event.Kind != EventAssistant ||
		event.Role != RoleMain || event.TurnID != "turn-1" || event.ItemID != "item-1" ||
		event.LogicalSection != 2 || event.Text != " chunk" {
		t.Fatalf("event = %#v", event)
	}

	raw := Delta{
		Kind: EventReasoning, Role: RoleMain, TurnID: "turn-1", ItemID: "item-1",
		LogicalSection: 2, Text: " reasoning", RawReasoning: true,
	}.Event("job-1")
	if raw.ID != "item-1:raw:2" || raw.Kind != EventReasoning || raw.Text != " reasoning" {
		t.Fatalf("raw reasoning event = %#v", raw)
	}
}

func rawItem(turnID string, status TurnStatus, item string) RawItem {
	return RawItem{Role: RoleMain, TurnID: turnID, TurnStatus: status, Item: json.RawMessage(item)}
}

// A model without reasoning summaries (glm-5.3) stores raw reasoning as plain strings in
// content; the snapshot shows them under the same ids as the live raw reasoning deltas.
func TestProjectShowsRawReasoningContentLikeTheLiveStream(t *testing.T) {
	snapshot, err := Project("job-1", []RawItem{
		rawItem("turn-1", TurnStatusCompleted, `{"type":"reasoning","id":"rs-1","summary":[],"content":["Ищу варианты имени.","", "Проверю главу 187."]}`),
		rawItem("turn-1", TurnStatusCompleted, `{"type":"agentMessage","id":"msg-1","text":"Готово","phase":"final_answer"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	live := Delta{Kind: EventReasoning, ItemID: "rs-1", LogicalSection: 2, Text: "Проверю главу 187.", RawReasoning: true}.Event("job-1")
	if len(snapshot.Events) != 4 || snapshot.Events[0].ID != "rs-1:raw:0" || snapshot.Events[0].Text != "Ищу варианты имени." ||
		snapshot.Events[1].ID != live.ID || snapshot.Events[1].LogicalSection != 2 || snapshot.Events[1].Kind != EventReasoning {
		t.Fatalf("events = %#v, want raw reasoning sections with live ids", snapshot.Events)
	}
}

// specs/2026-10-29-saga-illustrations.md R15.9: the timeline shows the images attached to a question
// from the native question itself.
func TestProjectShowsTheImagesAttachedToAQuestion(t *testing.T) {
	question := `{"question":"Нарисуй его так","projectRef":"project","sourceView":"published","scope":"project","attachedImages":[81,82]}`
	encoded, err := json.Marshal(question)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Project("job-1", []RawItem{
		rawItem("turn-1", TurnStatusCompleted, `{"type":"userMessage","id":"question-1","content":[{"type":"text","text":`+string(encoded)+`}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events) < 1 || snapshot.Events[0].Kind != EventUser || snapshot.Events[0].Text != "Нарисуй его так" ||
		len(snapshot.Events[0].Images) != 2 || snapshot.Events[0].Images[1] != 82 {
		t.Fatalf("question = %+v", snapshot.Events)
	}
}

// specs/2026-10-30-chat-steering.md R3.1, R3.2: a correction sent into a running turn is the same
// native user item format; it stays in its turn as a steer event and never becomes a question.
func TestProjectKeepsACorrectionInsideItsTurn(t *testing.T) {
	item := func(id, clientID, text string) string {
		encoded, err := json.Marshal(text)
		if err != nil {
			t.Fatal(err)
		}
		return `{"type":"userMessage","id":"` + id + `","clientId":"` + clientID +
			`","content":[{"type":"text","text":` + string(encoded) + `,"text_elements":[]}]}`
	}
	question := `{"question":"Нарисуй обложку","projectRef":"project","sourceView":"published","scope":"project","attachedImages":[81,82],"attachedPhotos":["фото 1 — изображение 81","фото 2 — изображение 82"]}`
	correction := `{"question":"Без дракона, лицо с фото 3","projectRef":"project","sourceView":"published","scope":"project","attachedImages":[90],"attachedPhotos":["фото 3 — изображение 90 (Анна)"],"correction":true,"firstPhoto":3}`
	snapshot, err := Project("job-1", []RawItem{
		rawItem("turn-1", TurnStatusInProgress, item("item-1", "turn-client", question)),
		rawItem("turn-1", TurnStatusInProgress, `{"type":"agentMessage","id":"item-2","text":"Читаю главу."}`),
		rawItem("turn-1", TurnStatusInProgress, item("item-3", "steer-abc", correction)),
		rawItem("turn-1", TurnStatusInProgress, `{"type":"agentMessage","id":"item-4","text":"Учту."}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]EventKind, 0, len(snapshot.Events))
	for _, event := range snapshot.Events {
		kinds = append(kinds, event.Kind)
	}
	if got, want := fmt.Sprint(kinds), fmt.Sprint([]EventKind{EventUser, EventAssistant, EventSteer, EventAssistant}); got != want {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	steer := snapshot.Events[2]
	if steer.Text != "Без дракона, лицо с фото 3" || steer.SteerID != "steer-abc" || steer.FirstPhoto != 3 ||
		len(steer.Images) != 1 || steer.Images[0] != 90 || steer.TurnID != "turn-1" {
		t.Fatalf("steer = %+v", steer)
	}
	if first := snapshot.Events[0]; first.Text != "Нарисуй обложку" || first.SteerID != "" || first.FirstPhoto != 0 {
		t.Fatalf("question = %+v", first)
	}
}

// R3.1: a wrapper that is not a valid assignment stays out of the feed, as any other native input.
func TestProjectHidesAMalformedCorrection(t *testing.T) {
	snapshot, err := Project("job-1", []RawItem{
		rawItem("turn-1", TurnStatusInProgress, `{"type":"userMessage","id":"item-1","content":[{"type":"text","text":"{\"question\":\"x\",\"correction\":true}"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events) != 0 {
		t.Fatalf("events = %+v", snapshot.Events)
	}
}

// specs/2026-11-07-chat-attach-chapter.md CA4.1: the card of the chapter attached to a question comes from the native
// question itself — its number, title and words — and never its text or its signature.
func TestProjectShowsTheChapterAttachedToAQuestion(t *testing.T) {
	question := `{"question":"Ложится ли на канон?","projectRef":"project","sourceView":"draft","scope":"project",` +
		`"attachment":{"number":1,"title":"Глава 634","paragraphs":40,"words":3120,"note":"текст автора, в книге его ещё нет; текст книги важнее"}}`
	encoded, err := json.Marshal(question)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Project("job-1", []RawItem{
		rawItem("turn-1", TurnStatusCompleted, `{"type":"userMessage","id":"question-1","content":[{"type":"text","text":`+string(encoded)+`}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events) < 1 || snapshot.Events[0].Kind != EventUser || snapshot.Events[0].Attachment == nil ||
		*snapshot.Events[0].Attachment != (QuestionAttachment{Number: 1, Title: "Глава 634", Words: 3120}) {
		t.Fatalf("question = %+v", snapshot.Events)
	}
}
