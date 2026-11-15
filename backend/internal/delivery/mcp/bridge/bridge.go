// SPDX-License-Identifier: AGPL-3.0-only

// Package bridge is the program of the bridge for the programs of the author (specs/2026-11-15-desk-mcp.md MC1): the MCP over
// stdio on the standard library. initialize, ping and tools/list it answers from its own code; a call it takes to the door of
// the app by one line over the local socket mcp.json names. It touches no cluster, no settings, no log of the run and no
// channel of the shell, opens no network, makes no file and reads no environment.
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"canonkeeper/backend/internal/delivery/mcp/programtools"
)

// Versions are the versions of the protocol the bridge speaks; a client that names another gets the last.
var Versions = []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"}

// The bounds of the bridge: a line of a message, the silence after the start, the answer of a call and the first word of a
// program.
const (
	MaxLine      = 1 << 20
	Silence      = 90 * time.Second
	CallTerm     = 120 * time.Second
	HelloTerm    = 2 * time.Second
	answerLimit  = 4 << 20
	parseError   = -32700
	invalidReq   = -32600
	noMethod     = -32601
	invalidParam = -32602
)

// Config is a run of the bridge: the data directory of the app it reads mcp.json in, its kind and version, its silence.
type Config struct {
	DataDir string
	Kind    string
	Version string
	Silence time.Duration
	// Dial makes the connection to the door: a local socket of the file system and nothing else (MC1.2). The tests watch it.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// Arguments are the arguments of the program: the absolute path of the data directory and, only in the copy inside the
// extension of Claude Desktop, the word extension (MC1.1).
func Arguments(arguments []string) (dataDir, kind string, ok bool) {
	switch {
	case len(arguments) == 1 && filepath.IsAbs(arguments[0]):
		return arguments[0], programtools.KindInstall, true
	case len(arguments) == 2 && filepath.IsAbs(arguments[0]) && arguments[1] == programtools.KindExtension:
		return arguments[0], programtools.KindExtension, true
	default:
		return "", "", false
	}
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// session is one run of the bridge with one program.
type session struct {
	config  Config
	out     io.Writer
	writeMu sync.Mutex
	failed  chan struct{}
	failure sync.Once

	mu          sync.Mutex
	initialized bool
	client      string
	title       string
	calls       map[string]context.CancelFunc
	ctx         context.Context
}

// Run serves the program on in and out until in closes, a write to out fails or the program says nothing within the silence
// after the start; it returns the code of the exit, 0.
func Run(ctx context.Context, in io.Reader, out io.Writer, config Config) int {
	if config.Silence <= 0 {
		config.Silence = Silence
	}
	if config.Dial == nil {
		dialer := &net.Dialer{}
		config.Dial = dialer.DialContext
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	current := &session{config: config, out: out, failed: make(chan struct{}), calls: map[string]context.CancelFunc{}, ctx: ctx}
	lines := make(chan []byte)
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReaderSize(in, 64<<10)
		for {
			line, tooLong, err := readLine(reader)
			if tooLong {
				lines <- nil
				continue
			}
			if len(bytes.TrimSpace(line)) > 0 {
				select {
				case lines <- line:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	silence := time.NewTimer(config.Silence)
	defer silence.Stop()
	for {
		select {
		case line := <-lines:
			silence.Stop()
			if line == nil {
				current.write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: invalidReq, Message: "the line is longer than 1 MiB"}})
				continue
			}
			current.handleLine(line)
		case <-done:
			return 0
		case <-silence.C:
			return 0
		case <-current.failed:
			return 0
		}
	}
}

// readLine reads a line; a line longer than MaxLine is read to its end and given as too long.
func readLine(reader *bufio.Reader) ([]byte, bool, error) {
	var line []byte
	tooLong := false
	for {
		chunk, err := reader.ReadSlice('\n')
		if !tooLong {
			line = append(line, chunk...)
			if len(line) > MaxLine+1 {
				tooLong, line = true, nil
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if tooLong {
			return nil, true, err
		}
		return bytes.TrimRight(line, "\r\n"), false, err
	}
}

func (current *session) write(value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	current.writeMu.Lock()
	defer current.writeMu.Unlock()
	if _, err := current.out.Write(append(encoded, '\n')); err != nil {
		current.failure.Do(func() { close(current.failed) })
	}
}

// handleLine answers one line: a message or an array of them.
func (current *session) handleLine(line []byte) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []json.RawMessage
		if json.Unmarshal(trimmed, &batch) != nil {
			current.write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: parseError, Message: "the line is no JSON"}})
			return
		}
		if len(batch) == 0 {
			current.write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: invalidReq, Message: "an empty array"}})
			return
		}
		// Every message but a call is answered in its order at once — initialize before what follows it —, the calls at once.
		answers := make([]*response, len(batch))
		var wait sync.WaitGroup
		for index, raw := range batch {
			if !isCall(raw) {
				answers[index] = current.handle(raw)
				continue
			}
			wait.Add(1)
			go func() {
				defer wait.Done()
				answers[index] = current.handle(raw)
			}()
		}
		go func() {
			wait.Wait()
			var out []*response
			for _, answer := range answers {
				if answer != nil {
					out = append(out, answer)
				}
			}
			if len(out) > 0 {
				current.write(out)
			}
		}()
		return
	}
	if !json.Valid(trimmed) {
		current.write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: parseError, Message: "the line is no JSON"}})
		return
	}
	if !isCall(trimmed) {
		if answer := current.handle(trimmed); answer != nil {
			current.write(answer)
		}
		return
	}
	go func() {
		if answer := current.handle(trimmed); answer != nil {
			current.write(answer)
		}
	}()
}

