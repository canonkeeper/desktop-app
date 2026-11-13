// SPDX-License-Identifier: MIT

// Package provider talks to the author's provider in its own protocol: it
// forwards a Responses request as is or translates it to Chat Completions and
// the Chat stream back to Responses events. Compatibility behavior is derived
// in part from codex-relay; see LICENSE.codex-relay for its MIT notice.
package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	ProtocolResponses       = Protocol("responses")
	ProtocolChatCompletions = Protocol("chat_completions")
)

type Protocol string

type Capabilities struct {
	Protocol Protocol

	Tools             bool
	ParallelToolCalls bool
	Reasoning         bool
	// MaxReasoningEffort is the highest effort the provider takes; a higher one
	// is lowered to it. Empty means "high".
	MaxReasoningEffort string
	ImageInput         bool
	ExtraBody          map[string]json.RawMessage
	DropBodyFields     []string
	EnableGLMThinking  bool
	// ThinkTags moves <think>, <thinking> and ◁think▷ markup leaked into the
	// text of a Chat answer to its reasoning.
	ThinkTags      bool
	AllowNoSSEDone bool
	// Some subscription SSE responses omit MIME; their body still must pass
	// the normal event, terminal and usage validation.
	AllowMissingSSEContentType bool
	MaxRequestBodyBytes        int64
}

// defaultMaxRequestBodyBytes is the request body ceiling of a target that
// sets none.
const defaultMaxRequestBodyBytes = 64 << 20

func (c Capabilities) normalized() (Capabilities, error) {
	switch c.Protocol {
	case ProtocolResponses, ProtocolChatCompletions:
	default:
		return c, &Error{Code: ErrorCodeInvalidConfiguration, Message: "upstream protocol is not configured"}
	}
	if _, known := effortRank[c.MaxReasoningEffort]; c.MaxReasoningEffort != "" && !known {
		return c, &Error{Code: ErrorCodeInvalidConfiguration, Message: fmt.Sprintf("unknown reasoning effort %q", c.MaxReasoningEffort)}
	}
	if c.MaxRequestBodyBytes <= 0 {
		c.MaxRequestBodyBytes = defaultMaxRequestBodyBytes
	}
	reserved := map[string]bool{
		"model": true, "messages": true, "tools": true, "stream": true,
		"stream_options": true, "input": true, "type": true,
	}
	for field, value := range c.ExtraBody {
		if reserved[field] {
			return c, &Error{Code: ErrorCodeInvalidConfiguration, Message: fmt.Sprintf("ExtraBody cannot override %q", field)}
		}
		if !json.Valid(value) {
			return c, &Error{Code: ErrorCodeInvalidConfiguration, Message: fmt.Sprintf("ExtraBody[%q] is not valid JSON", field)}
		}
	}
	for _, field := range c.DropBodyFields {
		if reserved[field] {
			return c, &Error{Code: ErrorCodeInvalidConfiguration, Message: fmt.Sprintf("DropBodyFields cannot remove %q", field)}
		}
	}
	return c, nil
}

// Target is where one request goes: the provider's address and key, the
// provider's model in place of the model of the request (empty keeps it), and
// the profile of the provider.
type Target struct {
	BaseURL    string
	Credential string
	Model      string
	// Headers are sent to the provider beside the module's own; the client's
	// headers never reach the provider unless the caller names them here.
	Headers http.Header
	Capabilities
}

type Request struct {
	Body json.RawMessage
}

type Event struct {
	Type string
	Data json.RawMessage
}

type Result struct {
	ResponseID    string
	Response      json.RawMessage
	Usage         Usage
	UsageKnown    bool
	UsageReported bool
	ServiceTier   string
	// Failed reports a terminal other than response.completed: a failed or an
	// incomplete response.
	Failed bool
	// IncompleteReason is set for an incomplete response, such as
	// max_output_tokens.
	IncompleteReason string
	ErrorCode        string
	ErrorMessage     string
}

