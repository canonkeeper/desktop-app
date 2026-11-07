// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	psnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// appServer is a minimal JSON-RPC client of `codex-app-server --listen stdio://`.
type appServer struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	mu      sync.Mutex
	nextID  int
	pending map[int]chan rpcMessage
	notify  func(rpcMessage)
	request func(rpcMessage) (any, *rpcError)
	exited  chan error
	stderr  *bytes.Buffer
}

type rpcMessage struct {
	ID     *json.RawMessage `json:"id,omitempty"`
	Method string           `json:"method,omitempty"`
	Params json.RawMessage  `json:"params,omitempty"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *appServer) send(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.stdin.Write(append(data, '\n'))
	return err
}

func (s *appServer) call(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	reply := make(chan rpcMessage, 1)
	s.pending[id] = reply
	s.mu.Unlock()
	if err := s.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case message := <-reply:
		if message.Error != nil {
			return nil, fmt.Errorf("%s: %d %s", method, message.Error.Code, message.Error.Message)
		}
		return message.Result, nil
	case err := <-s.exited:
		return nil, fmt.Errorf("%s: app-server exited: %v: %s", method, err, lastLines(s.stderr.String(), 15))
	case <-time.After(timeout):
		return nil, fmt.Errorf("%s: no reply in %s", method, timeout)
	}
}

func (s *appServer) read(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		var message rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			continue
		}
		switch {
		case message.ID != nil && message.Method != "":
			go func(message rpcMessage) {
				result, failure := s.request(message)
				reply := map[string]any{"id": message.ID}
				if failure != nil {
					reply["error"] = failure
				} else {
					reply["result"] = result
				}
				_ = s.send(reply)
			}(message)
		case message.ID != nil:
			var id int
			if json.Unmarshal(*message.ID, &id) == nil {
				s.mu.Lock()
				reply := s.pending[id]
				delete(s.pending, id)
				s.mu.Unlock()
				if reply != nil {
					reply <- message
				}
			}
		default:
			s.notify(message)
		}
	}
}

// codexEnvironment is the allowlisted environment of the app-server: its homes, a temporary
// directory, a minimal PATH and the gateway token, nothing of the parent process.
func codexEnvironment(home, sqlite, private, tmp, token string) []string {
	common := []string{
		"CODEX_HOME=" + home,
		"CODEX_SQLITE_HOME=" + sqlite,
		"CANONKEEPER_GATEWAY_TOKEN=" + token,
		"NO_PROXY=127.0.0.1,localhost,::1",
		"no_proxy=127.0.0.1,localhost,::1",
	}
	if runtime.GOOS == "windows" {
		root := os.Getenv("SystemRoot")
		return append(common,
			"USERPROFILE="+private, "HOME="+private, "APPDATA="+filepath.Join(private, "AppData", "Roaming"),
			"LOCALAPPDATA="+filepath.Join(private, "AppData", "Local"), "TEMP="+tmp, "TMP="+tmp,
			"SystemRoot="+root, "windir="+root, "PATH="+filepath.Join(root, "System32")+";"+root,
			"PATHEXT=.COM;.EXE;.BAT;.CMD", "ComSpec="+filepath.Join(root, "System32", "cmd.exe"))
	}
	lang := "C.UTF-8"
	if runtime.GOOS == "darwin" {
		lang = "en_US.UTF-8"
	}
	return append(common, "HOME="+private, "TMPDIR="+tmp, "PATH=/usr/bin:/bin", "LANG="+lang, "LC_ALL="+lang, "TZ=UTC")
}

type turnOutcome struct {
	ThreadID      string         `json:"thread_id"`
	TurnID        string         `json:"turn_id"`
	Status        string         `json:"status"`
	Error         string         `json:"error,omitempty"`
	Seconds       float64        `json:"seconds"`
	ToolCalls     []string       `json:"tool_calls"`
	ToolsInFlight int            `json:"max_tool_calls_in_flight"`
	Answer        string         `json:"answer"`
	Notifications map[string]int `json:"notifications"`
	Warnings      []string       `json:"warnings,omitempty"`
}

func runCodex(args []string) error {
	flags := flag.NewFlagSet("codex", flag.ExitOnError)
	codexPath := flags.String("codex", "", "codex-app-server binary")
	profile := flags.String("home", "", "profile directory (config.toml, model-catalog.json, AGENTS.md)")
	root := flags.String("root", "", "data root (default: the product data directory with a Cyrillic segment)")
	label := flags.String("label", "codex", "name of this run in the data root")
	out := flags.String("out", "-", "result JSON")
	flags.Parse(args)
	if *codexPath == "" || *profile == "" {
		return fmt.Errorf("-codex and -home are required")
	}
	if *root == "" {
		value, err := productDataRoot()
		if err != nil {
			return err
		}
		*root = value
	}
	result := map[string]any{"system": systemInfo(), "codex": *codexPath}
	err := codexRun(*codexPath, *profile, filepath.Join(*root, *label), result)
	if err != nil {
		result["error"] = err.Error()
	}
	if writeErr := writeJSON(*out, result); writeErr != nil {
		return writeErr
	}
	return err
}

func codexRun(codexPath, profile, dir string, result map[string]any) error {
	codexPath, err := filepath.Abs(codexPath)
	if err != nil {
		return err
	}
	if info, err := os.Stat(codexPath); err == nil {
		result["codex_bytes"] = info.Size()
	}
	_ = os.RemoveAll(dir)
	home := filepath.Join(dir, "codex", "home")
	sqlite := filepath.Join(dir, "codex", "sqlite")
	private := filepath.Join(dir, "codex", "private")
	tmp := filepath.Join(dir, "codex", "tmp")
	work := filepath.Join(dir, "codex", "work")
	for _, directory := range []string{home, sqlite, private, tmp, work} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
	}
	for _, name := range []string{"config.toml", "model-catalog.json", "AGENTS.md"} {
		data, err := os.ReadFile(filepath.Join(profile, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(home, name), data, 0o600); err != nil {
			return err
		}
	}

	stub, stubAddr, stopStub, err := startStub("127.0.0.1:0")
	if err != nil {
		return err
	}
	defer stopStub()
	gw, gatewayAddr, stopGateway, err := startGateway(stubAddr)
	if err != nil {
		return err
	}
	defer stopGateway()
	defer func() {
		result["gateway_requests"] = gw.log()
		result["stub_requests"] = stub.log()
	}()
	result["gateway_rejects"] = gatewayRejects(gatewayAddr, gw.token)

	window := 128000
	overrides := []string{
		`model="` + chatAlias + `"`,
		`model_provider="canonkeeper"`,
		`model_providers.canonkeeper.name="CanonKeeper"`,
		`model_providers.canonkeeper.base_url="http://` + gatewayAddr + `/v1"`,
		`model_providers.canonkeeper.wire_api="responses"`,
		`model_providers.canonkeeper.env_key="CANONKEEPER_GATEWAY_TOKEN"`,
		`model_providers.canonkeeper.requires_openai_auth=false`,
		`model_providers.canonkeeper.supports_websockets=false`,
		`model_providers.canonkeeper.stream_max_retries=10`,
		fmt.Sprintf("model_context_window=%d", window),
		fmt.Sprintf("model_auto_compact_token_limit=%d", window*9/10),
		fmt.Sprintf("tool_output_token_limit=%d", min(400000, window/4)),
		`model_reasoning_effort="medium"`,
	}
	args := []string{}
	for _, override := range overrides {
		args = append(args, "-c", override)
	}
	args = append(args, "--listen", "stdio://")
	result["overrides"] = overrides

	command := exec.Command(codexPath, args...)
	command.Dir = work
	command.Env = codexEnvironment(home, sqlite, private, tmp, gw.token)
	names := []string{}
	for _, entry := range command.Env {
		names = append(names, strings.SplitN(entry, "=", 2)[0])
	}
	result["environment"] = names
	stderr := &bytes.Buffer{}
	command.Stderr = &lockedWriter{buffer: stderr}
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	server := &appServer{stdin: stdin, pending: map[int]chan rpcMessage{}, exited: make(chan error, 1), stderr: stderr, command: command}

	var turnMu sync.Mutex
	var current *turnOutcome
	turnDone := make(chan struct{}, 1)
	inFlight := 0
	server.notify = func(message rpcMessage) {
		turnMu.Lock()
		defer turnMu.Unlock()
		if current == nil {
			return
		}
		current.Notifications[message.Method]++
		switch message.Method {
		case "warning", "error", "configWarning":
			text := string(message.Params)
			if len(text) > 400 {
				text = text[:400] + "…"
			}
			current.Warnings = append(current.Warnings, message.Method+": "+text)
		case "item/completed":
			var params struct {
				Item struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"item"`
			}
			if json.Unmarshal(message.Params, &params) == nil && params.Item.Type == "agentMessage" {
				current.Answer += params.Item.Text
			}
		case "turn/completed":
			var params struct {
				Turn struct {
					ID     string          `json:"id"`
					Status string          `json:"status"`
					Error  json.RawMessage `json:"error"`
				} `json:"turn"`
			}
			if json.Unmarshal(message.Params, &params) == nil {
				current.TurnID = params.Turn.ID
				current.Status = params.Turn.Status
				if len(params.Turn.Error) > 0 && string(params.Turn.Error) != "null" {
					current.Error = string(params.Turn.Error)
				}
			}
			select {
			case turnDone <- struct{}{}:
			default:
			}
		}
	}
	server.request = func(message rpcMessage) (any, *rpcError) {
		if message.Method != "item/tool/call" {
			turnMu.Lock()
			if current != nil {
				current.Notifications["request:"+message.Method]++
			}
			turnMu.Unlock()
			return nil, &rpcError{Code: -32601, Message: "the probe does not serve " + message.Method}
		}
		var params struct {
			Namespace *string         `json:"namespace"`
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(message.Params, &params)
		name := params.Tool
		if params.Namespace != nil {
			name = *params.Namespace + "." + name
		}
		turnMu.Lock()
		inFlight++
		if current != nil {
			current.ToolCalls = append(current.ToolCalls, name+" "+string(params.Arguments))
			if inFlight > current.ToolsInFlight {
				current.ToolsInFlight = inFlight
			}
		}
		turnMu.Unlock()
		time.Sleep(400 * time.Millisecond)
		turnMu.Lock()
		inFlight--
		turnMu.Unlock()
		return map[string]any{"success": true, "contentItems": []any{map[string]any{
			"type": "inputText", "text": "[1] Война и мир, том 1, глава 2, ¶4: «Пьер был неуклюж…»",
		}}}, nil
	}

	started := time.Now()
	if err := command.Start(); err != nil {
		return err
	}
	pid := int32(command.Process.Pid)
	go server.read(stdout)
	go func() { server.exited <- command.Wait() }()
	stopSampling := sampleConnections(pid, result)
	defer stopSampling()
	defer func() {
		result["stderr_tail"] = lastLines(stderr.String(), 30)
		result["stderr_mentions"] = mentions(stderr.String())
	}()

	initResult, err := server.call("initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "canonkeeper_desktop_probe", "title": "CanonKeeper desktop probe", "version": "0.0.1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}, 60*time.Second)
	if err != nil {
		return err
	}
	result["initialize_seconds"] = seconds(time.Since(started))
	result["initialize_result"] = json.RawMessage(initResult)
	if err := server.send(map[string]any{"method": "initialized"}); err != nil {
		return err
	}

	startThread := func() (string, error) {
		raw, err := server.call("thread/start", map[string]any{
			"cwd": work,
			"dynamicTools": []any{map[string]any{
				"type": "namespace", "name": "saga", "description": "Чтение саги автора.",
				"tools": []any{map[string]any{
					"type": "function", "name": "search", "description": "Найти места саги по запросу.",
					"inputSchema": map[string]any{
						"type": "object", "additionalProperties": false, "required": []string{"query"},
						"properties": map[string]any{"query": map[string]any{"type": "string"}},
					},
				}},
			}},
		}, 60*time.Second)
		if err != nil {
			return "", err
		}
		var response struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if err := json.Unmarshal(raw, &response); err != nil || response.Thread.ID == "" {
			return "", fmt.Errorf("thread/start: no thread id in %s", raw)
		}
		return response.Thread.ID, nil
	}
	runTurn := func(threadID, text string) (turnOutcome, error) {
		outcome := turnOutcome{ThreadID: threadID, Notifications: map[string]int{}}
		turnMu.Lock()
		current = &outcome
		turnMu.Unlock()
		defer func() {
			turnMu.Lock()
			current = nil
			turnMu.Unlock()
		}()
		turnStarted := time.Now()
		if _, err := server.call("turn/start", map[string]any{
			"threadId": threadID,
			"input":    []any{map[string]any{"type": "text", "text": text, "text_elements": []any{}}},
		}, 60*time.Second); err != nil {
			return outcome, err
		}
		select {
		case <-turnDone:
		case err := <-server.exited:
			return outcome, fmt.Errorf("app-server exited during the turn: %v", err)
		case <-time.After(3 * time.Minute):
			return outcome, fmt.Errorf("turn did not complete in 3 minutes")
		}
		turnMu.Lock()
		defer turnMu.Unlock()
		outcome.Seconds = seconds(time.Since(turnStarted))
		return outcome, nil
	}

	threadA, err := startThread()
	if err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	result["idle_memory_before_turn"] = processTreeMemory(pid, nil)
	turns := []turnOutcome{}
	defer func() { result["turns"] = turns }()
	for _, step := range []struct{ thread, text string }{
		{threadA, "Кто такой Пьер Безухов и кто такая Наташа Ростова?"},
		{threadA, "Спасибо."},
	} {
		outcome, err := runTurn(step.thread, step.text)
		turns = append(turns, outcome)
		if err != nil {
			return err
		}
	}
	threadB, err := startThread()
	if err != nil {
		return err
	}
	outcome, err := runTurn(threadB, "Кто такой Пьер Безухов?")
	turns = append(turns, outcome)
	if err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	result["idle_memory_after_turns"] = processTreeMemory(pid, nil)
	result["thread_header"] = threadHeaderFacts(gw.log(), []string{threadA, threadB})

	stopStarted := time.Now()
	_ = stdin.Close()
	select {
	case err := <-server.exited:
		result["shutdown_seconds"] = seconds(time.Since(stopStarted))
		if err != nil {
			result["exit"] = err.Error()
		}
	case <-time.After(15 * time.Second):
		_ = command.Process.Kill()
		result["shutdown"] = "killed after 15 s without exit on closed stdin"
	}
	if size, err := dirSize(filepath.Join(dir, "codex")); err == nil {
		result["codex_dirs_bytes"] = size
	}
	return nil
}

