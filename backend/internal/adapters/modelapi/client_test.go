package modelapi

import (
	"encoding/json"
	"fmt"
	sdkimages "github.com/openai/codex/sdk/go/images"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
)

func TestCompleteStreamsTextWithKeyFromEnvironment(t *testing.T) {
	t.Setenv("MODEL_TEST_KEY", "secret-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/responses" || r.Header.Get("Authorization") != "Bearer secret-key" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var body struct {
			Model     string         `json:"model"`
			Reasoning map[string]any `json:"reasoning"`
		}
		data, _ := io.ReadAll(r.Body)
		if json.Unmarshal(data, &body) != nil || body.Model != "gpt-6-luna" || body.Reasoning["effort"] != "low" {
			http.Error(w, "unexpected body", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"При\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"вет\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{}}\n\n")
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL + "/backend-api/codex", KeyEnv: "MODEL_TEST_KEY", Model: "gpt-6-luna", Effort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	text, _, err := client.Background().Complete(t.Context(), "инструкция", "вход")
	if err != nil || text != "Привет" {
		t.Fatalf("Complete() = %q, %v", text, err)
	}
}

func TestCompleteRetriesRateLimitAndRejectsClientErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\"}\n\n")
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL, Model: "m"})
	if text, usage, err := client.Background().Complete(t.Context(), "", "x"); err != nil || text != "ok" || calls.Load() != 2 || usage.Calls != 1 {
		t.Fatalf("Complete() = %q, %v after %d calls", text, err, calls.Load())
	}
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer rejecting.Close()
	client, _ = New(Config{BaseURL: rejecting.URL, Model: "m"})
	if _, _, err := client.Background().Complete(t.Context(), "", "x"); err == nil {
		t.Fatal("client error was not reported")
	}
	if _, err := New(Config{BaseURL: rejecting.URL}); err != ErrNotConfigured {
		t.Fatalf("New() without a model = %v", err)
	}
	// specs/2026-11-07-desktop-app.md DA2.6: without a base URL no AI is connected; a call makes no request.
	unconnected, err := New(Config{Model: "m"})
	if err != nil {
		t.Fatalf("New() without URL = %v", err)
	}
	if _, _, err := unconnected.Background().Complete(t.Context(), "", "x"); err != ErrNotConfigured {
		t.Fatalf("Complete() without URL = %v", err)
	}
	if _, _, err := unconnected.Vision().Look(t.Context(), "", "x", []byte{1}); err != ErrNotConfigured {
		t.Fatalf("Look() without URL = %v", err)
	}
}

// specs/2026-11-12-ux10-scene-stream.md ST1.1: Stream hands the text of the answer so far to its portions as the deltas
// come, and "" at the start of each HTTP attempt, so what a broken attempt gave goes; the answer is the one Complete gives.
func TestStreamHandsThePortionsAndStartsAgainOnARetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			// The first attempt breaks after a delta: the stream ends before its completion.
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Обры\"}\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Кира \"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"шла\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{}}\n\n")
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL, Model: "m", WritingModel: "w"})
	client.pause = func(int) time.Duration { return 0 }
	var portions []string
	text, usage, err := client.Prose().Stream(t.Context(), "", "x", func(sofar string) { portions = append(portions, sofar) })
	if err != nil || text != "Кира шла" || usage.Calls != 2 || calls.Load() != 2 {
		t.Fatalf("Stream() = %q, %+v, %v after %d calls", text, usage, err, calls.Load())
	}
	if want := []string{"", "Обры", "", "Кира ", "Кира шла"}; fmt.Sprint(portions) != fmt.Sprint(want) {
		t.Fatalf("portions = %q, want %q", portions, want)
	}
	// Without portions the call is Complete as it was.
	if text, _, err := client.Prose().Stream(t.Context(), "", "x", nil); err != nil || text != "Кира шла" {
		t.Fatalf("Stream(nil) = %q, %v", text, err)
	}
}

