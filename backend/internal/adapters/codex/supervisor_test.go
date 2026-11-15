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

// specs/2026-11-13-desk-gateway.md GW2.3: new settings of the app-server — a new window of the chat — start a new
// app-server at once when no turn runs; while a turn runs they wait, the client says so, and the app-server is replaced
// once the turn ends. The next start takes the new settings.
func TestNewSettingsRestartTheAppServerWhenNoTurnRuns(t *testing.T) {
	var started [][]string
	var transports []*fakeTransport
	var client *Client
	client, err := NewClient(t.Context(), ClientConfig{
		CWD: "/tmp", ConfigOverrides: []string{"model_context_window=128000"},
		transportFactory: func(context.Context) (Transport, error) {
			transport := newFakeTransport()
			transport.autoComplete = true
			transports = append(transports, transport)
			if client != nil {
				// The factory runs under the lock of the supervisor: the settings it starts with are these.
				started = append(started, client.supervisor.overrides)
			}
			return transport, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if now, err := client.Configure(t.Context(), []string{"model_context_window=128000"}); !now || err != nil || len(transports) != 1 {
		t.Fatalf("the same settings = %v, %v, %d app-servers", now, err, len(transports))
	}
	if now, err := client.Configure(t.Context(), []string{"model_context_window=32000"}); !now || err != nil || len(transports) != 2 ||
		!transports[0].closedForTest() || client.Pending() {
		t.Fatalf("new settings without a turn = %v, %v, %d app-servers", now, err, len(transports))
	}

	turnStarted, turnGoesOn := make(chan struct{}), make(chan struct{})
	var turn string
	request := testRunRequest("chat-a", "turn-a", "", false, &turn)
	bind := request.BindRoot
	request.BindRoot = func(ctx context.Context, root string) error {
		close(turnStarted)
		<-turnGoesOn
		return bind(ctx, root)
	}
	ran := make(chan error, 1)
	go func() {
		_, err := client.Run(t.Context(), request)
		ran <- err
	}()
	<-turnStarted
	if now, err := client.Configure(t.Context(), []string{"model_context_window=64000"}); now || err != nil || !client.Pending() || len(transports) != 2 {
		t.Fatalf("new settings while a turn runs = %v, %v, pending %v, %d app-servers", now, err, client.Pending(), len(transports))
	}
	close(turnGoesOn)
	if err := <-ran; err != nil {
		t.Fatalf("the turn = %v", err)
	}
	if len(transports) != 3 || !transports[1].closedForTest() || client.Pending() {
		t.Fatalf("after the turn: %d app-servers, pending %v", len(transports), client.Pending())
	}
	if want := [][]string{{"model_context_window=32000"}, {"model_context_window=64000"}}; len(started) != 2 ||
		started[0][0] != want[0][0] || started[1][0] != want[1][0] {
		t.Fatalf("the app-servers started with %v", started)
	}
	if _, err := client.Configure(t.Context(), []string{"model=\"x\"\nsandbox_mode=\"danger\""}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("settings with a new line = %v", err)
	}
}

func (transport *fakeTransport) closedForTest() bool {
	select {
	case <-transport.done:
		return true
	default:
		return false
	}
}
