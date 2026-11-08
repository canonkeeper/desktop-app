package agenttimeline

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/agenttimeline"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/observability"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
)

const (
	defaultAuthorizationLeaseDuration = 15 * time.Second
	// defaultBindingPollInterval — как часто поток ждёт root thread хода, которого ещё нет.
	defaultBindingPollInterval = time.Second
	authorizationExpiredReason = "authorization expired"
	timelineUnavailableReason  = "timeline unavailable"
)

type CurrentUserService interface {
	CurrentUser(context.Context, string) (auth.User, error)
}

type JobAccessService interface {
	GetJob(context.Context, jobs.Actor, string) (jobs.Job, error)
}

func NewHandlers(
	timeline Timeline,
	currentUser CurrentUserService,
	jobAccess JobAccessService,
	allowedOrigins []string,
) Handlers {
	if timeline == nil || currentUser == nil || jobAccess == nil {
		panic("agent timeline: HTTP dependencies are required")
	}
	return Handlers{
		timeline: timeline, currentUser: currentUser, jobs: jobAccess,
		allowedOrigins:             append([]string(nil), allowedOrigins...),
		authorizationLeaseDuration: defaultAuthorizationLeaseDuration,
		bindingPollInterval:        defaultBindingPollInterval,
	}
}

func (handlers Handlers) Mount(router chi.Router) {
	router.Group(func(router chi.Router) {
		router.Use(httpapi.StrictOriginRequiredMiddleware(handlers.allowedOrigins...))
		router.Get("/projects/{projectId}/jobs/{jobId}/agent-timeline", handlers.GetTimeline)
		router.Get("/projects/{projectId}/jobs/{jobId}/agent-timeline/stream", handlers.StreamTimeline)
	})
}

func (handlers Handlers) GetTimeline(w http.ResponseWriter, request *http.Request) {
	_, projectID, jobID, ok := handlers.authorize(w, request)
	if !ok || !handlers.rejectRole(w, request) {
		return
	}
	snapshot, err := handlers.Snapshot(request.Context(), projectID, jobID, request.URL.Query().Get("cursor"))
	if err != nil {
		handlers.writeTimelineError(w, request, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (handlers Handlers) StreamTimeline(w http.ResponseWriter, request *http.Request) {
	user, projectID, jobID, ok := handlers.authorize(w, request)
	if !ok || !handlers.rejectRole(w, request) {
		return
	}
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	updates, err := handlers.timeline.Updates(ctx, projectID, jobID)
	pending := errors.Is(err, domain.ErrThreadPending)
	if err != nil && !pending {
		handlers.writeTimelineError(w, request, err)
		return
	}
	envelope, err := handlers.Live(ctx, projectID, jobID)
	if err != nil {
		handlers.writeTimelineError(w, request, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	connection, err := websocket.Accept(w, request, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer func() { _ = connection.Close(websocket.StatusNormalClosure, "") }()
	connection.SetReadLimit(1)
	go rejectClientCommands(ctx, cancel, connection)
	if err := wsjson.Write(ctx, connection, envelope); err != nil {
		return
	}
	sessionID := auth.SessionCookieValue(request)
	if pending {
		var bound bool
		if updates, bound = handlers.awaitRootThread(ctx, connection, sessionID, user.ID, projectID, jobID); !bound {
			return
		}
	}
	lease := time.NewTicker(handlers.authorizationLeaseDuration)
	defer lease.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-lease.C:
			if !handlers.stillAuthorized(ctx, sessionID, user.ID, projectID, jobID) {
				_ = connection.Close(websocket.StatusPolicyViolation, authorizationExpiredReason)
				return
			}
		case update, open := <-updates:
			if !open || update.Err != nil {
				_ = connection.Close(websocket.StatusInternalError, timelineUnavailableReason)
				return
			}
			if !handlers.stillAuthorized(ctx, sessionID, user.ID, projectID, jobID) {
				_ = connection.Close(websocket.StatusPolicyViolation, authorizationExpiredReason)
				return
			}
			batch := drainTimelineUpdates(update, updates)
			for _, pending := range batch {
				if pending.Err != nil {
					_ = connection.Close(websocket.StatusInternalError, timelineUnavailableReason)
					return
				}
			}
			refresh := false
			for _, pending := range batch {
				if pending.Context != nil {
					if wsjson.Write(ctx, connection, contextEnvelope(*pending.Context)) != nil {
						return
					}
					continue
				}
				if pending.Delta == nil {
					refresh = true
					continue
				}
				if wsjson.Write(ctx, connection, deltaEnvelope(jobID, *pending.Delta)) != nil {
					return
				}
			}
			if refresh {
				envelope, err = handlers.Live(ctx, projectID, jobID)
				if err != nil {
					if ctx.Err() == nil {
						observability.ReportUnexpectedError(ctx, err)
						_ = connection.Close(websocket.StatusInternalError, timelineUnavailableReason)
					}
					return
				}
				if wsjson.Write(ctx, connection, envelope) != nil {
					return
				}
			}
		}
	}
}

// awaitRootThread держит открытый поток хода, у которого ещё нет root thread: раз в
// bindingPollInterval проверяет доступ к задаче и привязку. После привязки отдаёт свежий снимок и
// подписку; задача, завершившаяся без thread, закрывает поток нормально.
func (handlers Handlers) awaitRootThread(
	ctx context.Context, connection *websocket.Conn, sessionID, userID, projectID, jobID string,
) (<-chan domain.Update, bool) {
	poll := time.NewTicker(handlers.bindingPollInterval)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, false
		case <-poll.C:
		}
		job, authorized := handlers.authorizedJob(ctx, sessionID, userID, projectID, jobID)
		if !authorized {
			_ = connection.Close(websocket.StatusPolicyViolation, authorizationExpiredReason)
			return nil, false
		}
		updates, err := handlers.timeline.Updates(ctx, projectID, jobID)
		if errors.Is(err, domain.ErrThreadPending) {
			if isTerminalJob(job.Status) {
				handlers.holdUntilClosed(ctx, connection, sessionID, userID, projectID, jobID)
				return nil, false
			}
			continue
		}
		var envelope EnvelopeDTO
		if err == nil {
			envelope, err = handlers.Live(ctx, projectID, jobID)
		}
		if err != nil {
			if ctx.Err() == nil {
				observability.ReportUnexpectedError(ctx, err)
				_ = connection.Close(websocket.StatusInternalError, timelineUnavailableReason)
			}
			return nil, false
		}
		if wsjson.Write(ctx, connection, envelope) != nil {
			return nil, false
		}
		return updates, true
	}
}

// holdUntilClosed держит поток задачи, завершившейся без root thread, так же, как поток
// завершённой задачи с thread: без опроса привязки, с проверкой доступа раз в
// authorizationLeaseDuration. Клиент переподключается после любого закрытия, поэтому поток не
// закрывается сам.
func (handlers Handlers) holdUntilClosed(
	ctx context.Context, connection *websocket.Conn, sessionID, userID, projectID, jobID string,
) {
	lease := time.NewTicker(handlers.authorizationLeaseDuration)
	defer lease.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-lease.C:
			if !handlers.stillAuthorized(ctx, sessionID, userID, projectID, jobID) {
				_ = connection.Close(websocket.StatusPolicyViolation, authorizationExpiredReason)
				return
			}
		}
	}
}

