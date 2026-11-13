// SPDX-License-Identifier: MIT

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TR2.7 (F7): a provider on the author's computer or local network is a usual
// case (Ollama, a server of their own): the module bans no address.
func TestLoopbackAndPrivateNetworkProvidersAreAccepted(t *testing.T) {
	server, _ := providerStub(t, chatAnswer)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	adapter := New(server.Client())
	defer adapter.Close()
	for _, base := range []string{server.URL, "http://localhost:" + port} {
		result, err := adapter.Execute(context.Background(), Target{BaseURL: base + "/v1", Capabilities: chatCapabilities()}, Request{Body: json.RawMessage(executeRequest)})
		if err != nil || !result.UsageKnown {
			t.Fatalf("%s: %+v %v", base, result, err)
		}
	}
	for _, base := range []string{
		"http://10.0.0.5:11434/v1", "http://172.16.0.9:8080/v1", "http://192.168.1.20/v1",
		"http://169.254.1.1/v1", "http://[::1]:11434/v1", "http://[fd00::7]/v1",
	} {
		if _, err := prepareTarget(Target{BaseURL: base, Capabilities: chatCapabilities()}); err != nil {
			t.Fatalf("%s refused: %v", base, err)
		}
	}
}

// rawStub answers with body and the Content-Type given, none when empty.
func rawStub(t *testing.T, contentType, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header()["Content-Type"] = nil
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

// TR2.8 (F8): a Chat stream that ends at its finish_reason without [DONE], and
// one sent without Content-Type: text/event-stream, are read only when the
// profile allows it; without the flags both are refused.
func TestChatStreamQuirksAreAcceptedOnlyWithTheProfileFlags(t *testing.T) {
	const answer = `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}` + "\n\n"
	const cut = `data: {"choices":[{"index":0,"delta":{"content":"ok"}}]}` + "\n\n"
	for _, test := range []struct {
		name, contentType, body string
		noDone, noContentType   bool
		want                    string
	}{
		{"no [DONE], refused", "text/event-stream", answer, false, false, ErrorCodeStreamIncomplete},
		{"no [DONE], allowed", "text/event-stream", answer, true, false, ""},
		{"no [DONE] and no finish_reason, allowed", "text/event-stream", cut, true, false, ErrorCodeStreamIncomplete},
		{"no Content-Type, refused", "", answer + "data: [DONE]\n\n", false, false, ErrorCodeInvalidStream},
		{"no Content-Type, allowed", "", answer + "data: [DONE]\n\n", false, true, ""},
		{"no Content-Type and no [DONE], allowed", "", answer, true, true, ""},
		{"JSON Content-Type, allowed", "application/json", answer + "data: [DONE]\n\n", true, true, ErrorCodeInvalidStream},
	} {
		server := rawStub(t, test.contentType, test.body)
		profile := chatCapabilities()
		profile.AllowNoSSEDone, profile.AllowMissingSSEContentType = test.noDone, test.noContentType
		adapter := New(server.Client())
		completed := 0
		result, err := adapter.OpenStream(context.Background(), testTarget(server.URL, profile), Request{Body: json.RawMessage(streamRequest)}, func(event Event) error {
			if event.Type == "response.completed" {
				completed++
			}
			return nil
		})
		adapter.Close()
		if errorCode(err) != test.want || (test.want == "") != (completed == 1) || test.want == "" && !result.UsageKnown {
			t.Fatalf("%s: code %q completed %d result %+v err %v", test.name, errorCode(err), completed, result, err)
		}
	}
}

// TR2.12: a refusal of the provider carries its Retry-After, in seconds or as
// a date, for Chat and for the Responses passthrough, streaming or not.
func TestARefusalCarriesTheProvidersRetryAfter(t *testing.T) {
	for _, test := range []struct{ header, want string }{
		{"7", "7"},
		{"Wed, 21 Oct 2026 07:28:00 GMT", "Wed, 21 Oct 2026 07:28:00 GMT"},
		{"soon", ""},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Retry-After", test.header)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`)
		}))
		adapter := New(server.Client())
		for _, profile := range []Capabilities{chatCapabilities(), responsesCapabilities()} {
			_, streamed := adapter.OpenStream(context.Background(), testTarget(server.URL, profile), Request{Body: json.RawMessage(streamRequest)}, func(Event) error { return nil })
			_, executed := adapter.Execute(context.Background(), testTarget(server.URL, profile), Request{Body: json.RawMessage(executeRequest)})
			for _, err := range []error{streamed, executed} {
				var failure *Error
				if !errors.As(err, &failure) || failure.Status != http.StatusTooManyRequests || failure.Code != ErrorCodeRateLimited || failure.RetryAfter != test.want {
					t.Fatalf("%s, Retry-After %q: %#v", profile.Protocol, test.header, err)
				}
			}
		}
		adapter.Close()
		server.Close()
	}
}

