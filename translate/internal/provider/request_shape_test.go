// SPDX-License-Identifier: MIT

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

const chatAnswer = `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`

// providerStub answers every request with reply (JSON, or SSE when it starts
// with "data:") and hands the request bodies to the test.
func providerStub(t *testing.T, reply string) (*httptest.Server, <-chan []byte) {
	t.Helper()
	bodies := make(chan []byte, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies <- body
		if len(reply) > 5 && reply[:5] == "data:" {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(server.Close)
	return server, bodies
}

// sentBody executes request against a stub and decodes what the provider got.
func sentBody(t *testing.T, capabilities Capabilities, request map[string]any) map[string]any {
	t.Helper()
	reply := chatAnswer
	if capabilities.Protocol == ProtocolResponses {
		reply = `{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":1}}`
	}
	server, bodies := providerStub(t, reply)
	adapter := New(server.Client())
	defer adapter.Close()
	if _, err := adapter.Execute(context.Background(), testTarget(server.URL, capabilities), Request{Body: mustJSON(request)}); err != nil {
		t.Fatal(err)
	}
	return decodeBody(t, <-bodies)
}

func messageText(t *testing.T, message any) string {
	t.Helper()
	text, ok := message.(map[string]any)["content"].(string)
	if !ok {
		t.Fatalf("message content is not text: %v", message)
	}
	return text
}

// TR2.1 (F1): instructions, system and developer messages become one first
// system message in the order of the input; none of them replaces another.
func TestInstructionsSystemAndDeveloperMessagesJoinIntoOneFirstSystemMessage(t *testing.T) {
	body := sentBody(t, chatCapabilities(), map[string]any{
		"model": "m", "instructions": "base instructions",
		"input": []any{
			map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "turn instructions"}}},
			map[string]any{"type": "message", "role": "user", "content": "question"},
			map[string]any{"type": "message", "role": "system", "content": "late system note"},
			map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "answer"}}},
			map[string]any{"type": "message", "role": "developer", "content": "permissions"},
			map[string]any{"type": "message", "role": "user", "content": "next"},
		},
	})
	messages := body["messages"].([]any)
	roles := make([]string, len(messages))
	for i, message := range messages {
		roles[i] = message.(map[string]any)["role"].(string)
	}
	if !reflect.DeepEqual(roles, []string{"system", "user", "assistant", "user"}) {
		t.Fatalf("roles: %v", roles)
	}
	if got := messageText(t, messages[0]); got != "base instructions\n\nturn instructions\n\nlate system note\n\npermissions" {
		t.Fatalf("system message: %q", got)
	}
}

// TR2.2 (F2): a model without reasoning gets no reasoning instead of a refusal.
func TestReasoningOfAModelWithoutReasoningIsDropped(t *testing.T) {
	chat := sentBody(t, chatCapabilities(), map[string]any{
		"model": "m", "input": "hi", "reasoning": map[string]any{"effort": "high", "summary": "auto"},
	})
	if _, ok := chat["reasoning_effort"]; ok {
		t.Fatalf("Chat request kept reasoning: %v", chat)
	}
	responses := sentBody(t, responsesCapabilities(), map[string]any{
		"model": "m", "input": "hi", "reasoning": map[string]any{"effort": "high"},
		"include": []string{"reasoning.encrypted_content", "message.output_text.logprobs"},
	})
	if _, ok := responses["reasoning"]; ok || !reflect.DeepEqual(responses["include"], []any{"message.output_text.logprobs"}) {
		t.Fatalf("Responses request kept reasoning: %v", responses)
	}
}

