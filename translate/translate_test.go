// SPDX-License-Identifier: MIT

package translate_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/canonkeeper/desktop-app/translate"
)

// The tests of this file see the module as the gateway does: only the names
// README lists.

const credential = "ck-test-credential-6f1d2a90"

func chatProfile() translate.Capabilities {
	return translate.Capabilities{Protocol: translate.ProtocolChatCompletions, Tools: true, ParallelToolCalls: true}
}

func responsesProfile() translate.Capabilities {
	return translate.Capabilities{Protocol: translate.ProtocolResponses, Tools: true, ParallelToolCalls: true}
}

// stub answers with status, headers and body, and counts the requests.
func stub(t *testing.T, status int, contentType, body string, sent *atomic.Int64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if sent != nil {
			sent.Add(1)
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, strings.ReplaceAll(body, "{key}", strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
	}))
	t.Cleanup(server.Close)
	return server
}

func target(server *httptest.Server, profile translate.Capabilities) translate.Target {
	return translate.Target{BaseURL: server.URL + "/v1", Credential: credential, Model: "provider-model", Capabilities: profile}
}

func request(stream bool, extra string) translate.Request {
	return translate.Request{Body: json.RawMessage(`{"model":"canonkeeper-chat","input":"hi","stream":` + map[bool]string{true: "true", false: "false"}[stream] + extra + `}`)}
}