// isCall says a message is tools/call: the one request the bridge answers in its own time, the others in their order.
func isCall(raw json.RawMessage) bool {
	var request message
	return json.Unmarshal(raw, &request) == nil && request.Method == "tools/call" && len(request.ID) > 0
}

func failed(id json.RawMessage, code int, text string) *response {
	return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: text}}
}

// handle answers one message; a notification and a cancelled call have no answer.
func (current *session) handle(raw json.RawMessage) *response {
	var request message
	if err := json.Unmarshal(raw, &request); err != nil || request.Method == "" {
		return failed(json.RawMessage("null"), invalidReq, "not a request")
	}
	if len(request.ID) == 0 {
		current.notice(request)
		return nil
	}
	current.mu.Lock()
	initialized := current.initialized
	current.mu.Unlock()
	switch {
	case request.Method == "ping":
		return &response{JSONRPC: "2.0", ID: request.ID, Result: struct{}{}}
	case request.Method == "initialize":
		return current.initialize(request)
	case !initialized:
		return failed(request.ID, noMethod, "not initialized")
	case request.Method == "tools/list":
		return &response{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"tools": programtools.Tools()}}
	case request.Method == "tools/call":
		return current.call(request)
	default:
		return failed(request.ID, noMethod, "no such method")
	}
}

// notice takes a notification: notifications/cancelled ends the call it names, without an answer.
func (current *session) notice(request message) {
	if request.Method != "notifications/cancelled" {
		return
	}
	var params struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(request.Params, &params) != nil {
		return
	}
	current.mu.Lock()
	cancel := current.calls[string(params.RequestID)]
	current.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (current *session) initialize(request message) *response {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
		ClientInfo      struct {
			Name  string `json:"name"`
			Title string `json:"title"`
		} `json:"clientInfo"`
	}
	_ = json.Unmarshal(request.Params, &params)
	current.mu.Lock()
	if current.initialized {
		current.mu.Unlock()
		return failed(request.ID, invalidReq, "initialized already")
	}
	current.initialized, current.client, current.title = true, params.ClientInfo.Name, params.ClientInfo.Title
	current.mu.Unlock()
	version := Versions[len(Versions)-1]
	if slices.Contains(Versions, params.ProtocolVersion) {
		version = params.ProtocolVersion
	}
	go current.hello()
	return &response{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": "canonkeeper", "title": "CanonKeeper", "version": current.config.Version},
		"instructions":    programtools.Instructions(),
	}}
}

