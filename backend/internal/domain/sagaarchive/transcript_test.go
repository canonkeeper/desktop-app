// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"encoding/json"
	"reflect"
	"testing"

	timeline "canonkeeper/backend/internal/domain/agenttimeline"
)

func question(t *testing.T, text string) string {
	t.Helper()
	assignment, err := json.Marshal(map[string]string{
		"question": text, "projectRef": "project", "sourceView": "draft", "scope": "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err := json.Marshal(map[string]any{
		"type": "userMessage", "id": "user-" + text, "content": []map[string]string{{"type": "text", "text": string(assignment)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(item)
}

func item(turnID string, status timeline.TurnStatus, raw string) timeline.RawItem {
	return timeline.RawItem{Role: timeline.RoleMain, TurnID: turnID, TurnStatus: status, Item: json.RawMessage(raw)}
}

func chatThread(t *testing.T) []timeline.RawItem {
	return []timeline.RawItem{
		item("turn-1", timeline.TurnStatusCompleted, question(t, "Кто такой Арен?")),
		item("turn-1", timeline.TurnStatusCompleted, `{"type":"agentMessage","id":"a1","text":"Ищу в главах","phase":"commentary"}`),
		item("turn-1", timeline.TurnStatusCompleted, `{"type":"dynamicToolCall","id":"tool-1","namespace":"saga","tool":"search","arguments":{"q":"Арен"},"status":"completed","success":true}`),
		item("turn-1", timeline.TurnStatusCompleted, `{"type":"reasoning","id":"r1","summary":["думаю"]}`),
		item("turn-1", timeline.TurnStatusCompleted, `{"type":"agentMessage","id":"a2","text":"Арен — наследник дома.","phase":"final_answer"}`),
		item("turn-2", timeline.TurnStatusInterrupted, question(t, "А его сестра?")),
		item("turn-2", timeline.TurnStatusInterrupted, `{"type":"agentMessage","id":"a3","text":"Проверяю","phase":"commentary"}`),
		item("turn-3", timeline.TurnStatusInProgress, question(t, "Где он родился?")),
	}
}

// The transcript of a chat holds the questions and the answers of its turns, not the steps (DA12.2.3).
func TestTranscriptKeepsQuestionsAndAnswersOnly(t *testing.T) {
	turns, err := Transcript("chat-1", chatThread(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []TranscriptTurn{
		{ProviderTurnID: "turn-1", Question: "Кто такой Арен?", Answer: "Арен — наследник дома.", Outcome: TurnAnswered},
		{ProviderTurnID: "turn-2", Question: "А его сестра?", Outcome: TurnInterrupted},
		{ProviderTurnID: "turn-3", Question: "Где он родился?", Outcome: TurnInProgress},
	}
	if len(turns) != len(want) {
		t.Fatalf("turns = %+v", turns)
	}
	for index := range want {
		if !reflect.DeepEqual(turns[index], want[index]) {
			t.Fatalf("turn %d = %+v, want %+v", index, turns[index], want[index])
		}
	}
}

// specs/2026-10-30-chat-steering.md R3.7: what the author told the agent during a turn is his text too.
func TestTranscriptKeepsTheCorrectionsOfATurn(t *testing.T) {
	correction, err := json.Marshal(`{"question":"Без дракона","projectRef":"project","sourceView":"draft","scope":"project","correction":true}`)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := Transcript("chat-1", []timeline.RawItem{
		item("turn-1", timeline.TurnStatusCompleted, question(t, "Нарисуй обложку")),
		item("turn-1", timeline.TurnStatusCompleted, `{"type":"userMessage","id":"s-1","content":[{"type":"text","text":`+string(correction)+`}]}`),
		item("turn-1", timeline.TurnStatusCompleted, `{"type":"agentMessage","id":"a-1","text":"Готово"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].Question != "Нарисуй обложку" || turns[0].Answer != "Готово" ||
		len(turns[0].Corrections) != 1 || turns[0].Corrections[0] != "Без дракона" {
		t.Fatalf("turns = %+v", turns)
	}
}
