// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// aiCheck is the answer of getAiCheck as the test reads it.
type aiCheck struct {
	State  string `json:"state"`
	Probes []struct {
		Probe  string `json:"probe"`
		Passed bool   `json:"passed"`
	} `json:"probes"`
	Report *struct {
		Calibration *float64 `json:"calibration"`
		Chat        struct {
			Fit string `json:"fit"`
		} `json:"chat"`
		Books struct {
			Fit string `json:"fit"`
		} `json:"books"`
		Stale bool `json:"stale"`
	} `json:"report"`
}

// specs/2026-11-13-desk-probes.md PB2.1, PB2.2, PB2.5, PB2.6: the built backend of the app with the key the shell handed over
// in start and a stub provider of Chat Completions. The window saves the settings, starts «Проверить связь» and reads it
// until it is done — «годится для чата и для чтения книг» with the calibration; every probe is a row of the journal of
// requests with the purpose probe; getAiSettings carries the report, stale once the model of the chat changed. The key, the
// words of the provider and of the probes stand in no output of the backend and in no file of the data directory.
func TestTheCheckOfTheAppLeavesNoKeyAndNoWords(t *testing.T) {
	binary, resources := desktopBinary(t)
	stub := &checkProvider{chat: true}
	address := startCheckProvider(t, stub)
	data := filepath.Join(t.TempDir(), "data")
	process := startBackend(t, binary, resources, data, map[string]string{"provider:own": checkKey})
	window := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 30 * time.Second}}
	if response := window.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	if response, body := window.do(http.MethodPost, "/api/v1/desktop/ai/check", window.csrf(), nil); response.StatusCode != http.StatusConflict ||
		!containsCode(body, "helper.not_connected") {
		t.Fatalf("a check without the helper = %d %s", response.StatusCode, body)
	}
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), map[string]any{
		"source": "own", "own": map[string]any{"preset": "custom", "baseUrl": address, "protocol": "chat", "chatModel": "north", "cardsModel": "north"},
	}); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings = %d %s", response.StatusCode, body)
	}
	if response, body := window.do(http.MethodPost, "/api/v1/desktop/ai/check", window.csrf(), nil); response.StatusCode != http.StatusAccepted {
		t.Fatalf("startAiCheck = %d %s", response.StatusCode, body)
	}
	var check aiCheck
	deadline := time.Now().Add(60 * time.Second)
	for {
		window.read("/api/v1/desktop/ai/check", &check)
		if check.State != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the check did not end")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if check.State != "done" || check.Report == nil || check.Report.Chat.Fit != "fit" || check.Report.Books.Fit != "fit" ||
		check.Report.Calibration == nil || len(check.Probes) != 5 {
		t.Fatalf("the check = %+v", check)
	}

	var state struct {
		Check *struct {
			Stale bool `json:"stale"`
		} `json:"check"`
	}
	window.read("/api/v1/desktop/ai", &state)
	if state.Check == nil || state.Check.Stale {
		t.Fatalf("getAiSettings after the check = %+v", state.Check)
	}
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), map[string]any{
		"source": "own", "own": map[string]any{"preset": "custom", "baseUrl": address, "protocol": "chat", "chatModel": "north-2", "cardsModel": "north"},
	}); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings = %d %s", response.StatusCode, body)
	}
	window.read("/api/v1/desktop/ai", &state)
	if state.Check == nil || !state.Check.Stale {
		t.Fatalf("getAiSettings after a new model = %+v", state.Check)
	}

	db := database(t, data)
	var probes int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM model_requests WHERE purpose = 'probe' AND job_id IS NULL`).Scan(&probes); err != nil {
		t.Fatal(err)
	}
	if probes != 5 {
		t.Fatalf("the journal holds %d probes", probes)
	}
	_ = db.Close(context.Background())
	process.stop()

	output := process.stderr.Bytes()
	for _, mark := range []string{checkKey, checkMark, "бакен", "Проверка связи", "смотрителя"} {
		if bytes.Contains(output, []byte(mark)) {
			t.Errorf("the output of the backend holds %q", mark)
		}
		if err := filepath.WalkDir(data, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || !entry.Type().IsRegular() {
				return nil
			}
			if raw, err := os.ReadFile(path); err == nil && bytes.Contains(raw, []byte(mark)) {
				t.Errorf("%s holds %q", path, mark)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	var settings map[string]json.RawMessage
	raw, _ := os.ReadFile(filepath.Join(data, "settings.json"))
	if json.Unmarshal(raw, &settings) != nil || !bytes.Contains(settings["ai"], []byte(`"calibration"`)) {
		t.Fatalf("settings.json does not keep the check: %s", settings["ai"])
	}
}

// specs/2026-11-13-desk-probes.md PB1.2, PB2.1: in the built backend the models of the provider, the start and the stop of a
// check have budgets of their own a minute — thirty, ten and thirty — counted before the operation answers; the window
// that spent one still reads the check.
func TestTheOperationsOfTheCheckHaveTheirOwnBudgets(t *testing.T) {
	binary, resources := desktopBinary(t)
	process := startBackend(t, binary, resources, filepath.Join(t.TempDir(), "data"), nil)
	window := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 30 * time.Second}}
	if response := window.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	csrf := window.csrf()
	for _, check := range []struct {
		method, path  string
		limit, answer int
	}{
		// Without the helper the models are ai.not_set and a check is helper.not_connected; nothing runs to stop.
		{http.MethodGet, "/api/v1/desktop/ai/models", 30, http.StatusConflict},
		{http.MethodPost, "/api/v1/desktop/ai/check", 10, http.StatusConflict},
		{http.MethodDelete, "/api/v1/desktop/ai/check", 30, http.StatusNoContent},
	} {
		for attempt := 1; attempt <= check.limit+1; attempt++ {
			want := check.answer
			if attempt > check.limit {
				want = http.StatusTooManyRequests
			}
			if response, body := window.do(check.method, check.path, csrf, nil); response.StatusCode != want {
				t.Fatalf("%s %s, attempt %d = %d %s, want %d", check.method, check.path, attempt, response.StatusCode, body, want)
			}
		}
	}
	var check aiCheck
	window.read("/api/v1/desktop/ai/check", &check)
	if check.State != "idle" {
		t.Fatalf("getAiCheck after the budgets = %+v", check)
	}
}
