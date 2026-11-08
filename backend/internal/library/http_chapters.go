package library

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h Handlers) ListChapters(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	page, err := bookPageFromRequest(r)
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	chapters, err := h.service.ListChapters(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "bookId"), page)
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	writeChapterListResponse(w, http.StatusOK, chapters, page.State)
}

func (h Handlers) GetChapter(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	variant := ParagraphVariant(r.URL.Query().Get("contentVariant"))
	if variant == "" {
		variant = ParagraphVariantDraft
	}
	chapter, err := h.service.GetChapter(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "chapterId"), variant)
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	writeChapterResponse(w, http.StatusOK, chapter)
}

func (h Handlers) DeleteChapter(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	if err := h.service.DeleteChapter(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "chapterId")); err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h Handlers) RestoreChapter(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	chapter, err := h.service.RestoreChapter(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "chapterId"))
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeImportJSON(w, http.StatusOK, chapterSummaryResponseFromChapter(ChapterSummary{
		Chapter:  chapter,
		HasDraft: chapterHasDraft(chapter),
	}))
}

type chapterListResponse struct {
	Data []chapterSummaryResponse `json:"data"`
	Meta pageMeta                 `json:"meta"`
}

type deletedChapterResponse struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	DeletedAt    *string `json:"deletedAt"`
	RestoreUntil *string `json:"restoreUntil"`
}

type deletedChapterListResponse struct {
	Data []deletedChapterResponse `json:"data"`
	Meta pageMeta                 `json:"meta"`
}

type chapterSummaryResponse struct {
	ID            string        `json:"id"`
	BookID        string        `json:"bookId"`
	Title         string        `json:"title"`
	Order         int           `json:"order"`
	DisplayNumber string        `json:"displayNumber"`
	Status        ChapterStatus `json:"status"`
	WordCount     int           `json:"wordCount"`
	HasDraft      bool          `json:"hasDraft"`
	DeletedAt     *string       `json:"deletedAt,omitempty"`
	RestoreUntil  *string       `json:"restoreUntil,omitempty"`
}

type chapterResponse struct {
	ID                string                     `json:"id"`
	BookID            string                     `json:"bookId"`
	Title             string                     `json:"title"`
	Order             int                        `json:"order"`
	ContentVariant    ParagraphVariant           `json:"contentVariant"`
	Status            ChapterStatus              `json:"status"`
	Paragraphs        []chapterParagraphResponse `json:"paragraphs"`
	WordCount         int                        `json:"wordCount"`
	Revision          int64                      `json:"revision"`
	DraftRevision     int64                      `json:"draftRevision"`
	PublishedRevision *int64                     `json:"publishedRevision"`
	SavedAt           string                     `json:"savedAt"`
	PublishedAt       *string                    `json:"publishedAt"`
	PublishError      *string                    `json:"publishError"`
	Navigation        chapterNavigationResponse  `json:"navigation"`
	CreatedAt         string                     `json:"createdAt"`
	UpdatedAt         string                     `json:"updatedAt"`
}

type chapterParagraphResponse struct {
	ID       string        `json:"id"`
	Order    int           `json:"order"`
	Kind     ParagraphKind `json:"kind"`
	Text     string        `json:"text"`
	Markdown string        `json:"markdown"`
}

type chapterNavigationResponse struct {
	DisplayNumber      string                         `json:"displayNumber"`
	Position           int                            `json:"position"`
	Total              int                            `json:"total"`
	ReadingProgress    float64                        `json:"readingProgress"`
	ReadingTimeMinutes int                            `json:"readingTimeMinutes"`
	PreviousChapterID  *string                        `json:"previousChapterId"`
	NextChapterID      *string                        `json:"nextChapterId"`
	Previous           *chapterNavigationItemResponse `json:"previous"`
	Next               *chapterNavigationItemResponse `json:"next"`
}

type chapterNavigationItemResponse struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	DisplayNumber string `json:"displayNumber"`
}