// TR2.2 (F2): efforts above high reach the provider as high unless the
// profile declares a higher one.
func TestReasoningEffortAboveTheProfileIsLowered(t *testing.T) {
	for _, test := range []struct {
		effort, max, want string
	}{
		{"medium", "", "medium"},
		{"high", "", "high"},
		{"xhigh", "", "high"},
		{"max", "", "high"},
		{"ultra", "", "high"},
		{"xhigh", "xhigh", "xhigh"},
		{"ultra", "xhigh", "xhigh"},
		{"ultra", "max", "max"},
		{"minimal", "max", "minimal"},
	} {
		chatProfile := chatCapabilities()
		chatProfile.Reasoning, chatProfile.MaxReasoningEffort = true, test.max
		chat := sentBody(t, chatProfile, map[string]any{"model": "m", "input": "hi", "reasoning": map[string]any{"effort": test.effort}})
		responsesProfile := responsesCapabilities()
		responsesProfile.Reasoning, responsesProfile.MaxReasoningEffort = true, test.max
		responses := sentBody(t, responsesProfile, map[string]any{"model": "m", "input": "hi", "reasoning": map[string]any{"effort": test.effort, "summary": "auto"}})
		if chat["reasoning_effort"] != test.want || !reflect.DeepEqual(responses["reasoning"], map[string]any{"effort": test.want, "summary": "auto"}) {
			t.Fatalf("%s under %q: chat=%v responses=%v", test.effort, test.max, chat["reasoning_effort"], responses["reasoning"])
		}
	}
}

func TestAProfileCannotDeclareAnUnknownEffort(t *testing.T) {
	profile := chatCapabilities()
	profile.MaxReasoningEffort = "extreme"
	_, err := New(nil).Execute(context.Background(), testTarget("http://127.0.0.1:1", profile), Request{Body: json.RawMessage(`{"model":"m","input":"hi"}`)})
	if code := errorCode(err); code != ErrorCodeInvalidConfiguration {
		t.Fatalf("unknown effort accepted: %v", err)
	}
}

func errorCode(err error) string {
	var failure *Error
	if !errors.As(err, &failure) {
		return ""
	}
	return failure.Code
}

// sagaTools are function, namespace and custom tools as Codex declares them.
func sagaTools() []any {
	return []any{
		map[string]any{"type": "function", "name": "saga_search", "parameters": map[string]any{"type": "object"}},
		map[string]any{"type": "namespace", "name": "saga", "tools": []any{
			map[string]any{"type": "function", "name": "read", "parameters": map[string]any{"type": "object"}},
		}},
		map[string]any{"type": "custom", "name": "apply_patch"},
	}
}

// TR2.9 (F9): tool_choice reaches a Chat provider in the Chat form.
func TestToolChoiceReachesTheChatProvider(t *testing.T) {
	named := func(name string) map[string]any {
		return map[string]any{"type": "function", "function": map[string]any{"name": name}}
	}
	for _, test := range []struct {
		choice, want any
	}{
		{"auto", "auto"},
		{"none", "none"},
		{"required", "required"},
		{map[string]any{"type": "function", "name": "saga_search"}, named("saga_search")},
		{map[string]any{"type": "function", "name": "read", "namespace": "saga"}, named("saga-read")},
		{map[string]any{"type": "custom", "name": "apply_patch"}, named("apply_patch")},
	} {
		body := sentBody(t, chatCapabilities(), map[string]any{"model": "m", "input": "hi", "tools": sagaTools(), "tool_choice": test.choice})
		if !reflect.DeepEqual(body["tool_choice"], test.want) {
			t.Fatalf("tool_choice %v reached the provider as %v", test.choice, body["tool_choice"])
		}
	}
	if body := sentBody(t, chatCapabilities(), map[string]any{"model": "m", "input": "hi", "tool_choice": "auto"}); body["tool_choice"] != nil {
		t.Fatalf("tool_choice without tools: %v", body["tool_choice"])
	}
	for _, test := range []struct {
		tools  []any
		choice any
		want   string
	}{
		{sagaTools(), map[string]any{"type": "function", "name": "missing"}, ErrorCodeInvalidRequest},
		{nil, "required", ErrorCodeInvalidRequest},
		{sagaTools(), "sometimes", ErrorCodeInvalidRequest},
		{sagaTools(), map[string]any{"type": "web_search"}, ErrorCodeUnsupportedCapability},
	} {
		_, err := New(nil).Execute(context.Background(), testTarget("http://127.0.0.1:1", chatCapabilities()), Request{Body: mustJSON(map[string]any{
			"model": "m", "input": "hi", "tools": test.tools, "tool_choice": test.choice,
		})})
		if errorCode(err) != test.want {
			t.Fatalf("tool_choice %v: %v", test.choice, err)
		}
	}
}

