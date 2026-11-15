// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"canonkeeper/backend/internal/adapters/tempfiles"
	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
	service "canonkeeper/backend/internal/services/sagaarchive"

	"github.com/go-chi/chi/v5"
)

const origin = "http://127.0.0.1:24680"

// opener keeps what it was asked to open.
type opener struct {
	inputs []service.OpenInput
	body   []byte
}

func (open *opener) Execute(_ context.Context, input service.OpenInput) (service.Opened, error) {
	size, _ := input.File.Size()
	body := make([]byte, size)
	_, _ = input.File.ReadAt(body, 0)
	_ = input.File.Remove()
	open.inputs, open.body = append(open.inputs, input), body
	if string(body) == "not a saga" {
		return service.Opened{}, &domain.Refusal{Code: domain.CodeNotArchive}
	}
	return service.Opened{ProjectID: "saga", Name: "Северный предел", Report: service.Report{ChatsNotRead: []string{}, CoversSkipped: []string{}, Sections: []string{}}}, nil
}

func form(t *testing.T, content string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "Северный предел.cksaga")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, content)
	_ = writer.Close()
	return &body, writer.FormDataContentType()
}

// specs/2026-11-13-desk-sagafile.md SF3.12: the open needs the session of the window, its Origin, its request-forgery token
// and one key of a repeat; the file reaches the use case whole through the disk, and the temporary file is gone after.
func TestTheOpenOfAFileIsAWriteOfTheWindow(t *testing.T) {
	directory, err := tempfiles.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	open := &opener{}
	router := chi.NewRouter()
	MountOpen(router, open, temp{directory}, sessions{}, []string{origin})
	send := func(change func(*http.Request), content string) *httptest.ResponseRecorder {
		body, contentType := form(t, content)
		request := httptest.NewRequest(http.MethodPost, "/saga-archives", body)
		request.Header.Set("Content-Type", contentType)
		request.Header.Set("Origin", origin)
		request.Header.Set("X-CSRF-Token", "token")
		request.Header.Set("Idempotency-Key", "open-the-saga-1")
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "author"})
		if change != nil {
			change(request)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	refusals := map[string]struct {
		change func(*http.Request)
		status int
	}{
		"no session":  {func(request *http.Request) { request.Header.Del("Cookie") }, http.StatusUnauthorized},
		"no Origin":   {func(request *http.Request) { request.Header.Del("Origin") }, http.StatusForbidden},
		"no token":    {func(request *http.Request) { request.Header.Del("X-CSRF-Token") }, http.StatusForbidden},
		"no key":      {func(request *http.Request) { request.Header.Del("Idempotency-Key") }, http.StatusBadRequest},
		"two keys":    {func(request *http.Request) { request.Header.Add("Idempotency-Key", "open-the-saga-2") }, http.StatusBadRequest},
		"a short key": {func(request *http.Request) { request.Header.Set("Idempotency-Key", "short") }, http.StatusBadRequest},
	}
	for name, item := range refusals {
		if response := send(item.change, "PK"); response.Code != item.status {
			t.Fatalf("%s = %d %s; want %d", name, response.Code, response.Body, item.status)
		}
	}
	if len(open.inputs) != 0 {
		t.Fatalf("a refused request reached the open: %+v", open.inputs)
	}
	response := send(nil, "the bytes of a saga file")
	if response.Code != http.StatusCreated || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("the open = %d %s", response.Code, response.Body)
	}
	var opened service.Opened
	if err := json.Unmarshal(response.Body.Bytes(), &opened); err != nil || opened.ProjectID != "saga" {
		t.Fatalf("the answer = %s, %v", response.Body, err)
	}
	input := open.inputs[0]
	if string(open.body) != "the bytes of a saga file" || input.ActorUserID != "author" || input.IdempotencyKey != "open-the-saga-1" || len(input.FileSHA256) != 64 {
		t.Fatalf("what the open got = %+v, %q", input, open.body)
	}
	if response := send(nil, "not a saga"); response.Code != http.StatusUnprocessableEntity || !bytes.Contains(response.Body.Bytes(), []byte(domain.CodeNotArchive)) {
		t.Fatalf("a file that is not a saga = %d %s", response.Code, response.Body)
	}
	if entries, err := os.ReadDir(directory.Path()); err != nil || len(entries) != 0 {
		t.Fatalf("the temporary files after = %v, %v", entries, err)
	}
}
