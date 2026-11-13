// SPDX-License-Identifier: MIT

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/canonkeeper/desktop-app/translate/internal/wirejson"
)

const maxErrorBodyBytes = 1 << 20

// HTTPAdapter sends requests to providers through one HTTP client.
type HTTPAdapter struct {
	client *http.Client
}

func (a *HTTPAdapter) Close() error {
	a.client.CloseIdleConnections()
	return nil
}

func New(client *http.Client) *HTTPAdapter {
	if client == nil {
		client = &http.Client{}
	}
	// A provider redirect must not carry an account token or replay a request
	// body to a different endpoint, even when called outside the server runtime.
	ownedClient := *client
	ownedClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPAdapter{client: &ownedClient}
}

// Execute sends a request without a stream and returns the provider's whole
// response as a Responses object.
func (a *HTTPAdapter) Execute(ctx context.Context, target Target, request Request) (Result, error) {
	result, err := a.execute(ctx, target, request)
	return failureText{target.Credential}.outcome(result, err)
}

// OpenStream sends a streaming request and hands every Responses event to
// emit until the terminal one.
func (a *HTTPAdapter) OpenStream(ctx context.Context, target Target, request Request, emit func(Event) error) (Result, error) {
	if emit == nil {
		return Result{}, &Error{Code: ErrorCodeInvalidRequest, Message: "stream callback is required"}
	}
	failure := failureText{target.Credential}
	result, err := a.openStream(ctx, target, request, failure.emit(emit))
	return failure.outcome(result, err)
}

func (a *HTTPAdapter) execute(ctx context.Context, target Target, request Request) (Result, error) {
	prepared, err := prepareTarget(target)
	if err != nil {
		return Result{}, err
	}
	req, items, catalog, err := prepareRequest(request, prepared)
	if err != nil {
		return Result{}, err
	}
	if req.Stream {
		return Result{}, &Error{Code: ErrorCodeInvalidRequest, Message: "streaming requests must use OpenStream"}
	}
	if prepared.Capabilities.Protocol == ProtocolResponses {
		return a.executeResponses(ctx, prepared, req)
	}
	return a.executeChat(ctx, prepared, req, items, catalog)
}

func (a *HTTPAdapter) openStream(ctx context.Context, target Target, request Request, emit func(Event) error) (Result, error) {
	prepared, err := prepareTarget(target)
	if err != nil {
		return Result{}, err
	}
	req, items, catalog, err := prepareRequest(request, prepared)
	if err != nil {
		return Result{}, err
	}
	if !req.Stream {
		return Result{}, &Error{Code: ErrorCodeInvalidRequest, Message: "OpenStream requires stream=true"}
	}
	if prepared.Capabilities.Protocol == ProtocolResponses {
		return a.streamResponses(ctx, prepared, req, emit)
	}
	return a.streamChat(ctx, prepared, req, items, catalog, emit)
}

func prepareTarget(target Target) (Target, error) {
	capabilities, err := target.Capabilities.normalized()
	if err != nil {
		return target, err
	}
	target.Capabilities = capabilities
	for name := range target.Headers {
		if reservedHeaders[http.CanonicalHeaderKey(name)] {
			return target, &Error{Code: ErrorCodeInvalidConfiguration, Message: fmt.Sprintf("the target cannot set the header %s", http.CanonicalHeaderKey(name))}
		}
	}
	parsed, err := url.Parse(target.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return target, &Error{Code: ErrorCodeInvalidConfiguration, Message: "base URL must be an absolute HTTP URL without query or fragment"}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return target, &Error{Code: ErrorCodeInvalidConfiguration, Message: "base URL must use HTTP or HTTPS"}
	}
	return target, nil
}

func prepareRequest(request Request, target Target) (responsesWireRequest, []responsesItem, toolCatalog, error) {
	req, err := decodeResponsesRequest(request.Body, target.Capabilities.MaxRequestBodyBytes)
	if err != nil {
		return req, nil, toolCatalog{}, err
	}
	items, err := req.items()
	if err != nil {
		return req, nil, toolCatalog{}, err
	}
	if err := validateRequestCapabilities(req, items, target.Capabilities); err != nil {
		return req, nil, toolCatalog{}, err
	}
	if target.Capabilities.Protocol == ProtocolResponses {
		// Responses forwards Raw; parsed payloads were needed only for validation.
		// Do not carry their full copies into a potentially long HTTP call.
		req.Input, req.Tools, req.System, req.Instructions = nil, nil, "", ""
		return req, nil, toolCatalog{}, nil
	}
	catalog, err := newToolCatalog(req)
	if err != nil {
		return req, nil, toolCatalog{}, err
	}
	return req, items, catalog, nil
}

