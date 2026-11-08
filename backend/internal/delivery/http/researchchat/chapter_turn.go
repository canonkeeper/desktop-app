package researchchat

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/chapterfile"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	service "canonkeeper/backend/internal/services/researchchat"

	"github.com/go-chi/chi/v5"
)

// The question with the chapter of the author over HTTP (specs/2026-11-07-chat-attach-chapter.md CA2, CA4.3): one
// multipart request with the question and the file or the text, and the attachment read back for the page.

// maxChapterTurnBody bounds a question with a chapter: the file and the small question with their headers (CA2.3).
const maxChapterTurnBody = chapterfile.MaxFileBytes + 1<<20

// maxQuestionPartBytes bounds the part of the question: 8 000 signs of four bytes at most.
const maxQuestionPartBytes = 32 << 10

// SubmitTurnWithChapter is createChatTurnWithChapter (CA2.1): the same turn as a question, with the chapter attached.
func (handlers Handlers) SubmitTurnWithChapter(response http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(response, request)
	if !ok {
		return
	}
	input, err := decodeChapterTurn(response, request)
	if err != nil {
		writeChapterError(response, err)
		return
	}
	input.IdempotencyKey = strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	input.RequestMethod, input.RequestPath = request.Method, request.URL.Path
	input.SessionWriteProof = sessionWriteProof(request.Context())
	job, err := handlers.submit.Execute(request.Context(), actor, chi.URLParam(request, "chatId"), input)
	if err != nil {
		writeChapterError(response, err)
		return
	}
	if err := writeJobStart(response, request.Context(), job); err != nil {
		writeServiceError(response, err)
	}
}

// decodeChapterTurn reads exactly one part «body» and exactly one part «file» (with its name) or «text»; anything else is
// a bad request, a body past the limit is too large.
func decodeChapterTurn(response http.ResponseWriter, request *http.Request) (service.SubmitTurnInput, error) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		return service.SubmitTurnInput{}, researchdomain.ErrValidation
	}
	request.Body = http.MaxBytesReader(response, request.Body, maxChapterTurnBody)
	reader, err := request.MultipartReader()
	if err != nil {
		return service.SubmitTurnInput{}, researchdomain.ErrValidation
	}
	var (
		input   service.SubmitTurnInput
		chapter service.ChapterInput
		seen    = map[string]bool{}
	)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return service.SubmitTurnInput{}, partError(err)
		}
		name := part.FormName()
		if seen[name] || name == "file" && seen["text"] || name == "text" && seen["file"] {
			return service.SubmitTurnInput{}, researchdomain.ErrValidation
		}
		seen[name] = true
		switch {
		case name == "body" && part.FileName() == "":
			raw, err := io.ReadAll(io.LimitReader(part, maxQuestionPartBytes+1))
			if err != nil {
				return service.SubmitTurnInput{}, partError(err)
			}
			if len(raw) > maxQuestionPartBytes || !utf8.Valid(raw) {
				return service.SubmitTurnInput{}, researchdomain.ErrValidation
			}
			input.Question = string(raw)
		case name == "file" && part.FileName() != "":
			chapter.FileName = part.FileName()
			if chapter.Content, err = io.ReadAll(io.LimitReader(part, chapterfile.MaxFileBytes+1)); err != nil {
				return service.SubmitTurnInput{}, partError(err)
			}
			if len(chapter.Content) > chapterfile.MaxFileBytes {
				return service.SubmitTurnInput{}, chapterfile.ErrTooLarge
			}
		case name == "text" && part.FileName() == "":
			raw, err := io.ReadAll(io.LimitReader(part, chapterfile.MaxFileBytes+1))
			if err != nil {
				return service.SubmitTurnInput{}, partError(err)
			}
			if len(raw) > chapterfile.MaxFileBytes {
				return service.SubmitTurnInput{}, chapterfile.ErrTooLarge
			}
			if !utf8.Valid(raw) {
				return service.SubmitTurnInput{}, researchdomain.ErrValidation
			}
			chapter.Text, chapter.HasText = string(raw), true
		default:
			return service.SubmitTurnInput{}, researchdomain.ErrValidation
		}
	}
	if !seen["body"] || !seen["file"] && !seen["text"] {
		return service.SubmitTurnInput{}, researchdomain.ErrValidation
	}
	input.Chapter = &chapter
	return input, nil
}

func partError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return chapterfile.ErrTooLarge
	}
	return researchdomain.ErrValidation
}

// writeChapterError names the refusals of the chapter (CA2.3), then those of every question.
func writeChapterError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, chapterfile.ErrTooLarge):
		writeProblem(response, http.StatusRequestEntityTooLarge, "Content Too Large", "the chapter file is over 10 MiB", "chat.attachment_too_large")
	case errors.Is(err, chapterfile.ErrFormat):
		writeProblem(response, http.StatusUnprocessableEntity, "Unprocessable Entity", "a chapter is read from .docx, .txt or .md", "chat.attachment_format")
	case errors.Is(err, chapterfile.ErrUnreadable):
		writeProblem(response, http.StatusUnprocessableEntity, "Unprocessable Entity", "the chapter file cannot be read", "chat.attachment_unreadable")
	case errors.Is(err, chapterfile.ErrTooLong):
		writeProblem(response, http.StatusUnprocessableEntity, "Unprocessable Entity", "the chapter is longer than 400 000 signs", "chat.attachment_too_long")
	case errors.Is(err, chapterfile.ErrEmpty):
		writeProblem(response, http.StatusUnprocessableEntity, "Unprocessable Entity", "the chapter has no text", "chat.attachment_empty")
	default:
		writeServiceError(response, err)
	}
}

// attachmentView is ChatAttachment of the contract.
type attachmentView struct {
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	FileName   string    `json:"fileName,omitempty"`
	Format     string    `json:"format"`
	Words      int       `json:"words"`
	Paragraphs []string  `json:"paragraphs"`
	CreatedAt  time.Time `json:"createdAt"`
}

// GetAttachment is getChatAttachment (CA4.3): the chapter attached to a question, for whoever reads the chat.
func (handlers Handlers) GetAttachment(response http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(response, request)
	if !ok {
		return
	}
	raw := chi.URLParam(request, "number")
	number, err := strconv.Atoi(raw)
	if err != nil || number < 1 || number > researchdomain.MaxAttachmentNumber || strconv.Itoa(number) != raw || len(request.URL.Query()) > 0 {
		writeServiceError(response, researchdomain.ErrValidation)
		return
	}
	attachment, err := handlers.attachment.Execute(request.Context(), actor, chi.URLParam(request, "chatId"), number)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, attachmentView{
		Number: attachment.Number, Title: attachment.Title, FileName: attachment.FileName, Format: attachment.Format,
		Words: attachment.Words, Paragraphs: append([]string{}, attachment.Paragraphs...), CreatedAt: attachment.CreatedAt.UTC(),
	})
}
