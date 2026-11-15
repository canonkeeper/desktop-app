// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"syscall"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/projects"
	service "canonkeeper/backend/internal/services/sagaarchive"

	"github.com/go-chi/chi/v5"
)

// OpenUseCase opens a saga file.
type OpenUseCase interface {
	Execute(context.Context, service.OpenInput) (service.Opened, error)
}

const (
	minIdempotencyKey = 8
	maxIdempotencyKey = 128
	// maxOpenBody is the file and the envelope of its form.
	maxOpenBody = domain.MaxFileBytes + 1<<20
)

// MountOpen serves openSagaArchive (SF3.12), mounted only by the desktop: the session of the window, its Origin, its
// request-forgery token, one key of a repeat; the file goes to the disk as it comes, never whole into memory.
func MountOpen(router chi.Router, open OpenUseCase, temp service.TempFiles, currentUser CurrentUserService, origins []string) {
	if open == nil || temp == nil || currentUser == nil {
		panic("saga archive HTTP: dependencies are required")
	}
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(origins...))
		router.Post("/saga-archives", func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("Cache-Control", "no-store")
			user, proof, err := currentUser.AuthenticateWrite(request.Context(), auth.SessionCookieValue(request), request.Header.Get("X-CSRF-Token"))
			if err != nil {
				writeAuthError(response, err)
				return
			}
			keys := request.Header.Values("Idempotency-Key")
			if len(keys) != 1 || len(strings.TrimSpace(keys[0])) < minIdempotencyKey || len(strings.TrimSpace(keys[0])) > maxIdempotencyKey ||
				request.URL.RawQuery != "" {
				writeProblem(response, http.StatusBadRequest, "Bad Request", "one Idempotency-Key of 8 to 128 signs is required", "request.validation")
				return
			}
			file, sum, err := receive(response, request, temp)
			if err != nil {
				writeOpenError(response, err)
				return
			}
			opened, err := open.Execute(request.Context(), service.OpenInput{
				OpenRequest: service.OpenRequest{ActorUserID: user.ID, IdempotencyKey: strings.TrimSpace(keys[0]), FileSHA256: sum},
				Proof:       proof, File: file,
			})
			if err != nil {
				writeOpenError(response, err)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(opened)
		})
	})
}

// errNoFile is a form without its one file.
var errNoFile = errors.New("saga archive: the form has no one file")

// receive writes the one file of the form to a temporary file as it comes, with its SHA-256.
func receive(response http.ResponseWriter, request *http.Request, temp service.TempFiles) (service.TempFile, string, error) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return nil, "", errUnsupported
	}
	request.Body = http.MaxBytesReader(response, request.Body, maxOpenBody)
	reader, err := request.MultipartReader()
	if err != nil {
		return nil, "", errNoFile
	}
	var file service.TempFile
	var sum string
	fail := func(err error) (service.TempFile, string, error) {
		if file != nil {
			_ = file.Remove()
		}
		return nil, "", err
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fail(sizeOr(err, errNoFile))
		}
		if part.FormName() != "file" || file != nil {
			_ = part.Close()
			return fail(errNoFile)
		}
		if file, err = temp.Create(); err != nil {
			return fail(spaceOr(err))
		}
		hash := sha256.New()
		written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(part, domain.MaxFileBytes+1))
		_ = part.Close()
		if err != nil {
			return fail(spaceOr(sizeOr(err, err)))
		}
		if written > domain.MaxFileBytes {
			return fail(&domain.Refusal{Code: domain.CodeTooLarge})
		}
		sum = hex.EncodeToString(hash.Sum(nil))
	}
	if file == nil {
		return fail(errNoFile)
	}
	return file, sum, nil
}

var errUnsupported = errors.New("saga archive: not a form")

func sizeOr(err, other error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return &domain.Refusal{Code: domain.CodeTooLarge}
	}
	return other
}

func spaceOr(err error) error {
	if errors.Is(err, syscall.ENOSPC) {
		return &domain.Refusal{Code: domain.CodeNoSpace}
	}
	return err
}

// writeOpenError answers a refusal of a file with its one code (SF3.2), a key of another file with 409, a limit of the
// quotas in their words.
func writeOpenError(response http.ResponseWriter, err error) {
	if httpapi.WriteQuotaProblem(response, err, time.Now()) {
		return
	}
	var refusal *domain.Refusal
	switch {
	case errors.As(err, &refusal):
		switch refusal.Code {
		case domain.CodeTooLarge:
			writeProblem(response, http.StatusRequestEntityTooLarge, "Payload Too Large", "the file is over the limits of a saga file", refusal.Code)
		case domain.CodeNoSpace:
			writeProblem(response, http.StatusInsufficientStorage, "Insufficient Storage", "no place on the disk for the file", refusal.Code)
		default:
			detail := "the file is not a saga file this app opens"
			if refusal.Entry != "" {
				detail = "the record " + refusal.Entry + " of the file is refused"
			}
			writeProblem(response, http.StatusUnprocessableEntity, "Unprocessable Entity", detail, refusal.Code)
		}
	case errors.Is(err, errUnsupported):
		writeProblem(response, http.StatusUnsupportedMediaType, "Unsupported Media Type", "a form with the file is required", "request.media_type")
	case errors.Is(err, errNoFile):
		writeProblem(response, http.StatusBadRequest, "Bad Request", "one file is required", "request.validation")
	case errors.Is(err, service.ErrConflict):
		writeProblem(response, http.StatusConflict, "Conflict", "the key opened another file", "idempotency.conflict")
	case errors.Is(err, projects.ErrForbidden):
		writeProblem(response, http.StatusForbidden, "Forbidden", "the account opens no saga", "forbidden")
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound), errors.Is(err, auth.ErrInvalidCSRF):
		writeAuthError(response, err)
	default:
		writeProblem(response, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}
