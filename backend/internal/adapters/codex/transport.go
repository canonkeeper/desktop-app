package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	agentdomain "canonkeeper/backend/internal/domain/agent"
)

var errInvalidTransport = errors.New("invalid Codex transport")

type Transport interface {
	Receive(context.Context) (json.RawMessage, error)
	Send(context.Context, json.RawMessage) error
	Close() error
}

type transportConfig struct {
	CodexPath        string
	CodeModeHostPath string
	CWD              string
	CodexHome        string
	CodexSQLiteHome  string
	PrivateHome      string
	TempDir          string
	// ConfigOverrides are environment-specific `key=value` Codex settings (model,
	// provider) passed as `-c` flags, so the repository config stays provider-neutral.
	ConfigOverrides []string
	// PassEnv names backend environment variables (provider API keys) that the
	// app-server may receive; everything else stays out of its environment.
	PassEnv []string
	// Logger receives the stderr tail of an app-server that exits unexpectedly or fails to start.
	Logger *log.Logger
}

type processTransport struct {
	stdin    *os.File
	stdout   io.ReadCloser
	command  *exec.Cmd
	pgid     int
	receive  chan receiveResult
	done     chan struct{}
	waitDone chan struct{}
	// stdoutRead closes when the read pump stops reading stdout; Wait runs only after it.
	stdoutRead chan struct{}
	waitErr    error
	stderr     *stderrTail
	logger     *log.Logger
	sendMu     sync.Mutex
	closeOnce  sync.Once
	closeErr   error
}

type receiveResult struct {
	frame json.RawMessage
	err   error
}

func (transport *processTransport) RuntimeDone() <-chan struct{} {
	if transport == nil {
		return nil
	}
	return transport.waitDone
}

func (transport *processTransport) RuntimeExitError() error {
	if transport == nil {
		return nil
	}
	select {
	case <-transport.waitDone:
		return unexpectedProcessExitError(transport.waitErr)
	default:
		return nil
	}
}

