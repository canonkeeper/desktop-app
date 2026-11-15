// SPDX-License-Identifier: AGPL-3.0-only

package desktoptest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Launch is how a test starts the backend: the data directory and the bundle of its start, and what it adds to the
// environment of the test for the process — the file of the certificate a stub of the site serves (SSL_CERT_FILE), as
// the computer of the author trusts the certificate of the site. The address of the site is the program's.
type Launch struct {
	Data        string
	Resources   string
	Environment []string
	// Unset are the names of the environment of the test the process does not inherit, in any case — the proxy of the
	// machine and its exceptions for a test that sets its own (specs/2026-11-14-desk-journal.md JR5.2).
	Unset []string
}

// Start is the first message of the shell (backend/internal/app/desktop/control.go): the data directory, the bundle, the
// records of the secrets the shell keeps and where, and the version of the shell, which the backend must be.
type Start struct {
	Type         string            `json:"type"`
	DataDir      string            `json:"dataDir"`
	Resources    string            `json:"resources"`
	Secrets      map[string]string `json:"secrets,omitempty"`
	SecretsStore string            `json:"secretsStore"`
	Version      string            `json:"version"`
}

// StartOf is the start of a launch with the records the shell hands over, kept in a file as a computer without a store
// of the system keeps them, and the version of the program.
func StartOf(launch Launch, version string, secrets map[string]string) Start {
	return Start{Type: "start", DataDir: launch.Data, Resources: launch.Resources, Secrets: secrets, SecretsStore: "file", Version: version}
}

// Message is a line of the backend as a test reads it: the fields a test of another package needs, and the line itself.
type Message struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Value   string `json:"value"`
	Request string `json:"request"`
	Port    int    `json:"port"`
	Code    string `json:"code"`
	Reason  string `json:"reason"`
	Line    []byte `json:"-"`
}

// Process is the built cmd/desktop run as the shell runs it: a child with the control channel on its stdin and stdout.
type Process struct {
	t       testing.TB
	Command *exec.Cmd
	Stdin   io.WriteCloser
	// lines are the lines of the channel as one reader takes them, closed once the backend closed its stdout.
	lines chan []byte
	// Stderr is what the backend wrote besides the channel; the test sees it on its own stderr too.
	Stderr *Output
	Began  time.Time
	launch Launch
	writes sync.Mutex
	// exit runs the one Wait of the command: Wait and End wait for the same exit, never for two Waits at once.
	exit    sync.Once
	exited  chan struct{}
	exitErr error
}

// Output is what a process writes while the test reads it.
type Output struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (output *Output) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.buffer.Write(data)
}

func (output *Output) Bytes() []byte {
	output.mu.Lock()
	defer output.mu.Unlock()
	return bytes.Clone(output.buffer.Bytes())
}

// Begin starts the backend and sends it start; its answers are the test's to read. The end of the test ends the backend
// the way the shell does.
func Begin(t testing.TB, binary string, launch Launch, start any) *Process {
	t.Helper()
	command := exec.Command(binary)
	if len(launch.Environment) > 0 || len(launch.Unset) > 0 {
		command.Env = append(inherited(launch.Unset), launch.Environment...)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &Output{}
	command.Stderr = io.MultiWriter(os.Stderr, stderr)
	began := time.Now()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	process := &Process{t: t, Command: command, Stdin: stdin, lines: make(chan []byte, 256), Stderr: stderr, Began: began, launch: launch}
	go process.read(stdout)
	t.Cleanup(process.End)
	if err := process.Send(start); err != nil {
		t.Fatal(err)
	}
	return process
}

// inherited is the environment of the test without the names unset, in any case.
func inherited(unset []string) []string {
	var kept []string
	for _, pair := range os.Environ() {
		name, _, _ := strings.Cut(pair, "=")
		if !slices.ContainsFunc(unset, func(other string) bool { return strings.EqualFold(other, name) }) {
			kept = append(kept, pair)
		}
	}
	return kept
}

// Send writes one message to the backend.
func (process *Process) Send(message any) error {
	line, err := json.Marshal(message)
	if err != nil {
		return err
	}
	process.writes.Lock()
	defer process.writes.Unlock()
	_, err = process.Stdin.Write(append(line, '\n'))
	return err
}

// read is the one reader of the channel.
func (process *Process) read(stdout io.Reader) {
	defer close(process.lines)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		process.lines <- bytes.Clone(scanner.Bytes())
	}
}

// ErrNoLine is a backend that wrote no line in the time given.
var ErrNoLine = errors.New("desktoptest: no control message in time")

// Line is the next line the backend wrote, nil once it closed the channel; ErrNoLine when none came in timeout.
func (process *Process) Line(timeout time.Duration) ([]byte, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case line := <-process.lines:
		return line, nil
	case <-timer.C:
		return nil, ErrNoLine
	}
}

// Next is the next message the backend wrote; a closed channel is a message without a type.
func (process *Process) Next(timeout time.Duration) (Message, error) {
	line, err := process.Line(timeout)
	if err != nil {
		return Message{}, err
	}
	return parse(line), nil
}

// Ready waits for the answer to start past the steps of a long start: ready or fatal.
func (process *Process) Ready(timeout time.Duration) Message {
	process.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		message, err := process.Next(time.Until(deadline))
		if err != nil {
			process.t.Fatalf("the backend did not answer start in %s", timeout)
		}
		if message.Type != "step" {
			return message
		}
	}
}

func parse(line []byte) Message {
	var message Message
	if line != nil {
		_ = json.Unmarshal(line, &message)
	}
	message.Line = line
	return message
}

// waited is closed once the backend exited; the first call starts the one Wait of the command.
func (process *Process) waited() <-chan struct{} {
	process.exit.Do(func() {
		process.exited = make(chan struct{})
		go func() {
			process.exitErr = process.Command.Wait()
			close(process.exited)
		}()
	})
	return process.exited
}

// Wait waits for the backend to exit; its error is the exit's.
func (process *Process) Wait(timeout time.Duration) error {
	select {
	case <-process.waited():
		return process.exitErr
	case <-time.After(timeout):
		return fmt.Errorf("desktoptest: the backend did not exit in %s", timeout)
	}
}

// End ends a backend its test left running, the way the shell ends it: the stop it answers stops PostgreSQL too. A
// backend killed instead leaves its cluster running — pg_ctl started it apart —, writing into the directory the test is
// about to remove; such a cluster is stopped by the tool of the set.
func (process *Process) End() {
	exited := process.waited()
	select {
	case <-exited:
	default:
		_ = process.Send(map[string]string{"type": "shutdown"})
		select {
		case <-exited:
		case <-time.After(90 * time.Second):
			_ = process.Command.Process.Kill()
			<-exited
		}
	}
	cluster := filepath.Join(process.launch.Data, "pg")
	if _, err := os.Stat(filepath.Join(cluster, "postmaster.pid")); err != nil {
		return
	}
	stop := exec.Command(filepath.Join(process.launch.Resources, "postgres", "bin", "pg_ctl"), "stop", "-w", "-t", "30", "-m", "immediate", "-D", cluster)
	_ = stop.Run()
}