func (a *HTTPAdapter) executeResponses(ctx context.Context, target Target, req responsesWireRequest) (Result, error) {
	body, err := passthroughBody(req, target)
	if err != nil {
		return Result{}, err
	}
	responseBody, status, header, err := a.do(ctx, target, http.MethodPost, endpoint(target.BaseURL, "responses"), body, "application/json")
	if err != nil {
		return Result{}, err
	}
	if status != http.StatusOK {
		return rejectedHTTPResult(status, header, responseBody, target.Capabilities.Protocol)
	}
	var payload responsesTerminal
	if err := decodeExactJSON(responseBody, &payload); err != nil {
		return Result{}, err
	}
	if payload.ID == "" {
		return Result{}, &Error{Code: ErrorCodeInvalidStream, Message: "upstream Responses payload omitted id"}
	}
	result := Result{ResponseID: payload.ID, Response: responseBody, ServiceTier: payload.ServiceTier}
	result.readUsage(payload.WireUsage, ProtocolResponses)
	if payload.Status == "failed" || payload.Status == "incomplete" || payload.Error != nil {
		result.Failed = true
		if payload.Status == "incomplete" {
			result.IncompleteReason = payload.incompleteReason()
		}
		if payload.Error != nil {
			result.ErrorCode = wireErrorCode(payload.Error)
			result.ErrorMessage = payload.Error.Message
		}
	}
	if result.UsageReported && !result.UsageKnown {
		return result, &Error{Code: ErrorCodeInvalidStream, Message: "Responses payload contains partial usage"}
	}
	return result, nil
}

func (a *HTTPAdapter) executeChat(ctx context.Context, target Target, req responsesWireRequest, items []responsesItem, catalog toolCatalog) (Result, error) {
	chat, err := translateToChat(req, items, catalog, target, false)
	if err != nil {
		return Result{}, err
	}
	body, err := mapRequestBody(chat, target.Capabilities)
	if err != nil {
		return Result{}, err
	}
	responseBody, status, header, err := a.do(ctx, target, http.MethodPost, endpoint(target.BaseURL, "chat/completions"), body, "application/json")
	if err != nil {
		return Result{}, err
	}
	if status != http.StatusOK {
		return rejectedHTTPResult(status, header, responseBody, target.Capabilities.Protocol)
	}
	var chatResponse chatWireResponse
	if err := decodeExactJSON(responseBody, &chatResponse); err != nil {
		return Result{}, err
	}
	result, err := responsesResponseFromChat(newID("resp_"), req.Model, chatResponse, catalog, target.Capabilities.ThinkTags)
	if err != nil {
		result.Response = responseBody
	}
	return result, err
}

// rejectedResult is a refusal of the provider; its text is the Error's Body,
// not a response of the result.
func rejectedResult(status int, body []byte, protocol Protocol) (Result, error) {
	failure := upstreamHTTPError(status, body)
	result := Result{Failed: true, ErrorCode: failure.Code}
	var rejected struct {
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(body, &rejected) == nil {
		result.readUsage(rejected.Usage, protocol)
	}
	return result, failure
}

// Stream error events share rejectedResult, but an HTTP validation rejection
// proves the request was not executed. Never infer this from a status alone.
// An HTTP refusal carries the provider's Retry-After.
func rejectedHTTPResult(status int, header http.Header, body []byte, protocol Protocol) (Result, error) {
	result, err := rejectedResult(status, body, protocol)
	var failure *Error
	if errors.As(err, &failure) {
		failure.RetryAfter = retryAfter(header)
	}
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return result, err
	}
	var object map[string]json.RawMessage
	var rejection struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(body, &object) != nil || json.Unmarshal(object["error"], &rejection) != nil || rejection.Type != "invalid_request_error" {
		return result, err
	}
	for _, field := range []string{"usage", "duration", "response", "id"} {
		if raw := bytes.TrimSpace(object[field]); len(raw) != 0 && !bytes.Equal(raw, []byte("null")) {
			return result, err
		}
	}
	for _, field := range []string{"output", "choices"} {
		var items []json.RawMessage
		if raw := object[field]; len(raw) != 0 && (json.Unmarshal(raw, &items) != nil || len(items) != 0) {
			return result, err
		}
	}
	if failure != nil {
		failure.RejectedBeforeExecution = true
	}
	return result, err
}

// retryAfter is the Retry-After of a response when it is a delay in seconds
// or an HTTP date (RFC 9110); anything else is no hint.
func retryAfter(header http.Header) string {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if _, err := strconv.ParseUint(value, 10, 63); err == nil {
		return value
	}
	if _, err := http.ParseTime(value); err == nil {
		return value
	}
	return ""
}