// TR3.1: a stream and a whole response go through the open face with the
// target's model, and come back as Responses events and a result.
func TestTheGatewayGoesThroughTheOpenFace(t *testing.T) {
	var model atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		model.Store(body.Model)
		if !body.Stream {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	translator := translate.New(server.Client())
	defer translator.Close()

	var events []translate.Event
	result, err := translator.OpenStream(context.Background(), target(server, chatProfile()), request(true, ""), func(event translate.Event) error {
		events = append(events, event)
		return nil
	})
	var usage translate.Usage = result.Usage
	if err != nil || !result.UsageKnown || usage.InputTokens != 3 || result.Failed || model.Load() != "provider-model" ||
		events[0].Type != "response.created" || events[len(events)-1].Type != "response.completed" {
		t.Fatalf("stream: %+v %v", result, err)
	}
	result, err = translator.Execute(context.Background(), target(server, chatProfile()), request(false, ""))
	if err != nil || result.Usage.TotalTokens != 4 || result.ResponseID == "" {
		t.Fatalf("whole response: %+v %v", result, err)
	}
}

// failures runs every way a provider can refuse or fail, each echoing the key
// it was given in its error, and returns what the module reports.
func failures(t *testing.T) map[string]struct {
	err    error
	result translate.Result
	events []translate.Event
} {
	t.Helper()
	big := `<html>` + strings.Repeat("Too many requests from {key}. ", 400) + `</html>`
	cases := []struct {
		name     string
		profile  translate.Capabilities
		stream   bool
		status   int
		mime     string
		response string
	}{
		{"chat refusal", chatProfile(), true, 401, "application/json", `{"error":{"code":"invalid_api_key","message":"Incorrect API key: {key}"}}`},
		{"chat whole refusal", chatProfile(), false, 401, "application/json", `{"error":{"code":"invalid_api_key","message":"Incorrect API key: {key}"}}`},
		{"responses refusal", responsesProfile(), true, 401, "application/json", `{"error":{"code":"invalid_api_key","message":"Incorrect API key: {key}"}}`},
		{"responses whole refusal", responsesProfile(), false, 401, "application/json", `{"error":{"code":"invalid_api_key","message":"Incorrect API key: {key}"}}`},
		{"large refusal", chatProfile(), true, 429, "text/html", big},
		{"chat stream error", chatProfile(), true, 200, "text/event-stream", `data: {"error":{"code":"server_error","message":"failed for {key}"}}` + "\n\n"},
		{"chat whole error", chatProfile(), false, 200, "application/json", `{"error":{"code":"server_error","message":"failed for {key}"}}`},
		{"responses error event", responsesProfile(), true, 200, "text/event-stream", `data: {"type":"error","error":{"code":"server_error","message":"failed for {key}"}}` + "\n\n"},
		{"responses failed", responsesProfile(), true, 200, "text/event-stream", `data: {"type":"response.failed","response":{"id":"r","status":"failed","error":{"code":"server_error","message":"failed for {key}"}}}` + "\n\n"},
		{"responses whole failed", responsesProfile(), false, 200, "application/json", `{"id":"r","status":"failed","error":{"code":"server_error","message":"failed for {key}"}}`},
		{"chat stream break", chatProfile(), true, 200, "text/event-stream", `data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n"},
	}
	out := make(map[string]struct {
		err    error
		result translate.Result
		events []translate.Event
	})
	for _, test := range cases {
		server := stub(t, test.status, test.mime, test.response, nil)
		translator := translate.New(server.Client())
		var events []translate.Event
		var result translate.Result
		var err error
		if test.stream {
			result, err = translator.OpenStream(context.Background(), target(server, test.profile), request(true, ""), func(event translate.Event) error {
				events = append(events, event)
				return nil
			})
		} else {
			result, err = translator.Execute(context.Background(), target(server, test.profile), request(false, ""))
		}
		translator.Close()
		out[test.name] = struct {
			err    error
			result translate.Result
			events []translate.Event
		}{err, result, events}
	}
	return out
}

// TR3.2: a refusal of the provider is typed with its code, status, body of at
// most 4 KiB and Retry-After, a broken stream and a cancellation are told
// apart, and the key of the provider is in none of them.
func TestErrorsAreTypedAndNeverCarryTheKey(t *testing.T) {
	for name, outcome := range failures(t) {
		var failure *translate.Error
		if !errors.As(outcome.err, &failure) && !outcome.result.Failed {
			t.Fatalf("%s: no failure: %+v %v", name, outcome.result, outcome.err)
		}
		text := []string{string(outcome.result.Response), outcome.result.ErrorMessage}
		if outcome.err != nil {
			text = append(text, outcome.err.Error())
		}
		if failure != nil {
			text = append(text, failure.Message, failure.Body)
			if len(failure.Body) > 4<<10 {
				t.Fatalf("%s: body of %d bytes", name, len(failure.Body))
			}
		}
		for _, event := range outcome.events {
			text = append(text, string(event.Data))
		}
		if strings.Contains(strings.Join(text, "\n"), credential) {
			t.Fatalf("%s: the key is in %q", name, text)
		}
	}
	outcomes := failures(t)
	var refusal *translate.Error
	if !errors.As(outcomes["chat refusal"].err, &refusal) || refusal.Code != "invalid_api_key" || refusal.Status != 401 ||
		refusal.RetryAfter != "3" || !strings.Contains(refusal.Body, "Incorrect API key") {
		t.Fatalf("refusal: %#v", outcomes["chat refusal"].err)
	}
	var limited *translate.Error
	if !errors.As(outcomes["large refusal"].err, &limited) || limited.Code != translate.ErrorCodeRateLimited || limited.RetryAfter != "3" {
		t.Fatalf("large refusal: %#v", outcomes["large refusal"].err)
	}
	var broken *translate.Error
	if !errors.As(outcomes["chat stream break"].err, &broken) || broken.Code != translate.ErrorCodeStreamIncomplete {
		t.Fatalf("stream break: %v", outcomes["chat stream break"].err)
	}

	server := stub(t, 200, "text/event-stream", "", nil)
	translator := translate.New(server.Client())
	defer translator.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := translator.OpenStream(ctx, target(server, chatProfile()), request(true, ""), func(translate.Event) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	for _, test := range []struct {
		body translate.Request
		code string
	}{
		{translate.Request{Body: json.RawMessage(`{"input":"hi","stream":true}`)}, translate.ErrorCodeInvalidRequest},
		{translate.Request{Body: json.RawMessage(`{"model":"m","input":"` + strings.Repeat("x", 2<<10) + `","stream":true}`)}, translate.ErrorCodeRequestTooLarge},
	} {
		limited := target(server, chatProfile())
		limited.MaxRequestBodyBytes = 1 << 10
		if _, err := translator.OpenStream(context.Background(), limited, test.body, func(translate.Event) error { return nil }); !errors.As(err, &refusal) || refusal.Code != test.code {
			t.Fatalf("%s: %v", test.code, err)
		}
	}
}

// TR3.3: built-in tools of Responses and their items are refused before the
// provider is called, in both protocols.
func TestBuiltInToolsAreRefusedBeforeTheProviderIsCalled(t *testing.T) {
	var sent atomic.Int64
	server := stub(t, 200, "text/event-stream", "", &sent)
	translator := translate.New(server.Client())
	defer translator.Close()
	var bodies []string
	for _, tool := range []string{"web_search", "local_shell", "image_generation", "code_interpreter"} {
		bodies = append(bodies, `{"model":"m","stream":true,"input":"hi","tools":[{"type":"`+tool+`"}]}`)
	}
	for _, item := range []string{"web_search_call", "local_shell_call", "local_shell_call_output", "image_generation_call", "code_interpreter_call"} {
		bodies = append(bodies, `{"model":"m","stream":true,"input":[{"type":"message","role":"user","content":"hi"},{"type":"`+item+`","id":"x","call_id":"c","status":"completed"}]}`)
	}
	for _, profile := range []translate.Capabilities{chatProfile(), responsesProfile()} {
		for _, body := range bodies {
			_, err := translator.OpenStream(context.Background(), target(server, profile), translate.Request{Body: json.RawMessage(body)}, func(translate.Event) error { return nil })
			var failure *translate.Error
			if !errors.As(err, &failure) || failure.Code != translate.ErrorCodeUnsupportedCapability || sent.Load() != 0 {
				t.Fatalf("%s %s: %v, %d requests", profile.Protocol, body, err, sent.Load())
			}
		}
	}
}

// The hook of a request hears the size of the body that goes to the provider — in the language of the provider, after the
// translation — before the provider gets a byte of it, for a stream and a whole response in both protocols; an error of the
// hook stops the call as it is, and the provider is asked nothing.
func TestTheSendingHearsTheBodyThatGoes(t *testing.T) {
	var heard, before atomic.Int64
	var received atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		before.Store(heard.Load())
		received.Store(int64(len(body)))
		var asked struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(body, &asked)
		chat := strings.HasSuffix(r.URL.Path, "/chat/completions")
		switch {
		case chat && asked.Stream:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`+"\n\ndata: [DONE]\n\n")
		case chat:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
		case asked.Stream:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n")
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"resp-1","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}`)
		}
	}))
	defer server.Close()
	translator := translate.New(server.Client())
	defer translator.Close()
	for _, profile := range []translate.Capabilities{chatProfile(), responsesProfile()} {
		for _, stream := range []bool{true, false} {
			heard.Store(0)
			call := request(stream, `,"instructions":"`+strings.Repeat("в", 100)+`"`)
			call.Sending = func(bytes int) error {
				heard.Store(int64(bytes))
				return nil
			}
			var err error
			if stream {
				_, err = translator.OpenStream(context.Background(), target(server, profile), call, func(translate.Event) error { return nil })
			} else {
				_, err = translator.Execute(context.Background(), target(server, profile), call)
			}
			if err != nil || heard.Load() == 0 || heard.Load() != received.Load() || before.Load() != heard.Load() {
				t.Fatalf("%s stream %v: heard %d, the provider got %d (heard before it: %d), %v", profile.Protocol, stream, heard.Load(), received.Load(), before.Load(), err)
			}
			if profile.Protocol == translate.ProtocolChatCompletions && heard.Load() == int64(len(call.Body)) {
				t.Fatalf("the size of Chat Completions is the size of the body of Responses: %d", heard.Load())
			}
		}
	}
	refused := errors.New("the record did not lie")
	var sent atomic.Int64
	quiet := stub(t, http.StatusOK, "application/json", `{}`, &sent)
	for _, profile := range []translate.Capabilities{chatProfile(), responsesProfile()} {
		call := request(true, "")
		call.Sending = func(int) error { return refused }
		_, err := translator.OpenStream(context.Background(), target(quiet, profile), call, func(translate.Event) error {
			t.Fatal("an event of a call the hook stopped")
			return nil
		})
		if !errors.Is(err, refused) || sent.Load() != 0 {
			t.Fatalf("%s: a stopped call = %v, the provider got %d", profile.Protocol, err, sent.Load())
		}
		call.Body = request(false, "").Body
		if _, err := translator.Execute(context.Background(), target(quiet, profile), call); !errors.Is(err, refused) || sent.Load() != 0 {
			t.Fatalf("%s: a stopped whole call = %v, the provider got %d", profile.Protocol, err, sent.Load())
		}
	}
}
