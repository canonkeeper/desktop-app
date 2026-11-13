// SPDX-License-Identifier: MIT

package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// The tool calls of the input, their outputs and tool_choice in the form of
// Chat Completions.

// callIDs gives every tool call of the input its own Chat id: a call_id that
// comes again (a provider numbering its calls in each answer) gets a suffix,
// and an output goes to the earliest call with its call_id still unanswered.
type callIDs struct {
	used    map[string]bool
	pending map[string][]string
}

func newCallIDs() *callIDs {
	return &callIDs{used: make(map[string]bool), pending: make(map[string][]string)}
}

func (c *callIDs) call(callID string) string {
	id := callID
	for n := 2; c.used[id]; n++ {
		id = fmt.Sprintf("%s_%d", callID, n)
	}
	c.used[id] = true
	c.pending[callID] = append(c.pending[callID], id)
	return id
}

func (c *callIDs) output(callID string) (string, bool) {
	queue := c.pending[callID]
	if len(queue) == 0 {
		return "", false
	}
	c.pending[callID] = queue[1:]
	return queue[0], true
}

func toolCallsToChat(items []responsesItem, ids *callIDs, catalog toolCatalog, pendingReasoning string) (ChatMessage, error) {
	calls := make([]ChatToolCall, 0, len(items))
	for _, item := range items {
		if item.CallID == "" {
			return ChatMessage{}, &Error{Code: ErrorCodeInvalidRequest, Message: "tool call has an empty call_id"}
		}
		name := item.Name
		if item.Namespace != "" {
			name = item.Namespace + "-" + item.Name
		}
		if !catalog.allowed[name] {
			return ChatMessage{}, &Error{Code: ErrorCodeInvalidRequest, Message: fmt.Sprintf("replayed tool call %q was not declared in this request", name)}
		}
		var arguments string
		if item.Type == "custom_tool_call" {
			var input string
			if len(item.Input) > 0 && !bytes.Equal(item.Input, []byte("null")) {
				if err := json.Unmarshal(item.Input, &input); err != nil {
					return ChatMessage{}, &Error{Code: ErrorCodeInvalidRequest, Message: "custom_tool_call.input must be a string"}
				}
			}
			field := "input"
			if custom, ok := catalog.custom[name]; ok {
				field = custom.ArgumentField
			}
			arguments = string(mustJSON(map[string]string{field: input}))
		} else {
			if len(item.Arguments) == 0 || bytes.Equal(item.Arguments, []byte("null")) {
				arguments = "{}"
			} else if err := json.Unmarshal(item.Arguments, &arguments); err != nil {
				return ChatMessage{}, &Error{Code: ErrorCodeInvalidRequest, Message: "function_call.arguments must be a JSON string"}
			} else {
				var object map[string]json.RawMessage
				if json.Unmarshal([]byte(arguments), &object) != nil {
					return ChatMessage{}, &Error{Code: ErrorCodeInvalidRequest, Message: "function_call.arguments must contain a JSON object"}
				}
			}
			arguments = completedArguments(arguments)
		}
		calls = append(calls, ChatToolCall{
			ID: ids.call(item.CallID), Type: "function",
			Function: ChatToolCallFunction{Name: name, Arguments: arguments},
		})
	}
	return ChatMessage{Role: "assistant", ToolCalls: calls, ReasoningContent: pendingReasoning}, nil
}

// chatToolChoice is the tool_choice of the input in the Chat form; nil leaves
// the provider's default, which is also what a Chat provider needs when no
// tool is declared.
func chatToolChoice(raw json.RawMessage, catalog toolCatalog) (json.RawMessage, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var mode string
	if json.Unmarshal(raw, &mode) == nil {
		switch {
		case mode != "auto" && mode != "none" && mode != "required":
			return nil, &Error{Code: ErrorCodeInvalidRequest, Message: fmt.Sprintf("unknown tool_choice %q", mode)}
		case len(catalog.tools) != 0:
			return raw, nil
		case mode == "required":
			return nil, &Error{Code: ErrorCodeInvalidRequest, Message: "tool_choice requires a tool, but none is declared"}
		}
		return nil, nil
	}
	var named struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	if json.Unmarshal(raw, &named) != nil {
		return nil, &Error{Code: ErrorCodeInvalidRequest, Message: "tool_choice must be a mode or a named tool"}
	}
	if named.Type != "function" && named.Type != "custom" {
		return nil, unsupported(fmt.Sprintf("tool_choice of type %q is not translatable to Chat Completions", named.Type))
	}
	name := strings.TrimSpace(named.Name)
	if named.Namespace != "" {
		name = named.Namespace + "-" + name
	}
	if !catalog.allowed[name] {
		return nil, &Error{Code: ErrorCodeInvalidRequest, Message: fmt.Sprintf("tool_choice names %q, which is not declared", name)}
	}
	return mustJSON(map[string]any{"type": "function", "function": map[string]string{"name": name}}), nil
}

// outputContent is the text of a tool message: a string as it is, an array of
// content items joined into their text, any other JSON as its text.
func outputContent(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return mustJSON("")
	}
	if raw[0] == '"' {
		return raw
	}
	var items []struct {
		Type *string `json:"type"`
		Text string  `json:"text"`
	}
	if raw[0] != '[' || json.Unmarshal(raw, &items) != nil {
		return mustJSON(string(raw))
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		switch {
		case item.Type == nil:
			return mustJSON(string(raw))
		case *item.Type == "input_text" || *item.Type == "output_text" || *item.Type == "text":
			parts = append(parts, item.Text)
		default:
			parts = append(parts, omittedToolPart(*item.Type))
		}
	}
	return mustJSON(strings.Join(parts, "\n"))
}

// omittedToolPart stands in the text of a tool output for a part a Chat tool
// message cannot carry.
func omittedToolPart(kind string) string {
	return "[" + kind + " omitted: a Chat Completions tool result carries text only]"
}
