// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/config"
	domain "canonkeeper/backend/internal/domain/desktop"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	librarystore "canonkeeper/backend/internal/store/library"
)

const testHost = "127.0.0.1:43123"

// desktopHandler is the root handler of the desktop over its real router on a core that connects nothing: a request the
// guards let through to a handler would need the database, so every answer here comes from the guards or the page.
func desktopHandler(t *testing.T, page fstest.MapFS) http.Handler {
	t.Helper()
	db := &storedb.DB{}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	model, err := modelapi.New(modelapi.Config{Model: chatModel, CardsModel: cardsModel})
	if err != nil {
		t.Fatal(err)
	}
	desktop := &build{host: testHost, codes: domain.NewOpenCodes(time.Now), files: page}
	routes, err := app.Routes(app.Core{
		Config: config.Config{AllowedOrigins: []string{"http://" + testHost}}, Logger: log.New(io.Discard, "", 0), DB: db,
		Files: root, Codex: &app.CodexClients{}, Model: model, Jobs: jobstore.New(db), Library: librarystore.New(db),
	}, desktop.extension(nil))
	if err != nil {
		t.Fatal(err)
	}
	return desktop.serve(routes)
}

func serveRequest(handler http.Handler, method, target, host string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(`{}`))
	request.Host = host
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// specs/2026-11-07-desktop-app.md DA3.4: a Host other than 127.0.0.1:P — another site a DNS rebinding pointed here, or
// localhost — gets 421 without a body, before any page or operation.
func TestARequestForAnotherHostGets421(t *testing.T) {
	handler := desktopHandler(t, fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}})
	for _, host := range []string{"evil.example", "localhost:43123", "127.0.0.1", "127.0.0.1:1", "[::1]:43123"} {
		for _, target := range []string{"/", "/api/v1/projects", "/healthz", "/api/v1/desktop/session"} {
			response := serveRequest(handler, http.MethodGet, target, host, nil)
			if response.Code != http.StatusMisdirectedRequest || response.Body.Len() != 0 {
				t.Errorf("Host %s, %s = %d %q; want 421 without a body", host, target, response.Code, response.Body.String())
			}
		}
	}
	if response := serveRequest(handler, http.MethodGet, "/healthz", testHost, nil); response.Code != http.StatusOK {
		t.Fatalf("Host %s = %d", testHost, response.Code)
	}
}

// DA3.4: a write and the WebSocket of the timeline need the Origin of the window; another origin is 403 before any
// session, CSRF or operation, and no CORS header is ever sent.
func TestAWriteOrTheTimelineFromAnotherOriginIsRefused(t *testing.T) {
	handler := desktopHandler(t, fstest.MapFS{})
	for _, request := range []struct{ method, target string }{
		{http.MethodPost, "/api/v1/projects"},
		{http.MethodPost, "/api/v1/desktop/session"},
		{http.MethodGet, "/api/v1/projects/00000000-0000-4000-8000-000000000001/jobs/00000000-0000-4000-8000-000000000002/agent-timeline/stream"},
	} {
		for _, origin := range []string{"http://evil.example", "http://localhost:43123", "null"} {
			response := serveRequest(handler, request.method, request.target, testHost, map[string]string{
				"Origin": origin, "Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13",
				"Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==", "Content-Type": "application/json",
			})
			if response.Code != http.StatusForbidden {
				t.Errorf("%s %s from %s = %d; want 403", request.method, request.target, origin, response.Code)
			}
			if allow := response.Header().Get("Access-Control-Allow-Origin"); allow != "" {
				t.Errorf("%s %s answered CORS %q", request.method, request.target, allow)
			}
		}
	}
	// No Origin and no same-origin fetch metadata is no window either.
	if response := serveRequest(handler, http.MethodPost, "/api/v1/desktop/session", testHost, nil); response.Code != http.StatusForbidden {
		t.Fatalf("a session without Origin = %d; want 403", response.Code)
	}
	// The window with its Origin and no CSRF token is refused too.
	if response := serveRequest(handler, http.MethodPost, "/api/v1/desktop/session", testHost, map[string]string{
		"Origin": "http://" + testHost,
	}); response.Code != http.StatusForbidden {
		t.Fatalf("a session without CSRF = %d; want 403", response.Code)
	}
}

// DA3.6: the page comes from the embedded build with the headers of the site without HSTS, the hashed assets for a
// year, any other path the index for the router of the page; without a build the page says so and the API works on.
func TestThePageIsServedWithTheHeadersOfTheSite(t *testing.T) {
	handler := desktopHandler(t, fstest.MapFS{
		"index.html":      {Data: []byte("<!doctype html><title>CanonKeeper</title>")},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
		".vite/manifest":  {Data: []byte("{}")},
		"README.md":       {Data: []byte("readme")},
	})
	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
			"font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'",
		"X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer",
		"Permissions-Policy": "camera=(), microphone=(), geolocation=(), payment=()",
	}
	for _, target := range []string{"/", "/desktop/open", "/projects/x/chat", "/assets/app-1.js", "/healthz", "/README.md", "/.vite/manifest"} {
		response := serveRequest(handler, http.MethodGet, target, testHost, nil)
		if response.Code != http.StatusOK {
			t.Errorf("%s = %d", target, response.Code)
		}
		for name, value := range want {
			if got := response.Header().Get(name); got != value {
				t.Errorf("%s header %s = %q, want %q", target, name, got, value)
			}
		}
		if hsts := response.Header().Get("Strict-Transport-Security"); hsts != "" {
			t.Errorf("%s sent HSTS %q", target, hsts)
		}
	}
	if response := serveRequest(handler, http.MethodGet, "/desktop/open", testHost, nil); !strings.Contains(response.Body.String(), "<title>CanonKeeper") ||
		response.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("the window's address = %q, cache %q", response.Body.String(), response.Header().Get("Cache-Control"))
	}
	if response := serveRequest(handler, http.MethodGet, "/assets/app-1.js", testHost, nil); response.Body.String() != "console.log(1)" ||
		response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("an asset = %q, cache %q", response.Body.String(), response.Header().Get("Cache-Control"))
	}
	for _, hidden := range []string{"/README.md", "/.vite/manifest"} {
		if response := serveRequest(handler, http.MethodGet, hidden, testHost, nil); strings.Contains(response.Body.String(), "readme") || response.Body.String() == "{}" {
			t.Fatalf("%s served itself", hidden)
		}
	}
	if response := serveRequest(handler, http.MethodPost, "/desktop/open", testHost, nil); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST to the page = %d", response.Code)
	}
	unbuilt := desktopHandler(t, fstest.MapFS{"README.md": {Data: []byte("readme")}})
	if response := serveRequest(unbuilt, http.MethodGet, "/", testHost, nil); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("the page without a build = %d", response.Code)
	}
}

// specs/2026-11-12-desk-window.md WN1.1: «кто автор» is an operation of the open core the desktop answers itself — without
// the session of the window it is the 401 of the site, not the 404 that left the window empty.
func TestTheWindowAsksWhoTheAuthorIs(t *testing.T) {
	handler := desktopHandler(t, fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}})
	response := serveRequest(handler, http.MethodGet, "/api/v1/auth/session", testHost, nil)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"auth.session_required"`) {
		t.Fatalf("getCurrentUser without a session = %d %s", response.Code, response.Body.String())
	}
}
