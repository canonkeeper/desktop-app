package clienterrors

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

const reportOrigin = "https://canon.test"

const validReport = `{"kind":"chunk","name":"TypeError","surface":"chat","build":"index-Bq3x9kLm.js",` +
	`"frames":["ChatPage-D4f2.js:1:2345"],"browser":"chrome","mobile":false}`

func mountedReports(t *testing.T, user string) (http.Handler, *strings.Builder) {
	t.Helper()
	var logs strings.Builder
	router := chi.NewRouter()
	passThrough := func(next http.Handler) http.Handler { return next }
	NewHandler(log.New(&logs, "", 0), func(*http.Request) string { return user }).Mount(router, passThrough, reportOrigin)
	return router, &logs
}

func postReport(handler http.Handler, body string) *httptest.ResponseRecorder {
	return postReportFrom(handler, body, reportOrigin)
}

func postReportFrom(handler http.Handler, body, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/client-errors", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// R5.3, R5.4: a valid report is logged as one content-free client_error line.
func TestReportIsLoggedAsOneContentFreeLine(t *testing.T) {
	handler, logs := mountedReports(t, "00000000-0000-4000-8000-000000000001")
	response := postReport(handler, validReport)
	if response.Code != http.StatusNoContent || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status = %d cache = %q body = %s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("log lines = %q, want one", lines)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"event": "client_error", "kind": "chunk", "name": "TypeError", "surface": "chat",
		"build": "index-Bq3x9kLm.js", "browser": "chrome", "mobile": false,
		"userId": "00000000-0000-4000-8000-000000000001",
	}
	for key, value := range want {
		if entry[key] != value {
			t.Fatalf("log %s = %v, want %v (line %s)", key, entry[key], value, lines[0])
		}
	}
	if frames, _ := entry["frames"].([]any); len(frames) != 1 || frames[0] != "ChatPage-D4f2.js:1:2345" {
		t.Fatalf("log frames = %v", entry["frames"])
	}
}

// R5.4: anything outside the strict shape is refused and never logged.
// The guest home page reports its errors under its own surface.
func TestReportFromTheGuestHomePageIsAccepted(t *testing.T) {
	handler, logs := mountedReports(t, "")
	response := postReport(handler, strings.Replace(validReport, `"chat"`, `"home"`, 1))
	if response.Code != http.StatusNoContent || !strings.Contains(logs.String(), `"surface":"home"`) {
		t.Fatalf("status = %d log = %s body = %s", response.Code, logs.String(), response.Body.String())
	}
}

// The admin panel (specs/2026-10-29-admin-panel.md R6.1) reports under its own surface.
func TestReportFromTheAdminPanelIsAccepted(t *testing.T) {
	handler, logs := mountedReports(t, "")
	response := postReport(handler, strings.Replace(validReport, `"chat"`, `"admin"`, 1))
	if response.Code != http.StatusNoContent || !strings.Contains(logs.String(), `"surface":"admin"`) {
		t.Fatalf("status = %d log = %s body = %s", response.Code, logs.String(), response.Body.String())
	}
}

// The page «Иллюстрации саги» (specs/2026-10-29-saga-illustrations.md R11.4) reports under its own surface.
func TestReportFromTheIllustrationsPageIsAccepted(t *testing.T) {
	handler, logs := mountedReports(t, "")
	response := postReport(handler, strings.Replace(validReport, `"chat"`, `"illustrations"`, 1))
	if response.Code != http.StatusNoContent || !strings.Contains(logs.String(), `"surface":"illustrations"`) {
		t.Fatalf("status = %d log = %s body = %s", response.Code, logs.String(), response.Body.String())
	}
}

// «Галерея» of the saga (specs/2026-10-29-saga-illustrations.md R11.9) reports under its own surface.
func TestReportFromTheGalleryIsAccepted(t *testing.T) {
	handler, logs := mountedReports(t, "")
	response := postReport(handler, strings.Replace(validReport, `"chat"`, `"gallery"`, 1))
	if response.Code != http.StatusNoContent || !strings.Contains(logs.String(), `"surface":"gallery"`) {
		t.Fatalf("status = %d log = %s body = %s", response.Code, logs.String(), response.Body.String())
	}
}

// «Разрешения на продвижение» and the page of a letter link (specs/2026-10-30-promo-permissions.md)
// report under their own surfaces.
func TestReportFromThePromotionPagesIsAccepted(t *testing.T) {
	for _, surface := range []string{"promo", "promo-link", "compare"} {
		handler, logs := mountedReports(t, "")
		response := postReport(handler, strings.Replace(validReport, `"chat"`, `"`+surface+`"`, 1))
		if response.Code != http.StatusNoContent || !strings.Contains(logs.String(), `"surface":"`+surface+`"`) {
			t.Fatalf("%s: status = %d log = %s body = %s", surface, response.Code, logs.String(), response.Body.String())
		}
	}
}

func TestReportOutsideStrictShapeIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"message field":    strings.Replace(validReport, `"mobile":false`, `"mobile":false,"message":"секрет пользователя"`, 1),
		"text in name":     strings.Replace(validReport, `"TypeError"`, `"Ошибка в главе"`, 1),
		"url as frame":     strings.Replace(validReport, `"ChatPage-D4f2.js:1:2345"`, `"https://canonkeeper.ru/chat?q=x:1:2"`, 1),
		"unknown surface":  strings.Replace(validReport, `"chat"`, `"settings"`, 1),
		"unknown kind":     strings.Replace(validReport, `"chunk"`, `"log"`, 1),
		"six frames":       strings.Replace(validReport, `["ChatPage-D4f2.js:1:2345"]`, `["a.js:1:1","a.js:1:1","a.js:1:1","a.js:1:1","a.js:1:1","a.js:1:1"]`, 1),
		"missing mobile":   strings.Replace(validReport, `,"mobile":false`, ``, 1),
		"trailing content": validReport + `{}`,
		"oversized body":   strings.Replace(validReport, `"build":"index-Bq3x9kLm.js"`, `"build":"index-Bq3x9kLm.js","pad":"`+strings.Repeat("x", 4096)+`"`, 1),
		"not json":         `kind=chunk`,
	} {
		t.Run(name, func(t *testing.T) {
			handler, logs := mountedReports(t, "")
			response := postReport(handler, body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.Code)
			}
			if logs.Len() != 0 {
				t.Fatalf("refused report was logged: %s", logs.String())
			}
		})
	}
}

// R5.4: an anonymous visitor's report has no user.
func TestAnonymousReportHasNoUser(t *testing.T) {
	handler, logs := mountedReports(t, "")
	if response := postReport(handler, validReport); response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(logs.String(), "userId") {
		t.Fatalf("anonymous report names a user: %s", logs.String())
	}
}

// R5.4: a report from another site is refused before it reaches the log.
func TestReportFromAnotherOriginIsRefused(t *testing.T) {
	handler, logs := mountedReports(t, "")
	if response := postReportFrom(handler, validReport, "https://evil.test"); response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	if logs.Len() != 0 {
		t.Fatalf("cross-site report was logged: %s", logs.String())
	}
}
