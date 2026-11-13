// SPDX-License-Identifier: MIT

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/canonkeeper/desktop-app/translate/internal/think"
)

type chatStreamState struct {
	model          string
	catalog        toolCatalog
	emit           func(Event) error
	responseID     string
	messageID      string
	reasoningID    string
	text           strings.Builder
	reasoning      strings.Builder
	toolCalls      map[int]*chatToolCallState
	outputIndexes  map[string]int
	nextIndex      int
	usage          Usage
	usageSeen      bool
	usageReported  bool
	serviceTier    string
	finishReason   string
	terminalChoice bool
	failed         bool
	errorCode      string
	// think is set when the profile moves leaked think markup to reasoning.
	think *think.Filter
}

func (a *HTTPAdapter) streamChat(ctx context.Context, target Target, req responsesWireRequest, items []responsesItem, catalog toolCatalog, emit func(Event) error) (Result, error) {
	chat, err := translateToChat(req, items, catalog, target, true)
	if err != nil {
		return Result{}, err
	}
	body, err := mapRequestBody(chat, target.Capabilities)
	if err != nil {
		return Result{}, err
	}
	state := &chatStreamState{
		model: req.Model, catalog: catalog, emit: emit,
		responseID: newID("resp_"), messageID: newID("msg_"), reasoningID: newID("rs_"),
		toolCalls: make(map[int]*chatToolCallState), outputIndexes: make(map[string]int),
	}
	if target.Capabilities.ThinkTags {
		state.think = &think.Filter{}
	}
	req.Raw, req.Input, req.Tools = nil, nil, nil
	response, rejected, err := a.beginStream(ctx, target, endpoint(target.BaseURL, "chat/completions"), body)
	if err != nil {
		return rejected, state.fail(wrapContext(ctx, err))
	}
	defer response.Body.Close()
	if err := state.emitJSON("response.created", map[string]any{
		"type":     "response.created",
		"response": map[string]any{"id": state.responseID, "status": "in_progress", "model": state.model},
	}); err != nil {
		return state.result(), err
	}

	doneSeen := false
	err = readSSE(response.Body, func(eventName string, data []byte) error {
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			doneSeen = true
			return io.EOF
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return nil
		}
		var chunk chatStreamChunk
		if err := decodeExactJSON(data, &chunk); err != nil {
			return err
		}
		return state.push(chunk)
	})
	if err != nil && !(doneSeen && errors.Is(err, io.EOF)) {
		return state.result(), state.fail(wrapContext(ctx, err))
	}
	if doneSeen {
		FinishStreamBody(response)
	}
	if !doneSeen && target.Capabilities.AllowNoSSEDone && state.finishReason != "" {
		doneSeen = true
	}
	if !doneSeen {
		return state.result(), state.fail(&Error{Code: ErrorCodeStreamIncomplete, Message: "upstream stream disconnected before [DONE]"})
	}
	if state.think != nil {
		tail := state.think.Finish()
		if err := state.emitSplit(tail.Reasoning, tail.Text); err != nil {
			return state.result(), state.fail(err)
		}
	}
	if err := state.validateToolCalls(); err != nil {
		return state.result(), state.fail(err)
	}

	terminal, err := state.complete()
	if err != nil {
		return state.result(), state.fail(err)
	}
	if err := state.emitTerminal(terminal); err != nil {
		return terminal, err
	}
	return terminal, nil
}

func (s *chatStreamState) result() Result {
	return Result{ResponseID: s.responseID, Usage: s.usage, UsageKnown: s.usageSeen, UsageReported: s.usageReported, ServiceTier: s.serviceTier, Failed: s.errorCode != "", ErrorCode: s.errorCode}
}

