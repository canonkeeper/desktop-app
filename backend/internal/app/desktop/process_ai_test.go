// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
)

// aiState is the answer of getAiSettings.
type aiState struct {
	Settings struct {
		Source string `json:"source"`
		Own    *struct {
			Preset    string `json:"preset"`
			BaseURL   string `json:"baseUrl"`
			ChatModel string `json:"chatModel"`
		} `json:"own"`
	} `json:"settings"`
	KeySaved     bool   `json:"keySaved"`
	SecretsStore string `json:"secretsStore"`
}

// answer is a request of the window that runs while the test plays the shell.
type answer struct {
	status int
	body   []byte
}

func (browser *client) async(method, path, csrf string, body any) <-chan answer {
	done := make(chan answer, 1)
	go func() {
		response, raw := browser.do(method, path, csrf, body)
		done <- answer{status: response.StatusCode, body: raw}
	}()
	return done
}

// holdsTheKey fails the test when a file of the data directory — settings.json, the logs, the cluster — holds the key.
func holdsTheKey(t *testing.T, data string, key []byte) {
	t.Helper()
	err := filepath.WalkDir(data, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return nil
		}
		if raw, err := os.ReadFile(path); err == nil && bytes.Contains(raw, key) {
			t.Errorf("%s holds the key", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// specs/2026-11-13-desk-secrets.md SE2.3–SE2.8: the built backend of the app, the test playing its shell. The settings of
// «Свой ИИ» go into settings.json beside the port; the key goes to the shell with secret-save and is held only after
// secret-result ok — a silent shell is ai.key_not_saved after 5 s; the next start holds the key the shell hands over in
// start. The key is in no answer, in no output of the backend and in no file of the data directory. The helper stays
// not connected: the reading of a saga waits for it. Writes take the session, the CSRF token and the Origin of the
// window, and the thirty-first key of a minute is 429.
func TestTheOwnAIOfTheAppIsKeptAndItsKeyGoesOneWay(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	const key = "key-of-the-test-north-pass-7f3a0c19"
	process := startBackend(t, binary, resources, data, nil)
	port := process.ready.Port
	window := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: desktoptest.Waited(30 * time.Second)}}
	if response := window.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	var state aiState
	window.read("/api/v1/desktop/ai", &state)
	if state.Settings.Source != "none" || state.Settings.Own != nil || state.KeySaved || state.SecretsStore != "file" {
		t.Fatalf("the first state = %+v", state)
	}

	// The guards of a write: a stranger without the session, the window without its token, another origin.
	stranger := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: desktoptest.Waited(10 * time.Second)}}
	if response, body := stranger.do(http.MethodPut, "/api/v1/desktop/ai/key", stranger.csrf(), map[string]string{"key": key}); response.StatusCode != http.StatusUnauthorized ||
		!containsCode(body, "auth.session_required") {
		t.Fatalf("setAiKey without the session = %d %s", response.StatusCode, body)
	}
	if response, _ := window.do(http.MethodPut, "/api/v1/desktop/ai", "", map[string]string{"source": "none"}); response.StatusCode != http.StatusForbidden {
		t.Fatalf("updateAiSettings without CSRF = %d", response.StatusCode)
	}
	elsewhere, _ := http.NewRequest(http.MethodDelete, process.origin()+"/api/v1/desktop/ai/key", nil)
	elsewhere.Header.Set("Origin", "http://evil.example")
	elsewhere.Header.Set("X-CSRF-Token", window.csrf())
	elsewhere.AddCookie(&http.Cookie{Name: "ck_session", Value: window.cookie})
	if response, err := window.http.Do(elsewhere); err != nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("deleteAiKey from another origin = %v, %v", response, err)
	} else {
		_ = response.Body.Close()
	}

	response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), map[string]any{
		"source": "own", "own": map[string]any{"preset": "custom", "baseUrl": "https://gateway.example/v1", "chatModel": "north-chat"},
	})
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &state) != nil || state.Settings.Own == nil ||
		state.Settings.Own.BaseURL != "https://gateway.example/v1" {
		t.Fatalf("updateAiSettings = %d %s", response.StatusCode, body)
	}
	remembered, values := rememberedPort(t, data)
	if remembered != port || compact(t, values["ai"]) == "" || !bytes.Contains(values["ai"], []byte("north-chat")) {
		t.Fatalf("settings.json after the settings = %v", values)
	}

	asked := window.async(http.MethodPut, "/api/v1/desktop/ai/key", window.csrf(), map[string]string{"key": key})
	save := process.receive(10 * time.Second)
	if save.Type != messageSecretSave || save.ID != "provider:own" || save.Value != key || save.Request == "" {
		t.Fatalf("the shell heard %s %s", save.Type, save.ID)
	}
	process.send(ControlMessage{Type: messageSecretResult, Request: save.Request, OK: true})
	if answered := <-asked; answered.status != http.StatusNoContent {
		t.Fatalf("setAiKey = %d %s", answered.status, answered.body)
	}
	body = window.read("/api/v1/desktop/ai", &state)
	if !state.KeySaved || bytes.Contains(body, []byte(key)) {
		t.Fatalf("getAiSettings after the key = %s", body)
	}

	// The shell is silent: the backend answers ai.key_not_saved after 5 s and holds the key it held.
	silent := window.async(http.MethodPut, "/api/v1/desktop/ai/key", window.csrf(), map[string]string{"key": key + "-second"})
	if unanswered := process.receive(10 * time.Second); unanswered.Type != messageSecretSave {
		t.Fatalf("the shell heard %s", unanswered.Type)
	}
	started := time.Now()
	if answered := <-silent; answered.status != http.StatusServiceUnavailable || !containsCode(answered.body, "ai.key_not_saved") ||
		bytes.Contains(answered.body, []byte(key)) {
		t.Fatalf("setAiKey to a silent shell = %d %s", answered.status, answered.body)
	}
	if waited := time.Since(started); waited > 10*time.Second {
		t.Fatalf("the backend waited %s for the shell", waited)
	}

	// SE2.8: the helper stays not connected — the reading of a saga waits for it.
	response, body = window.do(http.MethodPost, "/api/v1/projects", window.csrf(), map[string]string{"title": "Северный предел"})
	var project struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &project) != nil {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	window.importFile(project.ID, "Застава у перевала.md", northernBook)
	deadline := desktoptest.Deadline(90 * time.Second)
	for {
		var status struct {
			Preparation *struct {
				Waiting       string `json:"waiting"`
				ChaptersTotal int    `json:"chaptersTotal"`
			} `json:"preparation"`
		}
		window.read("/api/v1/projects/"+project.ID+"/indexing-status", &status)
		if status.Preparation != nil && status.Preparation.ChaptersTotal == 2 {
			if status.Preparation.Waiting != "helper" {
				t.Fatalf("the preparation with the settings and the key = %+v", status.Preparation)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the book was not read: %+v", status.Preparation)
		}
		time.Sleep(200 * time.Millisecond)
	}
	process.stop()

	// The next start: the shell hands the key over in start.
	again := startBackend(t, binary, resources, data, map[string]string{"provider:own": key})
	window = &client{t: t, origin: again.origin(), http: &http.Client{Timeout: desktoptest.Waited(10 * time.Second)}}
	if response := window.open(again.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	window.read("/api/v1/desktop/ai", &state)
	if !state.KeySaved || state.Settings.Source != "own" || state.Settings.Own == nil || state.Settings.Own.ChatModel != "north-chat" {
		t.Fatalf("the state after the restart = %+v", state)
	}
	csrf := window.csrf()
	for attempt := 1; attempt <= 31; attempt++ {
		response, _ := window.do(http.MethodPut, "/api/v1/desktop/ai/key", csrf, map[string]string{"key": "not a key"})
		want := http.StatusBadRequest
		if attempt == 31 {
			want = http.StatusTooManyRequests
		}
		if response.StatusCode != want {
			t.Fatalf("setAiKey %d of the minute = %d; want %d", attempt, response.StatusCode, want)
		}
	}
	again.stop()

	if bytes.Contains(process.stderr.Bytes(), []byte(key)) || bytes.Contains(again.stderr.Bytes(), []byte(key)) {
		t.Fatal("the output of the backend holds the key")
	}
	holdsTheKey(t, data, []byte(key))
}
