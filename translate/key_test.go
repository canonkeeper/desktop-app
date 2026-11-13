// SPDX-License-Identifier: MIT

package translate_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/canonkeeper/desktop-app/translate"
)

// The key of the provider never leaves the module in the text of a failure,
// and removing it changes nothing else.

// outcomeText is everything a call reported: the error and its fields, the
// result and every event.
func outcomeText(err error, result translate.Result, events []translate.Event) string {
	text := []string{string(result.Response), result.ErrorMessage}
	if err != nil {
		text = append(text, err.Error())
	}
	var failure *translate.Error
	if errors.As(err, &failure) {
		text = append(text, failure.Message, failure.Body)
	}
	for _, event := range events {
		text = append(text, string(event.Data))
	}
	return strings.Join(text, "\n")
}

// TR3.2: a provider that repeats the key right where its refusal is cut to
// 4 KiB leaves no part of the key in the error, its text or the failed event.
func TestAKeyAtTheCutOfARefusalLeavesNoPartOfIt(t *testing.T) {
	// No text of the module or of the stub has "zq": any "zq" is the key.
	const key = "zq-secret-91f3c0d7e2b4"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		pad, _ := strconv.Atoi(r.Header.Get("X-Test-Pad"))
		text := strings.Repeat("a", pad) + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") + " and more"
		mime := r.Header.Get("X-Test-Mime")
		w.Header().Set("Content-Type", mime)
		w.WriteHeader(http.StatusTooManyRequests)
		if mime == "application/json" {
			_, _ = fmt.Fprintf(w, `{"error":{"code":"rate_limit_exceeded","message":%q}}`, text)
			return
		}
		_, _ = io.WriteString(w, text)
	}))
	defer server.Close()
	translator := translate.New(server.Client())
	defer translator.Close()

	cuts := 0
	for _, profile := range []translate.Capabilities{chatProfile(), responsesProfile()} {
		for _, mime := range []string{"text/plain", "application/json"} {
			for pad := 4<<10 - len(key) - 64; pad <= 4<<10; pad++ {
				for _, stream := range []bool{true, false} {
					to := translate.Target{BaseURL: server.URL + "/v1", Credential: key, Capabilities: profile,
						Headers: http.Header{"X-Test-Pad": {strconv.Itoa(pad)}, "X-Test-Mime": {mime}}}
					var events []translate.Event
					var result translate.Result
					var err error
					if stream {
						result, err = translator.OpenStream(context.Background(), to, request(true, ""), func(event translate.Event) error {
							events = append(events, event)
							return nil
						})
					} else {
						result, err = translator.Execute(context.Background(), to, request(false, ""))
					}
					var failure *translate.Error
					if !errors.As(err, &failure) || failure.Code != translate.ErrorCodeRateLimited || len(failure.Body) > 4<<10 || len(failure.Message) > 4<<10 {
						t.Fatalf("%s %s pad %d: %v", profile.Protocol, mime, pad, err)
					}
					if strings.HasSuffix(failure.Body, "[provider k") || strings.HasSuffix(failure.Message, "[provider") {
						cuts++
					}
					if text := outcomeText(err, result, events); strings.Contains(text, "zq") {
						t.Fatalf("%s %s pad %d stream %v: a part of the key is in %q", profile.Protocol, mime, pad, stream, text[max(0, strings.Index(text, "zq")-40):])
					}
				}
			}
		}
	}
	if cuts == 0 {
		t.Fatal("no case put the key at the cut")
	}
}

