// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
)

// bridgeOfTheTest is the bridge with the vaults of a build of a test, whose shell hears nothing.
func bridgeOfTheTest() *obsidianBridge {
	return newObsidianBridge(func(ControlMessage) error { return nil }, log.New(io.Discard, "", 0))
}

// files of a test whose root never opens until the test lets it: a folder on a network that does not answer.
type silentFiles struct{ release chan struct{} }

func (files silentFiles) Open(string) (service.Vault, error) {
	<-files.release
	return nil, obsidian.FolderError{Reason: obsidian.ReasonUnreadable}
}

// specs/2026-11-13-desk-obsidian.md OB1.3: the loop of the channel hands folder-picked to the bridge and goes on at once —
// to a shutdown too — while the check of the folder hangs; the shell hears how the check ended once it ends.
func TestTheBridgeHearsAFolderWithoutHoldingTheChannel(t *testing.T) {
	sent := make(chan ControlMessage, 4)
	bridge := newObsidianBridge(func(message ControlMessage) error {
		sent <- message
		return nil
	}, log.New(io.Discard, "", 0))
	files := silentFiles{release: make(chan struct{})}
	vaults := &linkedSaga{}
	bridge.pick = &service.PickFolder{
		Choices: bridge.choices, Vaults: vaults, Files: files, Shell: bridge.shell, CheckTime: time.Minute, Logger: bridge.logger,
	}
	request, err := service.BeginChoice{Vaults: vaults, Choices: bridge.choices, Shell: bridge.shell}.Execute(context.Background(), "author", "saga")
	if err != nil {
		t.Fatal(err)
	}
	if declared := <-sent; declared.Type != messageFolderChoice || declared.Request != request {
		t.Fatalf("folder-choice = %+v", declared)
	}
	heard := make(chan struct{})
	go func() {
		bridge.picked(ControlMessage{Type: messageFolderPicked, Request: request, Path: "/vault/on/a/network"})
		close(heard)
	}()
	select {
	case <-heard:
	case <-time.After(time.Second):
		t.Fatal("the loop of the channel waits for the check of the folder")
	}
	select {
	case message := <-sent:
		t.Fatalf("the shell heard %+v before the check ended", message)
	case <-time.After(50 * time.Millisecond):
	}
	close(files.release)
	select {
	case result := <-sent:
		if result.Type != messageFolderResult || result.Request != request || result.Outcome != string(obsidian.ReasonUnreadable) {
			t.Fatalf("folder-result = %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no folder-result")
	}
}

// linkedSaga is the saga of a test the author may change.
type linkedSaga struct{}

func (*linkedSaga) Vault(context.Context, string, string, service.Access) (service.Linked, error) {
	return service.Linked{Title: "Северный предел"}, nil
}
func (*linkedSaga) Link(context.Context, string, string) error   { return nil }
func (*linkedSaga) Unlink(context.Context, string, string) error { return nil }
