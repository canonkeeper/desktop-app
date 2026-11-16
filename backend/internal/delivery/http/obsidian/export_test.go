// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"

	"github.com/go-chi/chi/v5"
)

// exports answers an export by the saga it is asked of and keeps what it was asked.
type exports struct{}

var askedExport service.ExportInput

func (exports) Execute(_ context.Context, input service.ExportInput) (service.Exported, error) {
	askedExport = input
	switch input.ProjectID {
	case "updating":
		return service.Exported{}, domain.MapNotReady{Reason: domain.MapUpdating}
	case "unread":
		return service.Exported{}, domain.MapNotReady{Reason: domain.MapNoCards}
	case "unsafe":
		return service.Exported{}, domain.ErrExportDirUnsafe
	case "broken":
		return service.Exported{}, domain.ErrExportManifestUnreadable
	case "busy":
		return service.Exported{}, service.Busy{RetryAfter: 5 * time.Second}
	case "unlinked":
		return service.Exported{}, service.ErrNotLinked
	case "reader":
		return service.Exported{}, service.ErrForbidden
	case "ended":
		return service.Exported{}, auth.ErrUnauthenticated
	}
	exported := service.Exported{
		Folder: "CanonKeeper/Северный предел", At: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC), Complete: input.ProjectID != "partial",
		Skipped: map[domain.ExportSkip]service.ExportGroup{domain.ExportLink: {Count: 1, Names: []string{"Места/Ссылка.md"}}, domain.ExportCloudOnly: {Count: 1, Names: []string{"Герои/Облако.md"}}},
	}
	exported.Created = service.ExportGroup{Count: 1, Names: []string{"Герои/Мара.md"}}
	exported.Kept = service.ExportGroup{Count: 1, Names: []string{"Герои/Сава.md"}}
	return exported, nil
}

// exported is the state of a saga after its export: its folder and the time of the last one that ended.
type exported struct{}

func (exported) Execute(context.Context, string, string) (service.VaultState, error) {
	at := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	return service.VaultState{Linked: true, Path: "/home/author/Северный предел", Name: "Северный предел", ExportDir: "CanonKeeper/Северный предел", ExportedAt: &at}, nil
}

// OE1.8: getObsidianVault names the folder of the export and the time of the last export that ended.
func TestTheVaultNamesItsExport(t *testing.T) {
	router := chi.NewRouter()
	Handlers{
		Read: exported{}, Begin: begin{}, Unlink: unlink{}, Scan: scan{}, Preview: preview{}, Load: load{}, Update: update{},
		LinkBook: bookFolders{}, UnlinkBook: bookFolders{}, Materials: materials{}, Export: exports{}, Users: sessions{},
		AllowedOrigins: []string{origin},
	}.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/projects/saga/obsidian", nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "author"})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if body := recorder.Body.String(); recorder.Code != http.StatusOK ||
		!strings.Contains(body, `"exportDir":"CanonKeeper/Северный предел","lastExportAt":"2026-11-15T10:00:00Z"`) {
		t.Fatalf("the vault = %d %s", recorder.Code, body)
	}
}

func exportOf(project, body string, change func(*http.Request)) *httptest.ResponseRecorder {
	return askWith(http.MethodPost, "/projects/"+project+"/obsidian/export", body, func(request *http.Request) {
		request.Header.Set("Idempotency-Key", "export-key-1")
		if change != nil {
			change(request)
		}
	})
}