type lockedWriter struct {
	mu     sync.Mutex
	buffer *bytes.Buffer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(p)
}

var mentionPattern = regexp.MustCompile(`(?i)bwrap|bubblewrap|sandbox|update|telemetry|otel|analytics|statsig|sentry|chatgpt\.com|openai\.com|https?://[^\s"']+`)

func mentions(text string) []string {
	found := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		if mentionPattern.MatchString(line) {
			if len(line) > 240 {
				line = line[:240] + "…"
			}
			found[line] = true
		}
	}
	lines := make([]string, 0, len(found))
	for line := range found {
		lines = append(lines, line)
	}
	sort.Strings(lines)
	if len(lines) > 40 {
		lines = lines[:40]
	}
	return lines
}

// threadHeaderFacts finds the request headers and body fields whose value is the thread id of
// the request: the gateway can map a request of Codex to the chat turn that made it (DA5.7).
func threadHeaderFacts(requests []map[string]any, threads []string) map[string]any {
	carriers := map[string]int{}
	responses := 0
	for _, request := range requests {
		if request["path"] != "/v1/responses" {
			continue
		}
		responses++
		headers, _ := request["headers"].(map[string]string)
		for name, value := range headers {
			for _, thread := range threads {
				if strings.Contains(value, thread) {
					carriers["header:"+name]++
				}
			}
		}
		if body, ok := request["body"].(map[string]any); ok {
			for key, value := range body {
				text, _ := value.(string)
				for _, thread := range threads {
					if strings.Contains(text, thread) {
						carriers["body:"+key]++
					}
				}
			}
		}
	}
	always := []string{}
	for name, count := range carriers {
		if count == responses {
			always = append(always, name)
		}
	}
	sort.Strings(always)
	return map[string]any{"responses_requests": responses, "carriers": carriers, "in_every_request": always, "threads": threads}
}

