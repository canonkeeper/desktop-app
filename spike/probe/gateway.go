// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/canonkeeper/desktop-app/spike/translate/upstream"
)

// gateway is the probe's stand-in for the provider gateway of the spec (DA3.5, DA5): it listens
// on 127.0.0.1, checks Host, refuses any Origin, requires its bearer token, maps the model alias
// to the provider model and translates Responses to Chat Completions with the codex-lb copy.
type gateway struct {
	token    string
	host     string
	stubURL  string
	adapter  *upstream.HTTPAdapter
	mu       sync.Mutex
	requests []map[string]any
}

const chatAlias = "canonkeeper-chat"

func startGateway(stubAddr string) (*gateway, string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", nil, err
	}
	g := &gateway{
		token:   randomHex(32),
		host:    listener.Addr().String(),
		stubURL: "http://" + stubAddr + "/v1",
		adapter: upstream.New(&http.Client{Timeout: 5 * time.Minute}, nil),
	}
	server := &http.Server{Handler: g, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return g, listener.Addr().String(), func() { _ = server.Close(); _ = g.adapter.Close() }, nil
}

func (g *gateway) log() []map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]map[string]any(nil), g.requests...)
}

func (g *gateway) record(entry map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests = append(g.requests, entry)
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	entry := map[string]any{"at": started.UTC().Format(time.RFC3339Nano), "method": r.Method, "path": r.URL.Path, "headers": headerSummary(r.Header)}
	status := g.serve(w, r, entry)
	entry["status"] = status
	entry["duration_ms"] = time.Since(started).Milliseconds()
	g.record(entry)
}

func headerSummary(header http.Header) map[string]string {
	summary := map[string]string{}
	for name, values := range header {
		lower := strings.ToLower(name)
		value := strings.Join(values, ", ")
		switch {
		case lower == "authorization":
			value = "[redacted]"
		case len(value) > 300:
			value = value[:300] + "…"
		}
		summary[lower] = value
	}
	return summary
}

func (g *gateway) serve(w http.ResponseWriter, r *http.Request, entry map[string]any) int {
	if r.Host != g.host {
		w.WriteHeader(http.StatusMisdirectedRequest)
		return http.StatusMisdirectedRequest
	}
	if r.Header.Get("Origin") != "" {
		w.WriteHeader(http.StatusForbidden)
		return http.StatusForbidden
	}
	presented := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(presented), []byte(g.token)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return http.StatusUnauthorized
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"`+chatAlias+`","object":"model"}]}`)
		return http.StatusOK
	case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
		return g.responses(w, r, entry)
	default:
		http.NotFound(w, r)
		return http.StatusNotFound
	}
}

func (g *gateway) responses(w http.ResponseWriter, r *http.Request, entry map[string]any) int {
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return http.StatusBadRequest
	}
	entry["body"] = bodySummary(body)
	var head struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &head)
	if head.Model != chatAlias {
		writeGatewayError(w, http.StatusBadRequest, "unknown_model", "the gateway serves only "+chatAlias)
		return http.StatusBadRequest
	}
	capabilities := upstream.ChatCompletionsCapabilities()
	capabilities.Reasoning = true
	capabilities.ModelMappings = map[string]string{chatAlias: "stub-chat"}
	target := upstream.Target{
		ProviderID: "stub", AccountID: "local", KeyID: "stub-key", BaseURL: g.stubURL,
		Credential: "stub-credential", Capabilities: capabilities,
	}
	request := upstream.Request{Body: body}
	if !head.Stream {
		result, err := g.adapter.Execute(r.Context(), target, request)
		if err != nil {
			return writeUpstreamError(w, err, entry)
		}
		entry["usage_known"] = result.UsageKnown
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result.Response)
		return http.StatusOK
	}
	flusher, _ := w.(http.Flusher)
	events := 0
	types := map[string]int{}
	emit := func(event upstream.Event) error {
		if events == 0 {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
		}
		events++
		types[event.Type]++
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	result, err := g.adapter.OpenStream(r.Context(), target, request, emit)
	entry["events"] = types
	if err != nil {
		if events == 0 {
			return writeUpstreamError(w, err, entry)
		}
		entry["error"] = err.Error()
		return http.StatusOK
	}
	entry["usage_known"] = result.UsageKnown
	entry["failed"] = result.Failed
	return http.StatusOK
}

func writeUpstreamError(w http.ResponseWriter, err error, entry map[string]any) int {
	entry["error"] = err.Error()
	var upstreamErr *upstream.Error
	status := http.StatusBadGateway
	code := "upstream_error"
	if errors.As(err, &upstreamErr) {
		code = upstreamErr.Code
		if upstreamErr.Status != 0 {
			status = upstreamErr.Status
		} else if upstreamErr.Code == upstream.ErrorCodeInvalidRequest || upstreamErr.Code == upstream.ErrorCodeUnsupportedCapability {
			status = http.StatusBadRequest
		}
	} else if errors.Is(err, context.Canceled) {
		status = 499
	}
	writeGatewayError(w, status, code, err.Error())
	return status
}

func writeGatewayError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "type": code}})
}

// bodySummary keeps the shape of a Responses request: top-level keys, tools and the fields that
// may carry a thread identity, never the conversation text.
func bodySummary(body []byte) map[string]any {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return map[string]any{"invalid_json": true, "bytes": len(body)}
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	summary := map[string]any{"bytes": len(body), "keys": keys}
	for _, key := range []string{"model", "prompt_cache_key", "stream", "store", "parallel_tool_calls", "tool_choice", "reasoning", "include", "client_metadata", "service_tier", "text"} {
		if value, ok := raw[key]; ok {
			text := string(value)
			if len(text) > 300 {
				text = text[:300] + "…"
			}
			summary[key] = text
		}
	}
	var input []map[string]any
	if json.Unmarshal(raw["input"], &input) == nil {
		kinds := []string{}
		for _, item := range input {
			kind, _ := item["type"].(string)
			if role, ok := item["role"].(string); ok {
				kind += ":" + role
			}
			// The size of a message, never its text: shows which instructions reach the model.
			if parts, ok := item["content"].([]any); ok {
				chars := 0
				for _, part := range parts {
					if partMap, ok := part.(map[string]any); ok {
						if text, ok := partMap["text"].(string); ok {
							chars += len([]rune(text))
						}
					}
				}
				kind += fmt.Sprintf("(%d)", chars)
			}
			kinds = append(kinds, kind)
		}
		summary["input_items"] = kinds
	}
	var tools []map[string]any
	if json.Unmarshal(raw["tools"], &tools) == nil {
		names := []string{}
		for _, tool := range tools {
			name, _ := tool["name"].(string)
			kind, _ := tool["type"].(string)
			if children, ok := tool["tools"].([]any); ok {
				for _, child := range children {
					if childMap, ok := child.(map[string]any); ok {
						childName, _ := childMap["name"].(string)
						names = append(names, kind+":"+name+"."+childName)
					}
				}
				continue
			}
			names = append(names, kind+":"+name)
		}
		summary["tools"] = names
	}
	if instructions, ok := raw["instructions"]; ok {
		var text string
		_ = json.Unmarshal(instructions, &text)
		summary["instructions_chars"] = len([]rune(text))
	}
	return summary
}
