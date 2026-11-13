// SPDX-License-Identifier: MIT

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// A turn of Codex through a Chat Completions stub: base instructions and a
// turn instruction, two parallel calls of a saga tool answered with arrays of
// items, and an answer that streams text and two new parallel calls. The
// provider gets one system message, both calls and both outputs as text and
// tool_choice; Codex gets the Responses events in order with the usage.
func TestATurnOfCodexThroughAChatProvider(t *testing.T) {
	var sent map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		sent = decodeBody(t, raw)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":"Checking "}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"both chapters."}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_0","function":{"name":"saga-read","arguments":"{\"chapter\":"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_1","function":{"name":"saga-read","arguments":"{\"chapter\":\"b1c2\"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"b1c1\"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":100}}}`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	items := func(text string) []any { return []any{map[string]any{"type": "input_text", "text": text}} }
	request := mustJSON(map[string]any{
		"model": "canonkeeper-chat", "stream": true, "instructions": "You answer from the saga.",
		"tools": sagaTools(), "tool_choice": "auto", "parallel_tool_calls": true,
		"input": []any{
			map[string]any{"type": "message", "role": "developer", "content": items("Cite the chapters.")},
			map[string]any{"type": "message", "role": "user", "content": items("Who crossed the northern pass?")},
			map[string]any{"type": "function_call", "call_id": "call_0", "namespace": "saga", "name": "read", "arguments": `{"chapter":"b1c3"}`},
			map[string]any{"type": "function_call", "call_id": "call_1", "namespace": "saga", "name": "read", "arguments": `{"chapter":"b1c4"}`},
			map[string]any{"type": "function_call_output", "call_id": "call_0", "output": items(`{"text":"The pass was closed."}`)},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": items(`{"text":"Mira crossed at dawn."}`)},
		},
	})
	log, emit := collectEvents()
	adapter := New(server.Client())
	defer adapter.Close()
	result, err := adapter.OpenStream(context.Background(), testTarget(server.URL, chatCapabilities()), Request{Body: request}, emit)
	if err != nil {
		t.Fatal(err)
	}

	messages := sent["messages"].([]any)
	if len(messages) != 5 || messageText(t, messages[0]) != "You answer from the saga.\n\nCite the chapters." ||
		messages[1].(map[string]any)["role"] != "user" || len(messages[2].(map[string]any)["tool_calls"].([]any)) != 2 {
		t.Fatalf("messages: %v", messages)
	}
	if _, contents, _ := toolMessages(t, sent); !reflect.DeepEqual(contents, []string{`{"text":"The pass was closed."}`, `{"text":"Mira crossed at dawn."}`}) {
		t.Fatalf("tool outputs: %q", contents)
	}
	if sent["tool_choice"] != "auto" || !reflect.DeepEqual(sent["stream_options"], map[string]any{"include_usage": true}) {
		t.Fatalf("request: %v", sent)
	}

	type step struct{ kind, item string }
	var steps []step
	lastIndex := -1
	for _, event := range log.events {
		var payload struct {
			OutputIndex *int `json:"output_index"`
			Item        struct {
				Type string `json:"type"`
			} `json:"item"`
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if event.Type == "response.output_item.added" {
			if *payload.OutputIndex != lastIndex+1 {
				t.Fatalf("output index %d after %d", *payload.OutputIndex, lastIndex)
			}
			lastIndex = *payload.OutputIndex
		}
		if len(steps) > 0 && steps[len(steps)-1].kind == event.Type && strings.HasSuffix(event.Type, ".delta") {
			continue
		}
		steps = append(steps, step{event.Type, payload.Item.Type})
	}
	want := []step{
		{"response.created", ""},
		{"response.output_item.added", "message"}, {"response.output_text.delta", ""},
		{"response.output_item.added", "function_call"}, {"response.function_call_arguments.delta", ""},
		{"response.output_item.added", "function_call"}, {"response.function_call_arguments.delta", ""},
		{"response.output_item.done", "message"},
		{"response.output_item.done", "function_call"}, {"response.output_item.done", "function_call"},
		{"response.completed", ""},
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("events:\n%v\nwant\n%v", steps, want)
	}
	terminal := terminalResponse(t, log.events)
	usage := terminal["usage"].(map[string]any)
	if usage["input_tokens"] != float64(120) || usage["output_tokens"] != float64(30) || usage["total_tokens"] != float64(150) ||
		usage["input_tokens_details"].(map[string]any)["cached_tokens"] != float64(100) {
		t.Fatalf("usage: %v", usage)
	}
	if result.Usage.TotalTokens != 150 || !result.UsageKnown || result.Failed {
		t.Fatalf("result: %+v", result)
	}
	var calls []map[string]any
	for _, item := range terminal["output"].([]any) {
		if item.(map[string]any)["type"] == "function_call" {
			calls = append(calls, item.(map[string]any))
		}
	}
	if len(calls) != 2 || calls[0]["call_id"] != "call_0" || calls[0]["namespace"] != "saga" || calls[0]["name"] != "read" ||
		calls[0]["arguments"] != `{"chapter":"b1c1"}` || calls[1]["arguments"] != `{"chapter":"b1c2"}` {
		t.Fatalf("calls: %v", calls)
	}
}
