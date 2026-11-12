package library

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"canonkeeper/backend/internal/jobs"

	"github.com/go-chi/chi/v5"
)

func (h Handlers) CreateImportQueue(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	var request createImportQueueRequest
	if err := decodeImportJSON(r, &request); err != nil {
		writeImportServiceError(w, r, ErrValidation)
		return
	}
	items := make([]CreateImportQueueItemInput, 0, len(request.Items))
	for _, item := range request.Items {
		items = append(items, CreateImportQueueItemInput{
			ClientItemID: item.ClientItemID, Position: item.Position, SourceFileName: item.SourceFileName,
		})
	}
	queue, err := h.service.CreateImportQueue(r.Context(), CreateImportQueueInput{
		ProjectID: chi.URLParam(r, "projectId"), ActorUserID: actorUserID,
		IdempotencyKey: r.Header.Get("Idempotency-Key"), RequestMethod: r.Method, RequestPath: r.URL.Path,
		SessionWriteProof: h.sessionWriteProof(r), Items: items,
	})
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/projects/"+queue.ProjectID+"/import-queues/"+queue.ID)
	h.writeImportQueueResponse(w, r, Actor{UserID: actorUserID}, http.StatusCreated, queue)
}

func (h Handlers) ListImportQueues(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	page, err := pageFromRequest(r)
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	queues, err := h.service.ListImportQueues(r.Context(), ListImportQueuesInput{
		ProjectID: chi.URLParam(r, "projectId"), ActorUserID: actorUserID, Limit: page.Limit,
	})
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	h.writeImportQueueListResponse(w, r, Actor{UserID: actorUserID}, queues)
}

func (h Handlers) GetImportQueue(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	queue, err := h.service.GetImportQueue(r.Context(), GetImportQueueInput{
		ProjectID: chi.URLParam(r, "projectId"), QueueID: chi.URLParam(r, "queueId"), ActorUserID: actorUserID,
	})
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	h.writeImportQueueResponse(w, r, Actor{UserID: actorUserID}, http.StatusOK, queue)
}

func (h Handlers) UploadImportQueueItem(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	upload, err := importQueueUploadFromRequest(w, r)
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	item, err := h.service.UploadImportQueueItem(r.Context(), UploadImportQueueItemInput{
		ProjectID: chi.URLParam(r, "projectId"), QueueID: chi.URLParam(r, "queueId"), ItemID: upload.itemID,
		ActorUserID: actorUserID, FileName: upload.fileName, MIMEType: upload.mimeType, Content: upload.content,
		IdempotencyKey: r.Header.Get("Idempotency-Key"), RequestMethod: r.Method, RequestPath: r.URL.Path,
		SessionWriteProof: h.sessionWriteProof(r),
	})
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	queue, err := h.service.GetImportQueue(r.Context(), GetImportQueueInput{
		ProjectID: item.ProjectID, QueueID: item.QueueID, ActorUserID: actorUserID,
	})
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	h.writeImportQueueResponse(w, r, Actor{UserID: actorUserID}, http.StatusOK, queue)
}

// SkipImportQueueItem is «Пропустить» of a volume that did not read (specs/2026-11-11-ux7-upload.md UP3.2).
func (h Handlers) SkipImportQueueItem(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	queue, err := h.service.SkipImportQueueItem(r.Context(), SkipImportQueueItemInput{
		ProjectID: chi.URLParam(r, "projectId"), QueueID: chi.URLParam(r, "queueId"), ItemID: chi.URLParam(r, "itemId"),
		ActorUserID: actorUserID, SessionWriteProof: h.sessionWriteProof(r),
	})
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	h.writeImportQueueResponse(w, r, Actor{UserID: actorUserID}, http.StatusOK, queue)
}

