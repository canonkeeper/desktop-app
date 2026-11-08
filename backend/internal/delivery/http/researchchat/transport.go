package researchchat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"canonkeeper/backend/internal/auth"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/observability"
	"canonkeeper/backend/internal/search"
	service "canonkeeper/backend/internal/services/researchchat"
)

const maxRequestBytes = 128 * 1024

type createSessionRequest struct {
	SourceView      string               `json:"sourceView"`
	Scope           researchdomain.Scope `json:"scope"`
	Title           *string              `json:"title,omitempty"`
	SavedAssignment *string              `json:"savedAssignment,omitempty"`
}

type updateSessionRequest struct {
	Title *string `json:"title"`
}

type submitTurnRequest struct {
	Body            string                                       `json:"body"`
	ContextLocators []governance.CanonicalChapterSourceLocatorV1 `json:"contextLocators,omitempty"`
}

type steerTurnRequest struct {
	Body           string `json:"body"`
	AttachedImages []int  `json:"attachedImages,omitempty"`
	FirstPhoto     int    `json:"firstPhoto,omitempty"`
}

type steerTurnResponse struct {
	SteerID string `json:"steerId"`
}

type jobStartResponse struct {
	JobID string                 `json:"jobId"`
	Job   jobs.PublicJobResponse `json:"job"`
	Links struct {
		Job string `json:"job"`
	} `json:"links"`
}

func decodeCreateSession(request *http.Request) (service.CreateSessionInput, error) {
	var body createSessionRequest
	if err := decodeRequest(request, &body); err != nil {
		return service.CreateSessionInput{}, err
	}
	title := ""
	if body.Title != nil {
		if strings.TrimSpace(*body.Title) == "" {
			return service.CreateSessionInput{}, researchdomain.ErrValidation
		}
		title = *body.Title
	}
	savedAssignment := ""
	if body.SavedAssignment != nil {
		if strings.TrimSpace(*body.SavedAssignment) == "" {
			return service.CreateSessionInput{}, researchdomain.ErrValidation
		}
		savedAssignment = *body.SavedAssignment
	}
	return service.CreateSessionInput{
		SourceView: body.SourceView, Scope: body.Scope, Title: title,
		SavedAssignment: savedAssignment,
		IdempotencyKey:  strings.TrimSpace(request.Header.Get("Idempotency-Key")),
	}, nil
}

func decodeUpdateSession(request *http.Request) (service.UpdateSessionInput, error) {
	var body updateSessionRequest
	if err := decodeRequest(request, &body); err != nil {
		return service.UpdateSessionInput{}, err
	}
	if body.Title == nil {
		return service.UpdateSessionInput{}, researchdomain.ErrValidation
	}
	return service.UpdateSessionInput{Title: body.Title}, nil
}

func decodeSubmitTurn(request *http.Request) (service.SubmitTurnInput, error) {
	var body submitTurnRequest
	if err := decodeRequest(request, &body); err != nil {
		return service.SubmitTurnInput{}, err
	}
	if body.ContextLocators == nil {
		body.ContextLocators = []governance.CanonicalChapterSourceLocatorV1{}
	}
	return service.SubmitTurnInput{
		Question: body.Body, ContextLocators: body.ContextLocators,
		IdempotencyKey: strings.TrimSpace(request.Header.Get("Idempotency-Key")),
	}, nil
}

func decodeSteerTurn(request *http.Request) (service.SteerTurnInput, error) {
	var body steerTurnRequest
	if err := decodeRequest(request, &body); err != nil {
		return service.SteerTurnInput{}, err
	}
	return service.SteerTurnInput{
		Text: body.Body, Images: body.AttachedImages, FirstPhoto: body.FirstPhoto,
		IdempotencyKey: strings.TrimSpace(request.Header.Get("Idempotency-Key")),
	}, nil
}

