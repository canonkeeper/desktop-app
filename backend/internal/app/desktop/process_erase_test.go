// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// specs/2026-11-13-desk-erase.md ER1.2: the built backend of the app, the test playing its shell. Another word is
// desktop.erase_unconfirmed and writes nothing to the shell; the word writes one line erase and the backend removes
// nothing itself; the shutdown the shell sends after it ends the backend cleanly, its data still in place for the shell.
func TestTheWordOfTheWindowAsksTheShellToEraseAndTheBackendRemovesNothing(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	process := startBackend(t, binary, resources, data, nil)
	window := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 30 * time.Second}}
	if response := window.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}

	if response, _ := window.do(http.MethodPost, "/api/v1/desktop/erase", "", map[string]string{"confirm": "стереть"}); response.StatusCode != http.StatusForbidden {
		t.Fatalf("eraseDesktopData without CSRF = %d", response.StatusCode)
	}
	stranger := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	if response, body := stranger.do(http.MethodPost, "/api/v1/desktop/erase", stranger.csrf(), map[string]string{"confirm": "стереть"}); response.StatusCode != http.StatusUnauthorized ||
		!containsCode(body, "auth.session_required") {
		t.Fatalf("eraseDesktopData without the session = %d %s", response.StatusCode, body)
	}
	if response, body := window.do(http.MethodPost, "/api/v1/desktop/erase", window.csrf(), map[string]string{"confirm": "удалить"}); response.StatusCode != http.StatusBadRequest ||
		!containsCode(body, "desktop.erase_unconfirmed") {
		t.Fatalf("another word = %d %s", response.StatusCode, body)
	}

	// The next line of the channel is the erase of the word: the refused requests wrote none.
	if response, body := window.do(http.MethodPost, "/api/v1/desktop/erase", window.csrf(), map[string]string{"confirm": " СТЕРЕТЬ "}); response.StatusCode != http.StatusAccepted {
		t.Fatalf("the word = %d %s", response.StatusCode, body)
	}
	if erase := process.receive(10 * time.Second); erase.Type != messageErase {
		t.Fatalf("the shell heard %q instead of erase", erase.Type)
	}
	for _, kept := range []string{"pg", "settings.json", "logs"} {
		if _, err := os.Stat(filepath.Join(data, kept)); err != nil {
			t.Fatalf("the backend removed %s itself: %v", kept, err)
		}
	}
	process.send(ControlMessage{Type: messageShutdown})
	process.wait(stopClusterTime)
}
