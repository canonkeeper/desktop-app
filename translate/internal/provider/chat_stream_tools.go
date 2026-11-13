// SPDX-License-Identifier: MIT

package provider

import (
	"encoding/json"
	"sort"
	"strings"
)

// The tool calls of a Chat stream: gathered from their deltas, streamed as
// Responses items once declared, checked at the end.

// chatToolCallState accumulates one Chat tool call in linear time. A declared
// function call starts streaming as a Responses function_call item at its
// first arguments delta; after that its ID and name are fixed.
type chatToolCallState struct {
	id          string
	name        string
	arguments   strings.Builder
	itemID      string
	outputIndex int
	streaming   bool
}

// call returns the accumulated call with completed (non-empty) arguments.
func (c *chatToolCallState) call() ChatToolCall {
	return ChatToolCall{ID: c.id, Type: "function", Function: ChatToolCallFunction{Name: c.name, Arguments: completedArguments(c.arguments.String())}}
}

// pushToolCall streams Chat tool-call deltas as soon as the call is known to be
// a declared function; custom tools keep terminal-time emission because their
// input is derived from the complete arguments object.
func (s *chatStreamState) pushToolCall(callIndex int, delta chatStreamToolCall) error {
	call := s.toolCalls[callIndex]
	if call == nil {
		call = &chatToolCallState{outputIndex: -1}
		s.toolCalls[callIndex] = call
	}
	changed := delta.ID != "" && delta.ID != call.id || delta.Function != nil && delta.Function.Name != ""
	if call.streaming && changed {
		return &Error{Code: ErrorCodeInvalidToolCall, Message: "upstream changed a tool call after its arguments started"}
	}
	if delta.ID != "" {
		call.id = delta.ID
	}
	if delta.Function == nil {
		return nil
	}
	call.name += delta.Function.Name
	if delta.Function.Arguments == "" {
		return nil
	}
	call.arguments.WriteString(delta.Function.Arguments)
	if call.streaming {
		return s.emitJSON("response.function_call_arguments.delta", map[string]any{
			"type": "response.function_call_arguments.delta", "item_id": call.itemID,
			"output_index": call.outputIndex, "delta": delta.Function.Arguments,
		})
	}
	if _, custom := s.catalog.custom[call.name]; call.id == "" || custom || !s.catalog.allowed[call.name] {
		return nil
	}
	call.streaming, call.itemID, call.outputIndex = true, newID("fc_"), s.nextIndex
	s.nextIndex++
	added, err := s.addedToolItem(call)
	if err != nil {
		return err
	}
	if err := s.emitJSON("response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": call.outputIndex, "item": added,
	}); err != nil {
		return err
	}
	return s.emitJSON("response.function_call_arguments.delta", map[string]any{
		"type": "response.function_call_arguments.delta", "item_id": call.itemID,
		"output_index": call.outputIndex, "delta": call.arguments.String(),
	})
}

// addedToolItem is the in-progress form of a tool call's completed item.
func (s *chatStreamState) addedToolItem(call *chatToolCallState) (map[string]json.RawMessage, error) {
	done, err := toolCallResponseItem(call.call(), s.catalog, call.itemID)
	if err != nil {
		return nil, err
	}
	var doneObject map[string]json.RawMessage
	if err := json.Unmarshal(done, &doneObject); err != nil {
		return nil, err
	}
	for key := range doneObject {
		switch key {
		case "status":
			doneObject[key] = mustJSON("in_progress")
		case "arguments", "input":
			doneObject[key] = mustJSON("")
		}
	}
	return doneObject, nil
}

func (s *chatStreamState) validateToolCalls() error {
	seen := make(map[string]bool)
	for _, index := range s.toolCallIndexes() {
		call := s.toolCalls[index]
		if call.id == "" || seen[call.id] {
			return &Error{Code: ErrorCodeInvalidToolCall, Message: "upstream returned an empty or duplicate tool call ID"}
		}
		seen[call.id] = true
		if !s.catalog.allowed[call.name] {
			return &Error{Code: ErrorCodeInvalidToolCall, Message: "upstream returned an undeclared tool call"}
		}
	}
	return nil
}

func (s *chatStreamState) toolCallIndexes() []int {
	indexes := make([]int, 0, len(s.toolCalls))
	for index := range s.toolCalls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	return indexes
}
