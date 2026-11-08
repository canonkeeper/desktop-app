package library

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	service "canonkeeper/backend/internal/services/library"

	"github.com/go-chi/chi/v5"
)

type coverFromImageFunc func(context.Context, service.SetCoverFromImageInput) (domainlibrary.CoverActivation, error)

func (function coverFromImageFunc) Execute(ctx context.Context, input service.SetCoverFromImageInput) (domainlibrary.CoverActivation, error) {
	return function(ctx, input)
}

type coverWriteStub struct{ err error }

func (stub coverWriteStub) CurrentUser(context.Context, string) (auth.User, error) {
	return auth.User{ID: "actor-1"}, stub.err
}

func (stub coverWriteStub) AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error) {
	return auth.User{ID: "actor-1"}, auth.SessionWriteProof{}, stub.err
}

func coverFromImageRouter(useCase CoverFromImageUseCase, user coverWriteStub) http.Handler {
	router := chi.NewRouter()
	MountCoverFromImage(router, useCase, func(_ context.Context, _ domainlibrary.Actor, bookID string, coverURL *string) (any, error) {
		return map[string]any{"id": bookID, "coverUrl": coverURL}, nil
	}, user, []string{"https://app.test"})
	return router
}

func postCoverFromImage(router http.Handler, origin, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/books/book-1/cover/from-image", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	request.Header.Set("X-CSRF-Token", "token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

// A8.6: the operation answers with the book as an upload does, so the shelf takes the new cover from the answer.
func TestCoverFromImageAnswersWithTheBook(t *testing.T) {
	var input service.SetCoverFromImageInput
	router := coverFromImageRouter(coverFromImageFunc(func(_ context.Context, got service.SetCoverFromImageInput) (domainlibrary.CoverActivation, error) {
		input = got
		return domainlibrary.CoverActivation{Current: domainlibrary.CoverArtifact{Version: 4}}, nil
	}), coverWriteStub{})
	response := postCoverFromImage(router, "https://app.test", `{"imageNumber":12}`)
	if response.Code != http.StatusOK || input.ImageNumber != 12 || input.BookID != "book-1" || input.Actor.UserID != "actor-1" {
		t.Fatalf("%d %s, input = %+v", response.Code, response.Body.String(), input)
	}
	if !strings.Contains(response.Body.String(), `"coverUrl":"/api/v1/books/book-1/cover/4"`) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%v %s", response.Header(), response.Body.String())
	}
}

func TestCoverFromImageRefusesWithACode(t *testing.T) {
	never := coverFromImageFunc(func(context.Context, service.SetCoverFromImageInput) (domainlibrary.CoverActivation, error) {
		t.Fatal("the use case ran")
		return domainlibrary.CoverActivation{}, nil
	})
	failing := func(err error) coverFromImageFunc {
		return func(context.Context, service.SetCoverFromImageInput) (domainlibrary.CoverActivation, error) {
			return domainlibrary.CoverActivation{}, err
		}
	}
	for name, test := range map[string]struct {
		useCase CoverFromImageUseCase
		user    coverWriteStub
		origin  string
		body    string
		status  int
		code    string
	}{
		"no number":     {never, coverWriteStub{}, "https://app.test", `{}`, http.StatusBadRequest, "request.validation"},
		"zero":          {never, coverWriteStub{}, "https://app.test", `{"imageNumber":0}`, http.StatusBadRequest, "request.validation"},
		"text":          {never, coverWriteStub{}, "https://app.test", `{"imageNumber":"7"}`, http.StatusBadRequest, "request.validation"},
		"another field": {never, coverWriteStub{}, "https://app.test", `{"imageNumber":7,"bookId":"x"}`, http.StatusBadRequest, "request.validation"},
		"another site":  {never, coverWriteStub{}, "https://evil.test", `{"imageNumber":7}`, http.StatusForbidden, "origin"},
		"bad token":     {never, coverWriteStub{err: auth.ErrInvalidCSRF}, "https://app.test", `{"imageNumber":7}`, http.StatusForbidden, "csrf.invalid"},
		"no session":    {never, coverWriteStub{err: auth.ErrUnauthenticated}, "https://app.test", `{"imageNumber":7}`, http.StatusUnauthorized, "auth.session_required"},
		"no drawing":    {failing(domainlibrary.ErrNotFound), coverWriteStub{}, "https://app.test", `{"imageNumber":7}`, http.StatusNotFound, "not_found"},
		"a reader":      {failing(domainlibrary.ErrForbidden), coverWriteStub{}, "https://app.test", `{"imageNumber":7}`, http.StatusForbidden, "forbidden"},
		"too small":     {failing(domainlibrary.ErrCoverTooSmall), coverWriteStub{}, "https://app.test", `{"imageNumber":7}`, http.StatusUnprocessableEntity, "cover.too_small"},
		"too large":     {failing(domainlibrary.ErrCoverTooLarge), coverWriteStub{}, "https://app.test", `{"imageNumber":7}`, http.StatusRequestEntityTooLarge, "cover.too_large"},
		"unavailable":   {failing(domainlibrary.ErrUnavailable), coverWriteStub{}, "https://app.test", `{"imageNumber":7}`, http.StatusServiceUnavailable, ""},
		"unexpected":    {failing(errors.New("boom")), coverWriteStub{}, "https://app.test", `{"imageNumber":7}`, http.StatusInternalServerError, ""},
	} {
		response := postCoverFromImage(coverFromImageRouter(test.useCase, test.user), test.origin, test.body)
		if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
			t.Errorf("%s: %d %s", name, response.Code, response.Body.String())
		}
	}
}
