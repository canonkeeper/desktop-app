// SPDX-License-Identifier: MIT

package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func translateToChat(req responsesWireRequest, items []responsesItem, catalog toolCatalog, target Target, stream bool) (ChatRequest, error) {
	cap := target.Capabilities
	var latestContentless, messages []ChatMessage
	// The instructions and every system and developer message, in the order of
	// the input, make one first system message: none replaces another.
	var system []string
	if text := strings.TrimSpace(req.Instructions); text != "" {
		system = append(system, text)
	} else if text := strings.TrimSpace(req.System); text != "" {
		system = append(system, text)
	}

	calls := newCallIDs()
	pendingReasoning := ""

	for i := 0; i < len(items); {
		item := items[i]
		switch item.Type {
		case "message", "agent_message":
			if isContentlessResponseMessage(item) {
				latestContentless = appendContentlessFallback(latestContentless, item)
				i++
				continue
			}
			msg, err := responseMessageToChat(item)
			if err != nil {
				return ChatRequest{}, err
			}
			if msg.Role == "system" {
				text, err := systemText(msg.Content)
				if err != nil {
					return ChatRequest{}, err
				}
				if text != "" {
					system = append(system, text)
				}
			} else {
				if msg.Role == "assistant" {
					msg.ReasoningContent = pendingReasoning
					pendingReasoning = ""
				}
				messages = append(messages, msg)
			}
			i++
		case "function_call", "custom_tool_call":
			group := []responsesItem{}
			for i < len(items) {
				current := items[i]
				if isContentlessResponseMessage(current) {
					latestContentless = appendContentlessFallback(latestContentless, current)
					i++
					continue
				}
				if current.Type != "function_call" && current.Type != "custom_tool_call" {
					break
				}
				group = append(group, current)
				i++
			}
			msg, err := toolCallsToChat(group, calls, catalog, pendingReasoning)
			if err != nil {
				return ChatRequest{}, err
			}
			messages = append(messages, msg)
			pendingReasoning = ""
		case "function_call_output", "custom_tool_call_output":
			if item.CallID == "" {
				return ChatRequest{}, &Error{Code: ErrorCodeInvalidRequest, Message: "tool output has an empty call_id"}
			}
			id, ok := calls.output(item.CallID)
			if !ok {
				return ChatRequest{}, &Error{Code: ErrorCodeInvalidRequest, Message: "tool output has no matching tool call"}
			}
			messages = append(messages, ChatMessage{Role: "tool", Content: outputContent(item.Output), ToolCallID: id})
			i++
		case "reasoning":
			// Chat providers cannot consume Responses replay summaries directly:
			// the summary goes back as reasoning_content of the next assistant turn.
			i++
			if summary := reasoningSummary(item.Summary); summary != "" {
				pendingReasoning = summary
			}
		default:
			return ChatRequest{}, unsupported(fmt.Sprintf("Responses input item type %q is not supported", item.Type))
		}
	}

	messages = retainContentMessages(messages)
	if len(system) > 0 {
		messages = append([]ChatMessage{{Role: "system", Content: mustJSON(strings.Join(system, "\n\n"))}}, messages...)
	}
	if len(messages) == 0 {
		if len(latestContentless) == 0 {
			latestContentless = []ChatMessage{{Role: "user", Content: mustJSON("")}}
		}
		messages = append(messages, latestContentless[len(latestContentless)-1])
	}

	toolChoice, err := chatToolChoice(req.ToolChoice, catalog)
	if err != nil {
		return ChatRequest{}, err
	}
	model := req.Model
	if mapped := strings.TrimSpace(target.Model); mapped != "" {
		model = mapped
	}
	chatReq := ChatRequest{
		Model:             model,
		Messages:          messages,
		Tools:             catalog.tools,
		Temperature:       req.Temperature,
		MaxTokens:         req.MaxOutputTokens,
		StreamOptions:     nil,
		ParallelToolCalls: req.ParallelToolCalls,
		ToolChoice:        toolChoice,
		Stream:            stream,
	}
	if stream {
		chatReq.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
	}
	if cap.EnableGLMThinking {
		chatReq.Thinking = &ChatThinking{Type: "enabled"}
	}
	// A model without reasoning gets none; the effort is lowered to the
	// profile's when the final body is encoded.
	if cap.Reasoning && req.Reasoning != nil && req.Reasoning.Effort != "" {
		chatReq.ReasoningEffort = req.Reasoning.Effort
	}
	return chatReq, nil
}

// systemText is the text of a system or developer message; a Chat provider
// takes only text there.
func systemText(content json.RawMessage) (string, error) {
	if len(content) == 0 {
		return "", nil
	}
	if content[0] != '"' {
		return "", unsupported("system and developer messages must carry text only")
	}
	return strings.TrimSpace(chatText(content)), nil
}

func appendContentlessFallback(current []ChatMessage, item responsesItem) []ChatMessage {
	msg, err := responseMessageToChat(item)
	if err == nil {
		return append(current, msg)
	}
	return current
}