// specs/2026-11-15-desk-obsidian-export.md OE1.1, OE1.8, Data: an export is a change of the window — its Origin, its token, its
// session and a key —, its body one word of the author; the answer names the folder, the time, whether it ended and the
// references by groups and reasons; the refusals of the map, of the folder and of its manifest have their codes.
func TestTheExportIsAChangeOfTheWindowWithItsAnswer(t *testing.T) {
	if response := exportOf("saga", `{}`, func(request *http.Request) { request.Header.Set("Origin", "https://example.test") }); response.Code != http.StatusForbidden {
		t.Fatalf("from another Origin = %d", response.Code)
	}
	if response := exportOf("saga", `{}`, func(request *http.Request) { request.Header.Set("X-CSRF-Token", "other") }); response.Code != http.StatusForbidden {
		t.Fatalf("without the token = %d", response.Code)
	}
	if response := exportOf("saga", `{}`, func(request *http.Request) { request.Header.Del("Idempotency-Key") }); response.Code != http.StatusBadRequest {
		t.Fatalf("without a key = %d %s", response.Code, response.Body)
	}
	for _, body := range []string{`{"replaceChanged":"yes"}`, `{"other":true}`, `{}{}`, `[]`} {
		if response := exportOf("saga", body, nil); response.Code != http.StatusBadRequest || code(t, response) != "request.validation" {
			t.Fatalf("the body %s = %d %s", body, response.Code, response.Body)
		}
	}
	response := exportOf("saga", `{"replaceChanged":true}`, nil)
	if response.Code != http.StatusOK || !askedExport.ReplaceChanged || askedExport.Key != "export-key-1" || askedExport.ActorUserID != "author" {
		t.Fatalf("an export = %d %s, asked %+v", response.Code, response.Body, askedExport)
	}
	want := `{"folder":"CanonKeeper/Северный предел","at":"2026-11-15T10:00:00Z","complete":true,` +
		`"created":{"count":1,"names":["Герои/Мара.md"]},"updated":{"count":0,"names":[]},"unchanged":{"count":0,"names":[]},` +
		`"moved":{"count":0,"names":[]},"kept":{"count":1,"names":["Герои/Сава.md"]},` +
		`"skipped":[{"reason":"cloud_only","count":1,"names":["Герои/Облако.md"]},{"reason":"link","count":1,"names":["Места/Ссылка.md"]}]}`
	if strings.TrimSpace(response.Body.String()) != want || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("the answer = %s", response.Body)
	}
	// The body is required, as the contract says; the object without the word leaves what the author changed.
	if response := exportOf("saga", ``, nil); response.Code != http.StatusBadRequest || code(t, response) != "request.validation" {
		t.Fatalf("an export without a body = %d %s", response.Code, response.Body)
	}
	if response := exportOf("saga", `{}`, nil); response.Code != http.StatusOK || askedExport.ReplaceChanged {
		t.Fatalf("an export without the word = %d %s", response.Code, response.Body)
	}
	var partial struct {
		Complete bool `json:"complete"`
	}
	if response := exportOf("partial", `{}`, nil); response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &partial) != nil || partial.Complete {
		t.Fatalf("a partial export = %d %s", response.Code, response.Body)
	}
	for project, refusal := range map[string]struct {
		status       int
		code, reason string
	}{
		"updating": {http.StatusConflict, domain.CodeMapNotReady, "updating"},
		"unread":   {http.StatusConflict, domain.CodeMapNotReady, "no_cards"},
		"unsafe":   {http.StatusConflict, domain.CodeExportDirUnsafe, ""},
		"broken":   {http.StatusConflict, domain.CodeExportManifestUnreadable, ""},
		"busy":     {http.StatusTooManyRequests, domain.CodeBusy, ""},
		"unlinked": {http.StatusConflict, domain.CodeNotLinked, ""},
		"reader":   {http.StatusForbidden, "forbidden", ""},
		// A session that ended between the check of the request and the transaction of the start.
		"ended": {http.StatusUnauthorized, "auth.session_required", ""},
	} {
		response := exportOf(project, `{}`, nil)
		var problem struct {
			Code   string `json:"code"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || response.Code != refusal.status || problem.Code != refusal.code ||
			problem.Reason != refusal.reason {
			t.Fatalf("%s = %d %s", project, response.Code, response.Body)
		}
	}
}