func decodeListSessions(request *http.Request) (service.ListSessionsInput, error) {
	query := request.URL.Query()
	cursor, err := oneQueryValue(query["cursor"], false)
	if err != nil {
		return service.ListSessionsInput{}, err
	}
	limitValue, err := oneQueryValue(query["limit"], false)
	if err != nil {
		return service.ListSessionsInput{}, err
	}
	limit := 50
	if limitValue != "" {
		limit, err = strconv.Atoi(limitValue)
		if err != nil || limit < 1 || limit > 100 {
			return service.ListSessionsInput{}, researchdomain.ErrValidation
		}
	}
	return service.ListSessionsInput{Page: researchdomain.Page{Limit: limit, Cursor: cursor}}, nil
}

func oneQueryValue(values []string, required bool) (string, error) {
	if len(values) == 0 {
		if required {
			return "", researchdomain.ErrValidation
		}
		return "", nil
	}
	if len(values) != 1 || required && values[0] == "" {
		return "", researchdomain.ErrValidation
	}
	return values[0], nil
}

func decodeRequest(request *http.Request, target any) error {
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxRequestBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxRequestBytes {
		return researchdomain.ErrValidation
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return researchdomain.ErrValidation
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return researchdomain.ErrValidation
	}
	return nil
}

func writeJobStart(response http.ResponseWriter, ctx context.Context, job jobs.Job) error {
	requestID, _ := observability.RequestIDFromContext(ctx)
	projected, err := jobs.PublicJobResponseFromJob(job, string(requestID))
	if err != nil {
		return err
	}
	path := "/api/v1/jobs/" + job.ID
	body := jobStartResponse{JobID: job.ID, Job: projected}
	body.Links.Job = path
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Location", path)
	response.Header().Set("Retry-After", "1")
	writeJSON(response, http.StatusAccepted, body)
	return nil
}

func writeServiceError(response http.ResponseWriter, err error) {
	if httpapi.WriteQuotaProblem(response, err, time.Now()) {
		return
	}
	var actionError interface{ SearchAction() search.Action }
	switch {
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrSessionNotFound):
		writeProblem(response, http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
	case errors.Is(err, auth.ErrEmailNotVerified):
		writeProblem(response, http.StatusForbidden, "Forbidden", "email address is not verified", "auth.email_unverified")
	case errors.Is(err, researchdomain.ErrNotFound):
		writeProblem(response, http.StatusNotFound, "Not Found", "chat resource not found", "not_found")
	case errors.Is(err, researchdomain.ErrForbidden):
		writeProblem(response, http.StatusForbidden, "Forbidden", "chat access denied", "forbidden")
	case errors.Is(err, researchdomain.ErrTitleEmpty):
		writeProblem(response, http.StatusUnprocessableEntity, "Unprocessable Entity", "title is empty", "title_empty")
	case errors.Is(err, researchdomain.ErrTitleTooLong):
		writeProblem(response, http.StatusUnprocessableEntity, "Unprocessable Entity", "title exceeds 120 characters", "title_too_long")
	case errors.Is(err, researchdomain.ErrValidation):
		writeProblem(response, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
	case errors.As(err, &actionError):
		writeProblem(response, http.StatusConflict, "Conflict", "current source text is unavailable", string(actionError.SearchAction()))
	case errors.Is(err, researchdomain.ErrOutdatedAgent):
		writeProblem(response, http.StatusConflict, "Conflict", "chat was created by a previous assistant version; start a new chat", "chat.outdated_agent")
	case errors.Is(err, researchdomain.ErrTurnEnded):
		writeProblem(response, http.StatusConflict, "Conflict", "the turn has already ended", "chat.turn_ended")
	case errors.Is(err, researchdomain.ErrSteerRetry):
		writeProblem(response, http.StatusConflict, "Conflict", "the turn takes no correction right now; try again", "chat.steer_retry")
	case errors.Is(err, researchdomain.ErrSteerLimit):
		writeProblem(response, http.StatusConflict, "Conflict", "too many corrections in this turn", "chat.steer_limit")
	case errors.Is(err, researchdomain.ErrConflict):
		writeProblem(response, http.StatusConflict, "Conflict", "request conflicts with current chat state", "conflict")
	default:
		writeProblem(response, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
	}
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}

func writeProblem(response http.ResponseWriter, status int, title, detail, code string) {
	response.Header().Set("Cache-Control", "no-store")
	httpapi.WriteProblem(response, "", status, title, detail, code)
}
