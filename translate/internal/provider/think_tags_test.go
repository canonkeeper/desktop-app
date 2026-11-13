// SPDX-License-Identifier: MIT

package provider

import (
	"encoding/json"
	"testing"
)

// outputText and outputReasoning read the message text and the reasoning
// summary of a translated response.
func outputText(t *testing.T, response json.RawMessage) string {
	t.Helper()
	content, _ := findOutput(t, response, "message")["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("message content: %v", content)
	}
	return content[0].(map[string]any)["text"].(string)
}

func outputReasoning(t *testing.T, response json.RawMessage) string {
	t.Helper()
	summary, _ := findOutput(t, response, "reasoning")["summary"].([]any)
	if len(summary) != 1 {
		t.Fatalf("reasoning summary: %v", summary)
	}
	return summary[0].(map[string]any)["text"].(string)
}

func deltas(t *testing.T, events []Event, kind string) string {
	t.Helper()
	text := ""
	for _, event := range events {
		if event.Type != kind {
			continue
		}
		var payload struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatal(err)
		}
		text += payload.Delta
	}
	return text
}

// TR2.6 (F6): with the profile's flag, think markup leaked into the text goes
// to the reasoning, also when a tag is torn by the chunks of the stream.
func TestThinkMarkupGoesToTheReasoningWhenTheProfileAsks(t *testing.T) {
	profile := chatCapabilities()
	profile.ThinkTags = true
	for _, pieces := range [][]string{
		{"<think>musing</think>Hello!"},
		{"<thi", "nk>mus", "ing</th", "ink>Hel", "lo!"},
		{"<thinking>mus", "ing</thinking>", "Hello!"},
		{"◁think▷musing◁/thi", "nk▷Hello!"},
	} {
		chunks := make([]string, 0, len(pieces)+1)
		for _, piece := range pieces {
			chunks = append(chunks, string(mustJSON(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": piece}}}})))
		}
		chunks = append(chunks, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`)
		result, events, err := streamThrough(t, profile, chatStream(chunks...), streamRequest)
		if err != nil {
			t.Fatal(err)
		}
		if text, reasoning := deltas(t, events, "response.output_text.delta"), deltas(t, events, "response.reasoning_text.delta"); text != "Hello!" || reasoning != "musing" {
			t.Fatalf("%q: text deltas %q, reasoning deltas %q", pieces, text, reasoning)
		}
		if outputText(t, result.Response) != "Hello!" || outputReasoning(t, result.Response) != "musing" {
			t.Fatalf("%q: terminal output %s", pieces, result.Response)
		}
	}
	// An unterminated block at the end of the stream stays reasoning.
	result, _, err := streamThrough(t, profile, chatStream(`{"choices":[{"index":0,"delta":{"content":"<think>cut off"},"finish_reason":"stop"}]}`), streamRequest)
	if err != nil || outputReasoning(t, result.Response) != "cut off" {
		t.Fatalf("unterminated block: %s %v", result.Response, err)
	}

	result, err = executeThrough(t, profile, `{"choices":[{"index":0,"message":{"role":"assistant","content":"<think>musing</think>Hello!"}}]}`, executeRequest)
	if err != nil || outputText(t, result.Response) != "Hello!" || outputReasoning(t, result.Response) != "musing" {
		t.Fatalf("whole answer: %s %v", result.Response, err)
	}
}

// Without the flag the markup is the provider's text and stays as it is.
func TestThinkMarkupStaysTextWithoutTheFlag(t *testing.T) {
	result, _, err := streamThrough(t, chatCapabilities(), chatStream(`{"choices":[{"index":0,"delta":{"content":"<think>musing</think>Hi"},"finish_reason":"stop"}]}`), streamRequest)
	if err != nil || outputText(t, result.Response) != "<think>musing</think>Hi" {
		t.Fatalf("stream: %s %v", result.Response, err)
	}
	result, err = executeThrough(t, chatCapabilities(), `{"choices":[{"index":0,"message":{"role":"assistant","content":"<think>musing</think>Hi"}}]}`, executeRequest)
	if err != nil || outputText(t, result.Response) != "<think>musing</think>Hi" {
		t.Fatalf("whole answer: %s %v", result.Response, err)
	}
}