func responseMessageToChat(item responsesItem) (ChatMessage, error) {
	role := item.Role
	if item.Type == "agent_message" || role == "" {
		role = "user"
	}
	if item.Type == "agent_message" {
		role = "assistant"
	}
	switch role {
	case "user", "assistant":
	case "developer":
		role = "system"
	case "system":
	default:
		return ChatMessage{}, &Error{Code: ErrorCodeInvalidRequest, Message: fmt.Sprintf("unsupported Responses message role %q", item.Role)}
	}
	content, err := chatContent(item.Content)
	if err != nil {
		return ChatMessage{}, err
	}
	return ChatMessage{Role: role, Content: content}, nil
}

func chatContent(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, invalidInput(err)
		}
		return raw, nil
	}
	var source []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, &Error{Code: ErrorCodeInvalidRequest, Message: "message content must be a string or an array of parts"}
	}
	textOnly := true
	text := strings.Builder{}
	for _, part := range source {
		switch string(part["type"]) {
		case `"input_text"`, `"text"`, `"output_text"`:
			var value string
			_ = json.Unmarshal(part["text"], &value)
			text.WriteString(value)
		default:
			textOnly = false
		}
	}
	if textOnly {
		return mustJSON(text.String()), nil
	}
	out := make([]json.RawMessage, 0, len(source))
	for _, part := range source {
		switch string(part["type"]) {
		case `"input_text"`, `"text"`, `"output_text"`:
			var value string
			_ = json.Unmarshal(part["text"], &value)
			out = append(out, mustJSON(map[string]string{"type": "text", "text": value}))
		case `"input_image"`:
			var url string
			if err := json.Unmarshal(part["image_url"], &url); err != nil || url == "" {
				return nil, &Error{Code: ErrorCodeInvalidRequest, Message: "input_image.image_url must be a non-empty string"}
			}
			out = append(out, mustJSON(map[string]any{"type": "image_url", "image_url": map[string]string{"url": url}}))
		case `"image_url"`:
			var url string
			if err := json.Unmarshal(part["image_url"], &url); err == nil && url != "" {
				out = append(out, mustJSON(map[string]any{"type": "image_url", "image_url": map[string]string{"url": url}}))
				continue
			}
			var object json.RawMessage
			if json.Unmarshal(part["image_url"], &object) == nil && len(object) > 0 && object[0] == '{' {
				out = append(out, mustJSON(map[string]any{"type": "image_url", "image_url": object}))
				continue
			}
			return nil, &Error{Code: ErrorCodeInvalidRequest, Message: "image_url must contain a url"}
		default:
			return nil, unsupported(fmt.Sprintf("content part type %s is not translatable", string(part["type"])))
		}
	}
	return mustJSON(out), nil
}

func reasoningSummary(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var summaries []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &summaries) != nil {
		return ""
	}
	values := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		if summary.Text != "" {
			values = append(values, summary.Text)
		}
	}
	return strings.Join(values, "\n")
}

func retainContentMessages(messages []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, 0, len(messages))
	for _, message := range messages {
		if !isDroppableContentMessage(message) {
			out = append(out, message)
		}
	}
	return out
}

func isContentlessResponseMessage(item responsesItem) bool {
	switch item.Role {
	case "user", "system", "developer":
	default:
		return false
	}
	if item.Type != "message" {
		return false
	}
	if len(item.Content) == 0 || bytes.Equal(item.Content, []byte("null")) {
		return true
	}
	var value any
	if json.Unmarshal(item.Content, &value) != nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		for _, partValue := range typed {
			part, _ := partValue.(map[string]any)
			kind, _ := part["type"].(string)
			switch kind {
			case "input_text", "text", "output_text":
				text, _ := part["text"].(string)
				if strings.TrimSpace(text) != "" {
					return false
				}
			default:
				return false
			}
		}
		return true
	default:
		return false
	}
}

func isDroppableContentMessage(message ChatMessage) bool {
	if message.Role != "user" && message.Role != "system" {
		return false
	}
	if len(message.ToolCalls) != 0 || message.ToolCallID != "" {
		return false
	}
	content := message.Content
	if len(content) == 0 || bytes.Equal(content, []byte("null")) {
		return true
	}
	if content[0] == '"' {
		var text string
		_ = json.Unmarshal(content, &text)
		return strings.TrimSpace(text) == ""
	}
	if content[0] != '[' {
		return false
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(content, &parts) != nil {
		return false
	}
	for _, part := range parts {
		switch string(part["type"]) {
		case `"text"`, `"input_text"`, `"output_text"`:
			var text string
			_ = json.Unmarshal(part["text"], &text)
			if strings.TrimSpace(text) != "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func chatText(raw json.RawMessage) string {
	if len(raw) == 0 || raw[0] != '"' {
		return ""
	}
	var text string
	_ = json.Unmarshal(raw, &text)
	return text
}

func completedArguments(arguments string) string {
	if strings.TrimSpace(arguments) == "" {
		return "{}"
	}
	return arguments
}

func completeHistoryToolArguments(messages []ChatMessage) {
	for i := range messages {
		for j := range messages[i].ToolCalls {
			messages[i].ToolCalls[j].Function.Arguments = completedArguments(messages[i].ToolCalls[j].Function.Arguments)
		}
	}
}
