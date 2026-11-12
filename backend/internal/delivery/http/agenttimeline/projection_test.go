package agenttimeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	domain "canonkeeper/backend/internal/domain/agenttimeline"
)

type timelineStub struct {
	snapshot domain.Snapshot
}

func (stub timelineStub) Snapshot(context.Context, string, string) (domain.Snapshot, error) {
	return stub.snapshot, nil
}

func (timelineStub) Updates(context.Context, string, string) (<-chan domain.Update, error) {
	return make(chan domain.Update), nil
}

func TestSnapshotAndLiveUseByteEquivalentOrderedDTOs(t *testing.T) {
	handlers := Handlers{places: &placesStub{}, timeline: timelineStub{snapshot: domain.Snapshot{Events: []domain.Event{
		{ID: "reasoning-1:0", Kind: domain.EventReasoning, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1", Text: "Сначала"},
		{ID: "assistant-1:0", Kind: domain.EventAssistant, JobID: "job-1", Role: domain.RoleCompleteness, TurnID: "turn-1", Text: "Ответ"},
	}}}}

	snapshot, err := handlers.Snapshot(t.Context(), "project-1", "job-1", "tl_MQ")
	if err != nil {
		t.Fatal(err)
	}
	live, err := handlers.Live(t.Context(), "project-1", "job-1", "tl_MQ")
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON, _ := json.Marshal(snapshot)
	liveJSON, _ := json.Marshal(live.Snapshot)
	if string(snapshotJSON) != string(liveJSON) {
		t.Fatalf("snapshot = %s\nlive = %s", snapshotJSON, liveJSON)
	}
	if len(snapshot.Events) != 2 || snapshot.Events[1].Role != "completeness" ||
		snapshot.Events[0].Cursor == "" || snapshot.Events[0].Cursor == snapshot.Events[1].Cursor {
		t.Fatalf("ordered DTO = %#v", snapshot.Events)
	}
}

func TestSnapshotCursorDoesNotHideReorderedThreadEvents(t *testing.T) {
	root := domain.Event{
		ID: "root-message:0", Kind: domain.EventAssistant,
		JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-root", Text: "root",
	}
	child := domain.Event{
		ID: "child-message:0", Kind: domain.EventAssistant,
		JobID: "job-1", Role: domain.RoleCompleteness, TurnID: "turn-child", Text: "child",
	}
	handlers := Handlers{places: &placesStub{}, timeline: timelineStub{snapshot: domain.Snapshot{Events: []domain.Event{root, child}}}}

	initial, err := handlers.Snapshot(t.Context(), "project-1", "job-1")
	if err != nil {
		t.Fatal(err)
	}
	childCursor := initial.Events[1].Cursor

	rootFinal := domain.Event{
		ID: "root-final:0", Kind: domain.EventAssistant,
		JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-root", Text: "done",
	}
	handlers.timeline = timelineStub{snapshot: domain.Snapshot{Events: []domain.Event{root, rootFinal, child}}}
	reconciled, err := handlers.Snapshot(t.Context(), "project-1", "job-1", childCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciled.Events) != 3 || reconciled.Events[2].ID != child.ID || reconciled.Events[2].Cursor != childCursor {
		t.Fatalf("reconciled = %#v, child cursor = %q", reconciled.Events, childCursor)
	}
}

func TestSnapshotRejectsMalformedCursor(t *testing.T) {
	handlers := Handlers{places: &placesStub{}, timeline: timelineStub{snapshot: domain.Snapshot{}}}
	if _, err := handlers.Snapshot(t.Context(), "project-1", "job-1", "1"); err == nil {
		t.Fatal("numeric cursor was accepted")
	}
}

func TestSnapshotShowsSagaToolSummaryWithoutRawPayload(t *testing.T) {
	handlers := Handlers{places: &placesStub{}, timeline: timelineStub{snapshot: domain.Snapshot{Events: []domain.Event{
		{ID: "assistant-1:0", Kind: domain.EventAssistant, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1", Text: "Ответ"},
		{ID: "tool-1:0", Kind: domain.EventToolResult, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1", Tool: &domain.ToolCall{
			Namespace: "saga", Name: "read", Arguments: json.RawMessage(`{"chapter":"312","around":15}`), Status: "completed",
			Content: []domain.ToolContent{{Type: "inputText", Text: "Том 9, Глава 312. Встреча — гл. 312, абзацы ¶11–¶19 из 40\n¶11 raw manuscript"}},
		}},
	}}}}

	snapshot, err := handlers.Snapshot(t.Context(), "project-1", "job-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	tool := snapshot.Events[1].Tool
	if len(snapshot.Events) != 2 || tool == nil || string(tool.Arguments) != `{"chapter":"312","around":15}` ||
		tool.Summary != "Том 9, Глава 312. Встреча — гл. 312, абзацы ¶11–¶19 из 40" ||
		strings.Contains(encoded, `"content"`) || strings.Contains(encoded, "raw manuscript") {
		t.Fatalf("saga tool view = %s", encoded)
	}
}

func TestSnapshotToolSummaryIsHeaderOrTypedError(t *testing.T) {
	long := strings.Repeat("а", 300)
	cases := []struct {
		name    string
		tool    domain.ToolCall
		args    string
		summary string
	}{
		{"search header", domain.ToolCall{Namespace: "saga", Name: "search", Arguments: json.RawMessage(`{"queries":["Шорох"]}`),
			Content: []domain.ToolContent{{Type: "inputText", Text: "### «Шорох» (any): 56 абзацев в 23 главах\nГлавы с совпадениями: 1(2)"}}},
			`{"queries":["Шорох"]}`, "«Шорох» (any): 56 абзацев в 23 главах"},
		{"typed error", domain.ToolCall{Namespace: "saga", Name: "chapters", Arguments: json.RawMessage(`{"to":"620"}`),
			Content: []domain.ToolContent{{Type: "inputText", Text: `{"status":"error","type":"invalid_arguments","path":"$.to","message":"глава \"620\" не найдена"}`}}},
			`{"to":"620"}`, `глава "620" не найдена`},
		{"long line", domain.ToolCall{Namespace: "saga", Name: "scan", Arguments: json.RawMessage(`{"task":"все места"}`),
			Content: []domain.ToolContent{{Type: "inputText", Text: long}}},
			`{"task":"все места"}`, strings.Repeat("а", 239) + "…"},
		// specs/2026-11-02-image-workshop.md IW9.3: the feed of the helper of a drawing names what each edit asked.
		{"drawing helper", domain.ToolCall{Namespace: "drawing", Name: "edit", Arguments: json.RawMessage(`{"instruction":"два рога","region":{"x":0.5,"y":0.1,"w":0.1,"h":0.1}}`),
			Content: []domain.ToolContent{{Type: "inputText", Text: "Рисунок 2: правка 1 готова.\nСверка: 1 из 1"}}},
			`{"instruction":"два рога","region":{"x":0.5,"y":0.1,"w":0.1,"h":0.1}}`, "Рисунок 2: правка 1 готова."},
		{"previous assistant", domain.ToolCall{Namespace: "source", Name: "read", Arguments: json.RawMessage(`{"ref":"s3"}`),
			Content: []domain.ToolContent{{Type: "inputText", Text: "Глава"}}}, "", ""},
		{"not an object", domain.ToolCall{Namespace: "saga", Name: "read", Arguments: json.RawMessage(`["312"]`)}, "", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tool := testCase.tool
			tool.Status = "completed"
			handlers := Handlers{places: &placesStub{}, timeline: timelineStub{snapshot: domain.Snapshot{Events: []domain.Event{
				{ID: "tool-1:0", Kind: domain.EventToolResult, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1", Tool: &tool},
			}}}}
			snapshot, err := handlers.Snapshot(t.Context(), "project-1", "job-1")
			if err != nil {
				t.Fatal(err)
			}
			got := snapshot.Events[0].Tool
			if string(got.Arguments) != testCase.args || got.Summary != testCase.summary {
				t.Fatalf("arguments = %s, summary = %q", got.Arguments, got.Summary)
			}
		})
	}
}

// specs/2026-10-29-saga-illustrations.md R15.9: a question event carries its attached images.
func TestSnapshotShowsTheImagesOfAQuestion(t *testing.T) {
	handlers := Handlers{places: &placesStub{}, timeline: timelineStub{snapshot: domain.Snapshot{Events: []domain.Event{
		{ID: "question-1", Kind: domain.EventUser, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1", Text: "Нарисуй", Images: []int{81, 82}},
		{ID: "assistant-1:0", Kind: domain.EventAssistant, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1", Text: "Ответ"},
	}}}}
	snapshot, err := handlers.Snapshot(t.Context(), "project-1", "job-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"text":"Нарисуй","images":[81,82]`) || strings.Count(string(raw), `"images"`) != 1 {
		t.Fatalf("snapshot = %s", raw)
	}
}

// specs/2026-10-30-chat-steering.md R3.1: a correction reaches the web with its text, images, anchor
// and id, and nothing of its native input.
func TestSnapshotShowsACorrectionWithoutItsNativeInput(t *testing.T) {
	input := `{"question":"Без дракона","projectRef":"project","sourceView":"published","scope":"project","attachedImages":[90],"attachedPhotos":["фото 3 — изображение 90 (Анна)"],"correction":true,"firstPhoto":3}`
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := domain.Project("job-1", []domain.RawItem{{
		Role: domain.RoleMain, TurnID: "turn-1", TurnStatus: domain.TurnStatusInProgress,
		Item: json.RawMessage(`{"type":"userMessage","id":"item-3","clientId":"steer-1","content":[{"type":"text","text":` + string(encoded) + `,"text_elements":[]}]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	dto := snapshotDTO(snapshot)
	if len(dto.Events) != 1 {
		t.Fatalf("events = %+v", dto.Events)
	}
	event := dto.Events[0]
	if event.Kind != domain.EventSteer || event.Text != "Без дракона" || event.SteerID != "steer-1" || event.FirstPhoto != 3 ||
		len(event.Images) != 1 || event.Images[0] != 90 {
		t.Fatalf("event = %+v", event)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"attachedPhotos", "изображение 90", "correction", "projectRef"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("the feed shows %q: %s", leak, raw)
		}
	}
}

// specs/2026-11-07-chat-attach-chapter.md CA4.1: a question event carries the card of its attached chapter.
func TestSnapshotShowsTheChapterAttachedToAQuestion(t *testing.T) {
	handlers := Handlers{places: &placesStub{}, timeline: timelineStub{snapshot: domain.Snapshot{Events: []domain.Event{
		{ID: "question-1", Kind: domain.EventUser, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1", Text: "Ложится?",
			Attachment: &domain.QuestionAttachment{Number: 2, Title: "Глава 634", Words: 3120}},
		{ID: "assistant-1:0", Kind: domain.EventAssistant, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1", Text: "Ответ"},
	}}}}
	snapshot, err := handlers.Snapshot(t.Context(), "project-1", "job-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"attachment":{"number":2,"title":"Глава 634","words":3120}`) || strings.Count(string(raw), `"attachment"`) != 1 {
		t.Fatalf("snapshot = %s", raw)
	}
}
