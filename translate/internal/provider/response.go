// SPDX-License-Identifier: MIT

package provider

import (
	"encoding/json"
	"strings"

	"github.com/canonkeeper/desktop-app/translate/internal/think"
)

func mapRequestBody(chat ChatRequest, cap Capabilities) (json.RawMessage, error) {
	value, err := json.Marshal(chat)
	if err != nil {
		return nil, &Error{Code: ErrorCodeInvalidConfiguration, Message: "could not encode Chat Completions request"}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(value, &object); err != nil {
		return nil, &Error{Code: ErrorCodeInvalidConfiguration, Message: "Chat Completions request was not an object"}
	}
	for _, field := range cap.DropBodyFields {
		delete(object, field)
	}
	for field, value := range cap.ExtraBody {
		object[field] = value
	}
	if raw, ok := object["n"]; ok {
		var n uint64
		if err := json.Unmarshal(raw, &n); err != nil || n != 1 {
			return nil, &Error{Code: ErrorCodeInvalidConfiguration, Message: "only one upstream completion (n=1) is supported"}
		}
	}
	normalizeWireReasoning(object, cap.MaxReasoningEffort)
	return mustJSON(object), nil
}

func responsesResponseFromChat(responseID string, model string, chat chatWireResponse, catalog toolCatalog, thinkTags bool) (Result, error) {
	result := Result{ResponseID: responseID, ServiceTier: chat.ServiceTier}
	result.readUsage(chat.Usage, ProtocolChatCompletions)
	if chat.Error != nil {
		result.Failed, result.ErrorCode, result.ErrorMessage = true, wireErrorCode(chat.Error), chat.Error.Message
		if result.ErrorCode == "" {
			result.ErrorCode = ErrorCodeUpstreamError
		}
		return result, &Error{Code: result.ErrorCode, Status: 200, Message: chat.Error.Message}
	}
	if result.UsageReported && !result.UsageKnown {
		return result, &Error{Code: ErrorCodeInvalidStream, Message: "upstream Chat response contains partial usage"}
	}
	if len(chat.Choices) != 1 || chat.Choices[0].Index != nil && *chat.Choices[0].Index != 0 {
		return result, &Error{Code: ErrorCodeInvalidStream, Message: "upstream must return exactly one choice at index 0"}
	}
	message := chat.Choices[0].Message
	if message.ReasoningContent == "" {
		message.ReasoningContent = message.Reasoning
	}
	message.Reasoning = ""
	if thinkTags {
		if split, found := think.Heal(chatText(message.Content)); found {
			message.Content = nil
			if split.Text != "" {
				message.Content = mustJSON(split.Text)
			}
			message.ReasoningContent += split.Reasoning
		}
	}
	completeHistoryToolArguments([]ChatMessage{message})
	output := make([]json.RawMessage, 0, 2)

	if strings.TrimSpace(message.ReasoningContent) != "" {
		output = append(output, mustJSON(map[string]any{
			"type": "reasoning", "id": newID("rs_"),
			"summary": []map[string]string{{"type": "summary_text", "text": message.ReasoningContent}},
		}))
	}
	text := chatText(message.Content)
	if text != "" || len(message.ToolCalls) == 0 {
		output = append(output, mustJSON(map[string]any{
			"type": "message", "role": "assistant",
			"content": []map[string]string{{"type": "output_text", "text": text}},
		}))
	}
	for _, call := range message.ToolCalls {
		if call.ID == "" || !catalog.allowed[call.Function.Name] {
			return result, &Error{Code: ErrorCodeInvalidToolCall, Message: "upstream returned an empty or undeclared tool call"}
		}
		item, err := toolCallResponseItem(call, catalog, "")
		if err != nil {
			return result, err
		}
		output = append(output, item)
	}
	var wireUsage any
	if result.UsageKnown {
		wireUsage = result.Usage
	}
	status, reason := "completed", incompleteReason(chat.Choices[0].FinishReason)
	if reason != "" {
		status = "incomplete"
		result.Failed, result.IncompleteReason = true, reason
	}
	payload := responseObject(responseID, model, status, reason, output, wireUsage)
	result.Response = payload
	return result, nil
}

// incompleteReason names why a Chat answer stopped short, in the words of
// Responses incomplete_details; a finish reason that ends an answer normally
// names none.
func incompleteReason(finishReason string) string {
	switch finishReason {
	case "length":
		return "max_output_tokens"
	case "content_filter":
		return "content_filter"
	}
	return ""
}

// responseObject is the Responses object of a translated answer.
func responseObject(id, model, status, reason string, output []json.RawMessage, usage any) json.RawMessage {
	object := map[string]any{
		"id": id, "object": "response", "model": model, "status": status,
		"output": output, "usage": usage,
	}
	if reason != "" {
		object["incomplete_details"] = map[string]string{"reason": reason}
	}
	return mustJSON(object)
}

func toolCallResponseItem(call ChatToolCall, catalog toolCatalog, itemID string) (json.RawMessage, error) {
	if itemID == "" {
		if _, custom := catalog.custom[call.Function.Name]; custom {
			itemID = newID("ctc_")
		} else {
			itemID = newID("fc_")
		}
	}
	if custom, ok := catalog.custom[call.Function.Name]; ok {
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(call.Function.Arguments), &object) != nil {
			return nil, &Error{Code: ErrorCodeInvalidToolCall, Message: "custom tool call arguments are not a JSON object"}
		}
		input := call.Function.Arguments
		var text string
		if json.Unmarshal(object[custom.ArgumentField], &text) == nil && text != "" {
			input = text
		}
		return mustJSON(map[string]any{
			"type": "custom_tool_call", "id": itemID, "call_id": call.ID,
			"name": custom.Name, "input": input, "status": "completed",
		}), nil
	}
	name := call.Function.Name
	namespace := ""
	if mapped, ok := catalog.namespace[name]; ok {
		name = mapped.Name
		namespace = mapped.Namespace
	}
	item := map[string]any{
		"type": "function_call", "id": itemID, "call_id": call.ID,
		"name": name, "arguments": completedArguments(call.Function.Arguments), "status": "completed",
	}
	if namespace == "collaboration" && (name == "spawn_agent" || name == "send_message" || name == "followup_task") {
		item["encrypted_function_args"] = []string{}
	}
	if namespace != "" {
		item["namespace"] = namespace
	}
	return mustJSON(item), nil
}
