// SPDX-License-Identifier: MIT

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func testTarget(serverURL string, capabilities Capabilities) Target {
	return Target{BaseURL: serverURL + "/v1", Credential: "secret-token", Capabilities: capabilities}
}

type eventLog struct {
	mu     sync.Mutex
	events []Event
}

func collectEvents() (*eventLog, func(Event) error) {
	log := &eventLog{}
	return log, func(event Event) error {
		log.mu.Lock()
		defer log.mu.Unlock()
		log.events = append(log.events, event)
		return nil
	}
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return value
}

func findOutput(t *testing.T, response []byte, kind string) map[string]any {
	t.Helper()
	var payload struct {
		Output []map[string]any `json:"output"`
	}
	if err := json.Unmarshal(response, &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, item := range payload.Output {
		if item["type"] == kind {
			return item
		}
	}
	t.Fatalf("output type %q not found in %s", kind, response)
	return nil
}

func TestPrepareResponsesReleasesParsedPayloadWithoutDroppingWireFields(t *testing.T) {
	body := mustJSON(map[string]any{"model": "m", "stream": true,
		"input":  []any{map[string]any{"role": "user", "content": strings.Repeat("x", 32<<10)}},
		"system": "system text", "instructions": "instruction text",
		"tools": []any{map[string]any{"type": "function", "name": "tool", "parameters": map[string]any{"type": "object"}}},
	})
	capabilities := responsesCapabilities()
	target, err := prepareTarget(testTarget("http://127.0.0.1", capabilities))
	if err != nil {
		t.Fatal(err)
	}
	req, items, _, err := prepareRequest(Request{Body: body}, target)
	if err != nil {
		t.Fatal(err)
	}
	if req.Input != nil || req.Tools != nil || req.System != "" || req.Instructions != "" || items != nil {
		t.Fatal("Responses retained a second parsed payload")
	}
	wire, err := passthroughBody(req, target)
	if err != nil || string(wire) != string(body) || &wire[0] != &body[0] {
		t.Fatalf("required wire fields changed or were copied: %v", err)
	}
}

func TestPrepareChatRequestReusesCallerWireAndParsedInput(t *testing.T) {
	body := json.RawMessage(`{"model":"m","stream":true,"unknown":true,"input":[{"role":"user","content":"Привет 🌊"}]}`)
	before := string(body)
	capabilities := chatCapabilities()
	target, err := prepareTarget(testTarget("http://127.0.0.1", capabilities))
	if err != nil {
		t.Fatal(err)
	}
	req, items, catalog, err := prepareRequest(Request{Body: body}, target)
	if err != nil {
		t.Fatal(err)
	}
	if &req.Raw[0] != &body[0] {
		t.Fatal("Responses wire was copied before translation")
	}
	chat, err := translateToChat(req, items, catalog, target, true)
	if err != nil {
		t.Fatal(err)
	}
	messages := chat.Messages
	if len(messages) != 1 || !strings.Contains(string(messages[0].Content), "Привет 🌊") ||
		!chat.Stream || string(body) != before {
		t.Fatalf("translation changed semantics: messages=%#v body=%s", messages, body)
	}
}

func TestExecuteChatTranslatesToolsReasoningImageAndFollowUp(t *testing.T) {
	requests := make(chan []byte, 2)
	responses := []string{
		`{"choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"thinking","tool_calls":[
			{"id":"call_custom","type":"function","function":{"name":"apply_patch","arguments":"{\"patch\":\"alpha\"}"}},
			{"id":"call_ns","type":"function","function":{"name":"mcp-git-get","arguments":"{\"ref\":\"main\"}"}}
		]}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":4}}}`,
		`{"choices":[{"index":0,"message":{"role":"assistant","content":"done"}}],"usage":{"prompt_tokens":20,"completion_tokens":2,"total_tokens":22}}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- body
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Error("credential was not sent as bearer token")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responses[len(requests)-1]))
	}))
	defer server.Close()

	capabilities := glmCapabilities()
	capabilities.ImageInput = true
	capabilities.ExtraBody = map[string]json.RawMessage{"provider_option": json.RawMessage(`"safe"`)}
	adapter := New(server.Client())
	question := map[string]any{
		"type": "message", "role": "user",
		"content": []any{
			map[string]any{"type": "input_text", "text": "look"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAA"},
		},
	}
	first := mustJSON(map[string]any{
		"model":        "codex-model",
		"instructions": "system prompt",
		"input":        []any{question},
		"tools": []any{
			map[string]any{"type": "custom", "name": "apply_patch"},
			map[string]any{"type": "namespace", "name": "mcp-git", "tools": []any{
				map[string]any{"type": "function", "name": "get", "parameters": map[string]string{"type": "object"}},
			}},
		},
		"parallel_tool_calls": true,
		"reasoning":           map[string]string{"effort": "high"},
		"stream":              false,
	})
	target := testTarget(server.URL, capabilities)
	target.Model = "glm-5.2"
	result, err := adapter.Execute(context.Background(), target, Request{Body: first})
	if err != nil {
		t.Fatal(err)
	}
	var requestBody map[string]any
	if err := json.Unmarshal(<-requests, &requestBody); err != nil {
		t.Fatal(err)
	}
	if requestBody["model"] != "glm-5.2" || !reflect.DeepEqual(requestBody["thinking"], map[string]any{"type": "enabled"}) ||
		requestBody["reasoning_effort"] != "high" || requestBody["provider_option"] != "safe" {
		t.Fatalf("request customization was not applied: %s", requestBody)
	}
	messages := requestBody["messages"].([]any)
	if messages[0].(map[string]any)["role"] != "system" || len(messages) != 2 {
		t.Fatalf("unexpected messages: %v", messages)
	}
	secondMessage := messages[1].(map[string]any)
	if secondMessage["role"] != "user" {
		t.Fatalf("second message role: %v", secondMessage)
	}
	parts := secondMessage["content"].([]any)
	if parts[1].(map[string]any)["type"] != "image_url" {
		t.Fatalf("image input was not translated: %v", parts)
	}

	custom := findOutput(t, result.Response, "custom_tool_call")
	namespaced := findOutput(t, result.Response, "function_call")
	if custom["input"] != "alpha" || namespaced["namespace"] != "mcp-git" || namespaced["name"] != "get" {
		t.Fatalf("tool outputs were not decoded: %v %v", custom, namespaced)
	}
	if result.Usage.CachedTokens != 4 || result.Usage.TotalTokens != 15 {
		t.Fatalf("usage was not preserved: %+v", result.Usage)
	}

	// The follow-up resends the whole conversation, as Codex over HTTP does:
	// the reasoning item of the first answer returns as reasoning_content of
	// the assistant message with its two calls.
	var replayed struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(result.Response, &replayed); err != nil {
		t.Fatal(err)
	}
	input := []any{question}
	for _, item := range replayed.Output {
		input = append(input, item)
	}
	input = append(input,
		map[string]any{"type": "custom_tool_call_output", "call_id": custom["call_id"], "output": "patch result"},
		map[string]any{"type": "function_call_output", "call_id": namespaced["call_id"], "output": map[string]any{"ok": true}},
	)
	second := mustJSON(map[string]any{
		"model": "codex-model", "instructions": "system prompt", "input": input,
		"tools": []any{
			map[string]any{"type": "custom", "name": "apply_patch"},
			map[string]any{"type": "namespace", "name": "mcp-git", "tools": []any{
				map[string]any{"type": "function", "name": "get", "parameters": map[string]string{"type": "object"}},
			}},
		},
		"stream": false,
	})
	if _, err = adapter.Execute(context.Background(), target, Request{Body: second}); err != nil {
		t.Fatal(err)
	}
	var followUp map[string]any
	if err := json.Unmarshal(<-requests, &followUp); err != nil {
		t.Fatal(err)
	}
	roles := make([]string, 0)
	for _, raw := range followUp["messages"].([]any) {
		message := raw.(map[string]any)
		roles = append(roles, message["role"].(string))
		if message["role"] == "assistant" && (len(message["tool_calls"].([]any)) != 2 || message["reasoning_content"] != "thinking") {
			t.Fatalf("assistant parallel tool calls or their reasoning were not retained: %v", message)
		}
	}
	if strings.Join(roles, ",") != "system,user,assistant,tool,tool" {
		t.Fatalf("follow-up roles: %v", roles)
	}
}

func TestOpenStreamChatEmitsReasoningTextParallelToolsAndUsage(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = decodeBody(t, raw)
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(value any) {
			encoded, _ := json.Marshal(value)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
		}
		send(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"reasoning_content": "think"}}}})
		send(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "hello "}}}})
		send(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "call_1", "function": map[string]any{"name": "shell", "arguments": `{"cmd":`}},
		}}}}})
		send(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 1, "id": "call_2", "function": map[string]any{"name": "shell", "arguments": "{}"}},
		}}}}})
		send(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}})
		send(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10}})
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	request := mustJSON(map[string]any{
		"model": "glm-5.2", "input": "hi", "stream": true,
		"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]string{"type": "object"}}},
	})
	log, emit := collectEvents()
	adapter := New(server.Client())
	result, err := adapter.OpenStream(context.Background(), testTarget(server.URL, glmCapabilities()), Request{Body: request}, emit)
	if err != nil {
		t.Fatal(err)
	}
	log.mu.Lock()
	types := make([]string, len(log.events))
	for i, event := range log.events {
		types[i] = event.Type
	}
	log.mu.Unlock()
	for _, required := range []string{"response.created", "response.reasoning_text.delta", "response.output_text.delta", "response.function_call_arguments.delta", "response.completed"} {
		found := false
		for _, kind := range types {
			if kind == required {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing event %q in %v", required, types)
		}
	}
	if result.Usage.TotalTokens != 10 || body["stream"] != true || !reflect.DeepEqual(body["stream_options"], map[string]any{"include_usage": true}) {
		t.Fatalf("stream contract or usage was not preserved: %+v %+v", result, body)
	}
	terminal := findOutput(t, result.Response, "function_call")
	if terminal["call_id"] != "call_1" || terminal["arguments"] != `{"cmd":` {
		t.Fatalf("parallel tool call was not finalized correctly: %v", terminal)
	}
	log.mu.Lock()
	eventItemID := ""
	for _, event := range log.events {
		if event.Type != "response.output_item.done" {
			continue
		}
		var payload struct {
			Item struct {
				Type   string `json:"type"`
				ID     string `json:"id"`
				CallID string `json:"call_id"`
			} `json:"item"`
		}
		_ = json.Unmarshal(event.Data, &payload)
		if payload.Item.Type == "function_call" && payload.Item.CallID == "call_1" {
			eventItemID = payload.Item.ID
		}
	}
	log.mu.Unlock()
	if eventItemID == "" || eventItemID != terminal["id"] {
		t.Fatalf("terminal output item ID differs from emitted item: %q != %v", eventItemID, terminal["id"])
	}
}

func TestOpenStreamDisconnectDoesNotCreateSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"))
	}))
	defer server.Close()
	adapter := New(server.Client())
	log, emit := collectEvents()
	_, err := adapter.OpenStream(context.Background(), testTarget(server.URL, chatCapabilities()), Request{
		Body: mustJSON(map[string]any{"model": "m", "input": "hi", "stream": true}),
	}, emit)
	var upstreamErr *Error
	if !errors.As(err, &upstreamErr) || upstreamErr.Code != ErrorCodeStreamIncomplete {
		t.Fatalf("expected stream_incomplete, got %#v", err)
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	for _, event := range log.events {
		if event.Type == "response.completed" {
			t.Fatal("disconnect was reported as completed")
		}
	}
}

func TestOpenStreamCancellationReturnsContextError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	adapter := New(server.Client())
	_, err := adapter.OpenStream(ctx, testTarget(server.URL, chatCapabilities()), Request{
		Body: mustJSON(map[string]any{"model": "m", "input": "hi", "stream": true}),
	}, func(Event) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline, got %#v", err)
	}
}

func TestResponsesPassthroughForwardAndTerminalUsage(t *testing.T) {
	var body []byte
	var sawHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		sawHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_up\",\"status\":\"in_progress\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_up\",\"status\":\"completed\",\"usage\":{\"input_tokens\":2,\"output_tokens\":1,\"total_tokens\":3}}}\n\n"))
	}))
	defer server.Close()
	request := mustJSON(map[string]any{
		"model": "native", "input": "hi", "stream": true,
		"text":    map[string]any{"format": map[string]any{"type": "text"}},
		"include": []string{"reasoning.encrypted_content"},
		"store":   false, "service_tier": "auto",
		"metadata":              map[string]string{"user_id": "u"},
		"future_response_field": map[string]bool{"enabled": true},
	})
	log, emit := collectEvents()
	adapter := New(server.Client())
	capabilities := responsesCapabilities()
	capabilities.Reasoning = true
	target := testTarget(server.URL, capabilities)
	target.Headers = http.Header{
		"Chatgpt-Account-Id": []string{"acct"},
		"X-Codex-Turn-State": []string{"turn"},
		"Session_id":         []string{"scoped-session"},
	}
	result, err := adapter.OpenStream(context.Background(), target, Request{Body: request}, emit)
	if err != nil {
		t.Fatal(err)
	}
	if result.ResponseID != "resp_up" || result.Usage.TotalTokens != 3 || result.Failed {
		t.Fatalf("passthrough terminal result: %+v", result)
	}
	if string(body) != string(request) {
		t.Fatalf("Responses body was changed: %s", body)
	}
	if sawHeaders.Get("ChatGPT-Account-ID") != "acct" || sawHeaders.Get("X-Codex-Turn-State") != "turn" ||
		sawHeaders.Get("Session_id") != "scoped-session" ||
		sawHeaders.Get("Authorization") != "Bearer secret-token" || sawHeaders.Get("Cookie") != "" {
		t.Fatalf("the headers the target names were not sent as named: %+v", sawHeaders)
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.events) != 2 || log.events[0].Type != "response.created" || log.events[1].Type != "response.completed" {
		t.Fatalf("unexpected passthrough events: %+v", log.events)
	}
}

func TestQuotaHTTPErrorCodeIsNotCollapsedToRateLimit(t *testing.T) {
	for _, code := range []string{"1308", "insufficient_quota", "usage_limit_reached"} {
		body := fmt.Sprintf(`{"error":{"code":%q,"message":"quota"}}`, code)
		err := upstreamHTTPError(http.StatusTooManyRequests, []byte(body))
		if err.Code != ErrorCodeInsufficientQuota {
			t.Fatalf("quota code %q became %q", code, err.Code)
		}
	}
	err := upstreamHTTPError(http.StatusTooManyRequests, []byte(`{"error":{"code":"temporarily_busy"}}`))
	if err.Code != ErrorCodeRateLimited {
		t.Fatalf("temporary 429 became %q", err.Code)
	}
}

func TestUnsupportedCapabilitiesAreRejectedBeforeDispatch(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	adapter := New(server.Client())

	_, err := adapter.Execute(context.Background(), testTarget(server.URL, chatCapabilities()), Request{
		Body: mustJSON(map[string]any{"model": "m", "input": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAA"}}}}, "stream": false}),
	})
	var upstreamErr *Error
	if !errors.As(err, &upstreamErr) || upstreamErr.Code != ErrorCodeUnsupportedCapability || calls != 0 {
		t.Fatalf("image capability was not rejected before dispatch: %#v calls=%d", err, calls)
	}
	_, err = adapter.Execute(context.Background(), testTarget(server.URL, chatCapabilities()), Request{
		Body: mustJSON(map[string]any{"model": "m", "input": "hi", "tools": []any{map[string]string{"type": "web_search"}}, "stream": false}),
	})
	if !errors.As(err, &upstreamErr) || upstreamErr.Code != ErrorCodeUnsupportedCapability || calls != 0 {
		t.Fatalf("hosted tool was not rejected before dispatch: %#v calls=%d", err, calls)
	}
	_, err = adapter.Execute(context.Background(), testTarget(server.URL, chatCapabilities()), Request{
		Body: mustJSON(map[string]any{"model": "m", "previous_response_id": "missing", "input": "hi", "stream": false}),
	})
	if !errors.As(err, &upstreamErr) || upstreamErr.Code != ErrorCodeUnsupportedCapability || calls != 0 {
		t.Fatalf("a continuation a Chat provider cannot hold was not rejected: %#v calls=%d", err, calls)
	}
}

func TestSafeCustomizationCannotOverrideProtocolFields(t *testing.T) {
	capabilities := chatCapabilities()
	capabilities.ExtraBody = map[string]json.RawMessage{"messages": json.RawMessage(`[]`)}
	_, err := New(nil).Execute(context.Background(), Target{
		BaseURL: "http://127.0.0.1:1", Capabilities: capabilities,
	}, Request{Body: mustJSON(map[string]any{"model": "m", "input": "hi"})})
	var upstreamErr *Error
	if !errors.As(err, &upstreamErr) || upstreamErr.Code != ErrorCodeInvalidConfiguration {
		t.Fatalf("unsafe customization was accepted: %#v", err)
	}
}
