package codex

import (
	"encoding/json"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agenttimeline"
	"canonkeeper/backend/internal/observability"

	sdk "github.com/openai/codex/sdk/go"
	"github.com/openai/codex/sdk/go/protocol"
)

func TestNativeTimelineEntriesRejectInvalidItemJSON(t *testing.T) {
	for _, test := range []struct {
		name string
		item json.RawMessage
	}{
		{name: "missing"},
		{name: "null", item: json.RawMessage(`null`)},
		{name: "scalar", item: json.RawMessage(`"not an item"`)},
		{name: "malformed", item: json.RawMessage(`{"type":`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			entries := []protocol.ThreadTimelineEntry{
				{TypeValue: "turnStarted", Position: protocol.SomeNonNull(uint64(1)), TurnID: protocol.SomeNonNull("turn-1")},
				{TypeValue: "item", Position: protocol.SomeNonNull(uint64(2)), TurnID: protocol.SomeNonNull("turn-1"), Item: test.item},
			}
			if _, err := nativeTimelineEntries(entries, "", agenttimeline.RoleMain); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid native item error = %v", err)
			}
		})
	}
}

func TestNativeTimelineEntriesPreserveUserContentJSON(t *testing.T) {
	entries := []protocol.ThreadTimelineEntry{
		{TypeValue: "turnStarted", Position: protocol.SomeNonNull(uint64(1)), TurnID: protocol.SomeNonNull("turn-1")},
		{TypeValue: "item", Position: protocol.SomeNonNull(uint64(2)), TurnID: protocol.SomeNonNull("turn-1"),
			Item: json.RawMessage(`{"type":"userMessage","id":"user-1","content":[{"type":"text","text":"Исследуй сагу"}]}`)},
	}
	items, err := nativeTimelineEntries(entries, "", agenttimeline.RoleMain)
	if err != nil || len(items) != 1 {
		t.Fatalf("native user item count = %d, error = %v", len(items), err)
	}
	var item struct {
		Content []struct{ Text string } `json:"content"`
	}
	if err := json.Unmarshal(items[0].Item, &item); err != nil {
		t.Fatal(err)
	}
	if len(item.Content) != 1 || item.Content[0].Text != "Исследуй сагу" {
		t.Fatalf("native user content = %#v", item.Content)
	}
}

// R4.2: a panic while fanning out timeline updates closes that stream with the panic and logs its
// stack; the process lives on.
func TestPublishThreadUpdatesTurnsPanicIntoStreamError(t *testing.T) {
	var logs strings.Builder
	client := &Client{logger: log.New(&logs, "", 0)}
	updates := make(chan agenttimeline.Update, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		client.publishThreadUpdates(t.Context(), nil, "root-1", map[string]agenttimeline.Role{}, updates)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publishThreadUpdates did not finish after a panic")
	}
	update, open := <-updates
	var panicked *observability.PanicError
	if !open || !errors.As(update.Err, &panicked) {
		t.Fatalf("update = %+v (open %v), want the recovered panic", update, open)
	}
	if _, open := <-updates; open {
		t.Fatal("updates stay open after the panic")
	}
	if !strings.Contains(logs.String(), `"event":"timeline_updates_panic"`) || !strings.Contains(logs.String(), "goroutine") {
		t.Fatalf("logs = %s, want timeline_updates_panic with the stack", logs.String())
	}
}

// The chat root is bound right after thread/start, but app-server writes the thread's history
// only once its first turn lands in the store; until then it answers thread/timeline/list with
// "not supported yet". The timeline of such a thread is empty, not a failure (feed spec,
// Decision log 30.10).
func TestThreadItemsOfRootWithoutWrittenHistoryAreEmpty(t *testing.T) {
	transport := newFakeTransport()
	root := testProtocolThread("root", "project-chat", "", "")
	root.HistoryMode = protocol.SomeNonNull(protocol.ThreadHistoryModePaginated)
	transport.addThread(root)
	transport.timelineListErr = &sdk.RPCError{Code: -32601, Message: "thread/timeline/list is not supported yet"}
	client := newTestClient(t, transport)

	items, err := client.ThreadItems(t.Context(), "root")
	if err != nil || len(items) != 0 {
		t.Fatalf("timeline of a root without written history = %#v, %v", items, err)
	}
}

// A thread whose history mode cannot be listed at all, or any other native failure, stays a
// failure and carries a safe stage code for the request log.
func TestThreadItemsKeepNativeTimelineFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		mode protocol.OptionalNonNull[protocol.ThreadHistoryMode]
		err  error
	}{
		{
			name: "legacy history", mode: protocol.SomeNonNull(protocol.ThreadHistoryModeLegacy),
			err: &sdk.RPCError{Code: -32601, Message: "thread/timeline/list is not supported yet"},
		},
		{
			name: "internal error", mode: protocol.SomeNonNull(protocol.ThreadHistoryModePaginated),
			err: &sdk.RPCError{Code: -32603, Message: "failed to list thread history: disk"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := newFakeTransport()
			root := testProtocolThread("root", "project-chat", "", "")
			root.HistoryMode = test.mode
			transport.addThread(root)
			transport.timelineListErr = test.err
			client := newTestClient(t, transport)

			_, err := client.ThreadItems(t.Context(), "root")
			var classified interface{ SafeErrorCode() string }
			if !errors.As(err, &classified) || classified.SafeErrorCode() != "timeline.history_list_failed" {
				t.Fatalf("native timeline failure = %v", err)
			}
		})
	}
}
