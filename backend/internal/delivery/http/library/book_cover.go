package library

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"canonkeeper/backend/internal/auth"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/library"

	"github.com/go-chi/chi/v5"
)

const coverCacheControl = "private, max-age=31536000, immutable"

type UploadCoverUseCase interface {
	Execute(context.Context, service.UploadCoverInput) (domainlibrary.CoverActivation, error)
}

type GetCoverUseCase interface {
	Execute(context.Context, domainlibrary.CoverLookup) (service.CoverContent, error)
}

type DeleteCoverUseCase interface {
	Execute(context.Context, domainlibrary.RemoveCoverInput) (domainlibrary.CoverArtifact, error)
}

type BookResponseFunc func(context.Context, domainlibrary.Actor, string, *string) (any, error)

type CurrentUserService interface {
	CurrentUser(context.Context, string) (auth.User, error)
	AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error)
}

type Handlers struct {
	upload         UploadCoverUseCase
	get            GetCoverUseCase
	delete         DeleteCoverUseCase
	bookResponse   BookResponseFunc
	currentUser    CurrentUserService
	allowedOrigins []string
}

type actorUserIDContextKey struct{}

func NewHandlers(
	upload UploadCoverUseCase,
	get GetCoverUseCase,
	deleteCover DeleteCoverUseCase,
	bookResponse BookResponseFunc,
	currentUser CurrentUserService,
	allowedOrigins []string,
) Handlers {
	if upload == nil || get == nil || deleteCover == nil || bookResponse == nil || currentUser == nil {
		panic("book cover HTTP: dependencies are required")
	}
	return Handlers{
		upload: upload, get: get, delete: deleteCover, bookResponse: bookResponse,
		currentUser: currentUser, allowedOrigins: slices.Clone(allowedOrigins),
	}
}

func (handlers Handlers) Mount(router chi.Router) {
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(handlers.allowedOrigins...))
		router.Use(handlers.csrfMiddleware)
		router.Post("/books/{bookId}/cover", handlers.uploadCover)
		router.Get("/books/{bookId}/cover/{version}", handlers.getCover)
		router.Delete("/books/{bookId}/cover", handlers.deleteCover)
	})
}

func (handlers Handlers) uploadCover(response http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(response, request)
	if !ok {
		return
	}
	content, err := decodeCoverMultipart(request)
	if err != nil {
		writeCoverError(response, err)
		return
	}
	bookID := chi.URLParam(request, "bookId")
	activation, err := handlers.upload.Execute(request.Context(), service.UploadCoverInput{
		Actor: actor, BookID: bookID, Content: content,
	})
	if err != nil {
		writeCoverError(response, err)
		return
	}
	coverURL := "/api/v1/books/" + bookID + "/cover/" + strconv.FormatInt(activation.Current.Version, 10)
	handlers.writeBook(response, request, actor, bookID, &coverURL)
}

