package researchchat

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"canonkeeper/backend/internal/auth"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"

	"github.com/go-chi/chi/v5"
)

type CreateSessionUseCase interface {
	Execute(context.Context, researchdomain.Actor, service.CreateSessionInput) (researchdomain.Session, error)
}

type ListSessionsUseCase interface {
	Execute(context.Context, researchdomain.Actor, service.ListSessionsInput) (researchdomain.SessionPage, error)
}

type GetSessionUseCase interface {
	Execute(context.Context, researchdomain.Actor, string) (researchdomain.Session, error)
}

type ResolveChapterKeyUseCase interface {
	Execute(context.Context, researchdomain.Actor, string, string) (researchdomain.ChapterLocation, error)
}

type UpdateSessionUseCase interface {
	Execute(context.Context, service.UpdateSessionInput) (researchdomain.Session, error)
}

type DeleteSessionUseCase interface {
	Execute(context.Context, researchdomain.Actor, string) error
}

type SubmitTurnUseCase interface {
	Execute(context.Context, researchdomain.Actor, string, service.SubmitTurnInput) (jobs.Job, error)
}

type SteerTurnUseCase interface {
	Execute(context.Context, researchdomain.Actor, string, string, service.SteerTurnInput) (string, error)
}

// GetAttachmentUseCase is getChatAttachment (specs/2026-11-07-chat-attach-chapter.md CA4.3).
type GetAttachmentUseCase interface {
	Execute(context.Context, researchdomain.Actor, string, int) (researchdomain.Attachment, error)
}

type CurrentUserService interface {
	CurrentUser(context.Context, string) (auth.User, error)
	AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error)
}

type Handlers struct {
	create         CreateSessionUseCase
	list           ListSessionsUseCase
	get            GetSessionUseCase
	chapterKey     ResolveChapterKeyUseCase
	update         UpdateSessionUseCase
	delete         DeleteSessionUseCase
	submit         SubmitTurnUseCase
	steer          SteerTurnUseCase
	attachment     GetAttachmentUseCase
	currentUser    CurrentUserService
	allowedOrigins []string
}

type actorUserIDContextKey struct{}
type sessionWriteProofContextKey struct{}

func NewHandlers(
	create CreateSessionUseCase,
	list ListSessionsUseCase,
	get GetSessionUseCase,
	chapterKey ResolveChapterKeyUseCase,
	update UpdateSessionUseCase,
	deleteSession DeleteSessionUseCase,
	submit SubmitTurnUseCase,
	steer SteerTurnUseCase,
	attachment GetAttachmentUseCase,
	currentUser CurrentUserService,
	allowedOrigins []string,
) Handlers {
	if create == nil || list == nil || get == nil || chapterKey == nil || update == nil || deleteSession == nil || submit == nil || steer == nil ||
		attachment == nil || currentUser == nil {
		panic("research chat HTTP: dependencies are required")
	}
	return Handlers{
		create: create, list: list, get: get, chapterKey: chapterKey, update: update, delete: deleteSession, submit: submit, steer: steer,
		attachment: attachment, currentUser: currentUser, allowedOrigins: slices.Clone(allowedOrigins),
	}
}

func (handlers Handlers) Mount(router chi.Router) {
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginMiddleware(handlers.allowedOrigins...))
		router.Use(handlers.CSRFMiddleware)
		router.Get("/projects/{projectId}/chats", handlers.ListSessions)
		router.Post("/projects/{projectId}/chats", handlers.CreateSession)
		router.Get("/chats/{chatId}", handlers.GetSession)
		router.Get("/chats/{chatId}/chapters/{chapterKey}", handlers.ResolveChapterKey)
		router.Patch("/chats/{chatId}", handlers.UpdateSession)
		router.Delete("/chats/{chatId}", handlers.DeleteSession)
		router.Post("/chats/{chatId}/turns", handlers.SubmitTurn)
		router.Post("/chats/{chatId}/turns/chapter", handlers.SubmitTurnWithChapter)
		router.Get("/chats/{chatId}/attachments/{number}", handlers.GetAttachment)
		router.Post("/chats/{chatId}/turns/{turnId}/steer", handlers.SteerTurn)
	})
}

func (handlers Handlers) CreateSession(response http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(response, request)
	if !ok {
		return
	}
	input, err := decodeCreateSession(request)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	input.ProjectID = chi.URLParam(request, "projectId")
	input.RequestMethod, input.RequestPath = request.Method, request.URL.Path
	input.SessionWriteProof = sessionWriteProof(request.Context())
	created, err := handlers.create.Execute(request.Context(), actor, input)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusCreated, created)
}

