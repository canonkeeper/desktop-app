// SPDX-License-Identifier: AGPL-3.0-only

package desktoptest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// Window is the page of the window of a process as a test drives it: the Origin of the window on every request, its
// session once a one-time code opened it, and the CSRF token of the session for its writes.
type Window struct {
	t      testing.TB
	Origin string
	client *http.Client
	cookie string
}

// OpenWindow opens the window of the process listening on 127.0.0.1:port with a one-time code of the channel.
func OpenWindow(t testing.TB, port int, code string) *Window {
	t.Helper()
	window := &Window{t: t, Origin: "http://127.0.0.1:" + strconv.Itoa(port), client: &http.Client{Timeout: Waited(60 * time.Second)}}
	response, body := window.Do(http.MethodPost, "/api/v1/desktop/session", window.CSRF(), map[string]string{"code": code})
	for _, cookie := range response.Cookies() {
		if cookie.Name == "ck_session" {
			window.cookie = cookie.Value
		}
	}
	if response.StatusCode != http.StatusOK || window.cookie == "" {
		t.Fatalf("openDesktopSession = %d %s", response.StatusCode, body)
	}
	return window
}

// Do sends one request of the page; csrf is the token of a write, empty for a read.
func (window *Window) Do(method, path, csrf string, body any) (*http.Response, []byte) {
	window.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, window.Origin+path, reader)
	if err != nil {
		window.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	return window.Send(request)
}

// Send sends a request the test built — a file of a book, say — as the page sends it: with the Origin of the window and
// its session.
func (window *Window) Send(request *http.Request) (*http.Response, []byte) {
	window.t.Helper()
	request.Header.Set("Origin", window.Origin)
	if window.cookie != "" {
		request.AddCookie(&http.Cookie{Name: "ck_session", Value: window.cookie})
	}
	response, err := window.client.Do(request)
	if err != nil {
		window.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response, raw
}

// CSRF is the token of the session of the window for its writes.
func (window *Window) CSRF() string {
	window.t.Helper()
	response, body := window.Do(http.MethodGet, "/api/v1/auth/csrf", "", nil)
	var token struct {
		CSRFToken string `json:"csrfToken"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &token) != nil || token.CSRFToken == "" {
		window.t.Fatalf("csrf = %d %s", response.StatusCode, body)
	}
	return token.CSRFToken
}
