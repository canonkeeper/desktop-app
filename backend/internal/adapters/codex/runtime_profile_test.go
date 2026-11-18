package codex

import (
	"context"
	"encoding/json"
	"maps"
	"net/http/httptest"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"

	"github.com/openai/codex/sdk/go/protocol"
)

// theOverrideOfWindows is the -c process_windows.go gives the app-server of Windows, word for word: on Windows the test
// of the command of the app-server holds the platform file to it.
const theOverrideOfWindows = `permissions.canonkeeper-runtime.filesystem={":root"="read"}`

// specs/2026-11-18-desk-windows-chat.md WC1.1, WC1.2 on the pinned binary: the -c of Windows, as Codex 0.167.2 reads it,
// adds the read of the root to the filesystem table of the profile canonkeeper-runtime — merged with the table of the
// profile, not in place of it — and leaves its network off; a chat goes on to the answer of the stub under it, in a home
// copied as the app copies it. The profile of the repository is the one 0.1.3 left in the homes of the authors (its
// config.toml has not changed since), and the -c needs nothing of a home but the name of the profile.
func TestRuntimeTheProfileOfWindowsReadsTheWholeDiskAndNothingMore(t *testing.T) {
	binary := runtimeTestBinary(t)
	server := httptest.NewServer(&stubModel{})
	defer server.Close()
	config := runtimeStubConfig(t, binary, t.TempDir(), server.URL)
	config.ConfigOverrides = append(config.ConfigOverrides, theOverrideOfWindows)
	config.HomeCopies = true
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	client, err := NewClient(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	native, err := client.runtime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	read, err := native.sdk.Config.Read(ctx, protocol.ConfigReadParams{Cwd: protocol.Some(config.CWD)})
	if err != nil {
		t.Fatal(err)
	}
	var permissions map[string]struct {
		Filesystem map[string]any `json:"filesystem"`
		Network    map[string]any `json:"network"`
	}
	if err := json.Unmarshal(read.Config.Additional["permissions"], &permissions); err != nil {
		t.Fatalf("the permissions of the effective config %s: %v", read.Config.Additional["permissions"], err)
	}
	profile, found := permissions["canonkeeper-runtime"]
	// The config read names the settings a table does not hold as null (glob_scan_max_depth, proxy_url …).
	for _, table := range []map[string]any{profile.Filesystem, profile.Network} {
		maps.DeleteFunc(table, func(_ string, value any) bool { return value == nil })
	}
	if want := map[string]any{":root": "read", ":workspace_roots": "read"}; !found || !maps.Equal(profile.Filesystem, want) {
		t.Fatalf("the filesystem of the profile with the -c of Windows = %v, want %v", profile.Filesystem, want)
	}
	if want := map[string]any{"enabled": false}; !maps.Equal(profile.Network, want) {
		t.Fatalf("the network of the profile with the -c of Windows = %v, want %v", profile.Network, want)
	}

	result, err := client.Run(ctx, runtimeChatRequest("turn-windows", &recordedCalls{}))
	if err != nil || result.Status != agent.TurnCompleted || result.FinalResponse != stubAnswer {
		t.Fatalf("the turn under the profile of Windows = %+v, %v", result, err)
	}
}
