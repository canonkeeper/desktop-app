package modelapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// specs/2026-11-03-voice-assistant.md V4.3: a turn of the assistant with tools — the model calls a function, the
// caller runs it, the next round carries the call, its reasoning and its output back, and the turn ends with the text.
func TestActRunsTheToolsOfATurn(t *testing.T) {
	t.Setenv("MODEL_TEST_KEY", "secret-key")
	var mu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		mu.Lock()
		bodies = append(bodies, body)
		round := len(bodies)
		mu.Unlock()
		if round == 1 {
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"id\":\"rs_1\",\"encrypted_content\":\"x\"}}\n\n")
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"write_scene\",\"arguments\":\"{\\\"scene\\\":1}\"}}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"Записал в сцену 1.\"}]}}\n\n")
		}
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":3}}}\n\n")
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, KeyEnv: "MODEL_TEST_KEY", Model: "gpt-6-luna", VoiceBrain: "gpt-6.1-sol", VoiceBrainEffort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	var ran []string
	answer, usage, err := client.Voice().Act(context.Background(), "Ты ассистент.", "Запиши.", []Function{
		{Name: "write_scene", Description: "Записать сцену.", Parameters: map[string]any{"type": "object"}},
	}, func(_ context.Context, callID, name, arguments string) (Output, error) {
		ran = append(ran, callID+" "+name+" "+arguments)
		return Output{Text: "Записал.", Images: []Image{{MediaType: "image/jpeg", Data: []byte("jpg")}}}, nil
	})
	if err != nil || answer != "Записал в сцену 1." || usage.Calls != 2 || strings.Join(ran, ";") != `call_1 write_scene {"scene":1}` {
		t.Fatalf("Act = %q %+v %v, ran %v", answer, usage, err, ran)
	}
	if bodies[0]["model"] != "gpt-6.1-sol" || bodies[0]["reasoning"].(map[string]any)["effort"] != "low" || bodies[0]["store"] != false {
		t.Fatalf("first round = %v", bodies[0])
	}
	second, _ := json.Marshal(bodies[1]["input"])
	for _, want := range []string{`"encrypted_content":"x"`, `"call_id":"call_1","name":"write_scene","type":"function_call"`,
		`"call_id":"call_1","output":"Записал.","type":"function_call_output"`,
		// The image of the function comes after its output, as the user's, for the model to see (V5.3).
		`"image_url":"data:image/jpeg;base64,anBn","type":"input_image"`} {
		if !strings.Contains(string(second), want) {
			t.Fatalf("second round input %s lacks %s", second, want)
		}
	}
}
