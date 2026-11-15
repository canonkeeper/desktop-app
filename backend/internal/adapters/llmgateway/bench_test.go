// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/aisettings"
)

// BenchmarkAThreadOfEightMegabytes is the time the gateway takes to put the profile into a request of Codex as long as an
// average thread of the site (≈ 8 MB, docs/diagnostics/desktop-2026-11-12/06) and to translate it to Chat Completions,
// the provider answering at once (specs/2026-11-13-desk-gateway.md, Open questions).
func BenchmarkAThreadOfEightMegabytes(b *testing.B) {
	held := &heldSettings{}
	gateway, err := Open(held, log.New(io.Discard, "", 0))
	if err != nil {
		b.Fatal(err)
	}
	defer gateway.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	settings, err := aisettings.Draft{Source: "own", Own: &aisettings.OwnDraft{
		Preset: "custom", BaseURL: text(provider.URL + "/v1"), Protocol: text("chat"), ChatModel: text("north-chat"), CardsModel: text("north-cards"),
	}}.Settings()
	if err != nil {
		b.Fatal(err)
	}
	held.settings = settings
	var items []string
	paragraph := strings.Repeat("Дозорные спустились к перевалу на рассвете и нашли следы чужого отряда. ", 40)
	for len(strings.Join(items, ",")) < 8<<20 {
		text, _ := json.Marshal(paragraph)
		items = append(items, `{"type":"message","role":"user","content":[{"type":"input_text","text":`+string(text)+`}]}`,
			`{"type":"message","role":"assistant","content":[{"type":"output_text","text":`+string(text)+`}]}`)
	}
	body := `{"model":"canonkeeper-chat","instructions":"Codex base","stream":true,"store":false,"input":[` + strings.Join(items, ",") + `]}`
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for range b.N {
		request, _ := http.NewRequest(http.MethodPost, gateway.BaseURL()+"/responses", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+gateway.Token())
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			b.Fatalf("status %d", response.StatusCode)
		}
	}
}

// BenchmarkTheRewriteOfEightMegabytes is the share of the gateway's own pass over such a body: the fields of the client
// taken off and the profile of the author put in.
func BenchmarkTheRewriteOfEightMegabytes(b *testing.B) {
	paragraph, _ := json.Marshal(strings.Repeat("Дозорные спустились к перевалу на рассвете и нашли следы чужого отряда. ", 40))
	var items []string
	for len(strings.Join(items, ",")) < 8<<20 {
		items = append(items, `{"type":"message","role":"user","content":[{"type":"input_text","text":`+string(paragraph)+`}]}`)
	}
	body := []byte(`{"model":"canonkeeper-chat","instructions":"Codex base","stream":true,"input":[` + strings.Join(items, ",") + `]}`)
	own := aisettings.Own{Advanced: aisettings.Advanced{Reasoning: aisettings.ReasoningMedium, MaxOutput: 16_000}}
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for range b.N {
		if _, err := rewrite(body, own); err != nil {
			b.Fatal(err)
		}
	}
}