// gatewayRejects checks the gateway boundary of DA3.5 with plain requests.
func gatewayRejects(addr, token string) map[string]any {
	client := &httpClient{}
	return map[string]any{
		"no_token":     client.status("http://"+addr+"/v1/models", addr, "", ""),
		"wrong_host":   client.status("http://"+addr+"/v1/models", "localhost", token, ""),
		"with_origin":  client.status("http://"+addr+"/v1/models", addr, token, "http://127.0.0.1"),
		"valid":        client.status("http://"+addr+"/v1/models", addr, token, ""),
		"expect_codes": "401 421 403 200",
	}
}

// sampleConnections lists the sockets of the app-server process tree every 250 ms until stopped
// and records every remote endpoint that is not loopback.
func sampleConnections(pid int32, result map[string]any) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	remotes := map[string]int{}
	samples := 0
	failures := map[string]int{}
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			pids := []int32{pid}
			if root, err := process.NewProcess(pid); err == nil {
				if children, err := root.Children(); err == nil {
					for _, child := range children {
						pids = append(pids, child.Pid)
					}
				}
			}
			for _, p := range pids {
				connections, err := psnet.ConnectionsPid("all", p)
				if err != nil {
					failures[err.Error()]++
					continue
				}
				samples++
				for _, connection := range connections {
					ip := connection.Raddr.IP
					if ip == "" || ip == "127.0.0.1" || ip == "::1" || ip == "0.0.0.0" || ip == "::" {
						continue
					}
					remotes[fmt.Sprintf("%s:%d %s", ip, connection.Raddr.Port, connection.Status)]++
				}
			}
		}
	}()
	return func() {
		close(stop)
		<-done
		result["connection_samples"] = samples
		result["non_loopback_connections"] = remotes
		if len(failures) > 0 {
			result["connection_sample_errors"] = failures
		}
	}
}
