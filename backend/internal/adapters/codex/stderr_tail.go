package codex

import (
	"bytes"
	"encoding/json"
	"log"
	"sync"

	"canonkeeper/backend/internal/observability"
)

const (
	stderrTailLines     = 20
	stderrTailLineBytes = 300
)

// stderrTail keeps the last lines of app-server stderr in memory. They reach the log only when
// the process exits unexpectedly or fails to start, because a healthy app-server may print
// content to stderr.
type stderrTail struct {
	mu       sync.Mutex
	lines    []string
	partial  []byte
	reported sync.Once
}

func (tail *stderrTail) Write(chunk []byte) (int, error) {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	for _, value := range chunk {
		if value != '\n' {
			if len(tail.partial) < stderrTailLineBytes*4 {
				tail.partial = append(tail.partial, value)
			}
			continue
		}
		tail.keep(tail.partial)
		tail.partial = tail.partial[:0]
	}
	return len(chunk), nil
}

func (tail *stderrTail) keep(line []byte) {
	line = bytes.TrimRight(line, "\r")
	if len(line) == 0 {
		return
	}
	tail.lines = append(tail.lines, observability.Truncate(string(line), stderrTailLineBytes))
	if len(tail.lines) > stderrTailLines {
		tail.lines = tail.lines[len(tail.lines)-stderrTailLines:]
	}
}

type stderrTailLogEntry struct {
	Event  string   `json:"event"`
	Reason string   `json:"reason"`
	Exit   string   `json:"exit,omitempty"`
	Lines  []string `json:"lines"`
}

// report writes the tail once: reason is "exited" for an unexpected exit and "start_failed" when
// the app-server never became ready.
func (tail *stderrTail) report(logger *log.Logger, reason string, exit error) {
	if tail == nil || logger == nil {
		return
	}
	tail.reported.Do(func() {
		tail.mu.Lock()
		tail.keep(tail.partial)
		tail.partial = nil
		entry := stderrTailLogEntry{Event: "codex_app_server_stderr", Reason: reason, Lines: append([]string{}, tail.lines...)}
		tail.mu.Unlock()
		if exit != nil {
			entry.Exit = exit.Error()
		}
		line, _ := json.Marshal(entry)
		logger.Print(string(line))
	})
}