// TR2.13: the caller sets the ceiling of the request body in the target; a
// body above it is refused with its own code before the provider is called.
func TestTheCallerSetsTheRequestBodyCeiling(t *testing.T) {
	for _, profile := range []Capabilities{chatCapabilities(), responsesCapabilities()} {
		target, err := prepareTarget(testTarget("http://127.0.0.1:1", profile))
		if err != nil || target.MaxRequestBodyBytes != defaultMaxRequestBodyBytes {
			t.Fatalf("%s: default ceiling %d, %v", profile.Protocol, target.MaxRequestBodyBytes, err)
		}
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatAnswer)
	}))
	defer server.Close()
	adapter := New(server.Client())
	defer adapter.Close()
	profile := chatCapabilities()
	profile.MaxRequestBodyBytes = 1 << 10
	small := mustJSON(map[string]any{"model": "m", "input": strings.Repeat("x", 512)})
	large := mustJSON(map[string]any{"model": "m", "input": strings.Repeat("x", 2<<10)})
	if _, err := adapter.Execute(context.Background(), testTarget(server.URL, profile), Request{Body: small}); err != nil || calls != 1 {
		t.Fatalf("a body under the ceiling: %v calls=%d", err, calls)
	}
	if _, err := adapter.Execute(context.Background(), testTarget(server.URL, profile), Request{Body: large}); errorCode(err) != ErrorCodeRequestTooLarge || calls != 1 {
		t.Fatalf("a body over the ceiling: %v calls=%d", err, calls)
	}
}

// TR2.14: the Responses passthrough leaves out the body fields the caller
// names and sends no header of the client, only those the target names.
func TestThePassthroughDropsNamedFieldsAndSendsOnlyNamedHeaders(t *testing.T) {
	var body map[string]any
	var headers http.Header
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body, headers = decodeBody(t, raw), r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","response":{"id":"r","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`+"\n\n")
	}))
	defer provider.Close()
	adapter := New(provider.Client())
	defer adapter.Close()
	// The gateway gets a request of Codex and hands the module its body and a
	// target that names the headers and the fields to leave out.
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		profile := responsesCapabilities()
		profile.DropBodyFields = []string{"client_metadata", "prompt_cache_key"}
		target := testTarget(provider.URL, profile)
		target.Headers = http.Header{"X-Title": {"Northern Reach"}}
		if _, err := adapter.OpenStream(r.Context(), target, Request{Body: raw}, func(Event) error { return nil }); err != nil {
			t.Errorf("passthrough: %v", err)
		}
	}))
	defer gateway.Close()
	request, _ := http.NewRequest(http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{"model":"m","input":"hi","stream":true,"store":false,`+
		`"client_metadata":{"installation_id":"i-1","thread_id":"t-1"},"prompt_cache_key":"t-1","text":{"verbosity":"low"}}`))
	request.Header.Set("thread-id", "t-1")
	request.Header.Set("session-id", "t-1")
	request.Header.Set("x-codex-turn-metadata", `{"turn_id":"u-1"}`)
	request.Header.Set("originator", "codex_cli_rs")
	request.Header.Set("User-Agent", "codex_cli_rs/0.167.2")
	response, err := gateway.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if _, ok := body["client_metadata"]; ok || body["prompt_cache_key"] != nil || body["store"] != false || body["text"] == nil {
		t.Fatalf("body: %v", body)
	}
	for _, name := range []string{"Thread-Id", "Session-Id", "X-Codex-Turn-Metadata", "Originator"} {
		if headers.Get(name) != "" {
			t.Fatalf("client header %s reached the provider: %v", name, headers)
		}
	}
	if strings.Contains(headers.Get("User-Agent"), "codex") || headers.Get("X-Title") != "Northern Reach" || headers.Get("Authorization") != "Bearer secret-token" {
		t.Fatalf("headers: %v", headers)
	}
	for _, name := range []string{"Authorization", "Cookie", "Host", "Content-Type"} {
		target := testTarget(provider.URL, responsesCapabilities())
		target.Headers = http.Header{name: {"x"}}
		if _, err := adapter.OpenStream(context.Background(), target, Request{Body: json.RawMessage(streamRequest)}, func(Event) error { return nil }); errorCode(err) != ErrorCodeInvalidConfiguration {
			t.Fatalf("the target set %s: %v", name, err)
		}
	}
}
