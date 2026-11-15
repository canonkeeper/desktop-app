package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"canonkeeper/backend/internal/observability"
)

const (
	// runtimeStartBackoff — пауза после неудачного запуска app-server; удваивается до
	// maxRuntimeStartBackoff и сбрасывается после успешного запуска.
	runtimeStartBackoff    = 5 * time.Second
	maxRuntimeStartBackoff = time.Minute
	runtimeErrorLogBytes   = 500
)

// ErrRuntimeUnavailable — среда чата не запущена: последний запуск app-server не удался, а пауза
// до следующей попытки ещё идёт.
var ErrRuntimeUnavailable = errors.New("Codex app-server is unavailable")

// clientSupervisor держит текущий процесс app-server и запускает новый по требованию: при старте,
// после падения процесса и после неудачного запуска — не чаще раза в паузу отсрочки. Запуск идёт
// под mu; current читается без него, поэтому /readyz не ждёт идущий запуск.
type clientSupervisor struct {
	mu        sync.Mutex
	current   atomic.Pointer[Client]
	factory   func(context.Context) (*Client, error)
	closed    bool
	now       func() time.Time
	logger    *log.Logger
	startErr  error
	backoff   time.Duration
	nextStart time.Time
	// overrides are the settings the next app-server starts with, running those of the current one; runs counts the
	// turns that run, and pending says new settings wait for their end (specs/2026-11-13-desk-gateway.md GW2.3).
	overrides []string
	running   []string
	runs      int
	pending   bool
}

// restartTime bounds the start of an app-server with new settings after the last turn.
const restartTime = time.Minute

func (supervisor *clientSupervisor) runtime(ctx context.Context) (*Client, error) {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	if supervisor.closed {
		return nil, ErrInvalid
	}
	if current := supervisor.current.Load(); current != nil && !runtimeStopped(current.runtimeDone) {
		return current, nil
	}
	if now := supervisor.now(); supervisor.startErr != nil && now.Before(supervisor.nextStart) {
		return nil, fmt.Errorf("%w, next start in %s: %w",
			ErrRuntimeUnavailable, supervisor.nextStart.Sub(now).Round(time.Second), supervisor.startErr)
	}
	return supervisor.startLocked(ctx)
}

// startLocked starts an app-server with the settings held now and keeps it in place of the current one.
func (supervisor *clientSupervisor) startLocked(ctx context.Context) (*Client, error) {
	replacement, err := supervisor.factory(ctx)
	if err != nil {
		supervisor.backoff = min(max(2*supervisor.backoff, runtimeStartBackoff), maxRuntimeStartBackoff)
		supervisor.startErr = err
		supervisor.nextStart = supervisor.now().Add(supervisor.backoff)
		supervisor.log("chat_runtime_unavailable", err)
		return nil, err
	}
	if supervisor.startErr != nil {
		supervisor.log("chat_runtime_restored", nil)
	}
	supervisor.startErr, supervisor.backoff, supervisor.nextStart = nil, 0, time.Time{}
	supervisor.running, supervisor.pending = supervisor.overrides, false
	_ = supervisor.current.Swap(replacement).Close()
	return replacement, nil
}

// configure holds new settings and replaces the app-server with one of them at once when no turn runs.
func (supervisor *clientSupervisor) configure(ctx context.Context, overrides []string) (bool, error) {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	if supervisor.closed {
		return false, ErrInvalid
	}
	supervisor.overrides = append([]string(nil), overrides...)
	if supervisor.current.Load() != nil && slices.Equal(supervisor.running, supervisor.overrides) {
		supervisor.pending = false
		return true, nil
	}
	if supervisor.runs > 0 {
		supervisor.pending = true
		return false, nil
	}
	_, err := supervisor.restartLocked(ctx)
	return err == nil, err
}

// restartLocked stops the current app-server and starts one with the settings held now; a start that fails leaves the
// next turn to start it after the backoff.
func (supervisor *clientSupervisor) restartLocked(ctx context.Context) (*Client, error) {
	_ = supervisor.current.Swap(nil).Close()
	supervisor.startErr, supervisor.backoff, supervisor.nextStart = nil, 0, time.Time{}
	return supervisor.startLocked(ctx)
}

func (supervisor *clientSupervisor) waiting() bool {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	return supervisor.pending
}

// begin counts a turn that runs.
func (supervisor *clientSupervisor) begin() {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	supervisor.runs++
}

// end counts a turn that ended; after the last one the settings that wait take their app-server.
func (supervisor *clientSupervisor) end() {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	supervisor.runs--
	if supervisor.runs > 0 || !supervisor.pending || supervisor.closed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), restartTime)
	defer cancel()
	_, _ = supervisor.restartLocked(ctx)
}

func (supervisor *clientSupervisor) ready() bool {
	current := supervisor.current.Load()
	return current != nil && !runtimeStopped(current.runtimeDone)
}

func (supervisor *clientSupervisor) close() error {
	supervisor.mu.Lock()
	if supervisor.closed {
		supervisor.mu.Unlock()
		return nil
	}
	supervisor.closed = true
	current := supervisor.current.Swap(nil)
	supervisor.mu.Unlock()
	return current.Close()
}

type runtimeLogEntry struct {
	Event     string `json:"event"`
	Error     string `json:"error,omitempty"`
	RetryInMS int64  `json:"retryInMs,omitempty"`
}

// log пишет причину сбоя запуска (цепочку ошибки без содержимого хода) и восстановление среды;
// хвост stderr процесса пишет сам транспорт (codex_app_server_stderr).
func (supervisor *clientSupervisor) log(event string, err error) {
	if supervisor.logger == nil {
		return
	}
	entry := runtimeLogEntry{Event: event}
	if err != nil {
		entry.Error = observability.Truncate(err.Error(), runtimeErrorLogBytes)
		entry.RetryInMS = supervisor.backoff.Milliseconds()
	}
	line, _ := json.Marshal(entry)
	supervisor.logger.Print(string(line))
}
