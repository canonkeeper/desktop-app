package agenttimeline

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	domain "canonkeeper/backend/internal/domain/agenttimeline"
)

// placesStub names the chapters it knows and remembers what each call asked.
type placesStub struct {
	known map[string]string
	asked [][]string
}

func (stub *placesStub) Places(_ context.Context, _ string, keys []string) (map[string]string, error) {
	stub.asked = append(stub.asked, slices.Clone(keys))
	places := map[string]string{}
	for _, key := range keys {
		if place, ok := stub.known[key]; ok {
			places[key] = place
		}
	}
	return places, nil
}

// TestTheTimelineNamesTheChaptersOfItsEvents: beside the keys an event shows — the arguments of its tool, the source
// links of its text — stand the places of their chapters, already while the chapter is being read; a paragraph number
// under the name of a key and a chapter the text does not have get none (specs/2026-11-10-ux1-names.md N2.1).
func TestTheTimelineNamesTheChaptersOfItsEvents(t *testing.T) {
	tool := func(name, arguments string) *domain.ToolCall {
		return &domain.ToolCall{Namespace: "saga", Name: name, Arguments: json.RawMessage(arguments)}
	}
	places := &placesStub{known: map[string]string{
		"b2c12": "Книга 2, гл. 12 „Белый порт“", "481": "Книга 7, гл. 481 „Северная пристань“", "b1c1": "Книга 1, гл. 1 „Туманный берег“",
	}}
	handlers := Handlers{places: places, timeline: timelineStub{snapshot: domain.Snapshot{Events: []domain.Event{
		{ID: "question:0", Kind: domain.EventUser, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1",
			Text: "Что в [гл. 481 ¶2](source:481/2-3)?"},
		{ID: "read:0", Kind: domain.EventToolRequest, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1",
			Tool: tool("read", `{"chapter":"b2c12","from":1,"to":8}`)},
		{ID: "search:0", Kind: domain.EventToolResult, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1",
			Tool: tool("search", `{"queries":["пожар"],"from":"b1c1","to":" b2c12 "}`)},
		{ID: "gone:0", Kind: domain.EventToolRequest, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1",
			Tool: tool("read", `{"chapter":"b9c99"}`)},
		{ID: "answer:0", Kind: domain.EventAssistant, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1",
			Text: "Пожар начался ночью [т. 2, «Белый порт» ¶3](source:b2c12/3)."},
		{ID: "thought:0", Kind: domain.EventReasoning, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1",
			Text: "[x](source:b1c1)"},
	}}}}

	snapshot, err := handlers.Snapshot(t.Context(), "project-1", "job-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]string{
		{"481": "Книга 7, гл. 481 „Северная пристань“"},
		{"b2c12": "Книга 2, гл. 12 „Белый порт“"},
		{"b1c1": "Книга 1, гл. 1 „Туманный берег“", "b2c12": "Книга 2, гл. 12 „Белый порт“"},
		nil,
		{"b2c12": "Книга 2, гл. 12 „Белый порт“"},
		nil,
	}
	for index, event := range snapshot.Events {
		if !reflect.DeepEqual(event.Places, want[index]) {
			t.Errorf("places of %s = %#v, want %#v", event.ID, event.Places, want[index])
		}
	}
	if len(places.asked) != 1 || !reflect.DeepEqual(places.asked[0], []string{"481", "b2c12", "b1c1", "b9c99"}) {
		t.Fatalf("asked = %#v, want one read of each key once", places.asked)
	}
	if body, _ := json.Marshal(snapshot.Events[3]); containsKey(body, "places") {
		t.Fatalf("an event without places = %s", body)
	}
}

// TestAStreamNamesAKeyOnce: the snapshots of one stream read the chapters only for a key they have not named yet.
func TestAStreamNamesAKeyOnce(t *testing.T) {
	places := &placesStub{known: map[string]string{"b2c12": "Книга 2, гл. 12 „Белый порт“"}}
	read := domain.Event{ID: "read:0", Kind: domain.EventToolRequest, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1",
		Tool: &domain.ToolCall{Namespace: "saga", Name: "read", Arguments: json.RawMessage(`{"chapter":"b2c12"}`)}}
	gone := domain.Event{ID: "gone:0", Kind: domain.EventToolRequest, JobID: "job-1", Role: domain.RoleMain, TurnID: "turn-1",
		Tool: &domain.ToolCall{Namespace: "saga", Name: "read", Arguments: json.RawMessage(`{"chapter":"b9c99"}`)}}
	handlers := Handlers{places: places, timeline: timelineStub{snapshot: domain.Snapshot{Events: []domain.Event{read, gone}}}}
	memo := placeMemo{}
	for range 3 {
		envelope, err := handlers.live(t.Context(), "project-1", "job-1", memo)
		if err != nil {
			t.Fatal(err)
		}
		if got := envelope.Snapshot.Events[0].Places["b2c12"]; got != "Книга 2, гл. 12 „Белый порт“" {
			t.Fatalf("place = %q", got)
		}
	}
	if len(places.asked) != 1 {
		t.Fatalf("asked = %#v, want one read for three snapshots", places.asked)
	}
}

func containsKey(body []byte, key string) bool {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	_, ok := fields[key]
	return ok
}
