package modelapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// specs/2026-10-29-saga-illustrations.md R12.2, R13.11: the drawing check asks the check model
// (not the image model) with medium effort and no tools: the text first, then the render as a data
// URL; the streamed text and the spend come back.
var jpegHeader = []byte("\xff\xd8\xff\xe0JFIF")

func TestLookSendsTheTextAndTheImageWithoutTools(t *testing.T) {
	var body struct {
		Model        string         `json:"model"`
		Instructions string         `json:"instructions"`
		Stream       bool           `json:"stream"`
		Store        bool           `json:"store"`
		Reasoning    map[string]any `json:"reasoning"`
		Tools        []any          `json:"tools"`
		Input        []struct {
			Role    string `json:"role"`
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL string `json:"image_url"`
			} `json:"content"`
		} `json:"input"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/responses" || json.Unmarshal(data, &body) != nil {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"{\"facts\":"}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"[]}"}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"usage":{"input_tokens":1700,"output_tokens":120,"output_tokens_details":{"reasoning_tokens":60}}}}`+"\n\n")
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Model: "gpt-6-luna", ImageModel: "gpt-image-2", CheckModel: "gpt-6.1-sol", Parallel: 1})
	if err != nil {
		t.Fatal(err)
	}
	text, usage, err := client.Vision().Look(t.Context(), "Проверь рисунок.", "Факты: 1. шрам", jpegHeader)
	if err != nil || text != `{"facts":[]}` || usage.Calls != 1 || usage.InputTokens != 1700 || usage.ReasoningOutputTokens != 60 {
		t.Fatalf("Look() = %q, %+v, %v", text, usage, err)
	}
	if client.CheckModel() != "gpt-6.1-sol" || body.Model != "gpt-6.1-sol" || body.Instructions != "Проверь рисунок." || !body.Stream || body.Store ||
		body.Reasoning["effort"] != "medium" || len(body.Tools) != 0 || len(body.Input) != 1 || len(body.Input[0].Content) != 2 {
		t.Fatalf("body = %+v", body)
	}
	content := body.Input[0].Content
	if content[0].Type != "input_text" || content[0].Text != "Факты: 1. шрам" || content[1].Type != "input_image" ||
		!strings.HasPrefix(content[1].ImageURL, "data:image/jpeg;base64,") {
		t.Fatalf("content = %+v", content)
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "busy", http.StatusBadGateway)
	}))
	defer failing.Close()
	broken, _ := New(Config{BaseURL: failing.URL, Model: "m", Parallel: 1})
	if _, _, err := broken.Vision().Look(t.Context(), "i", "p", jpegHeader); err == nil {
		t.Fatal("a failed call is not an error")
	}
}

// R13.11: without its own setting the check model is the model of the process.
func TestCheckModelDefaultsToTheModel(t *testing.T) {
	if model := (Config{Model: "gpt-6-luna", ImageModel: "gpt-image-2"}).Check(); model != "gpt-6-luna" {
		t.Fatalf("Check() = %q", model)
	}
	if model := (Config{Model: "gpt-6-luna", CheckModel: "gpt-6.1-sol"}).Check(); model != "gpt-6.1-sol" {
		t.Fatalf("Check() = %q", model)
	}
}
