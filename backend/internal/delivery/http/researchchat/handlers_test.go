package researchchat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"

	"github.com/go-chi/chi/v5"
)

type currentUserStub struct {
	proof auth.SessionWriteProof
}

func (currentUserStub) CurrentUser(context.Context, string) (auth.User, error) {
	return auth.User{ID: "user-1"}, nil
}

func (stub currentUserStub) AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error) {
	return auth.User{ID: "user-1"}, stub.proof, nil
}

func TestCreateSessionMapsActorSessionAndIdempotency(t *testing.T) {
	proof := auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"}
	var actor researchdomain.Actor
	var input service.CreateSessionInput
	handlers := NewHandlers(
		CreateSessionFunc(func(_ context.Context, gotActor researchdomain.Actor, gotInput service.CreateSessionInput) (researchdomain.Session, error) {
			actor, input = gotActor, gotInput
			return researchdomain.Session{ID: "chat-1", ProjectID: gotInput.ProjectID}, nil
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
		SubmitTurnFunc(func(context.Context, researchdomain.Actor, string, service.SubmitTurnInput) (jobs.Job, error) {
			return jobs.Job{}, nil
		}),
		SteerTurnFunc(func(context.Context, researchdomain.Actor, string, string, service.SteerTurnInput) (string, error) {
			return "", nil
		}),
		GetAttachmentFunc(func(context.Context, researchdomain.Actor, string, int) (researchdomain.Attachment, error) {
			return researchdomain.Attachment{}, researchdomain.ErrNotFound
		}),
		currentUserStub{proof: proof},
		[]string{"https://app.test"},
	)
	router := chi.NewRouter()
	handlers.Mount(router)
	request := httptest.NewRequest(http.MethodPost, "/projects/project-1/chats", strings.NewReader(
		`{"sourceView":"draft","scope":"project","title":"Исследование","savedAssignment":"Проверь первую главу"}`,
	))
	request.Header.Set("Origin", "https://app.test")
	request.Header.Set("X-CSRF-Token", "csrf")
	request.Header.Set("Idempotency-Key", "idempotency-1")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated || actor.UserID != "user-1" || input.ProjectID != "project-1" ||
		input.SavedAssignment != "Проверь первую главу" ||
		input.IdempotencyKey != "idempotency-1" || input.SessionWriteProof != proof ||
		input.RequestMethod != http.MethodPost || input.RequestPath != "/projects/project-1/chats" {
		t.Fatalf("response/input = (%d, actor=%+v, input=%+v)", response.Code, actor, input)
	}
}

func TestTurnInOutdatedChatReturnsOutdatedAgentConflict(t *testing.T) {
	proof := auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"}
	var submitted service.SubmitTurnInput
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
		SubmitTurnFunc(func(_ context.Context, _ researchdomain.Actor, _ string, input service.SubmitTurnInput) (jobs.Job, error) {
			submitted = input
			return jobs.Job{}, researchdomain.ErrOutdatedAgent
		}),
		SteerTurnFunc(func(context.Context, researchdomain.Actor, string, string, service.SteerTurnInput) (string, error) {
			return "", nil
		}),
		GetAttachmentFunc(func(context.Context, researchdomain.Actor, string, int) (researchdomain.Attachment, error) {
			return researchdomain.Attachment{}, researchdomain.ErrNotFound
		}),
		currentUserStub{proof: proof},
		[]string{"https://app.test"},
	)
	router := chi.NewRouter()
	handlers.Mount(router)
	// The chat draws nothing and takes no images (image-workshop IW7.1, IW7.2): neither the images chosen for a drawing
	// nor the author's images attached to a question are part of it — the author's images live in the workshop.
	for index, body := range []string{`{"body":"Что дальше?","references":[7,12]}`, `{"body":"Что дальше?","images":[81,82]}`} {
		withImages := httptest.NewRequest(http.MethodPost, "/chats/chat-1/turns", strings.NewReader(body))
		withImages.Header.Set("Origin", "https://app.test")
		withImages.Header.Set("X-CSRF-Token", "csrf")
		withImages.Header.Set("Idempotency-Key", "idempotency-0"+strconv.Itoa(index))
		refused := httptest.NewRecorder()
		router.ServeHTTP(refused, withImages)
		if refused.Code != http.StatusBadRequest {
			t.Fatalf("a question with images %s = %d %s", body, refused.Code, refused.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/chats/chat-1/turns", strings.NewReader(`{"body":"Что дальше?"}`))
	request.Header.Set("Origin", "https://app.test")
	request.Header.Set("X-CSRF-Token", "csrf")
	request.Header.Set("Idempotency-Key", "idempotency-1")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"chat.outdated_agent"`) {
		t.Fatalf("turn in an outdated chat = %d %s", response.Code, response.Body.String())
	}
	if submitted.Question != "Что дальше?" {
		t.Fatalf("submitted question = %q", submitted.Question)
	}
}

func TestResolveChapterKeyMapsActorChatAndKey(t *testing.T) {
	var gotActor, gotChat, gotKey string
	handlers := Handlers{currentUser: currentUserStub{}, chapterKey: ResolveChapterKeyFunc(func(_ context.Context, actor researchdomain.Actor, chatID, key string) (researchdomain.ChapterLocation, error) {
		gotActor, gotChat, gotKey = actor.UserID, chatID, key
		if key != "312" {
			return researchdomain.ChapterLocation{}, researchdomain.ErrNotFound
		}
		return researchdomain.ChapterLocation{ChapterKey: "312", BookID: "book-9", ChapterID: "chapter-312", BookTitle: "Том 9", Title: "Глава 312"}, nil
	})}
	router := chi.NewRouter()
	handlers.Mount(router)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/chats/chat-1/chapters/312", nil))
	if response.Code != http.StatusOK || gotActor != "user-1" || gotChat != "chat-1" || gotKey != "312" ||
		!strings.Contains(response.Body.String(), `"chapterId":"chapter-312"`) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("resolve = %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/chats/chat-1/chapters/b9c99", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown key = %d", response.Code)
	}
}

type CreateSessionFunc func(context.Context, researchdomain.Actor, service.CreateSessionInput) (researchdomain.Session, error)

func (function CreateSessionFunc) Execute(ctx context.Context, actor researchdomain.Actor, input service.CreateSessionInput) (researchdomain.Session, error) {
	return function(ctx, actor, input)
}

type ListSessionsFunc func(context.Context, researchdomain.Actor, service.ListSessionsInput) (researchdomain.SessionPage, error)

func (function ListSessionsFunc) Execute(ctx context.Context, actor researchdomain.Actor, input service.ListSessionsInput) (researchdomain.SessionPage, error) {
	return function(ctx, actor, input)
}

type GetSessionFunc func(context.Context, researchdomain.Actor, string) (researchdomain.Session, error)

func (function GetSessionFunc) Execute(ctx context.Context, actor researchdomain.Actor, chatID string) (researchdomain.Session, error) {
	return function(ctx, actor, chatID)
}

type ResolveChapterKeyFunc func(context.Context, researchdomain.Actor, string, string) (researchdomain.ChapterLocation, error)

func (function ResolveChapterKeyFunc) Execute(ctx context.Context, actor researchdomain.Actor, chatID, key string) (researchdomain.ChapterLocation, error) {
	return function(ctx, actor, chatID, key)
}

type UpdateSessionFunc func(context.Context, service.UpdateSessionInput) (researchdomain.Session, error)

func (function UpdateSessionFunc) Execute(ctx context.Context, input service.UpdateSessionInput) (researchdomain.Session, error) {
	return function(ctx, input)
}

type DeleteSessionFunc func(context.Context, researchdomain.Actor, string) error

func (function DeleteSessionFunc) Execute(ctx context.Context, actor researchdomain.Actor, chatID string) error {
	return function(ctx, actor, chatID)
}

type SubmitTurnFunc func(context.Context, researchdomain.Actor, string, service.SubmitTurnInput) (jobs.Job, error)

func (function SubmitTurnFunc) Execute(ctx context.Context, actor researchdomain.Actor, chatID string, input service.SubmitTurnInput) (jobs.Job, error) {
	return function(ctx, actor, chatID, input)
}

type GetAttachmentFunc func(context.Context, researchdomain.Actor, string, int) (researchdomain.Attachment, error)

func (function GetAttachmentFunc) Execute(ctx context.Context, actor researchdomain.Actor, chatID string, number int) (researchdomain.Attachment, error) {
	return function(ctx, actor, chatID, number)
}

type SteerTurnFunc func(context.Context, researchdomain.Actor, string, string, service.SteerTurnInput) (string, error)

func (function SteerTurnFunc) Execute(ctx context.Context, actor researchdomain.Actor, chatID, turnID string, input service.SteerTurnInput) (string, error) {
	return function(ctx, actor, chatID, turnID, input)
}

func steerRouter(t *testing.T, steer SteerTurnFunc, currentUser CurrentUserService) chi.Router {
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
		SubmitTurnFunc(func(context.Context, researchdomain.Actor, string, service.SubmitTurnInput) (jobs.Job, error) {
			return jobs.Job{}, nil
		}),
		steer,
		GetAttachmentFunc(func(context.Context, researchdomain.Actor, string, int) (researchdomain.Attachment, error) {
			return researchdomain.Attachment{}, researchdomain.ErrNotFound
		}),
		currentUser,
		[]string{"https://app.test"},
	)
	router := chi.NewRouter()
	handlers.Mount(router)
	return router
}

func steerRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/chats/chat-1/turns/turn-1/steer", strings.NewReader(body))
	request.Header.Set("Origin", "https://app.test")
	request.Header.Set("X-CSRF-Token", "csrf")
	request.Header.Set("Idempotency-Key", "steer-key-1")
	return request
}

// specs/2026-10-30-chat-steering.md R1.1: the operation maps the body, the key, the proof and the path.
func TestSteerTurnMapsTheRequest(t *testing.T) {
	proof := auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"}
	var chat, turn string
	var input service.SteerTurnInput
	router := steerRouter(t, func(_ context.Context, _ researchdomain.Actor, chatID, turnID string, got service.SteerTurnInput) (string, error) {
		chat, turn, input = chatID, turnID, got
		return "steer-1", nil
	}, currentUserStub{proof: proof})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, steerRequest(`{"body":"Без дракона","attachedImages":[90],"firstPhoto":3}`))
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"steerId":"steer-1"`) ||
		response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if chat != "chat-1" || turn != "turn-1" || input.Text != "Без дракона" || len(input.Images) != 1 || input.Images[0] != 90 ||
		input.FirstPhoto != 3 || input.IdempotencyKey != "steer-key-1" || input.SessionWriteProof != proof ||
		input.RequestMethod != http.MethodPost || input.RequestPath != "/chats/chat-1/turns/turn-1/steer" {
		t.Fatalf("input = %q %q %+v", chat, turn, input)
	}
}

// R1.2: the strict Origin and the CSRF proof of every chat write hold the operation too.
func TestSteerTurnRefusesAForeignOriginAndAWrongCSRF(t *testing.T) {
	called := false
	steer := func(context.Context, researchdomain.Actor, string, string, service.SteerTurnInput) (string, error) {
		called = true
		return "steer-1", nil
	}
	foreign := steerRequest(`{"body":"Без дракона"}`)
	foreign.Header.Set("Origin", "https://evil.test")
	response := httptest.NewRecorder()
	steerRouter(t, steer, currentUserStub{}).ServeHTTP(response, foreign)
	if response.Code != http.StatusForbidden || called {
		t.Fatalf("foreign origin = %d, called %v", response.Code, called)
	}
	response = httptest.NewRecorder()
	steerRouter(t, steer, csrfRefusingUser{}).ServeHTTP(response, steerRequest(`{"body":"Без дракона"}`))
	if response.Code != http.StatusForbidden || called || !strings.Contains(response.Body.String(), "csrf.invalid") {
		t.Fatalf("wrong CSRF = %d %s, called %v", response.Code, response.Body.String(), called)
	}
}

// R2.1, R2.2, R1.8: each outcome has its own code; a hidden chat is a plain 404.
func TestSteerTurnAnswersEachOutcomeWithItsCode(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{researchdomain.ErrTurnEnded, http.StatusConflict, "chat.turn_ended"},
		{researchdomain.ErrSteerRetry, http.StatusConflict, "chat.steer_retry"},
		{researchdomain.ErrSteerLimit, http.StatusConflict, "chat.steer_limit"},
		{researchdomain.ErrOutdatedAgent, http.StatusConflict, "chat.outdated_agent"},
		{researchdomain.ErrNotFound, http.StatusNotFound, "not_found"},
		{researchdomain.ErrForbidden, http.StatusForbidden, "forbidden"},
		{researchdomain.ErrValidation, http.StatusBadRequest, "request.validation"},
		{context.DeadlineExceeded, http.StatusInternalServerError, "server.error"},
	} {
		router := steerRouter(t, func(context.Context, researchdomain.Actor, string, string, service.SteerTurnInput) (string, error) {
			return "", test.err
		}, currentUserStub{})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, steerRequest(`{"body":"Без дракона"}`))
		if response.Code != test.status || !strings.Contains(response.Body.String(), `"`+test.code+`"`) {
			t.Fatalf("%v = %d %s", test.err, response.Code, response.Body.String())
		}
	}
}

// R1.1: an unknown field is refused like in every chat request.
func TestSteerTurnRefusesUnknownFields(t *testing.T) {
	router := steerRouter(t, func(context.Context, researchdomain.Actor, string, string, service.SteerTurnInput) (string, error) {
		t.Fatal("not expected")
		return "", nil
	}, currentUserStub{})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, steerRequest(`{"body":"Без дракона","question":"x"}`))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", response.Code)
	}
}

type csrfRefusingUser struct{ currentUserStub }

func (csrfRefusingUser) AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error) {
	return auth.User{}, auth.SessionWriteProof{}, auth.ErrInvalidCSRF
}
