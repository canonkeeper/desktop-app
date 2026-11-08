package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
}

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
	_ = supervisor.current.Swap(replacement).Close()
	return replacement, nil
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
