// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// desktopState is the answer of getDesktopState.
type desktopState struct {
	Version *string `json:"version"`
	DataDir string  `json:"dataDir"`
	Usage   struct {
		Bytes    *int64 `json:"bytes"`
		Counting bool   `json:"counting"`
	} `json:"usage"`
	Backups []struct {
		MadeAt  string  `json:"madeAt"`
		Version *string `json:"version"`
	} `json:"backups"`
	Network []struct {
		Host string `json:"host"`
	} `json:"network"`
}

// stateOfTheWindow reads getDesktopState until the place of the data is counted. How long a request waits for the count is
// the unit test's (TestTheCountOfThePlaceAnswersWithinItsLimit): a request through the process measured by the clock
// would answer for the load of the machine.
func stateOfTheWindow(t *testing.T, window *client) desktopState {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var state desktopState
		window.read("/api/v1/desktop/state", &state)
		if !state.Usage.Counting || time.Now().After(deadline) {
			return state
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// specs/2026-11-13-desk-settings.md ST2.1, ST2.3, ST4.2, ST4.3: the built backend of the app answers its window the
// version it was built with, the data directory as the author sees it, the place it takes, no copies after the first
// start and no host of the network; after an update its copy before the migrations is listed with the version of the
// app that made it, beside an older copy made before the copies named their version. Without the session of the window
// it is the 401 of the site.
func TestTheSettingsOfTheAppReadItsDataAndItsCopies(t *testing.T) {
	_, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")

	first := startBackend(t, program(t, programVariant{name: "tree-1.1.3", version: "1.1.3"}), resources, data, nil)
	stranger := &client{t: t, origin: first.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	if response, body := stranger.do(http.MethodGet, "/api/v1/desktop/state", "", nil); response.StatusCode != http.StatusUnauthorized ||
		!containsCode(body, "auth.session_required") {
		t.Fatalf("getDesktopState without the session = %d %s", response.StatusCode, body)
	}
	window := &client{t: t, origin: first.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	if response := window.open(first.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	state := stateOfTheWindow(t, window)
	if state.Version == nil || *state.Version != "1.1.3" || state.DataDir != data || state.Usage.Counting ||
		state.Usage.Bytes == nil || *state.Usage.Bytes <= 0 || len(state.Backups) != 0 || len(state.Network) != 0 {
		encoded, _ := json.Marshal(state)
		t.Fatalf("the state after the first start = %s", encoded)
	}
	first.stop()

	makeCopy(t, DataDir{Root: data}.Backups(), "pre-7-20260101T000000.000Z", true)
	updated := time.Now().UTC()
	second := startBackend(t, program(t, programVariant{name: "ahead-1-1.1.4", ahead: 1, version: "1.1.4"}), resources, data, nil)
	window = &client{t: t, origin: second.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	if response := window.open(second.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	state = stateOfTheWindow(t, window)
	if state.Version == nil || *state.Version != "1.1.4" || len(state.Backups) != 2 {
		encoded, _ := json.Marshal(state)
		t.Fatalf("the state after the update = %s", encoded)
	}
	made, err := time.Parse(time.RFC3339Nano, state.Backups[0].MadeAt)
	if err != nil || made.Before(updated.Add(-time.Second)) || made.After(time.Now().Add(time.Second)) ||
		state.Backups[0].Version == nil || *state.Backups[0].Version != "1.1.4" {
		t.Fatalf("the copy of the update = %+v (%v), made after %s", state.Backups[0], err, updated)
	}
	if state.Backups[1].MadeAt != "2026-01-01T00:00:00.000Z" || state.Backups[1].Version != nil {
		t.Fatalf("the copy made before the copies named their version = %+v", state.Backups[1])
	}
	second.stop()
}
