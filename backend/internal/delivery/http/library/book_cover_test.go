package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	service "canonkeeper/backend/internal/services/library"

	"github.com/go-chi/chi/v5"
)

type coverCurrentUserStub struct{}

func (coverCurrentUserStub) CurrentUser(context.Context, string) (auth.User, error) {
	return auth.User{ID: "actor-1"}, nil
}

func (coverCurrentUserStub) AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error) {
	return auth.User{ID: "actor-1"}, auth.SessionWriteProof{}, nil
}

func TestUploadBookCoverAcceptsOnlyFileMultipartAndReturnsCentralBook(t *testing.T) {
	var input service.UploadCoverInput
	handlers := NewHandlers(
		UploadCoverFunc(func(_ context.Context, got service.UploadCoverInput) (domainlibrary.CoverActivation, error) {
			input = got
			return domainlibrary.CoverActivation{Current: domainlibrary.CoverArtifact{Version: 1}}, nil
		}),
		GetCoverFunc(func(context.Context, domainlibrary.CoverLookup) (service.CoverContent, error) {
			return service.CoverContent{}, nil
		}),
		DeleteCoverFunc(func(context.Context, domainlibrary.RemoveCoverInput) (domainlibrary.CoverArtifact, error) {
			return domainlibrary.CoverArtifact{}, nil
		}),
		BookResponseFunc(func(_ context.Context, _ domainlibrary.Actor, _ string, coverURL *string) (any, error) {
			return map[string]any{"id": "book-1", "coverUrl": coverURL}, nil
		}),
		coverCurrentUserStub{},
		[]string{"https://app.test"},
	)
	router := chi.NewRouter()
	handlers.Mount(router)

	request := multipartCoverRequest(t, http.MethodPost, "/books/book-1/cover", []byte("cover"), false)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || input.Actor.UserID != "actor-1" || input.BookID != "book-1" || string(input.Content) != "cover" {
		t.Fatalf("response/input = %d/%#v", response.Code, input)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["coverUrl"] != "/api/v1/books/book-1/cover/1" {
		t.Fatalf("body = %#v, %v", body, err)
	}

	invalid := multipartCoverRequest(t, http.MethodPost, "/books/book-1/cover", []byte("cover"), true)
	invalidResponse := httptest.NewRecorder()
	router.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown multipart field status = %d", invalidResponse.Code)
	}
}

