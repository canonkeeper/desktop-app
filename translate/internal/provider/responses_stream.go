// SPDX-License-Identifier: MIT

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func (a *HTTPAdapter) streamResponses(ctx context.Context, target Target, req responsesWireRequest, emit func(Event) error) (Result, error) {
	body, err := passthroughBody(req, target)
	if err != nil {
		return Result{}, err
	}
	response, rejected, err := a.beginStream(ctx, target, endpoint(target.BaseURL, "responses"), body)
	if err != nil {
		return rejected, err
	}
	defer response.Body.Close()

	var terminal Result
	terminalSeen := false
	err = readSSE(response.Body, func(eventName string, data []byte) error {
		if len(bytes.TrimSpace(data)) == 0 {
			return nil
		}
		header, err := decodeEventHeader(data)
		if err != nil {
			return err
		}
		eventType := header.Type
		if eventType == "" {
			eventType = strings.TrimSpace(eventName)
		}
		if eventType == "" {
			return &Error{Code: ErrorCodeInvalidStream, Message: "upstream SSE event omitted its type"}
		}
		if eventType == "error" {
			var envelope struct {
				Status int `json:"status"`
			}
			_ = json.Unmarshal(data, &envelope)
			terminal, _ = rejectedResult(envelope.Status, data, target.Capabilities.Protocol)
			return responseEventError(data)
		}
		switch eventType {
		case "response.completed", "response.failed", "response.incomplete":
			if terminalSeen {
				return &Error{Code: ErrorCodeInvalidStream, Message: "upstream sent more than one terminal Responses event"}
			}
			terminalSeen = true
			terminal, err = terminalFromResponsesEvent(eventType, data)
			if err != nil {
				return err
			}
			if err := emit(Event{Type: eventType, Data: data}); err != nil {
				return err
			}
			return errResponsesTerminalReached
		}
		// readSSE hands each event its own buffer; no further copy is needed.
		return emit(Event{Type: eventType, Data: data})
	})
	if err != nil && !errors.Is(err, errResponsesTerminalReached) {
		return terminal, wrapContext(ctx, err)
	}
	if terminalSeen {
		FinishStreamBody(response)
	}
	if !terminalSeen {
		return Result{}, &Error{Code: ErrorCodeStreamIncomplete, Message: "upstream stream disconnected before a terminal event"}
	}
	return terminal, nil
}

var errResponsesTerminalReached = errors.New("responses terminal event reached")

// responsesTerminal is the part of a Responses object that says how it ended.
type responsesTerminal struct {
	ID                string          `json:"id"`
	Status            string          `json:"status"`
	Error             *wireError      `json:"error"`
	WireUsage         json.RawMessage `json:"usage"`
	ServiceTier       string          `json:"service_tier"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

func (t responsesTerminal) incompleteReason() string {
	if t.IncompleteDetails == nil {
		return ""
	}
	return t.IncompleteDetails.Reason
}

func terminalFromResponsesEvent(eventType string, data []byte) (Result, error) {
	var envelope struct {
		Response json.RawMessage `json:"response"`
	}
	if err := decodeExactJSON(data, &envelope); err != nil {
		return Result{}, err
	}
	var payload struct {
		Response *responsesTerminal `json:"response"`
	}
	if err := decodeExactJSON(data, &payload); err != nil {
		return Result{}, err
	}
	if payload.Response == nil || payload.Response.ID == "" {
		return Result{}, &Error{Code: ErrorCodeInvalidStream, Message: "terminal Responses event omitted response id"}
	}
	result := Result{
		ResponseID:  payload.Response.ID,
		Response:    envelope.Response,
		ServiceTier: payload.Response.ServiceTier,
	}
	result.readUsage(payload.Response.WireUsage, ProtocolResponses)
	if eventType != "response.completed" || payload.Response.Error != nil || payload.Response.Status != "" && payload.Response.Status != "completed" {
		result.Failed = true
		if eventType == "response.incomplete" || payload.Response.Status == "incomplete" {
			result.IncompleteReason = payload.Response.incompleteReason()
		}
		if payload.Response.Error != nil {
			result.ErrorCode = wireErrorCode(payload.Response.Error)
			result.ErrorMessage = payload.Response.Error.Message
		}
	}
	if result.UsageReported && !result.UsageKnown {
		return result, &Error{Code: ErrorCodeInvalidStream, Message: "terminal Responses event contains partial usage"}
	}
	if eventType == "response.completed" && (payload.Response.Error != nil || payload.Response.Status != "" && payload.Response.Status != "completed") {
		return result, &Error{Code: ErrorCodeInvalidStream, Message: "completed Responses event contains an inconsistent terminal status"}
	}
	return result, nil
}
