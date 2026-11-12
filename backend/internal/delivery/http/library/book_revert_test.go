package library

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/domain/quota"
	service "canonkeeper/backend/internal/services/library"

	"github.com/go-chi/chi/v5"
)

type revertPreviewFunc func(context.Context, domainlibrary.Actor, string) (domainlibrary.BookRevertPreview, error)

func (function revertPreviewFunc) Execute(ctx context.Context, actor domainlibrary.Actor, bookID string) (domainlibrary.BookRevertPreview, error) {
	return function(ctx, actor, bookID)
}

type revertFunc func(context.Context, service.RevertBookUpdateInput) (string, error)

func (function revertFunc) Execute(ctx context.Context, input service.RevertBookUpdateInput) (string, error) {
	return function(ctx, input)
}

func bookRevertRouter(preview PreviewBookRevertUseCase, revert RevertBookUpdateUseCase, user coverWriteStub) http.Handler {
	router := chi.NewRouter()
	MountBookRevert(router, preview, revert, func(_ context.Context, _ domainlibrary.Actor, bookID string) (any, error) {
		return map[string]any{"id": bookID}, nil
	}, user, []string{"https://app.test"})
	return router
}

func revertRequest(router http.Handler, method, body, csrf string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/books/book-1/update-revert", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://app.test")
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

// specs/2026-11-12-ux10-book-revert.md BR2.1: the preview names the chapters of each part and the fingerprint.
func TestThePreviewOfARevertAnswersItsParts(t *testing.T) {
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	router := bookRevertRouter(revertPreviewFunc(func(_ context.Context, actor domainlibrary.Actor, bookID string) (domainlibrary.BookRevertPreview, error) {
		if actor.UserID != "actor-1" || bookID != "book-1" {
			t.Fatalf("actor %s, book %s", actor.UserID, bookID)
		}
		return domainlibrary.BookRevertPreview{
			BookID: bookID, BookTitle: "Том", UpdatedAt: at, RevertUntil: at.Add(7 * 24 * time.Hour), Fingerprint: strings.Repeat("a", 32),
			Text: []domainlibrary.RevertChapter{{ID: "c1", Title: "Глава 1"}}, Place: []domainlibrary.RevertChapter{{ID: "c2", Title: "Новая", PreviousTitle: "Старая"}},
			Returned: []domainlibrary.RevertChapter{}, Removed: []domainlibrary.RevertChapter{{ID: "c4", Title: "Добавленная"}},
			Kept: []domainlibrary.RevertChapter{{ID: "c5", Title: "Правленая", Stay: domainlibrary.StayEdited}},
		}, nil
	}), revertFunc(func(context.Context, service.RevertBookUpdateInput) (string, error) { return "", nil }), coverWriteStub{})
	response := revertRequest(router, http.MethodGet, "", "")
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["revertUntil"] != "2026-11-19T10:00:00.000Z" || got["fingerprint"] != strings.Repeat("a", 32) ||
		len(got["returned"].([]any)) != 0 || got["place"].([]any)[0].(map[string]any)["previousTitle"] != "Старая" ||
		got["kept"].([]any)[0].(map[string]any)["stay"] != "edited" {
		t.Fatalf("preview = %s", response.Body.String())
	}
}

// BR2.2, BR2.4: the return carries the fingerprint and answers the book; every refusal has its code.
func TestTheRevertAnswersTheBookOrItsRefusal(t *testing.T) {
	var input service.RevertBookUpdateInput
	ok := revertFunc(func(_ context.Context, got service.RevertBookUpdateInput) (string, error) {
		input = got
		return "project-1", nil
	})
	never := revertPreviewFunc(func(context.Context, domainlibrary.Actor, string) (domainlibrary.BookRevertPreview, error) {
		return domainlibrary.BookRevertPreview{}, nil
	})
	response := revertRequest(bookRevertRouter(never, ok, coverWriteStub{}), http.MethodPost, `{"fingerprint":"`+strings.Repeat("b", 32)+`"}`, "token")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"book-1"`) || input.Fingerprint != strings.Repeat("b", 32) || input.Actor.UserID != "actor-1" {
		t.Fatalf("%d %s, input %+v", response.Code, response.Body.String(), input)
	}
	for name, test := range map[string]struct {
		err    error
		user   coverWriteStub
		body   string
		status int
		code   string
	}{
		"no fingerprint": {body: `{}`, status: http.StatusBadRequest, code: "request.validation"},
		"no token":       {user: coverWriteStub{err: auth.ErrInvalidCSRF}, status: http.StatusForbidden, code: "csrf.invalid"},
		"gone":           {err: domainlibrary.ErrUpdateGone, status: http.StatusNotFound, code: "book.update_gone"},
		"busy":           {err: domainlibrary.ErrBookBusy, status: http.StatusConflict, code: "book.busy"},
		"changed":        {err: domainlibrary.ErrUpdateChanged, status: http.StatusConflict, code: "book.update_changed"},
		"demo":           {err: quota.ErrDemoSaga, status: http.StatusConflict, code: quota.DemoSagaCode},
		"over the tier":  {err: &quota.Exceeded{Key: quota.KeyProjectCharacters, Limit: 10}, status: http.StatusConflict, code: "quota.project_characters"},
		"reader":         {err: domainlibrary.ErrForbidden, status: http.StatusForbidden, code: "forbidden"},
		"stranger":       {err: domainlibrary.ErrNotFound, status: http.StatusNotFound, code: "not_found"},
	} {
		failing := revertFunc(func(context.Context, service.RevertBookUpdateInput) (string, error) { return "", test.err })
		body := test.body
		if body == "" {
			body = `{"fingerprint":"` + strings.Repeat("c", 32) + `"}`
		}
		response := revertRequest(bookRevertRouter(never, failing, test.user), http.MethodPost, body, "token")
		var problem struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &problem)
		if response.Code != test.status || problem.Code != test.code {
			t.Errorf("%s: %d %s, want %d %s", name, response.Code, response.Body.String(), test.status, test.code)
		}
	}
}