func (a *HTTPAdapter) do(ctx context.Context, target Target, method string, endpoint string, body []byte, contentType string) ([]byte, int, http.Header, error) {
	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, &Error{Code: ErrorCodeInvalidConfiguration, Message: "invalid upstream endpoint: " + err.Error()}
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Accept", "application/json")
	if target.Credential != "" {
		httpReq.Header.Set("Authorization", "Bearer "+target.Credential)
	}
	copyTargetHeaders(httpReq.Header, target.Headers)
	response, err := a.client.Do(httpReq)
	if err != nil {
		return nil, 0, nil, wrapContext(ctx, &Error{Code: ErrorCodeConnection, Message: err.Error()})
	}
	defer response.Body.Close()
	limit := int64(maxErrorBodyBytes)
	if response.StatusCode < 400 {
		limit = 64 << 20
	}
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if readErr != nil {
		return nil, response.StatusCode, nil, wrapContext(ctx, &Error{Code: ErrorCodeConnection, Message: readErr.Error()})
	}
	if int64(len(payload)) > limit {
		return nil, response.StatusCode, nil, &Error{Code: ErrorCodeConnection, Message: "upstream response exceeds bounded read limit"}
	}
	return payload, response.StatusCode, response.Header, nil
}

func decodeExactJSON(body []byte, value any) error {
	if !json.Valid(body) {
		return &Error{Code: ErrorCodeInvalidStream, Message: "upstream returned invalid JSON"}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(value); err != nil {
		return &Error{Code: ErrorCodeInvalidStream, Message: "invalid upstream JSON: " + err.Error()}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return &Error{Code: ErrorCodeInvalidStream, Message: "upstream JSON contains trailing data"}
	}
	return nil
}

// passthroughObject parses the Responses request once and applies provider
// body mappings; changed reports whether the top level differs from req.Raw.
func passthroughObject(req responsesWireRequest, target Target) (object map[string]json.RawMessage, changed bool, err error) {
	if err := json.Unmarshal(req.Raw, &object); err != nil || object == nil {
		return nil, false, &Error{Code: ErrorCodeInvalidConfiguration, Message: "could not reshape Responses request"}
	}
	cap := target.Capabilities
	mapped := strings.TrimSpace(target.Model)
	if mapped != "" {
		object["model"] = mustJSON(mapped)
	}
	reasoningDropped := !cap.Reasoning && dropReasoning(object)
	for _, field := range cap.DropBodyFields {
		delete(object, field)
	}
	for field, value := range cap.ExtraBody {
		object[field] = value
	}
	changed = normalizeWireReasoning(object, cap.MaxReasoningEffort) || reasoningDropped || mapped != "" || len(cap.DropBodyFields) != 0 || len(cap.ExtraBody) != 0
	return object, changed, nil
}

// dropReasoning removes what asks a model without reasoning to reason: the
// reasoning object and the encrypted reasoning it would include.
func dropReasoning(object map[string]json.RawMessage) bool {
	_, changed := object["reasoning"]
	delete(object, "reasoning")
	var include []json.RawMessage
	if json.Unmarshal(object["include"], &include) != nil {
		return changed
	}
	kept := make([]json.RawMessage, 0, len(include))
	for _, value := range include {
		var name string
		if json.Unmarshal(value, &name) != nil || name != "reasoning.encrypted_content" {
			kept = append(kept, value)
		}
	}
	switch {
	case len(kept) == len(include):
	case len(kept) == 0:
		delete(object, "include")
		changed = true
	default:
		object["include"] = mustJSON(kept)
		changed = true
	}
	return changed
}

func passthroughBody(req responsesWireRequest, target Target) (json.RawMessage, error) {
	object, changed, err := passthroughObject(req, target)
	if err != nil || !changed {
		return req.Raw, err
	}
	return wirejson.EncodeObject(object), nil
}

func endpoint(base string, path string) string {
	return strings.TrimSuffix(base, "/") + "/" + path
}

// reservedHeaders are the headers of the module's own request: the credential,
// the body and the connection. A target cannot name them.
var reservedHeaders = map[string]bool{
	"Authorization": true, "Cookie": true, "Host": true, "Content-Type": true, "Content-Length": true,
	"Accept": true, "Transfer-Encoding": true, "Connection": true,
}

// copyTargetHeaders adds the headers the target names; no header of the
// client reaches the provider unless the caller put it there.
func copyTargetHeaders(dst http.Header, src http.Header) {
	for name, values := range src {
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

// wrapContext joins the cancellation of the call to the error it caused, so
// that errors.Is finds the cancellation and errors.As the module's Error.
func wrapContext(ctx context.Context, err error) error {
	if ctx.Err() == nil {
		return err
	}
	return fmt.Errorf("%w: %w", ctx.Err(), err)
}