const (
	ErrorCodeInvalidRequest        = "invalid_request"
	ErrorCodeRequestTooLarge       = "request_too_large"
	ErrorCodeInvalidConfiguration  = "invalid_configuration"
	ErrorCodeUnsupportedCapability = "unsupported_capability"
	ErrorCodeUpstreamError         = "upstream_error"
	ErrorCodeInsufficientQuota     = "insufficient_quota"
	ErrorCodeRateLimited           = "rate_limit_exceeded"
	ErrorCodeConnection            = "connection_error"
	ErrorCodeStreamIncomplete      = "stream_incomplete"
	ErrorCodeInvalidStream         = "invalid_stream"
	ErrorCodeInvalidToolCall       = "invalid_tool_call"
)

type Error struct {
	Code    string
	Status  int
	Message string
	// RetryAfter is the provider's Retry-After of a refusal: seconds or a date.
	RetryAfter string
	// Body is the provider's refusal as it came; out of the module it is cut
	// to maxRefusalBodyBytes without the key (failureText).
	Body string
	// Set only for a complete HTTP validation rejection with no output/billing.
	RejectedBeforeExecution bool
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("upstream %s (HTTP %d): %s", e.Code, e.Status, e.Message)
	}
	return fmt.Sprintf("upstream %s: %s", e.Code, e.Message)
}