func (h Handlers) StartImportQueue(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	var request startImportQueueRequest
	if err := decodeImportJSON(r, &request); err != nil {
		writeImportServiceError(w, r, ErrValidation)
		return
	}
	queue, err := h.service.StartImportQueue(r.Context(), StartImportQueueInput{
		ProjectID: chi.URLParam(r, "projectId"), QueueID: chi.URLParam(r, "queueId"), ActorUserID: actorUserID,
		ExpectedVersion: request.ExpectedVersion,
		IdempotencyKey:  r.Header.Get("Idempotency-Key"),
		RequestMethod:   r.Method, RequestPath: r.URL.Path, SessionWriteProof: h.sessionWriteProof(r),
	})
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	h.writeImportQueueResponse(w, r, Actor{UserID: actorUserID}, http.StatusAccepted, queue)
}

func (h Handlers) CancelImportQueue(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	queue, err := h.service.CancelImportQueue(r.Context(), CancelImportQueueInput{
		ProjectID: chi.URLParam(r, "projectId"), QueueID: chi.URLParam(r, "queueId"), ActorUserID: actorUserID,
		SessionWriteProof: h.sessionWriteProof(r),
	})
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	h.writeImportQueueResponse(w, r, Actor{UserID: actorUserID}, http.StatusOK, queue)
}

type createImportQueueRequest struct {
	Items []createImportQueueItemRequest `json:"items"`
}

type createImportQueueItemRequest struct {
	ClientItemID   string `json:"clientItemId"`
	Position       int    `json:"position"`
	SourceFileName string `json:"sourceFileName"`
}

type startImportQueueRequest struct {
	ExpectedVersion int64 `json:"expectedVersion"`
}

type importQueueUploadRequest struct {
	itemID   string
	fileName string
	mimeType string
	content  []byte
}

type importQueueListResponse struct {
	Data []importQueueResponse `json:"data"`
}

type importQueueResponse struct {
	ID                string                    `json:"id"`
	ProjectID         string                    `json:"projectId"`
	Status            string                    `json:"status"`
	Version           int64                     `json:"version"`
	IntendedItemCount int                       `json:"intendedItemCount"`
	CurrentItemID     *string                   `json:"currentItemId"`
	Items             []importQueueItemResponse `json:"items"`
	CreatedAt         string                    `json:"createdAt"`
	UpdatedAt         string                    `json:"updatedAt"`
	StartedAt         *string                   `json:"startedAt"`
	CompletedAt       *string                   `json:"completedAt"`
	Links             importQueueLinksResponse  `json:"links"`
}

type importQueueItemResponse struct {
	ID               string                        `json:"id"`
	ClientItemID     string                        `json:"clientItemId"`
	Position         int                           `json:"position"`
	SourceFileName   string                        `json:"sourceFileName"`
	MIMEType         *string                       `json:"mimeType"`
	SizeBytes        *int64                        `json:"sizeBytes"`
	PreflightStatus  string                        `json:"preflightStatus"`
	PreflightError   *importQueueItemErrorResponse `json:"preflightError"`
	ProcessingStatus string                        `json:"processingStatus"`
	FailureClass     *string                       `json:"failureClass"`
	FailedStep       *string                       `json:"failedStep"`
	ImportJobID      *string                       `json:"importJobId"`
	BookID           *string                       `json:"bookId"`
	UpdatedAt        string                        `json:"updatedAt"`
	Links            importQueueItemLinksResponse  `json:"links"`
}

type importQueueItemErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type importQueueLinksResponse struct {
	Self   string  `json:"self"`
	Cancel *string `json:"cancel"`
}

type importQueueItemLinksResponse struct {
	Status *string `json:"status"`
}

