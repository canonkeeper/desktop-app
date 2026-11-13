// SPDX-License-Identifier: MIT

package provider

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// benchmarkRequestBody builds a realistic Codex Responses body of about size
// bytes (instructions, tools, alternating messages and tool calls).
func benchmarkRequestBody(size int) []byte {
	tools := make([]map[string]any, 0, 12)
	for i := range 12 {
		tools = append(tools, map[string]any{"type": "function", "name": fmt.Sprintf("tool_%d", i), "description": strings.Repeat("Describe the tool. ", 20),
			"strict": false, "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]string{"type": "string"}}, "required": []string{"path"}}})
	}
	input := []map[string]any{}
	chunk := strings.Repeat("Some <source> code & text with \"quotes\" and unicode é. ", 40)
	for total := 0; total < size; total += len(chunk) + 200 {
		switch len(input) % 3 {
		case 0:
			input = append(input, map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": chunk}}})
		case 1:
			input = append(input, map[string]any{"type": "function_call", "call_id": fmt.Sprintf("call_%d", len(input)), "name": "tool_1", "arguments": `{"path":"a.go"}`})
		default:
			input = append(input, map[string]any{"type": "function_call_output", "call_id": fmt.Sprintf("call_%d", len(input)-1), "output": chunk})
		}
	}
	body, _ := json.Marshal(map[string]any{
		"model": "gpt-6-sol", "instructions": strings.Repeat("You are Codex. ", 800), "input": input, "tools": tools,
		"tool_choice": "auto", "parallel_tool_calls": true, "reasoning": map[string]string{"effort": "medium", "summary": "auto"},
		"store": false, "stream": true, "include": []string{"reasoning.encrypted_content"}, "prompt_cache_key": "pc_123",
	})
	return body
}

// BenchmarkChatRequestPipeline measures the translation of a Codex Responses
// request into the Chat Completions body a provider receives.
func BenchmarkChatRequestPipeline(b *testing.B) {
	capabilities := chatCapabilities()
	capabilities.Reasoning = true
	target, err := prepareTarget(Target{BaseURL: "https://upstream.invalid/v1", Capabilities: capabilities})
	if err != nil {
		b.Fatal(err)
	}
	for _, size := range []int{16 << 10, 1 << 20} {
		body := benchmarkRequestBody(size)
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				req, items, catalog, err := prepareRequest(Request{Body: body}, target)
				if err != nil {
					b.Fatal(err)
				}
				chat, err := translateToChat(req, items, catalog, target, true)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := mapRequestBody(chat, target.Capabilities); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