func writeChapterListResponse(w http.ResponseWriter, status int, page ChapterPage, state SourceListState) {
	meta := pageMeta{HasMore: page.Meta.HasMore, NextCursor: page.Meta.NextCursor}
	if state == SourceListDeleted {
		response := deletedChapterListResponse{Data: make([]deletedChapterResponse, 0, len(page.Data)), Meta: meta}
		for _, chapter := range page.Data {
			item := deletedChapterResponse{ID: chapter.ID, Title: chapter.Title}
			if chapter.DeletedAt != nil {
				deletedAt := formatImportTime(*chapter.DeletedAt)
				item.DeletedAt = &deletedAt
			}
			if chapter.RestoreUntil != nil {
				restoreUntil := formatImportTime(*chapter.RestoreUntil)
				item.RestoreUntil = &restoreUntil
			}
			response.Data = append(response.Data, item)
		}
		w.Header().Set("Cache-Control", "no-store")
		writeImportJSON(w, status, response)
		return
	}
	response := chapterListResponse{
		Data: make([]chapterSummaryResponse, 0, len(page.Data)),
		Meta: meta,
	}
	for _, chapter := range page.Data {
		response.Data = append(response.Data, chapterSummaryResponseFromChapter(chapter))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeImportJSON(w, status, response)
}
func chapterSummaryResponseFromChapter(chapter ChapterSummary) chapterSummaryResponse {
	response := chapterSummaryResponse{
		ID:            chapter.ID,
		BookID:        chapter.BookID,
		Title:         chapter.Title,
		Order:         chapter.Order,
		DisplayNumber: ChapterDisplayNumber(chapter.Order, chapter.Title),
		Status:        chapter.Status,
		WordCount:     chapter.WordCount,
		HasDraft:      chapter.HasDraft,
	}
	if chapter.DeletedAt != nil {
		deletedAt := formatImportTime(*chapter.DeletedAt)
		response.DeletedAt = &deletedAt
	}
	if chapter.RestoreUntil != nil {
		restoreUntil := formatImportTime(*chapter.RestoreUntil)
		response.RestoreUntil = &restoreUntil
	}
	return response
}

func writeChapterResponse(w http.ResponseWriter, status int, chapter ChapterContent) {
	w.Header().Set("Cache-Control", "no-store")
	writeImportJSON(w, status, chapterResponseFromContent(chapter))
}

func chapterResponseFromContent(chapter ChapterContent) chapterResponse {
	paragraphs := make([]chapterParagraphResponse, 0, len(chapter.Paragraphs))
	for _, paragraph := range chapter.Paragraphs {
		paragraphs = append(paragraphs, chapterParagraphResponse{
			ID:       paragraph.ID,
			Order:    paragraph.Order,
			Kind:     paragraph.Kind,
			Text:     paragraph.PlainText,
			Markdown: paragraph.Markdown,
		})
	}
	var publishedAt *string
	if chapter.PublishedAt != nil {
		formatted := formatImportTime(*chapter.PublishedAt)
		publishedAt = &formatted
	}
	var previous *chapterNavigationItemResponse
	var previousChapterID *string
	if chapter.Navigation.Previous != nil {
		previous = &chapterNavigationItemResponse{
			ID:            chapter.Navigation.Previous.ID,
			Title:         chapter.Navigation.Previous.Title,
			DisplayNumber: chapter.Navigation.Previous.DisplayNumber,
		}
		previousChapterID = &previous.ID
	}
	var next *chapterNavigationItemResponse
	var nextChapterID *string
	if chapter.Navigation.Next != nil {
		next = &chapterNavigationItemResponse{
			ID:            chapter.Navigation.Next.ID,
			Title:         chapter.Navigation.Next.Title,
			DisplayNumber: chapter.Navigation.Next.DisplayNumber,
		}
		nextChapterID = &next.ID
	}
	readingTimeMinutes := 0
	if chapter.WordCount > 0 {
		readingTimeMinutes = (chapter.WordCount + 219) / 220
	}
	return chapterResponse{
		ID:                chapter.ID,
		BookID:            chapter.BookID,
		Title:             chapter.Title,
		Order:             chapter.Order,
		ContentVariant:    chapter.ContentVariant,
		Status:            chapter.Status,
		Paragraphs:        paragraphs,
		WordCount:         chapter.WordCount,
		Revision:          chapter.ContentRevision,
		DraftRevision:     chapter.Revision,
		PublishedRevision: chapter.PublishedRevision,
		SavedAt:           formatImportTime(chapter.UpdatedAt),
		PublishedAt:       publishedAt,
		PublishError:      chapter.PublishError,
		Navigation: chapterNavigationResponse{
			DisplayNumber:      ChapterDisplayNumber(chapter.Order, chapter.Title),
			Position:           chapter.Navigation.Position,
			Total:              chapter.Navigation.Total,
			ReadingProgress:    0,
			ReadingTimeMinutes: readingTimeMinutes,
			PreviousChapterID:  previousChapterID,
			NextChapterID:      nextChapterID,
			Previous:           previous,
			Next:               next,
		},
		CreatedAt: formatImportTime(chapter.CreatedAt),
		UpdatedAt: formatImportTime(chapter.UpdatedAt),
	}
}