func startTransport(ctx context.Context, config transportConfig) (*processTransport, error) {
	if ctx == nil || !validTransportConfig(config) {
		return nil, errInvalidTransport
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, directory := range []string{config.CodexSQLiteHome, config.PrivateHome, config.TempDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
	}
	args := appServerArgs(config.CodexPath)
	for _, override := range config.ConfigOverrides {
		args = append(args, "-c", override)
	}
	args = append(args, "--listen", "stdio://")
	command := exec.Command(config.CodexPath, args...)
	command.Dir = config.CWD
	command.Env = processEnvironment(config)
	if config.CodeModeHostPath != "" {
		command.Env = append(command.Env, "CODEX_CODE_MODE_HOST_PATH="+config.CodeModeHostPath)
	}
	for _, name := range config.PassEnv {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	stderr := &stderrTail{}
	command.Stderr = stderr
	startInOwnGroup(command)
	stdinPipe, err := command.StdinPipe()
	if err != nil {
		return nil, errInvalidTransport
	}
	stdin, ok := stdinPipe.(*os.File)
	if !ok {
		_ = stdinPipe.Close()
		return nil, errInvalidTransport
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, errInvalidTransport
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start Codex app-server: %w", err)
	}
	pgid, err := processGroup(command)
	if err != nil {
		killProcessGroup(command, command.Process.Pid)
		_ = command.Wait()
		stderr.report(config.Logger, "start_failed", nil)
		return nil, errInvalidTransport
	}
	transport := &processTransport{
		stdin: stdin, stdout: stdout, command: command, pgid: pgid, stderr: stderr, logger: config.Logger,
		receive: make(chan receiveResult, 1), done: make(chan struct{}), waitDone: make(chan struct{}),
		stdoutRead: make(chan struct{}),
	}
	go func() {
		// Wait closes the stdout pipe, so it runs only once the read pump has read stdout to its
		// end; otherwise the last lines of an exiting app-server could be lost (os/exec, StdoutPipe).
		<-transport.stdoutRead
		transport.waitErr = command.Wait()
		select {
		case <-transport.done:
		default:
			transport.stderr.report(transport.logger, "exited", unexpectedProcessExitError(transport.waitErr))
		}
		close(transport.waitDone)
	}()
	go transport.readPump()
	return transport, nil
}

// appServerArgs are the first arguments of the app-server: the codex CLI runs it as its `app-server` subcommand, the
// codex-app-server of the release bundle is the app-server itself and knows no subcommand (specs/2026-11-07-desktop-app.md
// DA1.4, S0).
func appServerArgs(codexPath string) []string {
	if strings.TrimSuffix(filepath.Base(codexPath), ".exe") == "codex-app-server" {
		return nil
	}
	return []string{"app-server"}
}

// reportStartFailure closes an app-server that never became ready and logs its stderr tail.
func (transport *processTransport) reportStartFailure() {
	_ = transport.Close()
	transport.stderr.report(transport.logger, "start_failed", transport.RuntimeExitError())
}

func (transport *processTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	if transport == nil || ctx == nil {
		return nil, errInvalidTransport
	}
	select {
	case result := <-transport.receive:
		return result.frame, result.err
	case <-transport.done:
		return nil, os.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (transport *processTransport) Send(ctx context.Context, frame json.RawMessage) error {
	if transport == nil || ctx == nil || len(bytes.TrimSpace(frame)) == 0 {
		return errInvalidTransport
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	transport.sendMu.Lock()
	defer transport.sendMu.Unlock()
	select {
	case <-transport.done:
		return os.ErrClosed
	default:
	}
	deadline := time.Time{}
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	// A pipe of Windows has no deadline (os.ErrNoDeadline); the write blocks as it would without one.
	if err := transport.stdin.SetWriteDeadline(deadline); err != nil && !errors.Is(err, os.ErrNoDeadline) {
		return err
	}
	_, err := transport.stdin.Write(append(append([]byte(nil), frame...), '\n'))
	return err
}

func (transport *processTransport) Close() error {
	if transport == nil {
		return nil
	}
	transport.closeOnce.Do(func() {
		close(transport.done)
		_ = transport.stdin.Close()
		terminateProcessGroup(transport.command, transport.pgid)
		select {
		case <-transport.waitDone:
		case <-time.After(2 * time.Second):
			killProcessGroup(transport.command, transport.pgid)
			<-transport.waitDone
		}
		if err := transport.stdout.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			transport.closeErr = err
		}
	})
	return transport.closeErr
}

func (transport *processTransport) readPump() {
	stopReading := sync.OnceFunc(func() { close(transport.stdoutRead) })
	defer stopReading()
	reader := bufio.NewReader(transport.stdout)
	for {
		line, err := reader.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			select {
			case transport.receive <- receiveResult{frame: append(json.RawMessage(nil), line...)}:
			case <-transport.done:
				return
			}
		}
		if err != nil {
			stopReading()
			select {
			case <-transport.done:
				return
			case <-transport.waitDone:
				err = unexpectedProcessExitError(transport.waitErr)
			}
			select {
			case transport.receive <- receiveResult{err: err}:
			case <-transport.done:
			}
			return
		}
	}
}

func unexpectedProcessExitError(waitErr error) error {
	if waitErr == nil {
		return fmt.Errorf("%w: exit_code=0", agentdomain.ErrRuntimeProcessExited)
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return fmt.Errorf("%w: signal=%d", agentdomain.ErrRuntimeProcessExited, status.Signal())
		}
		return fmt.Errorf("%w: exit_code=%d", agentdomain.ErrRuntimeProcessExited, exitErr.ExitCode())
	}
	return fmt.Errorf("%w: wait_error", agentdomain.ErrRuntimeProcessExited)
}

func validTransportConfig(config transportConfig) bool {
	for _, value := range []string{
		config.CodexPath, config.CWD, config.CodexHome, config.CodexSQLiteHome, config.PrivateHome, config.TempDir,
	} {
		if !filepath.IsAbs(value) {
			return false
		}
	}
	if config.CodeModeHostPath != "" && !filepath.IsAbs(config.CodeModeHostPath) {
		return false
	}
	return agentdomain.ValidConfigOverrides(config.ConfigOverrides) && agentdomain.ValidPassEnv(config.PassEnv)
}