func (s *chatStreamState) push(chunk chatStreamChunk) error {
	var result Result
	result.readUsage(chunk.Usage, ProtocolChatCompletions)
	if result.UsageReported {
		s.usage, s.usageSeen, s.usageReported = result.Usage, result.UsageKnown, true
	}
	if chunk.Error != nil {
		code := wireErrorCode(chunk.Error)
		if code == "" {
			code = ErrorCodeUpstreamError
		}
		s.errorCode = code
		return &Error{Code: code, Message: chunk.Error.Message}
	}
	if result.UsageReported && !result.UsageKnown {
		return &Error{Code: ErrorCodeInvalidStream, Message: "upstream Chat stream contains partial usage"}
	}
	if chunk.ServiceTier != "" {
		s.serviceTier = chunk.ServiceTier
	}
	if len(chunk.Choices) > 1 || len(chunk.Choices) == 1 && chunk.Choices[0].Index != nil && *chunk.Choices[0].Index != 0 {
		return &Error{Code: ErrorCodeInvalidStream, Message: "only upstream choice index 0 is supported"}
	}
	if s.terminalChoice && len(chunk.Choices) != 0 {
		return &Error{Code: ErrorCodeInvalidStream, Message: "upstream sent choice data after finish_reason"}
	}
	for _, choice := range chunk.Choices {
		if choice.FinishReason != "" {
			s.finishReason = choice.FinishReason
			s.terminalChoice = true
		}
		reasoning := choice.Delta.ReasoningContent
		if reasoning == "" {
			reasoning = choice.Delta.Reasoning
		}
		text := choice.Delta.Content
		if s.think != nil && text != "" {
			split := s.think.Push(text)
			reasoning, text = reasoning+split.Reasoning, split.Text
		}
		if err := s.emitSplit(reasoning, text); err != nil {
			return err
		}
		for index, call := range choice.Delta.ToolCalls {
			callIndex := index
			if call.Index != nil {
				callIndex = *call.Index
			}
			if err := s.pushToolCall(callIndex, call); err != nil {
				return err
			}
		}
	}
	return nil
}

// emitSplit emits a piece of reasoning, then a piece of the answer.
func (s *chatStreamState) emitSplit(reasoning, text string) error {
	if reasoning != "" {
		if err := s.emitText("reasoning", reasoning); err != nil {
			return err
		}
	}
	if text != "" {
		return s.emitText("message", text)
	}
	return nil
}

func (s *chatStreamState) emitText(kind string, delta string) error {
	itemID := s.messageID
	itemType := "message"
	if kind == "reasoning" {
		itemID = s.reasoningID
		itemType = "reasoning"
	}
	outputIndex, exists := s.outputIndexes[kind]
	if !exists {
		outputIndex = s.nextIndex
		s.nextIndex++
		s.outputIndexes[kind] = outputIndex
		item := map[string]any{"type": itemType, "id": itemID}
		if itemType == "message" {
			item["role"], item["status"], item["content"] = "assistant", "in_progress", []any{}
		} else {
			item["summary"] = []any{}
		}
		if err := s.emitJSON("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": outputIndex, "item": item,
		}); err != nil {
			return err
		}
	}
	if kind == "reasoning" {
		s.reasoning.WriteString(delta)
		return s.emitJSON("response.reasoning_text.delta", map[string]any{
			"type": "response.reasoning_text.delta", "item_id": itemID,
			"output_index": outputIndex, "content_index": 0, "delta": delta,
		})
	}
	s.text.WriteString(delta)
	return s.emitJSON("response.output_text.delta", map[string]any{
		"type": "response.output_text.delta", "item_id": itemID,
		"output_index": outputIndex, "delta": delta,
	})
}

// status is the Responses status the answer ends with and, for an answer cut
// short, the reason.
func (s *chatStreamState) status() (string, string) {
	if reason := incompleteReason(s.finishReason); reason != "" {
		return "incomplete", reason
	}
	return "completed", ""
}

func (s *chatStreamState) complete() (Result, error) {
	status, reason := s.status()
	text := s.text.String()
	assistant := ChatMessage{Role: "assistant", Content: mustJSON(text), ReasoningContent: s.reasoning.String()}
	for _, index := range s.toolCallIndexes() {
		assistant.ToolCalls = append(assistant.ToolCalls, s.toolCalls[index].call())
	}
	type indexedOutput struct {
		index int
		value json.RawMessage
	}
	outputs := make([]indexedOutput, 0, len(s.toolCalls)+2)
	if assistant.ReasoningContent != "" {
		outputs = append(outputs, indexedOutput{s.outputIndexes["reasoning"], mustJSON(map[string]any{
			"type": "reasoning", "id": s.reasoningID,
			"summary": []map[string]string{{"type": "summary_text", "text": assistant.ReasoningContent}},
		})})
	}
	if text != "" || len(assistant.ToolCalls) == 0 {
		outputs = append(outputs, indexedOutput{s.outputIndexes["message"], mustJSON(map[string]any{
			"type": "message", "id": s.messageID, "role": "assistant", "status": status,
			"content": []map[string]string{{"type": "output_text", "text": text}},
		})})
	}
	for offset, index := range s.toolCallIndexes() {
		call, state := assistant.ToolCalls[offset], s.toolCalls[index]
		if !state.streaming {
			state.itemID = newID("fc_")
			if _, custom := s.catalog.custom[call.Function.Name]; custom {
				state.itemID = newID("ctc_")
			}
			state.outputIndex = s.nextIndex
			s.nextIndex++
		}
		item, err := toolCallResponseItem(call, s.catalog, state.itemID)
		if err != nil {
			return Result{}, err
		}
		outputs = append(outputs, indexedOutput{state.outputIndex, item})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].index < outputs[j].index })
	output := make([]json.RawMessage, len(outputs))
	for i, item := range outputs {
		output[i] = item.value
	}
	var wireUsage any
	if s.usageSeen {
		wireUsage = s.usage
	}
	payload := responseObject(s.responseID, s.model, status, reason, output, wireUsage)
	return Result{
		ResponseID: s.responseID, Response: payload, Usage: s.usage, UsageKnown: s.usageSeen, UsageReported: s.usageReported, ServiceTier: s.serviceTier,
		Failed: reason != "", IncompleteReason: reason,
	}, nil
}