func (handlers Handlers) getCover(response http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(response, request)
	if !ok {
		return
	}
	version, err := strconv.ParseInt(chi.URLParam(request, "version"), 10, 64)
	if err != nil || version < 1 {
		writeCoverError(response, domainlibrary.ErrValidation)
		return
	}
	content, err := handlers.get.Execute(request.Context(), domainlibrary.CoverLookup{
		Actor: actor, BookID: chi.URLParam(request, "bookId"), Version: version,
	})
	if err != nil {
		writeCoverError(response, err)
		return
	}
	response.Header().Set("Cache-Control", coverCacheControl)
	response.Header().Set("ETag", content.ETag)
	if request.Header.Get("If-None-Match") == content.ETag {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	response.Header().Set("Content-Type", content.MIMEType)
	response.Header().Set("Content-Length", strconv.Itoa(len(content.Content)))
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(content.Content)
}

func (handlers Handlers) deleteCover(response http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(response, request)
	if !ok {
		return
	}
	bookID := chi.URLParam(request, "bookId")
	if _, err := handlers.delete.Execute(request.Context(), domainlibrary.RemoveCoverInput{
		Actor: actor, BookID: bookID, At: time.Now().UTC(),
	}); err != nil {
		writeCoverError(response, err)
		return
	}
	handlers.writeBook(response, request, actor, bookID, nil)
}

func (handlers Handlers) writeBook(response http.ResponseWriter, request *http.Request, actor domainlibrary.Actor, bookID string, coverURL *string) {
	book, err := handlers.bookResponse(request.Context(), actor, bookID, coverURL)
	if err != nil {
		writeCoverError(response, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(book)
}

func (handlers Handlers) csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !httpapi.RequiresCSRF(request.Method) {
			next.ServeHTTP(response, request)
			return
		}
		user, _, err := handlers.currentUser.AuthenticateWrite(
			request.Context(), auth.SessionCookieValue(request), request.Header.Get("X-CSRF-Token"),
		)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidCSRF) {
				writeCoverProblem(response, http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid")
			} else {
				writeCoverError(response, err)
			}
			return
		}
		ctx := context.WithValue(request.Context(), actorUserIDContextKey{}, user.ID)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func (handlers Handlers) actor(response http.ResponseWriter, request *http.Request) (domainlibrary.Actor, bool) {
	if actorID, ok := request.Context().Value(actorUserIDContextKey{}).(string); ok {
		return domainlibrary.Actor{UserID: actorID}, true
	}
	user, err := handlers.currentUser.CurrentUser(request.Context(), auth.SessionCookieValue(request))
	if err != nil {
		writeCoverError(response, err)
		return domainlibrary.Actor{}, false
	}
	return domainlibrary.Actor{UserID: user.ID}, true
}

func decodeCoverMultipart(request *http.Request) ([]byte, error) {
	reader, err := request.MultipartReader()
	if err != nil {
		return nil, domainlibrary.ErrValidation
	}
	var content []byte
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, domainlibrary.ErrValidation
		}
		if part.FormName() != "file" || part.FileName() == "" || content != nil {
			_ = part.Close()
			return nil, domainlibrary.ErrValidation
		}
		content, err = io.ReadAll(io.LimitReader(part, domainlibrary.MaxCoverBytes+1))
		closeErr := part.Close()
		if err != nil || closeErr != nil {
			return nil, domainlibrary.ErrValidation
		}
		if int64(len(content)) > domainlibrary.MaxCoverBytes {
			return nil, domainlibrary.ErrCoverTooLarge
		}
	}
	if content == nil {
		return nil, domainlibrary.ErrValidation
	}
	return content, nil
}

func writeCoverError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound):
		writeCoverProblem(response, http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, domainlibrary.ErrNotFound):
		writeCoverProblem(response, http.StatusNotFound, "Not Found", "book cover not found", "not_found")
	case errors.Is(err, domainlibrary.ErrForbidden):
		writeCoverProblem(response, http.StatusForbidden, "Forbidden", "book cover access denied", "forbidden")
	case errors.Is(err, domainlibrary.ErrCoverTooLarge):
		writeCoverProblem(response, http.StatusRequestEntityTooLarge, "Payload Too Large", "cover exceeds 5 MiB", "cover.too_large")
	case errors.Is(err, domainlibrary.ErrUnsupportedCoverMedia):
		writeCoverProblem(response, http.StatusUnsupportedMediaType, "Unsupported Media Type", "cover must be JPEG, PNG, or WebP", "cover.unsupported_media")
	case errors.Is(err, domainlibrary.ErrCoverTooSmall):
		writeCoverProblem(response, http.StatusUnprocessableEntity, "Unprocessable Entity", "cover must be at least 600 by 900 pixels", "cover.too_small")
	case errors.Is(err, domainlibrary.ErrConflict):
		writeCoverProblem(response, http.StatusConflict, "Conflict", "cover changed concurrently", "conflict")
	case errors.Is(err, domainlibrary.ErrUnavailable):
		writeCoverProblem(response, http.StatusServiceUnavailable, "Service Unavailable", "cover bytes are unavailable", "cover.unavailable")
	case errors.Is(err, domainlibrary.ErrValidation):
		writeCoverProblem(response, http.StatusBadRequest, "Bad Request", "invalid cover request", "request.validation")
	default:
		writeCoverProblem(response, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}

func writeCoverProblem(response http.ResponseWriter, status int, title, detail, code string) {
	response.Header().Set("Cache-Control", "no-store")
	httpapi.WriteProblem(response, "", status, title, detail, code)
}