// hello tells the door a program turned to it (MC1.5), when the door is open; it waits at most HelloTerm and never complains.
func (current *session) hello() {
	ctx, cancel := context.WithTimeout(current.ctx, HelloTerm)
	defer cancel()
	door, err := readDoor(current.config.DataDir)
	if err != nil {
		return
	}
	_, _ = current.exchange(ctx, door, current.request(programtools.Request{Hello: true}), false)
}

func (current *session) request(request programtools.Request) programtools.Request {
	current.mu.Lock()
	defer current.mu.Unlock()
	request.Fingerprint, request.Version, request.Kind = programtools.Fingerprint(), current.config.Version, current.config.Kind
	request.Client, request.Title = current.client, current.title
	return request
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content []content `json:"content"`
	IsError bool      `json:"isError"`
}

func toolText(text string, isError bool) callResult {
	return callResult{Content: []content{{Type: "text", Text: text}}, IsError: isError}
}

// errCancelled is a call the program cancelled: no answer goes.
var errCancelled = errors.New("bridge: the call was cancelled")

func (current *session) call(request message) *response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(request.Params) == 0 || json.Unmarshal(request.Params, &params) != nil || params.Name == "" {
		return failed(request.ID, invalidParam, "tools/call needs a name")
	}
	if len(params.Arguments) == 0 || string(params.Arguments) == "null" {
		params.Arguments = json.RawMessage("{}")
	}
	ctx, cancel := context.WithTimeout(current.ctx, CallTerm)
	defer cancel()
	cancelled, cancelCall := context.WithCancel(ctx)
	defer cancelCall()
	key := string(request.ID)
	current.mu.Lock()
	current.calls[key] = cancelCall
	current.mu.Unlock()
	defer func() {
		current.mu.Lock()
		delete(current.calls, key)
		current.mu.Unlock()
	}()
	door, err := readDoor(current.config.DataDir)
	if err != nil {
		return &response{JSONRPC: "2.0", ID: request.ID, Result: toolText(programtools.WordsClosed, true)}
	}
	if door.Fingerprint != programtools.Fingerprint() {
		return &response{JSONRPC: "2.0", ID: request.ID, Result: toolText(programtools.Mismatch(door.Version, current.config.Version, current.config.Kind), true)}
	}
	answer, err := current.exchange(cancelled, door, current.request(programtools.Request{Tool: params.Name, Arguments: params.Arguments}), true)
	switch {
	case errors.Is(err, errCancelled):
		return nil
	case errors.Is(err, errNoDoor):
		return &response{JSONRPC: "2.0", ID: request.ID, Result: toolText(programtools.WordsClosed, true)}
	case err != nil:
		return &response{JSONRPC: "2.0", ID: request.ID, Result: toolText(programtools.WordsNoAnswer, true)}
	}
	return &response{JSONRPC: "2.0", ID: request.ID, Result: toolText(answer.Text, answer.Error)}
}

// errNoDoor is a socket that does not take the connection: the app is closed or the reading is off.
var errNoDoor = errors.New("bridge: the door is closed")

// exchange sends one line to the door and reads one line of its answer, unless the program cancels the call or ctx ends.
func (current *session) exchange(ctx context.Context, door programtools.DoorFile, request programtools.Request, answered bool) (programtools.Answer, error) {
	conn, err := current.config.Dial(ctx, "unix", door.Socket)
	if err != nil {
		if errors.Is(current.why(ctx, err), errCancelled) {
			return programtools.Answer{}, errCancelled
		}
		return programtools.Answer{}, errNoDoor
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	line, err := json.Marshal(request)
	if err != nil {
		return programtools.Answer{}, err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return programtools.Answer{}, current.why(ctx, err)
	}
	if !answered {
		return programtools.Answer{}, nil
	}
	reader := bufio.NewReaderSize(io.LimitReader(conn, answerLimit), 64<<10)
	raw, err := reader.ReadBytes('\n')
	if err != nil {
		return programtools.Answer{}, current.why(ctx, err)
	}
	var answer programtools.Answer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return programtools.Answer{}, err
	}
	return answer, nil
}

// why says whether a broken exchange was the cancel of the program or a door that did not answer.
func (current *session) why(ctx context.Context, err error) error {
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) && current.ctx.Err() == nil {
		return errCancelled
	}
	return err
}
