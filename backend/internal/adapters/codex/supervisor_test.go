package codex

import (
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"
)

// R3.1–R3.5: a failed app-server start leaves the client without a runtime; a turn gets a clear
// refusal with the cause, a new start waits for the backoff, and the next start brings it back.
func TestClientStartsWithoutRuntimeAndRecoversAfterBackoff(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	working := newFakeTransport()
	working.autoComplete = true
	factoryCalls := 0
	var logs strings.Builder
	client, err := NewClient(t.Context(), ClientConfig{
		CWD:    "/tmp",
		Logger: log.New(&logs, "", 0),
		now:    func() time.Time { return now },
		transportFactory: func(context.Context) (Transport, error) {
			factoryCalls++
			if factoryCalls < 3 {
				return nil, errors.New("bwrap: setting up uid map: Permission denied")
			}
			return working, nil
		},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v, want a client without runtime", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if client.RuntimeReady() {
		t.Fatal("RuntimeReady() = true after a failed start")
	}
	if !strings.Contains(logs.String(), `"event":"chat_runtime_unavailable"`) || !strings.Contains(logs.String(), "Permission denied") {
		t.Fatalf("logs = %s, want chat_runtime_unavailable with the cause", logs.String())
	}

	var turn string
	_, err = client.Run(t.Context(), testRunRequest("chat-a", "turn-a", "", false, &turn))
	var classified interface{ SafeErrorCode() string }
	if !errors.As(err, &classified) || classified.SafeErrorCode() != "runtime.client_start_failed" ||
		!strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("Run() before backoff error = %v, want runtime.client_start_failed with the cause", err)
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls inside the backoff = %d, want 1", factoryCalls)
	}

	now = now.Add(runtimeStartBackoff)
	if _, err := client.Run(t.Context(), testRunRequest("chat-a", "turn-a", "", false, &turn)); err == nil || factoryCalls != 2 {
		t.Fatalf("second start = %v (factory calls %d), want one more failed start", err, factoryCalls)
	}
	now = now.Add(runtimeStartBackoff)
	if _, err := client.Run(t.Context(), testRunRequest("chat-a", "turn-a", "", false, &turn)); err == nil || factoryCalls != 2 {
		t.Fatalf("start inside the doubled backoff = %v (factory calls %d), want no new start", err, factoryCalls)
	}
	now = now.Add(runtimeStartBackoff)
	if _, err := client.Run(t.Context(), testRunRequest("chat-a", "turn-a", "", false, &turn)); err != nil {
		t.Fatalf("Run() after the runtime is back = %v", err)
	}
	if factoryCalls != 3 || !client.RuntimeReady() {
		t.Fatalf("factory calls = %d, ready = %v, want the third start to bring the runtime back", factoryCalls, client.RuntimeReady())
	}
}

// R3.1: a configuration error still stops the start.
func TestNewClientRejectsInvalidConfiguration(t *testing.T) {
	if _, err := NewClient(t.Context(), ClientConfig{CWD: "relative"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewClient(relative CWD) error = %v, want ErrInvalid", err)
	}
}