// TR3.2, TR2.7: a placeholder key of a local server ("x", "ollama",
// "lm-studio") changes nothing that is not the text of a failure: the events,
// the answer and the result come as they would without a key.
func TestAPlaceholderKeyChangesNothingOfTheAnswer(t *testing.T) {
	const answer = "Ollama runs locally: ollama, lm-studio and x are names here."
	chatPieces := []string{"Ollama runs locally: ", "ollama, lm-studio ", "and x are names here."}
	responsesEvents := []string{
		`{"type":"response.created","response":{"id":"r","status":"in_progress","output":[]}}`,
		`{"type":"response.output_text.delta","item_id":"m","output_index":0,"content_index":0,"delta":` + strconv.Quote(answer) + `}`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","output":[{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"output_text","text":` + strconv.Quote(answer) + `}]}],"usage":{"input_tokens":3,"output_tokens":9}}}`,
	}
	responsesWhole := `{"id":"r","status":"completed","output":[{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"output_text","text":` + strconv.Quote(answer) + `}]}],"usage":{"input_tokens":3,"output_tokens":9}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		chat := strings.HasSuffix(r.URL.Path, "/chat/completions")
		switch {
		case chat && body.Stream:
			w.Header().Set("Content-Type", "text/event-stream")
			for _, piece := range chatPieces {
				_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", piece)
			}
			_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":9}}`+"\n\ndata: [DONE]\n\n")
		case chat:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":9}}`, answer)
		case body.Stream:
			w.Header().Set("Content-Type", "text/event-stream")
			for _, event := range responsesEvents {
				_, _ = io.WriteString(w, "data: "+event+"\n\n")
			}
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, responsesWhole)
		}
	}))
	defer server.Close()
	translator := translate.New(server.Client())
	defer translator.Close()

	for _, key := range []string{"", "x", "ollama", "lm-studio"} {
		to := target(server, chatProfile())
		to.Credential = key
		var events []translate.Event
		result, err := translator.OpenStream(context.Background(), to, request(true, ""), func(event translate.Event) error {
			events = append(events, event)
			return nil
		})
		if err != nil || result.Failed || messageText(t, result.Response) != answer {
			t.Fatalf("key %q, Chat stream: %s %v", key, result.Response, err)
		}
		deltas := ""
		for _, event := range events {
			var fields struct {
				Type  string `json:"type"`
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal(event.Data, &fields); err != nil || fields.Type != event.Type {
				t.Fatalf("key %q, Chat stream: event %s is not itself: %s", key, event.Type, event.Data)
			}
			if event.Type == "response.output_text.delta" {
				deltas += fields.Delta
			}
		}
		if deltas != answer {
			t.Fatalf("key %q, Chat stream: the answer came as %q", key, deltas)
		}
		result, err = translator.Execute(context.Background(), to, request(false, ""))
		if err != nil || messageText(t, result.Response) != answer {
			t.Fatalf("key %q, Chat whole: %s %v", key, result.Response, err)
		}

		to = target(server, responsesProfile())
		to.Credential = key
		events = nil
		result, err = translator.OpenStream(context.Background(), to, request(true, ""), func(event translate.Event) error {
			events = append(events, event)
			return nil
		})
		if err != nil || len(events) != len(responsesEvents) {
			t.Fatalf("key %q, Responses stream: %d events, %v", key, len(events), err)
		}
		for i, event := range events {
			if string(event.Data) != responsesEvents[i] {
				t.Fatalf("key %q, Responses stream: event %d changed to %s", key, i, event.Data)
			}
		}
		result, err = translator.Execute(context.Background(), to, request(false, ""))
		if err != nil || string(result.Response) != responsesWhole {
			t.Fatalf("key %q, Responses whole: changed to %s %v", key, result.Response, err)
		}
	}
}

// messageText is the text of the message of a Responses object.
func messageText(t *testing.T, response json.RawMessage) string {
	t.Helper()
	var object struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(response, &object); err != nil {
		t.Fatalf("response %s: %v", response, err)
	}
	for _, item := range object.Output {
		if item.Type == "message" && len(item.Content) == 1 {
			return item.Content[0].Text
		}
	}
	return ""
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TR3.2: the error of a call cancelled while the provider's last word echoed
// the key keeps no key in its text, and is still a cancellation and an Error.
func TestACancelledCallKeepsNoKeyInItsError(t *testing.T) {
	streams := map[translate.Protocol]string{
		translate.ProtocolResponses: `data: {"type":"response.created","response":{"id":"r","status":"in_progress"}}` + "\n\n" +
			`data: {"type":"error","error":{"code":"server_error","message":"failed for {key}"}}` + "\n\n",
		translate.ProtocolChatCompletions: `data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n" +
			`data: {"error":{"code":"server_error","message":"failed for {key}"}}` + "\n\n",
	}
	// The body is read from memory: the cancellation cannot cut it before the
	// provider's error is read.
	translator := translate.New(&http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		protocol := translate.ProtocolResponses
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			protocol = translate.ProtocolChatCompletions
		}
		body := strings.ReplaceAll(streams[protocol], "{key}", strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		return &http.Response{StatusCode: 200, ProtoMajor: 2, Header: http.Header{"Content-Type": {"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	defer translator.Close()
	for _, profile := range []translate.Capabilities{responsesProfile(), chatProfile()} {
		ctx, cancel := context.WithCancel(context.Background())
		var events []translate.Event
		result, err := translator.OpenStream(ctx, translate.Target{BaseURL: "http://provider.test/v1", Credential: credential, Capabilities: profile},
			request(true, ""), func(event translate.Event) error {
				events = append(events, event)
				cancel()
				return nil
			})
		cancel()
		var failure *translate.Error
		if !errors.Is(err, context.Canceled) || !errors.As(err, &failure) || failure.Code != "server_error" {
			t.Fatalf("%s: %v", profile.Protocol, err)
		}
		if text := outcomeText(err, result, events); strings.Contains(text, credential) {
			t.Fatalf("%s: the key is in %q", profile.Protocol, text)
		}
	}
}