func (s *chatStreamState) emitTerminal(terminal Result) error {
	status, _ := s.status()
	if index, ok := s.outputIndexes["reasoning"]; ok {
		if err := s.emitItemDone(index, mustJSON(map[string]any{
			"type": "reasoning", "id": s.reasoningID,
			"summary": []map[string]string{{"type": "summary_text", "text": s.reasoning.String()}},
		})); err != nil {
			return err
		}
	}
	if index, ok := s.outputIndexes["message"]; ok {
		if err := s.emitItemDone(index, mustJSON(map[string]any{
			"type": "message", "id": s.messageID, "role": "assistant", "status": status,
			"content": []map[string]string{{"type": "output_text", "text": s.text.String()}},
		})); err != nil {
			return err
		}
	}
	for _, index := range s.toolCallIndexes() {
		state := s.toolCalls[index]
		call := state.call()
		itemID, outputIndex := state.itemID, state.outputIndex
		done, err := toolCallResponseItem(call, s.catalog, itemID)
		if err != nil {
			return err
		}
		if state.streaming {
			if err := s.emitItemDone(outputIndex, done); err != nil {
				return err
			}
			continue
		}
		added, err := s.addedToolItem(state)
		if err != nil {
			return err
		}
		if err := s.emitJSON("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": outputIndex, "item": added,
		}); err != nil {
			return err
		}
		if _, custom := s.catalog.custom[call.Function.Name]; custom {
			var doneItem map[string]json.RawMessage
			_ = json.Unmarshal(done, &doneItem)
			var input string
			_ = json.Unmarshal(doneItem["input"], &input)
			if input != "" {
				if err := s.emitJSON("response.custom_tool_call_input.delta", map[string]any{
					"type": "response.custom_tool_call_input.delta", "item_id": itemID,
					"output_index": outputIndex, "delta": input,
				}); err != nil {
					return err
				}
			}
		} else if call.Function.Arguments != "" {
			if err := s.emitJSON("response.function_call_arguments.delta", map[string]any{
				"type": "response.function_call_arguments.delta", "item_id": itemID,
				"output_index": outputIndex, "delta": call.Function.Arguments,
			}); err != nil {
				return err
			}
		}
		if err := s.emitItemDone(outputIndex, done); err != nil {
			return err
		}
	}
	// An answer cut short ends as response.incomplete, never as completed.
	eventType := "response." + status
	return s.emitJSON(eventType, map[string]any{"type": eventType, "response": terminal.Response})
}

func (s *chatStreamState) emitItemDone(index int, item json.RawMessage) error {
	return s.emitJSON("response.output_item.done", map[string]any{
		"type": "response.output_item.done", "output_index": index, "item": item,
	})
}

func (s *chatStreamState) fail(err error) error {
	if s.failed {
		return err
	}
	s.failed = true
	code := ErrorCodeUpstreamError
	var upstreamErr *Error
	if errors.As(err, &upstreamErr) && upstreamErr.Code != "" {
		code = upstreamErr.Code
	}
	message := err.Error()
	if errors.Is(err, context.Canceled) {
		code, message = ErrorCodeConnection, context.Canceled.Error()
	} else if errors.Is(err, context.DeadlineExceeded) {
		code, message = ErrorCodeConnection, context.DeadlineExceeded.Error()
	}
	_ = s.emitJSON("response.failed", map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"id": s.responseID, "status": "failed",
			"error": map[string]string{"code": code, "message": message},
		},
	})
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if upstreamErr != nil {
		return upstreamErr
	}
	return &Error{Code: code, Message: message}
}

func (s *chatStreamState) emitJSON(eventType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return &Error{Code: ErrorCodeInvalidConfiguration, Message: "could not encode Responses event"}
	}
	return s.emit(Event{Type: eventType, Data: data})
}