// toolMessages are the role "tool" messages a provider got, with their ids.
func toolMessages(t *testing.T, body map[string]any) (ids, contents []string, callIDs [][]string) {
	t.Helper()
	for _, raw := range body["messages"].([]any) {
		message := raw.(map[string]any)
		switch message["role"] {
		case "tool":
			ids = append(ids, message["tool_call_id"].(string))
			contents = append(contents, message["content"].(string))
		case "assistant":
			calls, _ := message["tool_calls"].([]any)
			group := []string{}
			for _, call := range calls {
				group = append(group, call.(map[string]any)["id"].(string))
			}
			callIDs = append(callIDs, group)
		}
	}
	return ids, contents, callIDs
}

// TR2.10 (F10): a tool output given as an array of content items reaches the
// provider as its text, never as the JSON of the array.
func TestAToolOutputGivenAsItemsIsJoinedIntoText(t *testing.T) {
	body := sentBody(t, chatCapabilities(), map[string]any{
		"model": "m", "tools": sagaTools(),
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "find"},
			map[string]any{"type": "function_call", "call_id": "c1", "name": "saga_search", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "c1", "output": []any{
				map[string]any{"type": "input_text", "text": `{"hits":2}`},
				map[string]any{"type": "input_text", "text": "second block"},
			}},
			map[string]any{"type": "function_call", "call_id": "c2", "name": "saga_search", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "c2", "output": []any{
				map[string]any{"type": "input_text", "text": "a picture"},
				map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAA"},
			}},
			map[string]any{"type": "function_call", "call_id": "c3", "name": "saga_search", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "c3", "output": map[string]any{"ok": true}},
		},
	})
	_, contents, _ := toolMessages(t, body)
	want := []string{`{"hits":2}` + "\nsecond block", "a picture\n" + omittedToolPart("input_image"), `{"ok":true}`}
	if !reflect.DeepEqual(contents, want) {
		t.Fatalf("tool outputs: %q", contents)
	}
}

// TR2.11 (F11): a call_id that comes again (a provider numbering its calls in
// each answer) keeps both calls and both outputs, told apart and in order.
func TestARepeatedCallIDKeepsBothCallsAndOutputsInOrder(t *testing.T) {
	call := func(id, query string) map[string]any {
		return map[string]any{"type": "function_call", "call_id": id, "name": "saga_search", "arguments": `{"q":"` + query + `"}`}
	}
	output := func(id, text string) map[string]any {
		return map[string]any{"type": "function_call_output", "call_id": id, "output": text}
	}
	body := sentBody(t, chatCapabilities(), map[string]any{
		"model": "m", "tools": sagaTools(),
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "find"},
			call("call_0", "a"), output("call_0", "first"),
			call("call_0", "b"), call("call_1", "c"), output("call_0", "second"), output("call_1", "third"),
			call("call_0", "d"), call("call_0", "e"), output("call_0", "fourth"), output("call_0", "fifth"),
			map[string]any{"type": "message", "role": "user", "content": "go on"},
		},
	})
	ids, contents, calls := toolMessages(t, body)
	if !reflect.DeepEqual(contents, []string{"first", "second", "third", "fourth", "fifth"}) {
		t.Fatalf("outputs lost or reordered: %q", contents)
	}
	flat := []string{}
	for _, group := range calls {
		flat = append(flat, group...)
	}
	if !reflect.DeepEqual(flat, ids) || len(calls) != 3 || len(calls[1]) != 2 || len(calls[2]) != 2 {
		t.Fatalf("calls %v do not pair with outputs %v", calls, ids)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("ids are not told apart: %v", ids)
		}
		seen[id] = true
	}
	if ids[0] != "call_0" || ids[2] != "call_1" {
		t.Fatalf("a first id changed: %v", ids)
	}
}