func (handlers Handlers) ListSessions(response http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(response, request)
	if !ok {
		return
	}
	input, err := decodeListSessions(request)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	input.ProjectID = chi.URLParam(request, "projectId")
	page, err := handlers.list.Execute(request.Context(), actor, input)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, page)
}

func (handlers Handlers) GetSession(response http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(response, request)
	if !ok {
		return
	}
	value, err := handlers.get.Execute(request.Context(), actor, chi.URLParam(request, "chatId"))
	if err != nil {
		writeServiceError(response, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, value)
}

// ResolveChapterKey names the chapter a citation key of a chat answer points to.
func (handlers Handlers) ResolveChapterKey(response http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(response, request)
	if !ok {
		return
	}
	location, err := handlers.chapterKey.Execute(request.Context(), actor, chi.URLParam(request, "chatId"), chi.URLParam(request, "chapterKey"))
	if err != nil {
		writeServiceError(response, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, location)
}

// UpdateSession is updateChatSession: a new title and the toggle «Рисунки».
func (handlers Handlers) UpdateSession(response http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(response, request)
	if !ok {
		return
	}
	input, err := decodeUpdateSession(request)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	input.Actor = actor
	input.ChatID = chi.URLParam(request, "chatId")
	updated, err := handlers.update.Execute(request.Context(), input)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, updated)
}

func (handlers Handlers) DeleteSession(response http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(response, request)
	if !ok {
		return
	}
	if err := handlers.delete.Execute(request.Context(), actor, chi.URLParam(request, "chatId")); err != nil {
		writeServiceError(response, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

func (handlers Handlers) SubmitTurn(response http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(response, request)
	if !ok {
		return
	}
	input, err := decodeSubmitTurn(request)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	input.RequestMethod, input.RequestPath = request.Method, request.URL.Path
	input.SessionWriteProof = sessionWriteProof(request.Context())
	job, err := handlers.submit.Execute(request.Context(), actor, chi.URLParam(request, "chatId"), input)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	if err := writeJobStart(response, request.Context(), job); err != nil {
		writeServiceError(response, err)
	}
}

// SteerTurn is steerChatTurn: the author's correction for the turn working now
// (specs/2026-10-30-chat-steering.md).
func (handlers Handlers) SteerTurn(response http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(response, request)
	if !ok {
		return
	}
	input, err := decodeSteerTurn(request)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	input.RequestMethod, input.RequestPath = request.Method, request.URL.Path
	input.SessionWriteProof = sessionWriteProof(request.Context())
	steerID, err := handlers.steer.Execute(request.Context(), actor, chi.URLParam(request, "chatId"), chi.URLParam(request, "turnId"), input)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusAccepted, steerTurnResponse{SteerID: steerID})
}

func (handlers Handlers) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !httpapi.RequiresCSRF(request.Method) {
			next.ServeHTTP(response, request)
			return
		}
		user, proof, err := handlers.currentUser.AuthenticateWrite(
			request.Context(), auth.SessionCookieValue(request), request.Header.Get("X-CSRF-Token"),
		)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidCSRF) {
				writeProblem(response, http.StatusForbidden, "Forbidden", "invalid CSRF token", "csrf.invalid")
			} else {
				writeServiceError(response, err)
			}
			return
		}
		ctx := context.WithValue(request.Context(), actorUserIDContextKey{}, user.ID)
		ctx = context.WithValue(ctx, sessionWriteProofContextKey{}, proof)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func (handlers Handlers) actor(response http.ResponseWriter, request *http.Request) (researchdomain.Actor, bool) {
	if actorUserID, ok := request.Context().Value(actorUserIDContextKey{}).(string); ok {
		return researchdomain.Actor{UserID: actorUserID}, true
	}
	user, err := handlers.currentUser.CurrentUser(request.Context(), auth.SessionCookieValue(request))
	if err != nil {
		writeServiceError(response, err)
		return researchdomain.Actor{}, false
	}
	return researchdomain.Actor{UserID: user.ID}, true
}

func sessionWriteProof(ctx context.Context) auth.SessionWriteProof {
	proof, _ := ctx.Value(sessionWriteProofContextKey{}).(auth.SessionWriteProof)
	return proof
}