func isTerminalJob(status jobs.Status) bool {
	switch status {
	case jobs.StatusSucceeded, jobs.StatusFailed, jobs.StatusCanceled, jobs.StatusExpired:
		return true
	default:
		return false
	}
}

func drainTimelineUpdates(first domain.Update, updates <-chan domain.Update) []domain.Update {
	limit := cap(updates)
	if limit < 1 {
		limit = 1
	}
	batch := make([]domain.Update, 1, limit)
	batch[0] = first
	for len(batch) < limit {
		select {
		case update, open := <-updates:
			if !open {
				return batch
			}
			batch = append(batch, update)
		default:
			return batch
		}
	}
	return batch
}

func (handlers Handlers) authorize(w http.ResponseWriter, request *http.Request) (auth.User, string, string, bool) {
	user, err := handlers.currentUser.CurrentUser(request.Context(), auth.SessionCookieValue(request))
	if errors.Is(err, auth.ErrUnauthenticated) || (err == nil && user.ID == "") {
		writeTimelineProblem(w, request, http.StatusUnauthorized, "Требуется вход.", "auth.unauthenticated")
		return auth.User{}, "", "", false
	}
	if err != nil {
		writeTimelineProblem(w, request, http.StatusServiceUnavailable, "Авторизация временно недоступна.", "auth.unavailable")
		return auth.User{}, "", "", false
	}
	projectID := chi.URLParam(request, "projectId")
	jobID := chi.URLParam(request, "jobId")
	job, err := handlers.jobs.GetJob(request.Context(), jobs.Actor{UserID: user.ID}, jobID)
	if err != nil || job.ProjectID != projectID {
		writeTimelineProblem(w, request, http.StatusNotFound, "Timeline не найдена.", "agent_timeline.not_found")
		return auth.User{}, "", "", false
	}
	return user, projectID, jobID, true
}

func (handlers Handlers) stillAuthorized(ctx context.Context, sessionID, userID, projectID, jobID string) bool {
	_, authorized := handlers.authorizedJob(ctx, sessionID, userID, projectID, jobID)
	return authorized
}

func (handlers Handlers) authorizedJob(ctx context.Context, sessionID, userID, projectID, jobID string) (jobs.Job, bool) {
	user, err := handlers.currentUser.CurrentUser(ctx, sessionID)
	if err != nil || user.ID != userID {
		return jobs.Job{}, false
	}
	job, err := handlers.jobs.GetJob(ctx, jobs.Actor{UserID: user.ID}, jobID)
	return job, err == nil && job.ProjectID == projectID
}

func (handlers Handlers) rejectRole(w http.ResponseWriter, request *http.Request) bool {
	if _, present := request.URL.Query()["role"]; !present {
		return true
	}
	writeTimelineProblem(w, request, http.StatusBadRequest, "Timeline не принимает роль.", "agent_timeline.role_forbidden")
	return false
}

func (handlers Handlers) writeTimelineError(w http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidCursor):
		writeTimelineProblem(w, request, http.StatusBadRequest, "Некорректный курсор timeline.", "agent_timeline.invalid_cursor")
	case errors.Is(err, domain.ErrNotFound):
		writeTimelineProblem(w, request, http.StatusNotFound, "Timeline не найдена.", "agent_timeline.not_found")
	default:
		observability.ReportUnexpectedError(request.Context(), err)
		writeTimelineProblem(w, request, http.StatusServiceUnavailable, "Timeline временно недоступен.", "agent_timeline.unavailable")
	}
}

func rejectClientCommands(ctx context.Context, cancel context.CancelFunc, connection *websocket.Conn) {
	defer cancel()
	_, _, err := connection.Read(ctx)
	if err == nil {
		_ = connection.Close(websocket.StatusPolicyViolation, "read-only stream")
	}
}

func writeTimelineProblem(w http.ResponseWriter, request *http.Request, status int, title, code string) {
	requestID, _ := observability.RequestIDFromContext(request.Context())
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteProblem(w, string(requestID), status, title, "", code)
}
