// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"
)

// chatStub is a scripted OpenAI Chat Completions provider. A request whose conversation ends
// with tool results gets a final answer; a request that offers a search tool gets two parallel
// calls of it; any other request gets a short answer. Everything streams with usage.
type chatStub struct {
	mu       sync.Mutex
	requests []map[string]any
	calls    int
}

func (s *chatStub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.chat)
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		s.record(map[string]any{"path": r.URL.Path, "method": r.Method})
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"stub-chat","object":"model"}]}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.record(map[string]any{"path": r.URL.Path, "method": r.Method, "status": 404})
		http.NotFound(w, r)
	})
	return mux
}

func (s *chatStub) record(entry map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry["at"] = time.Now().UTC().Format(time.RFC3339Nano)
	s.requests = append(s.requests, entry)
}

func (s *chatStub) log() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.requests...)
}

type stubMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  json.RawMessage `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
}

type stubRequest struct {
	Model           string          `json:"model"`
	Stream          bool            `json:"stream"`
	Messages        []stubMessage   `json:"messages"`
	Tools           []stubTool      `json:"tools"`
	ToolChoice      json.RawMessage `json:"tool_choice"`
	StreamOptions   json.RawMessage `json:"stream_options"`
	ReasoningEffort string          `json:"reasoning_effort"`
	MaxTokens       *int            `json:"max_tokens"`
	MaxCompletion   *int            `json:"max_completion_tokens"`
	ParallelTools   *bool           `json:"parallel_tool_calls"`
}

type stubTool struct {
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

func (s *chatStub) chat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var request stubRequest
	if err := json.Unmarshal(body, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	roles := []string{}
	firstSystemChars := 0
	systemMessages := 0
	for index, message := range request.Messages {
		roles = append(roles, message.Role)
		if message.Role == "system" || message.Role == "developer" {
			systemMessages++
			if index == 0 {
				var text string
				if json.Unmarshal(message.Content, &text) == nil {
					firstSystemChars = len([]rune(text))
				}
			}
		}
	}
	tools := []string{}
	searchTool := ""
	for _, tool := range request.Tools {
		tools = append(tools, tool.Function.Name)
		if searchTool == "" && strings.HasSuffix(tool.Function.Name, "search") {
			searchTool = tool.Function.Name
		}
	}
	last := ""
	if len(request.Messages) > 0 {
		last = request.Messages[len(request.Messages)-1].Role
	}
	reply := "text"
	switch {
	case last == "tool":
		reply = "final_answer"
	case searchTool != "" && last == "user":
		reply = "tool_calls"
	}
	s.record(map[string]any{
		"path": r.URL.Path, "model": request.Model, "stream": request.Stream, "roles": roles,
		"system_messages": systemMessages, "first_system_chars": firstSystemChars, "tools": tools,
		"tool_choice": string(request.ToolChoice), "stream_options": string(request.StreamOptions),
		"reasoning_effort": request.ReasoningEffort, "max_tokens": request.MaxTokens,
		"max_completion_tokens": request.MaxCompletion, "parallel_tool_calls": request.ParallelTools,
		"reply": reply, "user_agent": r.Header.Get("User-Agent"),
	})

	// Call ids are unique across the conversation, as real providers make them: the translation
	// drops a replayed call whose id it has already seen (a provider reusing ids loops the turn).
	s.mu.Lock()
	s.calls += 2
	first, second := fmt.Sprintf("call_stub_%d", s.calls-1), fmt.Sprintf("call_stub_%d", s.calls)
	s.mu.Unlock()
	chunks := []map[string]any{}
	delta := func(d map[string]any, finish any) map[string]any {
		return map[string]any{"id": "chatcmpl-stub", "object": "chat.completion.chunk", "created": time.Now().Unix(),
			"model": request.Model, "choices": []any{map[string]any{"index": 0, "delta": d, "finish_reason": finish}}}
	}
	switch reply {
	case "tool_calls":
		chunks = append(chunks, delta(map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
			map[string]any{"index": 0, "id": first, "type": "function", "function": map[string]any{"name": searchTool, "arguments": ""}},
		}}, nil))
		chunks = append(chunks, delta(map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "function": map[string]any{"arguments": `{"query":"Пьер Безухов"}`}},
		}}, nil))
		chunks = append(chunks, delta(map[string]any{"tool_calls": []any{
			map[string]any{"index": 1, "id": second, "type": "function", "function": map[string]any{"name": searchTool, "arguments": `{"query":"Наташа Ростова"}`}},
		}}, nil))
		chunks = append(chunks, delta(map[string]any{}, "tool_calls"))
	case "final_answer":
		for _, part := range []string{"Пьер Безухов — ", "незаконный сын графа Безухова [источник 1]; ", "Наташа Ростова — младшая дочь Ростовых [источник 2]."} {
			chunks = append(chunks, delta(map[string]any{"content": part}, nil))
		}
		chunks = append(chunks, delta(map[string]any{}, "stop"))
	default:
		chunks = append(chunks, delta(map[string]any{"role": "assistant", "content": "Пробный ответ заглушки."}, nil))
		chunks = append(chunks, delta(map[string]any{}, "stop"))
	}
	usage := map[string]any{"id": "chatcmpl-stub", "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": request.Model,
		"choices": []any{}, "usage": map[string]any{"prompt_tokens": len(body) / 4, "completion_tokens": 40, "total_tokens": len(body)/4 + 40}}
	chunks = append(chunks, usage)

	if !request.Stream {
		http.Error(w, "the stub streams only", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	for _, chunk := range chunks {
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func startStub(addr string) (*chatStub, string, func(), error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", nil, err
	}
	stub := &chatStub{}
	server := &http.Server{Handler: stub.handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return stub, listener.Addr().String(), func() { _ = server.Close() }, nil
}

func runStubAlone(args []string) error {
	flags := flag.NewFlagSet("stub", flag.ExitOnError)
	addr := flags.String("addr", "127.0.0.1:0", "listen address")
	out := flags.String("out", "-", "request log JSON, written on interrupt")
	flags.Parse(args)
	stub, listen, stop, err := startStub(*addr)
	if err != nil {
		return err
	}
	fmt.Println("stub listening on", listen)
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	stop()
	return writeJSON(*out, stub.log())
}