func upstreamHTTPError(status int, body []byte) *Error {
	var payload struct {
		Error *struct {
			Code    string          `json:"code"`
			Type    string          `json:"type"`
			Message string          `json:"message"`
			Param   json.RawMessage `json:"param"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)
	code := fmt.Sprintf("http_%d", status)
	message := strings.TrimSpace(string(body))
	if payload.Error != nil {
		if payload.Error.Code != "" {
			code = payload.Error.Code
		} else if payload.Error.Type != "" {
			code = payload.Error.Type
		}
		if payload.Error.Message != "" {
			message = payload.Error.Message
		}
		if previousResponseMissing(code, message, payload.Error.Param) {
			code = "previous_response_not_found"
		}
	}
	if status == http.StatusTooManyRequests {
		if insufficientQuotaCode(code) {
			code = ErrorCodeInsufficientQuota
		} else {
			code = ErrorCodeRateLimited
		}
	}
	if message == "" {
		message = http.StatusText(status)
	}
	return &Error{Code: code, Status: status, Message: message, Body: string(body)}
}

func insufficientQuotaCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "1308", "insufficient_quota", "usage_limit_reached", "usage_limit_exceeded", "quota_exceeded", "billing_hard_limit_reached":
		return true
	default:
		return false
	}
}

func responseEventError(body []byte) *Error {
	var envelope struct {
		Status int `json:"status"`
	}
	_ = json.Unmarshal(body, &envelope)
	err := upstreamHTTPError(envelope.Status, body)
	if insufficientQuotaCode(err.Code) {
		err.Code = ErrorCodeInsufficientQuota
	}
	return err
}

type Usage struct {
	InputTokens          uint64 `json:"input_tokens"`
	OutputTokens         uint64 `json:"output_tokens"`
	TotalTokens          uint64 `json:"total_tokens"`
	CachedTokens         uint64 `json:"cached_tokens"`
	ReasoningTokens      uint64 `json:"reasoning_tokens"`
	ReasoningTokensKnown bool   `json:"-"`
}

type responsesWireUsage struct {
	InputTokens        *uint64 `json:"input_tokens"`
	OutputTokens       *uint64 `json:"output_tokens"`
	InputTokensDetails *struct {
		CachedTokens uint64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails *struct {
		ReasoningTokens uint64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func (u responsesWireUsage) known() bool {
	return validTokenCounts(u.InputTokens, u.OutputTokens) &&
		(u.InputTokensDetails == nil || u.InputTokensDetails.CachedTokens <= *u.InputTokens) &&
		(u.OutputTokensDetails == nil || u.OutputTokensDetails.ReasoningTokens <= *u.OutputTokens)
}

func (u responsesWireUsage) responseUsage() Usage {
	if !u.known() {
		return Usage{}
	}
	cached, reasoning := uint64(0), uint64(0)
	if u.InputTokensDetails != nil {
		cached = u.InputTokensDetails.CachedTokens
	}
	if u.OutputTokensDetails != nil {
		reasoning = u.OutputTokensDetails.ReasoningTokens
	}
	return Usage{
		InputTokens: *u.InputTokens, OutputTokens: *u.OutputTokens, TotalTokens: *u.InputTokens + *u.OutputTokens,
		CachedTokens: cached, ReasoningTokens: reasoning,
		ReasoningTokensKnown: u.OutputTokensDetails != nil,
	}
}

type responsesWireRequest struct {
	Raw                json.RawMessage     `json:"-"`
	Model              string              `json:"model"`
	Input              json.RawMessage     `json:"input"`
	PreviousResponseID string              `json:"previous_response_id"`
	Tools              []json.RawMessage   `json:"tools"`
	Stream             bool                `json:"stream"`
	Temperature        *float64            `json:"temperature"`
	MaxOutputTokens    *uint64             `json:"max_output_tokens"`
	System             string              `json:"system"`
	Instructions       string              `json:"instructions"`
	Reasoning          *responsesReasoning `json:"reasoning"`
	ParallelToolCalls  *bool               `json:"parallel_tool_calls"`
	ToolChoice         json.RawMessage     `json:"tool_choice"`
}

type responsesReasoning struct {
	Effort string `json:"effort"`
}

type responsesItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	ID        string          `json:"id"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Namespace string          `json:"namespace"`
	Arguments json.RawMessage `json:"arguments"`
	Input     json.RawMessage `json:"input"`
	Output    json.RawMessage `json:"output"`
	Content   json.RawMessage `json:"content"`
	Summary   json.RawMessage `json:"summary"`
}

type ChatRequest struct {
	Model             string             `json:"model"`
	Messages          []ChatMessage      `json:"messages"`
	Tools             []json.RawMessage  `json:"tools,omitempty"`
	Temperature       *float64           `json:"temperature,omitempty"`
	MaxTokens         *uint64            `json:"max_tokens,omitempty"`
	StreamOptions     *ChatStreamOptions `json:"stream_options,omitempty"`
	Thinking          *ChatThinking      `json:"thinking,omitempty"`
	ReasoningEffort   string             `json:"reasoning_effort,omitempty"`
	ParallelToolCalls *bool              `json:"parallel_tool_calls,omitempty"`
	ToolChoice        json.RawMessage    `json:"tool_choice,omitempty"`
	Stream            bool               `json:"stream"`
}

type ChatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type ChatThinking struct {
	Type string `json:"type"`
}

type ChatMessage struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Reasoning        string          `json:"reasoning,omitempty"`
	ToolCalls        []ChatToolCall  `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	Name             string          `json:"name,omitempty"`
}

type ChatToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function ChatToolCallFunction `json:"function"`
}

type ChatToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatWireResponse struct {
	Choices     []chatWireChoice `json:"choices"`
	Usage       json.RawMessage  `json:"usage"`
	Error       *wireError       `json:"error"`
	ServiceTier string           `json:"service_tier"`
}

type chatWireChoice struct {
	Index        *int        `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type wireError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Type    string          `json:"type"`
	Param   json.RawMessage `json:"param"`
}

func previousResponseMissing(code, message string, param json.RawMessage) bool {
	parameter := ""
	if len(param) > 0 && string(param) != "null" {
		if json.Unmarshal(param, &parameter) != nil {
			return false
		}
		if parameter != "previous_response_id" {
			return false
		}
	}
	if code == "previous_response_not_found" {
		return true
	}
	if code != "invalid_request_error" {
		return false
	}
	normalized := strings.TrimSuffix(strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(message, "`", ""))), " "), ".")
	return normalized == "invalid previous_response_id" || parameter == "previous_response_id" && strings.Contains(normalized, "previous response") && strings.Contains(normalized, "not found")
}