func TestDeleteBookCoverResponseStaysNullAcrossConcurrentReplacement(t *testing.T) {
	replacementURL := "/api/v1/books/book-1/cover/2"
	handlers := NewHandlers(
		UploadCoverFunc(func(context.Context, service.UploadCoverInput) (domainlibrary.CoverActivation, error) {
			return domainlibrary.CoverActivation{}, errors.New("unused")
		}),
		GetCoverFunc(func(context.Context, domainlibrary.CoverLookup) (service.CoverContent, error) {
			return service.CoverContent{}, errors.New("unused")
		}),
		DeleteCoverFunc(func(context.Context, domainlibrary.RemoveCoverInput) (domainlibrary.CoverArtifact, error) {
			return domainlibrary.CoverArtifact{ID: "removed", Version: 1}, nil
		}),
		BookResponseFunc(func(_ context.Context, _ domainlibrary.Actor, _ string, coverURL *string) (any, error) {
			if coverURL != nil {
				return map[string]any{"id": "book-1", "coverUrl": replacementURL}, nil
			}
			return map[string]any{"id": "book-1", "coverUrl": nil}, nil
		}),
		coverCurrentUserStub{},
		[]string{"https://app.test"},
	)
	router := chi.NewRouter()
	handlers.Mount(router)
	request := httptest.NewRequest(http.MethodDelete, "/books/book-1/cover", nil)
	request.Header.Set("Origin", "https://app.test")
	request.Header.Set("X-CSRF-Token", "csrf")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"coverUrl":null`) {
		t.Fatalf("delete response = %d %s", response.Code, response.Body.String())
	}
}

func TestGetBookCoverUsesImmutablePrivateCacheAndStrongETag(t *testing.T) {
	const etag = `"abc123"`
	handlers := testCoverHandlers(
		GetCoverFunc(func(context.Context, domainlibrary.CoverLookup) (service.CoverContent, error) {
			return service.CoverContent{Content: []byte("cover"), MIMEType: "image/jpeg", ETag: etag}, nil
		}),
	)
	router := chi.NewRouter()
	handlers.Mount(router)

	request := httptest.NewRequest(http.MethodGet, "/books/book-1/cover/1", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "cover" || response.Header().Get("Content-Type") != "image/jpeg" ||
		response.Header().Get("ETag") != etag || response.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("cover response = %d %q %#v", response.Code, response.Body.String(), response.Header())
	}

	request = httptest.NewRequest(http.MethodGet, "/books/book-1/cover/1", nil)
	request.Header.Set("If-None-Match", etag)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotModified || response.Body.Len() != 0 {
		t.Fatalf("conditional response = %d %q", response.Code, response.Body.String())
	}
}

func TestBookCoverErrorsUseExactProblemStatuses(t *testing.T) {
	tests := map[string]struct {
		err    error
		status int
	}{
		"too large":   {domainlibrary.ErrCoverTooLarge, http.StatusRequestEntityTooLarge},
		"media":       {domainlibrary.ErrUnsupportedCoverMedia, http.StatusUnsupportedMediaType},
		"dimensions":  {domainlibrary.ErrCoverTooSmall, http.StatusUnprocessableEntity},
		"forbidden":   {domainlibrary.ErrForbidden, http.StatusForbidden},
		"not found":   {domainlibrary.ErrNotFound, http.StatusNotFound},
		"race":        {domainlibrary.ErrConflict, http.StatusConflict},
		"unavailable": {domainlibrary.ErrUnavailable, http.StatusServiceUnavailable},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			handlers := testCoverHandlers(GetCoverFunc(func(context.Context, domainlibrary.CoverLookup) (service.CoverContent, error) {
				return service.CoverContent{}, test.err
			}))
			router := chi.NewRouter()
			handlers.Mount(router)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/books/book-1/cover/1", nil))
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func testCoverHandlers(read GetCoverUseCase) Handlers {
	return NewHandlers(
		UploadCoverFunc(func(context.Context, service.UploadCoverInput) (domainlibrary.CoverActivation, error) {
			return domainlibrary.CoverActivation{}, errors.New("unused")
		}),
		read,
		DeleteCoverFunc(func(context.Context, domainlibrary.RemoveCoverInput) (domainlibrary.CoverArtifact, error) {
			return domainlibrary.CoverArtifact{}, errors.New("unused")
		}),
		BookResponseFunc(func(_ context.Context, _ domainlibrary.Actor, _ string, coverURL *string) (any, error) {
			return map[string]any{"id": "book-1", "coverUrl": coverURL}, nil
		}),
		coverCurrentUserStub{},
		[]string{"https://app.test"},
	)
}

func multipartCoverRequest(t *testing.T, method, path string, content []byte, extra bool) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "cover.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if extra {
		if err := writer.WriteField("unknown", "value"); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Origin", "https://app.test")
	request.Header.Set("X-CSRF-Token", "csrf")
	return request
}

type UploadCoverFunc func(context.Context, service.UploadCoverInput) (domainlibrary.CoverActivation, error)

func (function UploadCoverFunc) Execute(ctx context.Context, input service.UploadCoverInput) (domainlibrary.CoverActivation, error) {
	return function(ctx, input)
}

type GetCoverFunc func(context.Context, domainlibrary.CoverLookup) (service.CoverContent, error)

func (function GetCoverFunc) Execute(ctx context.Context, input domainlibrary.CoverLookup) (service.CoverContent, error) {
	return function(ctx, input)
}

type DeleteCoverFunc func(context.Context, domainlibrary.RemoveCoverInput) (domainlibrary.CoverArtifact, error)

func (function DeleteCoverFunc) Execute(ctx context.Context, input domainlibrary.RemoveCoverInput) (domainlibrary.CoverArtifact, error) {
	return function(ctx, input)
}
