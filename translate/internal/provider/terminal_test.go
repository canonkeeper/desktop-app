// SPDX-License-Identifier: MIT

package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// chatStream is an SSE body of Chat chunks ending with [DONE].
func chatStream(chunks ...string) string {
	var body strings.Builder
	for _, chunk := range chunks {
		body.WriteString("data: " + chunk + "\n\n")
	}
	body.WriteString("data: [DONE]\n\n")
	return body.String()
}

// streamThrough opens a stream against a stub that answers reply.
func streamThrough(t *testing.T, capabilities Capabilities, reply string, request string) (Result, []Event, error) {
	t.Helper()
	server, _ := providerStub(t, reply)
	adapter := New(server.Client())
	defer adapter.Close()
	log, emit := collectEvents()
	result, err := adapter.OpenStream(context.Background(), testTarget(server.URL, capabilities), Request{Body: json.RawMessage(request)}, emit)
	return result, log.events, err
}

func executeThrough(t *testing.T, capabilities Capabilities, reply string, request string) (Result, error) {
	t.Helper()
	server, _ := providerStub(t, reply)
	adapter := New(server.Client())
	defer adapter.Close()
	return adapter.Execute(context.Background(), testTarget(server.URL, capabilities), Request{Body: json.RawMessage(request)})
}

func eventTypes(events []Event) []string {
	types := make([]string, len(events))
	for i, event := range events {
		types[i] = event.Type
	}
	return types
}

// terminalResponse decodes the response object of the last event.
func terminalResponse(t *testing.T, events []Event) map[string]any {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no events")
	}
	var payload struct {
		Response map[string]any `json:"response"`
	}
	if err := json.Unmarshal(events[len(events)-1].Data, &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Response
}

const (
	streamRequest  = `{"model":"m","input":"hi","stream":true}`
	executeRequest = `{"model":"m","input":"hi","stream":false}`
)

// TR2.3 (F3): a provider that sends no usage ends the response normally; the
// tokens are unknown, not zero, and the stream still asks for usage.
func TestAResponseWithoutUsageEndsWithUnknownTokens(t *testing.T) {
	server, bodies := providerStub(t, chatStream(`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`))
	adapter := New(server.Client())
	defer adapter.Close()
	log, emit := collectEvents()
	result, err := adapter.OpenStream(context.Background(), testTarget(server.URL, chatCapabilities()), Request{Body: json.RawMessage(streamRequest)}, emit)
	if err != nil || result.UsageKnown || result.UsageReported || result.Failed {
		t.Fatalf("Chat stream without usage: %+v %v", result, err)
	}
	if sent := decodeBody(t, <-bodies); sent["stream_options"].(map[string]any)["include_usage"] != true {
		t.Fatalf("stream did not ask for usage: %v", sent)
	}
	if terminal := terminalResponse(t, log.events); log.events[len(log.events)-1].Type != "response.completed" || terminal["usage"] != nil {
		t.Fatalf("terminal: %v", terminal)
	}

	result, err = executeThrough(t, chatCapabilities(), `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`, executeRequest)
	if err != nil || result.UsageKnown || result.Failed {
		t.Fatalf("Chat response without usage: %+v %v", result, err)
	}
	result, _, err = streamThrough(t, responsesCapabilities(), `data: {"type":"response.completed","response":{"id":"r","status":"completed"}}`+"\n\n", streamRequest)
	if err != nil || result.UsageKnown || result.Failed {
		t.Fatalf("Responses stream without usage: %+v %v", result, err)
	}
	result, err = executeThrough(t, responsesCapabilities(), `{"id":"r","status":"completed","output":[]}`, executeRequest)
	if err != nil || result.UsageKnown || result.Failed {
		t.Fatalf("Responses response without usage: %+v %v", result, err)
	}
}

// TR2.4 (F4): a usage whose total disagrees with input + output is accepted
// and its total recomputed.
func TestAUsageWhoseTotalDisagreesIsAcceptedWithTheTotalRecomputed(t *testing.T) {
	check := func(name string, result Result, err error) {
		t.Helper()
		if err != nil || !result.UsageKnown || result.Usage.InputTokens != 2 || result.Usage.OutputTokens != 1 || result.Usage.TotalTokens != 3 {
			t.Fatalf("%s: %+v %v", name, result, err)
		}
	}
	result, _, err := streamThrough(t, chatCapabilities(), chatStream(
		`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":5}}`,
	), streamRequest)
	check("chat stream", result, err)
	result, err = executeThrough(t, chatCapabilities(), `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":9}}`, executeRequest)
	check("chat response", result, err)
	result, _, err = streamThrough(t, responsesCapabilities(), `data: {"type":"response.completed","response":{"id":"r","status":"completed","usage":{"input_tokens":2,"output_tokens":1,"total_tokens":4}}}`+"\n\n", streamRequest)
	check("responses stream", result, err)
}

// TR2.5 (F5): a Chat answer cut by its length ends as response.incomplete
// with the reason max_output_tokens, in a stream and in a whole response;
// response.completed never comes.
func TestAnAnswerCutByLengthEndsIncomplete(t *testing.T) {
	for _, test := range []struct{ finish, reason string }{{"length", "max_output_tokens"}, {"content_filter", "content_filter"}} {
		result, events, err := streamThrough(t, chatCapabilities(), chatStream(
			`{"choices":[{"index":0,"delta":{"content":"partial"}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"`+test.finish+`"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`,
		), streamRequest)
		types := eventTypes(events)
		terminal := terminalResponse(t, events)
		if err != nil || !result.Failed || result.IncompleteReason != test.reason || !result.UsageKnown {
			t.Fatalf("%s stream result: %+v %v", test.finish, result, err)
		}
		if strings.Contains(strings.Join(types, " "), "response.completed") || types[len(types)-1] != "response.incomplete" ||
			terminal["status"] != "incomplete" || terminal["incomplete_details"].(map[string]any)["reason"] != test.reason {
			t.Fatalf("%s stream events: %v %v", test.finish, types, terminal)
		}

		result, err = executeThrough(t, chatCapabilities(), `{"choices":[{"index":0,"message":{"role":"assistant","content":"partial"},"finish_reason":"`+test.finish+`"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`, executeRequest)
		var response map[string]any
		_ = json.Unmarshal(result.Response, &response)
		if err != nil || !result.Failed || result.IncompleteReason != test.reason || response["status"] != "incomplete" ||
			response["incomplete_details"].(map[string]any)["reason"] != test.reason {
			t.Fatalf("%s response: %+v %s %v", test.finish, result, result.Response, err)
		}
	}
	result, events, err := streamThrough(t, responsesCapabilities(), `data: {"type":"response.incomplete","response":{"id":"r","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":2,"output_tokens":1}}}`+"\n\n", streamRequest)
	if err != nil || !result.Failed || result.IncompleteReason != "max_output_tokens" || eventTypes(events)[0] != "response.incomplete" {
		t.Fatalf("Responses incomplete: %+v %v", result, err)
	}
}
