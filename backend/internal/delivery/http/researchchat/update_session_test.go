package researchchat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"

	"github.com/go-chi/chi/v5"
)

func TestUpdateSessionDecodesClosedRequestAndMapsActor(t *testing.T) {
	var input service.UpdateSessionInput
	calls := 0
	handlers := updateHTTPHandlers(UpdateSessionFunc(func(_ context.Context, got service.UpdateSessionInput) (researchdomain.Session, error) {
		input = got
		calls++
		session := researchdomain.Session{ID: got.ChatID, ProjectID: "project-1", Scope: researchdomain.ScopeProject, SourceView: "published"}
		if got.Title != nil {
			session.Title = *got.Title
		}
		return session, nil
	}))
	router := chi.NewRouter()
	handlers.Mount(router)

	request := updateRequest(`{"title":"Новое название"}`)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || calls != 1 || input.Actor.UserID != "user-1" || input.ChatID != "chat-1" ||
		input.Title == nil || *input.Title != "Новое название" || strings.Contains(response.Body.String(), "illustrationsEnabled") {
		t.Fatalf("response/input/calls = %d/%#v/%d: %s", response.Code, input, calls, response.Body.String())
	}

	for name, body := range map[string]string{
		"unknown":   `{"title":"x","extra":true}`,
		"malformed": `{"title":`,
		"nothing":   `{}`,
		// specs/2026-11-02-image-workshop.md IW7.1: the toggle «Рисунки» is gone with the drawings of the chat.
		"toggle": `{"illustrationsEnabled":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			before := calls
			response := httptest.NewRecorder()
			router.ServeHTTP(response, updateRequest(body))
			if response.Code != http.StatusBadRequest || calls != before {
				t.Fatalf("response/calls = %d/%d: %s", response.Code, calls, response.Body.String())
			}
		})
	}
}

func TestUpdateSessionMapsValidationRoleAndRaceProblems(t *testing.T) {
	tests := map[string]struct {
		err    error
		status int
		code   string
	}{
		"empty":     {researchdomain.ErrTitleEmpty, http.StatusUnprocessableEntity, "title_empty"},
		"too long":  {researchdomain.ErrTitleTooLong, http.StatusUnprocessableEntity, "title_too_long"},
		"viewer":    {researchdomain.ErrForbidden, http.StatusForbidden, "forbidden"},
		"concealed": {researchdomain.ErrNotFound, http.StatusNotFound, "not_found"},
		"race":      {researchdomain.ErrConflict, http.StatusConflict, "conflict"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			handlers := updateHTTPHandlers(UpdateSessionFunc(func(context.Context, service.UpdateSessionInput) (researchdomain.Session, error) {
				return researchdomain.Session{}, test.err
			}))
			router := chi.NewRouter()
			handlers.Mount(router)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, updateRequest(`{"title":"x"}`))
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func updateHTTPHandlers(update UpdateSessionUseCase) Handlers {
	return NewHandlers(
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
		update,
		DeleteSessionFunc(func(context.Context, researchdomain.Actor, string) error { return nil }),
		SubmitTurnFunc(func(context.Context, researchdomain.Actor, string, service.SubmitTurnInput) (jobs.Job, error) {
			return jobs.Job{}, nil
		}),
		SteerTurnFunc(func(context.Context, researchdomain.Actor, string, string, service.SteerTurnInput) (string, error) {
			return "", nil
		}),
		GetAttachmentFunc(func(context.Context, researchdomain.Actor, string, int) (researchdomain.Attachment, error) {
			return researchdomain.Attachment{}, researchdomain.ErrNotFound
		}),
		currentUserStub{},
		[]string{"https://app.test"},
	)
}

func updateRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPatch, "/chats/chat-1", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://app.test")
	request.Header.Set("X-CSRF-Token", "csrf")
	return request
}
