// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	domain "canonkeeper/backend/internal/domain/desktop"
)

// specs/2026-11-13-desk-gateway.md GW4.1, GW4.2: the helper of the app is connected by the one rule of the domain, and a
// change that connects it queues the cards it is to read at once — no restart of the app; a change that leaves it not
// connected queues nothing.
func TestAConnectedHelperQueuesTheCardsAtOnce(t *testing.T) {
	saved, err := loadSettings(filepath.Join(t.TempDir(), "settings.json"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	secrets := newShellSecrets(func(ControlMessage) error { return nil }, domain.SecretsInFile, nil, time.Second)
	queued := make(chan struct{}, 4)
	desktop := &build{
		settings: saved, secrets: secrets, gateway: gatewayOfTheTest(t), logger: log.New(io.Discard, "", 0),
		catchUp: func(context.Context) (int, error) { queued <- struct{}{}; return 1, nil },
	}
	helper := ownAI{settings: saved, secrets: secrets}
	heard := func() bool {
		select {
		case <-queued:
			return true
		case <-time.After(300 * time.Millisecond):
			return false
		}
	}

	desktop.AIChanged(context.Background())
	if helper.Connected(context.Background()) || heard() {
		t.Fatal("the app without settings queued the cards")
	}
	deepSeek, err := aisettings.Draft{Source: "own", Own: &aisettings.OwnDraft{Preset: "deepseek", ChatModel: ptr("deepseek-chat"), CardsModel: ptr("deepseek-chat")}}.Settings()
	if err != nil || saved.KeepAI(deepSeek) != nil {
		t.Fatal(err)
	}
	desktop.AIChanged(context.Background())
	if helper.Connected(context.Background()) || heard() {
		t.Fatal("DeepSeek without its key queued the cards")
	}
	secrets.values[domain.SecretOwnAI] = "key-test-connected-0001"
	desktop.AIChanged(context.Background())
	if !helper.Connected(context.Background()) || !heard() {
		t.Fatal("the connected helper did not queue the cards")
	}
}
