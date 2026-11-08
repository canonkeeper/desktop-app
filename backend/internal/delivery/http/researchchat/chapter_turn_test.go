package researchchat

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/chapterfile"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"

	"github.com/go-chi/chi/v5"
)

type chapterPart struct{ name, fileName, value string }

func chapterRequest(t *testing.T, key string, parts ...chapterPart) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range parts {
		var (
			field interface{ Write([]byte) (int, error) }
			err   error
		)
		if part.fileName != "" {
			field, err = writer.CreateFormFile(part.name, part.fileName)
		} else {
			field, err = writer.CreateFormField(part.name)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := field.Write([]byte(part.value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/chats/chat-1/turns/chapter", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Origin", "https://app.test")
	request.Header.Set("X-CSRF-Token", "csrf")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	return request
}

func chapterRouter(t *testing.T, submit SubmitTurnFunc, attachment GetAttachmentFunc) chi.Router {
	t.Helper()
	handlers := NewHandlers(
		CreateSessionFunc(func(context.Context, researchdomain.Actor, service.CreateSessionInput) (researchdomain.Session, error) {
			return researchdomain.Session{}, nil
		}),
		ListSessionsFunc(func(context.Context, researchdomain.Actor, service.ListSessionsInput) (researchdomain.SessionPage, error) {
			return researchdomain.SessionPage{}, nil
		}),
		GetSessionFunc(func(context.Context, researchdomain.Actor, string) (researchdomain.Session, error) {
			return researchdomain.Session{}, nil
		}),
		ResolveChapterKeyFunc(func(context.Context, researchdomain.Actor, string, string) (researchdomain.ChapterLocation, error) {
			return researchdomain.ChapterLocation{}, nil
		}),
		UpdateSessionFunc(func(context.Context, service.UpdateSessionInput) (researchdomain.Session, error) {
			return researchdomain.Session{}, nil
		}),
		DeleteSessionFunc(func(context.Context, researchdomain.Actor, string) error { return nil }),
		submit,
		SteerTurnFunc(func(context.Context, researchdomain.Actor, string, string, service.SteerTurnInput) (string, error) {
			return "", nil
		}),
		attachment,
		currentUserStub{proof: auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"}},
		[]string{"https://app.test"},
	)
	router := chi.NewRouter()
	handlers.Mount(router)
	return router
}

// specs/2026-11-07-chat-attach-chapter.md CA2.1, CA2.3: one question and exactly one chapter — a file or a text — go to
// the same use case as a question; everything else is refused before it.
func TestTurnWithChapterHandsTheQuestionAndTheChapterToTheTurn(t *testing.T) {
	var got []service.SubmitTurnInput
	router := chapterRouter(t, func(_ context.Context, _ researchdomain.Actor, chatID string, input service.SubmitTurnInput) (jobs.Job, error) {
		got = append(got, input)
		return jobs.Job{}, researchdomain.ErrOutdatedAgent
	}, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, chapterRequest(t, "key-1",
		chapterPart{name: "body", value: "Ложится ли глава на канон?"}, chapterPart{name: "file", fileName: "634.md", value: "# Глава 634\n\nКайл шёл."}))
	if len(got) != 1 || got[0].Question != "Ложится ли глава на канон?" || got[0].Chapter == nil || got[0].Chapter.FileName != "634.md" ||
		string(got[0].Chapter.Content) != "# Глава 634\n\nКайл шёл." || got[0].IdempotencyKey != "key-1" ||
		got[0].RequestPath != "/chats/chat-1/turns/chapter" || !got[0].SessionWriteProof.Valid() {
		t.Fatalf("submitted = %+v", got)
	}
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"chat.outdated_agent"`) {
		t.Fatalf("the refusal of the turn = %d %s", response.Code, response.Body.String())
	}
	router.ServeHTTP(httptest.NewRecorder(), chapterRequest(t, "key-2",
		chapterPart{name: "body", value: "Где противоречит?"}, chapterPart{name: "text", value: "Кайл шёл.\nОльм отстал."}))
	if len(got) != 2 || got[1].Chapter == nil || !got[1].Chapter.HasText || got[1].Chapter.Text != "Кайл шёл.\nОльм отстал." {
		t.Fatalf("a pasted chapter = %+v", got)
	}

	for name, parts := range map[string][]chapterPart{
		"no question":     {{name: "text", value: "Кайл шёл."}},
		"no chapter":      {{name: "body", value: "Вопрос"}},
		"two questions":   {{name: "body", value: "А"}, {name: "body", value: "Б"}, {name: "text", value: "Кайл шёл."}},
		"file and text":   {{name: "body", value: "А"}, {name: "file", fileName: "a.txt", value: "x"}, {name: "text", value: "y"}},
		"another field":   {{name: "body", value: "А"}, {name: "text", value: "x"}, {name: "title", value: "y"}},
		"file as a field": {{name: "body", value: "А"}, {name: "file", value: "x"}},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, chapterRequest(t, "key-3", parts...))
		if response.Code != http.StatusBadRequest || len(got) != 2 {
			t.Fatalf("%s = %d %s", name, response.Code, response.Body.String())
		}
	}
}

// CA2.3: the refusals of the reader of the chapter are the chat's own, and a body past the limit is too large.
func TestTurnWithChapterNamesTheRefusalsOfTheChapter(t *testing.T) {
	for refusal, want := range map[error]struct {
		status int
		code   string
	}{
		chapterfile.ErrTooLarge:   {http.StatusRequestEntityTooLarge, "chat.attachment_too_large"},
		chapterfile.ErrFormat:     {http.StatusUnprocessableEntity, "chat.attachment_format"},
		chapterfile.ErrUnreadable: {http.StatusUnprocessableEntity, "chat.attachment_unreadable"},
		chapterfile.ErrTooLong:    {http.StatusUnprocessableEntity, "chat.attachment_too_long"},
		chapterfile.ErrEmpty:      {http.StatusUnprocessableEntity, "chat.attachment_empty"},
	} {
		router := chapterRouter(t, func(context.Context, researchdomain.Actor, string, service.SubmitTurnInput) (jobs.Job, error) {
			return jobs.Job{}, refusal
		}, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, chapterRequest(t, "key-1", chapterPart{name: "body", value: "Вопрос"}, chapterPart{name: "text", value: "Кайл"}))
		if response.Code != want.status || !strings.Contains(response.Body.String(), `"code":"`+want.code+`"`) {
			t.Fatalf("%v = %d %s", refusal, response.Code, response.Body.String())
		}
	}
	called := false
	router := chapterRouter(t, func(context.Context, researchdomain.Actor, string, service.SubmitTurnInput) (jobs.Job, error) {
		called = true
		return jobs.Job{}, nil
	}, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, chapterRequest(t, "key-1", chapterPart{name: "body", value: "Вопрос"},
		chapterPart{name: "file", fileName: "634.txt", value: strings.Repeat("я", chapterfile.MaxFileBytes/2+1)}))
	if called || response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "chat.attachment_too_large") {
		t.Fatalf("a file past the limit = %d %s", response.Code, response.Body.String())
	}
}

// CA4.3: the attachment of a chat is read whole by whoever reads the chat; a number that is not one is refused.
func TestGetAttachmentShowsTheChapterOfTheChat(t *testing.T) {
	var asked []int
	router := chapterRouter(t, nil, func(_ context.Context, actor researchdomain.Actor, chatID string, number int) (researchdomain.Attachment, error) {
		asked = append(asked, number)
		if actor.UserID != "user-1" || chatID != "chat-1" || number != 2 {
			return researchdomain.Attachment{}, researchdomain.ErrNotFound
		}
		return researchdomain.Attachment{
			Number: 2, Title: "Глава 634", FileName: "634.docx", Format: "docx", Paragraphs: []string{"Кайл шёл.", "Ольм отстал."},
			Words: 4, CreatedAt: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC),
		}, nil
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/chats/chat-1/attachments/2", nil))
	var view map[string]any
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(response.Body.Bytes(), &view) != nil ||
		view["title"] != "Глава 634" || view["fileName"] != "634.docx" || len(view["paragraphs"].([]any)) != 2 || view["words"] != float64(4) {
		t.Fatalf("attachment = %d %s", response.Code, response.Body.String())
	}
	for path, status := range map[string]int{"/chats/chat-1/attachments/3": http.StatusNotFound, "/chats/chat-1/attachments/x": http.StatusBadRequest,
		"/chats/chat-1/attachments/0": http.StatusBadRequest, "/chats/chat-1/attachments/02": http.StatusBadRequest} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != status {
			t.Fatalf("%s = %d %s", path, response.Code, response.Body.String())
		}
	}
	if len(asked) != 2 {
		t.Fatalf("asked = %v", asked)
	}
}