func (h Handlers) writeImportQueueListResponse(w http.ResponseWriter, r *http.Request, actor Actor, queues []ImportQueue) {
	bookStatuses, err := h.importQueueBookStatuses(r.Context(), actor, queues)
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	data := make([]importQueueResponse, 0, len(queues))
	for _, queue := range queues {
		response, err := importQueueResponseFor(queue, bookStatuses)
		if err != nil {
			writeImportServiceError(w, r, err)
			return
		}
		data = append(data, response)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeImportJSON(w, http.StatusOK, importQueueListResponse{Data: data})
}

func (h Handlers) writeImportQueueResponse(w http.ResponseWriter, r *http.Request, actor Actor, status int, queue ImportQueue) {
	bookStatuses, err := h.importQueueBookStatuses(r.Context(), actor, []ImportQueue{queue})
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	response, err := importQueueResponseFor(queue, bookStatuses)
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeImportJSON(w, status, response)
}

func (h Handlers) importQueueBookStatuses(ctx context.Context, actor Actor, queues []ImportQueue) (map[string]BookStatus, error) {
	bookIDs := make(map[string]struct{})
	for _, queue := range queues {
		for _, item := range queue.Items {
			if item.BookID != "" {
				bookIDs[item.BookID] = struct{}{}
			}
		}
	}
	statuses := make(map[string]BookStatus, len(bookIDs))
	for bookID := range bookIDs {
		book, err := h.service.GetBook(ctx, actor, bookID)
		if errors.Is(err, ErrNotFound) {
			// The author deleted the volume after its import; the queue item stays done.
			continue
		}
		if err != nil {
			return nil, err
		}
		statuses[bookID] = book.Status
	}
	return statuses, nil
}

func importQueueResponseFor(queue ImportQueue, bookStatuses map[string]BookStatus) (importQueueResponse, error) {
	items := make([]importQueueItemResponse, 0, len(queue.Items))
	currentItemID := ""
	for _, item := range queue.Items {
		var preflightError *importQueueItemErrorResponse
		if len(item.PreflightErrorJSON) > 0 {
			preflightError = &importQueueItemErrorResponse{}
			if err := json.Unmarshal(item.PreflightErrorJSON, preflightError); err != nil {
				return importQueueResponse{}, ErrValidation
			}
		}
		processingStatus, failureClass, failedStep, err := importQueueProcessingStatus(item, bookStatuses)
		if err != nil {
			return importQueueResponse{}, err
		}
		if currentItemID == "" && processingStatus != "succeeded" && processingStatus != "skipped" && item.ImportJobID != "" {
			currentItemID = item.ID
		}
		items = append(items, importQueueItemResponse{
			ID: item.ID, ClientItemID: item.ClientItemID, Position: item.Position,
			SourceFileName: item.SourceFileName, MIMEType: optionalImportQueueString(item.MIMEType),
			SizeBytes: optionalImportQueueInt64(item.SizeBytes), PreflightStatus: item.PreflightStatus,
			PreflightError: preflightError, ProcessingStatus: processingStatus,
			FailureClass: optionalImportQueueString(failureClass), FailedStep: optionalImportQueueString(failedStep),
			ImportJobID: optionalImportQueueString(item.ImportJobID),
			BookID:      optionalImportQueueString(item.BookID),
			UpdatedAt:   formatImportTime(item.UpdatedAt),
			Links:       importQueueItemLinksResponse{Status: optionalImportQueueString(importQueueStatusLink(queue, item))},
		})
	}
	self := "/api/v1/projects/" + queue.ProjectID + "/import-queues/" + queue.ID
	var cancel *string
	if importQueueCancelAllowed(queue.Status) {
		cancel = &self
	}
	return importQueueResponse{
		ID: queue.ID, ProjectID: queue.ProjectID, Status: string(queue.Status),
		Version:           queue.Version,
		IntendedItemCount: len(queue.Items),
		CurrentItemID:     optionalImportQueueString(currentItemID), Items: items,
		CreatedAt: formatImportTime(queue.CreatedAt), UpdatedAt: formatImportTime(queue.UpdatedAt),
		StartedAt: optionalImportQueueTime(queue.StartedAt), CompletedAt: optionalImportQueueTime(queue.CompletedAt),
		Links: importQueueLinksResponse{Self: self, Cancel: cancel},
	}, nil
}

func importQueueProcessingStatus(item ImportQueueItem, bookStatuses map[string]BookStatus) (string, string, string, error) {
	// A volume the author took out of the upload (UP3.2): neither in work nor waiting.
	if item.SkippedAt != nil {
		return "skipped", "", "", nil
	}
	if item.ImportJobID != "" {
		switch jobs.Status(item.ImportJobStatus) {
		case jobs.StatusQueued:
			if item.ImportJobRetryCount > 0 {
				return "retrying", "", "", nil
			}
			return "queued", "", "", nil
		case jobs.StatusRunning:
			return "importing", "", "", nil
		case jobs.StatusCanceling:
			return "canceling", "", "", nil
		case jobs.StatusSucceeded:
			if status, present := bookStatuses[item.BookID]; present && status != BookStatusReady {
				return "indexing", "", "", nil
			}
			return "succeeded", "", "", nil
		case jobs.StatusFailed:
			failureClass, err := importQueueFailureClass(item.ImportJobErrorJSON)
			return "failed", failureClass, "import", err
		case jobs.StatusExpired:
			// An expired Job stopped before it could finish and carries no error.
			return "failed", "infrastructure", "import", nil
		case jobs.StatusCanceled:
			return "canceled", "", "", nil
		default:
			return "", "", "", ErrValidation
		}
	}
	if item.BookID != "" && bookStatuses[item.BookID] == BookStatusReady {
		return "succeeded", "", "", nil
	}
	if item.PreflightStatus == ImportQueueItemReady {
		return "ready", "", "", nil
	}
	// A file that did not pass the check waits for the author at its place with its reason (UP1.2, UP3.2).
	if item.PreflightStatus == ImportQueueItemInvalid {
		return "failed", "", "preflight", nil
	}
	return "uploading", "", "", nil
}

func importQueueFailureClass(raw []byte) (string, error) {
	jobError, err := jobs.DecodeError(raw)
	if err != nil {
		return "", ErrValidation
	}
	if jobError.Code == jobs.RetryExhaustedCode {
		return "infrastructure", nil
	}
	return "unexpected_deterministic_failure", nil
}

func importQueueStatusLink(queue ImportQueue, item ImportQueueItem) string {
	if item.BookID == "" {
		return ""
	}
	query := url.Values{
		"projectId":  {queue.ProjectID},
		"bookId":     {item.BookID},
		"sourceView": {"draft"},
	}
	return (&url.URL{Path: "/manuscript/books", RawQuery: query.Encode()}).String()
}

func importQueueCancelAllowed(status ImportQueueStatus) bool {
	switch status {
	case ImportQueueUploading, ImportQueueDraft, ImportQueueRunning, ImportQueueFailed:
		return true
	default:
		return false
	}
}

func optionalImportQueueString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalImportQueueInt64(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func optionalImportQueueTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := formatImportTime(*value)
	return &formatted
}

func importQueueUploadFromRequest(w http.ResponseWriter, r *http.Request) (importQueueUploadRequest, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return importQueueUploadRequest{}, ErrImportUnsupportedMediaType
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMultipartImportBodyBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		if isMaxBytesError(err) {
			return importQueueUploadRequest{}, ErrImportFileTooLarge
		}
		return importQueueUploadRequest{}, ErrValidation
	}
	request := importQueueUploadRequest{}
	itemIDParts, fileParts := 0, 0
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if isMaxBytesError(err) {
				return importQueueUploadRequest{}, ErrImportFileTooLarge
			}
			return importQueueUploadRequest{}, ErrValidation
		}
		switch part.FormName() {
		case "itemId":
			value, err := importFormValue(part)
			if err != nil || strings.TrimSpace(value) == "" {
				_ = part.Close()
				return importQueueUploadRequest{}, ErrValidation
			}
			request.itemID = strings.TrimSpace(value)
			itemIDParts++
		case "file":
			request.fileName = part.FileName()
			request.mimeType = part.Header.Get("Content-Type")
			request.content, err = importFilePartBytes(part)
			if err != nil {
				_ = part.Close()
				return importQueueUploadRequest{}, err
			}
			fileParts++
		default:
			_ = part.Close()
			return importQueueUploadRequest{}, ErrValidation
		}
		_ = part.Close()
	}
	if itemIDParts != 1 || fileParts != 1 {
		return importQueueUploadRequest{}, ErrValidation
	}
	return request, nil
}