func wireErrorCode(value *wireError) string {
	code := value.Code
	if code == "" {
		code = value.Type
	}
	if previousResponseMissing(code, value.Message, value.Param) {
		return "previous_response_not_found"
	}
	if insufficientQuotaCode(code) {
		return ErrorCodeInsufficientQuota
	}
	return code
}

type chatWireUsage struct {
	PromptTokens          *uint64 `json:"prompt_tokens"`
	CompletionTokens      *uint64 `json:"completion_tokens"`
	PromptCacheHitTokens  *uint64 `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens *uint64 `json:"prompt_cache_miss_tokens"`
	PromptTokensDetails   *struct {
		CachedTokens uint64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens uint64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u chatWireUsage) known() bool {
	if !validTokenCounts(u.PromptTokens, u.CompletionTokens) ||
		u.PromptCacheHitTokens != nil && *u.PromptCacheHitTokens > *u.PromptTokens ||
		u.PromptCacheMissTokens != nil && *u.PromptCacheMissTokens > *u.PromptTokens ||
		u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > *u.PromptTokens ||
		u.CompletionTokensDetails != nil && u.CompletionTokensDetails.ReasoningTokens > *u.CompletionTokens {
		return false
	}
	if u.PromptCacheHitTokens != nil && u.PromptTokensDetails != nil && *u.PromptCacheHitTokens != u.PromptTokensDetails.CachedTokens {
		return false
	}
	if u.PromptCacheMissTokens != nil {
		cached := *u.PromptTokens - *u.PromptCacheMissTokens
		if u.PromptCacheHitTokens != nil && *u.PromptCacheHitTokens != cached ||
			u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens != cached {
			return false
		}
	}
	return true
}

func (u chatWireUsage) responseUsage() Usage {
	if !u.known() {
		return Usage{}
	}
	cached := uint64(0)
	if u.PromptCacheHitTokens != nil {
		cached = *u.PromptCacheHitTokens
	} else if u.PromptTokensDetails != nil {
		cached = u.PromptTokensDetails.CachedTokens
	} else if u.PromptCacheMissTokens != nil {
		cached = *u.PromptTokens - *u.PromptCacheMissTokens
	}
	return Usage{
		InputTokens: *u.PromptTokens, OutputTokens: *u.CompletionTokens,
		TotalTokens: *u.PromptTokens + *u.CompletionTokens, CachedTokens: cached,
		ReasoningTokensKnown: u.CompletionTokensDetails != nil,
		ReasoningTokens: func() uint64 {
			if u.CompletionTokensDetails == nil {
				return 0
			}
			return u.CompletionTokensDetails.ReasoningTokens
		}(),
	}
}

type chatStreamChunk struct {
	Choices     []chatStreamChoice `json:"choices"`
	Usage       json.RawMessage    `json:"usage"`
	Error       *wireError         `json:"error"`
	ServiceTier string             `json:"service_tier"`
}

type chatStreamChoice struct {
	Index        *int            `json:"index"`
	Delta        chatStreamDelta `json:"delta"`
	FinishReason string          `json:"finish_reason"`
}

type chatStreamDelta struct {
	Role             string               `json:"role"`
	Content          string               `json:"content"`
	ReasoningContent string               `json:"reasoning_content"`
	Reasoning        string               `json:"reasoning"`
	ToolCalls        []chatStreamToolCall `json:"tool_calls"`
}

type chatStreamToolCall struct {
	Index    *int                        `json:"index"`
	ID       string                      `json:"id"`
	Function *chatStreamToolCallFunction `json:"function"`
}

type chatStreamToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
