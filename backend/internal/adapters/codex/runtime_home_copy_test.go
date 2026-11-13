package codex

import (
	"context"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	agenttimeline "canonkeeper/backend/internal/domain/agenttimeline"
)

// specs/2026-11-13-desk-home.md HM1.4 on the pinned binary: a chat that ran on a home copied from the bundle at one place
// is read back whole and goes on from the same bundle at another place, the first place gone; the home holds no link.
func TestRuntimeAHomeCopiedFromAMovedBundleKeepsItsThreads(t *testing.T) {
	binary := runtimeTestBinary(t)
	server := httptest.NewServer(&stubModel{})
	defer server.Close()
	config := runtimeStubConfig(t, binary, t.TempDir(), server.URL)
	profile := config.RepositoryHome
	place := func(mount string) string {
		bundle := filepath.Join(mount, "codex-homes", "research-chat")
		if err := os.CopyFS(bundle, os.DirFS(profile)); err != nil {
			t.Fatal(err)
		}
		return bundle
	}
	noLinks := func() {
		t.Helper()
		for _, name := range homeEntries {
			err := filepath.WalkDir(filepath.Join(config.CodexHome, name), func(path string, entry fs.DirEntry, err error) error {
				if err == nil && entry.Type()&fs.ModeSymlink != 0 {
					t.Fatalf("%s is a link", path)
				}
				return err
			})
			if err != nil && !(name == "agents" && os.IsNotExist(err)) {
				t.Fatal(err)
			}
		}
	}
	timeline := func(client *Client, root string) []string {
		t.Helper()
		items, err := client.ThreadItems(t.Context(), root)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := agenttimeline.Project("job-runtime-home", items)
		if err != nil {
			t.Fatal(err)
		}
		return timelineKinds(snapshot.Events)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	calls := &recordedCalls{}

	first := t.TempDir()
	config.RepositoryHome, config.HomeCopies = place(first), true
	client, err := NewClient(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Run(ctx, runtimeChatRequest("turn-1", calls))
	if err != nil || result.Status != agent.TurnCompleted || result.RootThreadID == "" {
		t.Fatalf("the turn before the move = %+v, %v", result, err)
	}
	root := result.RootThreadID
	before := timeline(client, root)
	_ = client.Close()
	noLinks()

	if err := os.RemoveAll(first); err != nil {
		t.Fatal(err)
	}
	config.RepositoryHome = place(t.TempDir())
	client, err = NewClient(ctx, config)
	if err != nil {
		t.Fatalf("the client from the moved bundle: %v", err)
	}
	defer client.Close()
	noLinks()
	if after := timeline(client, root); len(before) == 0 || !slices.Equal(after, before) {
		t.Fatalf("the thread after the move = %v; before it %v", after, before)
	}
	request := runtimeChatRequest("turn-2", calls)
	request.ExistingRootThreadID = root
	if result, err := client.Run(ctx, request); err != nil || result.Status != agent.TurnCompleted || result.RootThreadID != root {
		t.Fatalf("the turn after the move = %+v, %v", result, err)
	}
	if after := timeline(client, root); len(after) != 2*len(before) {
		t.Fatalf("the thread after the next turn = %v", after)
	}
}
