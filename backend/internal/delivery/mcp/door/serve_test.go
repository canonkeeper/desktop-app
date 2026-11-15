// SPDX-License-Identifier: AGPL-3.0-only

package door

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/delivery/mcp/programtools"
	"canonkeeper/backend/internal/domain/agent"
	researchservice "canonkeeper/backend/internal/services/researchchat"
)

// held is a handler that holds every call until it is let go or its context ends: the reading that stands on the channel.
type held struct {
	started chan struct{}
	release chan struct{}
}

func (hold held) handlers() Handlers {
	return func(string, researchservice.Naming) Handler {
		return func(ctx context.Context, _ agent.ToolCall) (agent.ToolResult, error) {
			hold.started <- struct{}{}
			select {
			case <-hold.release:
				return agent.ToolResult{Success: true, Payload: json.RawMessage("текст")}, nil
			case <-ctx.Done():
				return agent.ToolResult{}, ctx.Err()
			}
		}
	}
}

func listen(t *testing.T) (net.Listener, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	return listener, path
}

// callOf sends one line of a call and reads the answer; ok is false when the door closed the connection without one.
func callOf(t *testing.T, path string) (programtools.Answer, bool) {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	line, _ := json.Marshal(request("read", "claude-code", `{"chapter":"1"}`))
	if _, err := conn.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	answer, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return programtools.Answer{}, false
	}
	var decoded programtools.Answer
	if err := json.Unmarshal(answer, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded, true
}

func testLimits() Limits {
	return Limits{Line: 64 << 10, LineTime: 300 * time.Millisecond, Places: 2, Wait: 200 * time.Millisecond, Run: time.Second}
}

// MC1.7: two calls hold both places; the third waits its term and is «занят»; a call past its term is «не ответил»; a
// connection closed amid a call gives its place back at once and leaves no record.
func TestTheDoorHoldsTwoPlacesAndItsTerms(t *testing.T) {
	listener, path := listen(t)
	hold := held{started: make(chan struct{}, 8), release: make(chan struct{})}
	reads := oneSaga()
	serving := New(reads, hold.handlers(), "1.4.0").Serve(listener, testLimits())
	defer serving.Shut(programtools.WordsClosing)
	answers := make(chan programtools.Answer, 2)
	for range 2 {
		go func() {
			answer, _ := callOf(t, path)
			answers <- answer
		}()
	}
	<-hold.started
	<-hold.started
	if answer, _ := callOf(t, path); answer.Text != programtools.WordsBusy || !answer.Error {
		t.Fatalf("the third call: %+v", answer)
	}
	close(hold.release)
	for range 2 {
		if answer := <-answers; answer.Text != "текст" {
			t.Fatalf("a held call: %+v", answer)
		}
	}
	serving.Shut(programtools.WordsClosing)

	listener, path = listen(t)
	hold = held{started: make(chan struct{}, 8), release: make(chan struct{})}
	serving = New(reads, hold.handlers(), "1.4.0").Serve(listener, testLimits())
	defer serving.Shut(programtools.WordsClosing)
	if answer, _ := callOf(t, path); answer.Text != programtools.WordsNoAnswer {
		t.Fatalf("a call past its term: %+v", answer)
	}
	<-hold.started

	// A connection closed amid a call: the place is free at once, nothing recorded.
	reads.mu.Lock()
	before := len(reads.recorded)
	reads.mu.Unlock()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(request("read", "claude-code", `{"chapter":"1"}`))
	_, _ = conn.Write(append(line, '\n'))
	<-hold.started
	_ = conn.Close()
	for range 2 {
		go func() { _, _ = callOf(t, path) }()
	}
	for range 2 {
		select {
		case <-hold.started:
		case <-time.After(time.Second):
			t.Fatal("the place of a closed connection was not given back")
		}
	}
	reads.mu.Lock()
	defer reads.mu.Unlock()
	if len(reads.recorded) != before {
		t.Fatal("a closed call left a record")
	}
}

// MC1.7: a call whose connection closed before its record leaves none — even when its handler ends with success after the
// close: the door hands the record the context of the call, which the close cancelled.
func TestACallClosedBeforeItsRecordLeavesNone(t *testing.T) {
	listener, path := listen(t)
	started := make(chan struct{}, 1)
	reads := oneSaga()
	reads.tried = make(chan struct{}, 1)
	serving := New(reads, func(string, researchservice.Naming) Handler {
		return func(ctx context.Context, _ agent.ToolCall) (agent.ToolResult, error) {
			started <- struct{}{}
			<-ctx.Done()
			return agent.ToolResult{Success: true, Payload: json.RawMessage("текст")}, nil
		}
	}, "1.4.0").Serve(listener, testLimits())
	defer serving.Shut(programtools.WordsClosing)
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(request("read", "claude-code", `{"chapter":"1"}`))
	_, _ = conn.Write(append(line, '\n'))
	<-started
	_ = conn.Close()
	select {
	case <-reads.tried:
	case <-time.After(2 * time.Second):
		t.Fatal("the call that ended with success after the close did not reach its record")
	}
	reads.mu.Lock()
	defer reads.mu.Unlock()
	if len(reads.recorded) != 0 {
		t.Fatalf("a call closed before its record left one: %+v", reads.recorded)
	}
}

// MC1.7: a line longer than 64 KiB and a silent caller are closed without an answer.
func TestTheDoorClosesALongLineAndASilence(t *testing.T) {
	listener, path := listen(t)
	hold := held{started: make(chan struct{}, 1), release: make(chan struct{})}
	serving := New(oneSaga(), hold.handlers(), "1.4.0").Serve(listener, testLimits())
	defer serving.Shut(programtools.WordsClosing)
	for name, body := range map[string]string{"a long line": strings.Repeat("я", 50<<10) + "\n", "a silence": ""} {
		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		go func() { _, _ = conn.Write([]byte(body)) }()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if answer, err := bufio.NewReader(conn).ReadBytes('\n'); err == nil || len(answer) > 0 {
			t.Fatalf("%s got an answer: %q", name, answer)
		} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			t.Fatalf("%s: the door did not close the connection", name)
		}
		_ = conn.Close()
	}
	select {
	case <-hold.started:
		t.Fatal("a refused line reached the handler")
	default:
	}
}

// MC1.7: a call that runs at a stop gets «закрывается», at the turning off «закрыт или выключено»; a call that waits for a
// place too; the door takes no new call, and its handlers end within the wait.
func TestTheShutDoorSaysWhy(t *testing.T) {
	for _, words := range []string{programtools.WordsClosing, programtools.WordsClosed} {
		listener, path := listen(t)
		hold := held{started: make(chan struct{}, 4), release: make(chan struct{})}
		serving := New(oneSaga(), hold.handlers(), "1.4.0").Serve(listener, Limits{Line: 64 << 10, LineTime: time.Second, Places: 1, Wait: 5 * time.Second, Run: 5 * time.Second})
		answers := make(chan programtools.Answer, 2)
		go func() {
			answer, _ := callOf(t, path)
			answers <- answer
		}()
		<-hold.started
		go func() {
			answer, _ := callOf(t, path)
			answers <- answer
		}()
		time.Sleep(50 * time.Millisecond)
		serving.Shut(words)
		for range 2 {
			if answer := <-answers; answer.Text != words || !answer.Error {
				t.Fatalf("%s: %+v", words, answer)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		serving.Wait(ctx)
		if ctx.Err() != nil {
			t.Fatal("the handlers of a shut door did not end")
		}
		cancel()
		if _, err := net.Dial("unix", path); err == nil {
			t.Fatal("a shut door takes a call")
		}
	}
}