// R7.1: tokens come from response.completed; a stream without usage is one call with no tokens;
// a retried attempt answered with 200 counts as a call too.
func TestCompleteReportsUsage(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
			fmt.Fprint(w, `data: {"type":"response.completed","response":{"usage":{"input_tokens":1200,"input_tokens_details":{"cached_tokens":1000},"output_tokens":300,"output_tokens_details":{"reasoning_tokens":120}}}}`+"\n\n")
		case 2:
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{}}\n\n")
		case 3:
			fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"busy\"}}}\n\n")
		default:
			fmt.Fprint(w, `data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}`+"\n\n")
		}
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL, Model: "m"})
	client.pause = func(int) time.Duration { return 0 }
	text, usage, err := client.Background().Complete(t.Context(), "", "x")
	want := quota.ModelUsage{Calls: 1, InputTokens: 1200, CachedInputTokens: 1000, OutputTokens: 300, ReasoningOutputTokens: 120}
	if err != nil || text != "ok" || usage != want {
		t.Fatalf("Complete() = %q, %+v, %v; want %+v", text, usage, err, want)
	}
	if _, usage, err := client.Background().Complete(t.Context(), "", "x"); err != nil || usage != (quota.ModelUsage{Calls: 1}) {
		t.Fatalf("Complete() without usage = %+v, %v", usage, err)
	}
	if _, usage, err := client.Background().Complete(t.Context(), "", "x"); err != nil || usage != (quota.ModelUsage{Calls: 2, InputTokens: 10, OutputTokens: 5}) {
		t.Fatalf("Complete() after a failed stream = %+v, %v; want two calls", usage, err)
	}
}

// specs/2026-10-29-chapter-cards-quality.md R3.1: chapter cards (Background) call the cards model
// and effort, the chat's scan (Interactive) the scan model; without a cards model both share it.
func TestPortsCallTheModelOfTheirClass(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model     string         `json:"model"`
			Reasoning map[string]any `json:"reasoning"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		seen = append(seen, fmt.Sprintf("%s/%v", body.Model, body.Reasoning["effort"]))
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{}}\n\n")
	}))
	defer server.Close()
	split, _ := New(Config{BaseURL: server.URL, Model: "gpt-6-luna", Effort: "medium", CardsModel: "gpt-6.1-sol", CardsEffort: "high", Parallel: 1})
	shared, _ := New(Config{BaseURL: server.URL, Model: "gpt-6-luna", Effort: "medium", Parallel: 1})
	for _, port := range []Port{split.Background(), split.Interactive(), shared.Background()} {
		if _, _, err := port.Complete(t.Context(), "", "x"); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"gpt-6.1-sol/high", "gpt-6-luna/medium", "gpt-6-luna/medium"}; fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("models called = %v, want %v", seen, want)
	}
	if split.CardsModel() != "gpt-6.1-sol" || shared.CardsModel() != "gpt-6-luna" {
		t.Fatalf("CardsModel() = %q, %q", split.CardsModel(), shared.CardsModel())
	}
	// A cards model without its own effort takes the shared effort.
	if model, effort := (Config{Model: "gpt-6-luna", Effort: "medium", CardsModel: "gpt-6.1-sol"}).Cards(); model != "gpt-6.1-sol" || effort != "medium" {
		t.Fatalf("Cards() = %q, %q; want the cards model with the shared effort", model, effort)
	}
	// specs/2026-11-06-writing-agent.md WA5.11: the agent writes with its own model, at high effort unless its own is set;
	// the writing ports wait as a person waits.
	if model, effort := (Config{Model: "gpt-6-luna", Effort: "medium", CardsModel: "gpt-6.1-sol", WritingModel: "gpt-6.1-terra"}).Prose(); model != "gpt-6.1-terra" || effort != "high" {
		t.Fatalf("Prose() = %q, %q; want the writing model at high effort", model, effort)
	}
	if model, effort := (Config{Model: "gpt-6-luna", WritingModel: "gpt-6.1-terra", WritingEffort: "medium"}).Prose(); model != "gpt-6.1-terra" || effort != "medium" {
		t.Fatalf("Prose() = %q, %q; want the writing model at its effort", model, effort)
	}
	if split.Prose().class != Interactive || split.Proofing().class != Interactive {
		t.Fatalf("the writing ports wait as %d and %d, want the class of a person waiting", split.Prose().class, split.Proofing().class)
	}
}

// specs/2026-10-29-saga-illustrations.md R13.2: the image model is ImageModel or the one of the SDK, the quality
// ImageQuality or medium.
func TestImagesConfigDefaults(t *testing.T) {
	if model, quality := (Config{Model: "gpt-6-luna"}).Images(); model != sdkimages.DefaultModel || quality != "medium" {
		t.Fatalf("Images() defaults = %q, %q", model, quality)
	}
	if model, quality := (Config{ImageModel: "m", ImageQuality: "high"}).Images(); model != "m" || quality != "high" {
		t.Fatalf("Images() = %q, %q", model, quality)
	}
}
