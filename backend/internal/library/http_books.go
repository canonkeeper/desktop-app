package library

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type createBookRequest struct {
	Title         string            `json:"title"`
	Subtitle      *string           `json:"subtitle"`
	CoverColor    *string           `json:"coverColor"`
	DisplayNumber string            `json:"displayNumber"`
	AfterBookID   optionalIDRequest `json:"afterBookId"`
}
type updateBookRequest struct {
	Title       stringPatchRequest `json:"title"`
	Subtitle    stringPatchRequest `json:"subtitle"`
	CoverColor  stringPatchRequest `json:"coverColor"`
	AfterBookID optionalIDRequest  `json:"afterBookId"`
}
type stringPatchRequest struct {
	Present bool
	Value   *string
}

type optionalIDRequest struct {
	Present bool
	Value   *string
}

func (patch *stringPatchRequest) UnmarshalJSON(data []byte) error {
	patch.Present = true
	if strings.TrimSpace(string(data)) == "null" {
		patch.Value = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	patch.Value = &value
	return nil
}

func (field *optionalIDRequest) UnmarshalJSON(data []byte) error {
	field.Present = true
	if strings.TrimSpace(string(data)) == "null" {
		field.Value = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	field.Value = &value
	return nil
}
func (h Handlers) ListBooks(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	page, err := bookPageFromRequest(r)
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	books, err := h.service.ListBooks(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "projectId"), page)
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	writeBookListResponse(w, http.StatusOK, books, page.State)
}

func (h Handlers) GetBook(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	book, err := h.service.GetBook(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "bookId"))
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	writeBookResponse(w, http.StatusOK, book)
}

func (h Handlers) CreateBook(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	var request createBookRequest
	if err := decodeImportJSON(r, &request); err != nil {
		writeImportProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON request body", "request.invalid")
		return
	}
	book, err := h.service.CreateBook(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "projectId"), BookInput{
		Title:         request.Title,
		Subtitle:      request.Subtitle,
		CoverColor:    request.CoverColor,
		DisplayNumber: request.DisplayNumber,
		AfterBookID:   request.AfterBookID.toLibraryPlacement(),
	})
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	writeBookResponse(w, http.StatusCreated, book)
}

func (h Handlers) UpdateBook(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	var request updateBookRequest
	if err := decodeImportJSON(r, &request); err != nil {
		writeImportProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON request body", "request.invalid")
		return
	}
	book, err := h.service.UpdateBook(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "bookId"), UpdateBookInput{
		Title:       request.Title.toLibraryPatch(),
		Subtitle:    request.Subtitle.toLibraryPatch(),
		CoverColor:  request.CoverColor.toLibraryPatch(),
		AfterBookID: request.AfterBookID.toLibraryPlacement(),
	})
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	writeBookResponse(w, http.StatusOK, book)
}

func (h Handlers) DeleteBook(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	if err := h.service.DeleteBook(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "bookId")); err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h Handlers) RestoreBook(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	book, err := h.service.RestoreBook(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "bookId"))
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	writeBookResponse(w, http.StatusOK, book)
}

func (patch stringPatchRequest) toLibraryPatch() StringPatch {
	return StringPatch{
		Present: patch.Present,
		Value:   patch.Value,
	}
}

func (field optionalIDRequest) toLibraryPlacement() OrderPlacement {
	if !field.Present {
		return OrderPlacement{}
	}
	if field.Value == nil {
		return OrderPlacement{Present: true}
	}
	value := *field.Value
	return OrderPlacement{Present: true, AfterID: &value}
}

type bookResponse struct {
	ID            string     `json:"id"`
	ProjectID     string     `json:"projectId"`
	Title         string     `json:"title"`
	Subtitle      *string    `json:"subtitle"`
	Order         int        `json:"order"`
	DisplayNumber string     `json:"displayNumber"`
	CoverColor    *string    `json:"coverColor"`
	CoverURL      *string    `json:"coverUrl"`
	Status        BookStatus `json:"status"`
	ChapterCount  int        `json:"chapterCount"`
	WordCount     int        `json:"wordCount"`
	CreatedAt     string     `json:"createdAt"`
	UpdatedAt     string     `json:"updatedAt"`
	DeletedAt     *string    `json:"deletedAt,omitempty"`
	RestoreUntil  *string    `json:"restoreUntil,omitempty"`
}

type bookListResponse struct {
	Data []bookResponse `json:"data"`
	Meta pageMeta       `json:"meta"`
}

type deletedBookResponse struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	DeletedAt    *string `json:"deletedAt"`
	RestoreUntil *string `json:"restoreUntil"`
}

type deletedBookListResponse struct {
	Data []deletedBookResponse `json:"data"`
	Meta pageMeta              `json:"meta"`
}

func writeBookResponse(w http.ResponseWriter, status int, book Book) {
	w.Header().Set("Cache-Control", "no-store")
	writeImportJSON(w, status, BookResponseFromBook(book))
}

func writeBookListResponse(w http.ResponseWriter, status int, page BookPage, state SourceListState) {
	meta := pageMeta{HasMore: page.Meta.HasMore, NextCursor: page.Meta.NextCursor}
	if state == SourceListDeleted {
		response := deletedBookListResponse{Data: make([]deletedBookResponse, 0, len(page.Data)), Meta: meta}
		for _, book := range page.Data {
			item := deletedBookResponse{ID: book.ID, Title: book.Title}
			if book.DeletedAt != nil {
				deletedAt := formatImportTime(*book.DeletedAt)
				item.DeletedAt = &deletedAt
			}
			if book.RestoreUntil != nil {
				restoreUntil := formatImportTime(*book.RestoreUntil)
				item.RestoreUntil = &restoreUntil
			}
			response.Data = append(response.Data, item)
		}
		w.Header().Set("Cache-Control", "no-store")
		writeImportJSON(w, status, response)
		return
	}
	response := bookListResponse{
		Data: make([]bookResponse, 0, len(page.Data)),
		Meta: meta,
	}
	for _, book := range page.Data {
		response.Data = append(response.Data, BookResponseFromBook(book))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeImportJSON(w, status, response)
}
func BookResponseFromBook(book Book) bookResponse {
	response := bookResponse{
		ID:            book.ID,
		ProjectID:     book.ProjectID,
		Title:         book.Title,
		Subtitle:      book.Subtitle,
		Order:         book.Order,
		DisplayNumber: book.DisplayNumber,
		CoverColor:    book.CoverColor,
		CoverURL:      book.CoverURL,
		Status:        book.Status,
		ChapterCount:  book.ChapterCount,
		WordCount:     book.WordCount,
		CreatedAt:     formatImportTime(book.CreatedAt),
		UpdatedAt:     formatImportTime(book.UpdatedAt),
	}
	if book.DeletedAt != nil {
		deletedAt := formatImportTime(*book.DeletedAt)
		response.DeletedAt = &deletedAt
	}
	if book.RestoreUntil != nil {
		restoreUntil := formatImportTime(*book.RestoreUntil)
		response.RestoreUntil = &restoreUntil
	}
	return response
}

func bookPageFromRequest(r *http.Request) (Page, error) {
	page, err := pageFromRequest(r)
	if err != nil {
		return Page{}, err
	}
	state := SourceListState(r.URL.Query().Get("state"))
	if state != "" && state != SourceListActive && state != SourceListDeleted {
		return Page{}, ErrValidation
	}
	page.State = state
	return page, nil
}
