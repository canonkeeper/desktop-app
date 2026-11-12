package app

import (
	"io"
	"net/http"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/indexing"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/ratelimit"
	"canonkeeper/backend/internal/search"

	"github.com/go-chi/chi/v5"
)

type AgentRoutes func(chi.Router, *auth.Service, *jobs.Service)
type BookCoverRoutes func(chi.Router, *auth.Service)
type NotesRoutes func(chi.Router, *auth.Service)
type AccountQuotaRoutes func(chi.Router, *auth.Service)

// TrashRoutes mounts listRecentlyDeleted: «Недавно удалённые» of the author (specs/2026-11-11-ux7-trash.md TB1.1).
type TrashRoutes func(chi.Router, *auth.Service)

// ClientErrorRoutes mounts reportClientError behind the CSRF check; currentUserID names the
// signed-in user of a request ("" for an anonymous visitor).
type ClientErrorRoutes func(router chi.Router, csrf func(http.Handler) http.Handler, currentUserID func(*http.Request) string)

type HTTPDependencies struct {
	ProjectsStore           projects.Store
	LibraryStore            library.Store
	JobsStore               jobs.Store
	LibraryService          *library.Service
	IndexingService         *indexing.Service
	JobTargetAccessResolver jobs.TargetAccessResolver
	SearchStore             search.Store
	AllowedOrigins          []string
	RequestLogWriter        io.Writer
	RateLimiter             ratelimit.Limiter
	// Accounts are the sessions of the build's users (specs/2026-11-07-desktop-app.md DA16.4).
	Accounts            Accounts
	AgentRoutes         AgentRoutes
	BookCoverRoutes     BookCoverRoutes
	NotesRoutes         NotesRoutes
	AccountQuotaRoutes  AccountQuotaRoutes
	TrashRoutes         TrashRoutes
	ChapterSearchRoutes func(chi.Router, *auth.Service)
	ClientErrorRoutes   ClientErrorRoutes
	// SiteRoutes mount the operations of the build's closed modules after the open ones.
	SiteRoutes func(chi.Router)
}

// RateLimits keeps one in-memory budget per client and operation: 600 per minute by default, tighter budgets for the
// anonymous CSRF token, the reports of the browser and the corrections of a turn, and the budgets of the build's
// operations (SEC-03).
func RateLimits(build map[string]ratelimit.Rule) ratelimit.MemoryLimiterConfig {
	operations := map[string]ratelimit.Rule{
		"getCsrfToken": {Limit: 60, Window: time.Minute},
		// Browser error reports (R5.4): a broken page must not flood the log.
		"reportClientError": {Limit: 30, Window: time.Minute},
		// Corrections of a running turn (specs/2026-10-30-chat-steering.md R1.8): an author sends
		// a few per turn; a turn takes at most MaxSteersPerTurn of them.
		"steerChatTurn": {Limit: 20, Window: time.Minute},
	}
	for operation, rule := range build {
		operations[operation] = rule
	}
	return ratelimit.MemoryLimiterConfig{Limit: 600, Window: time.Minute, Operations: operations}
}

func NewHTTPHandler(deps HTTPDependencies) http.Handler {
	return httpapi.RequestLogger(deps.RequestLogWriter)(http.Handler(newRouter(deps)))
}

// newRouter mounts the open operations, then the build's, under /api/v1 behind the build's middleware and the rate limit.
func newRouter(deps HTTPDependencies) chi.Router {
	if deps.Accounts.Session == nil || deps.Accounts.CSRF == nil {
		panic("app: Accounts are required")
	}
	if deps.ProjectsStore == nil {
		panic("app: ProjectsStore is required")
	}
	if deps.LibraryStore == nil {
		panic("app: LibraryStore is required")
	}
	if deps.JobsStore == nil {
		panic("app: JobsStore is required")
	}
	if deps.RateLimiter == nil {
		panic("app: RateLimiter is required")
	}
	if deps.RequestLogWriter == nil {
		panic("app: RequestLogWriter is required")
	}
	if deps.LibraryService == nil {
		panic("app: LibraryService is required")
	}
	if deps.IndexingService == nil {
		panic("app: IndexingService is required")
	}
	if deps.JobTargetAccessResolver == nil {
		panic("app: JobTargetAccessResolver is required")
	}
	if deps.SearchStore == nil {
		panic("app: SearchStore is required")
	}
	if deps.AgentRoutes == nil {
		panic("app: AgentRoutes is required")
	}
	if deps.BookCoverRoutes == nil {
		panic("app: BookCoverRoutes is required")
	}
	if deps.NotesRoutes == nil {
		panic("app: NotesRoutes is required")
	}
	if deps.AccountQuotaRoutes == nil {
		panic("app: AccountQuotaRoutes is required")
	}
	if deps.TrashRoutes == nil {
		panic("app: TrashRoutes is required")
	}
	if deps.ChapterSearchRoutes == nil {
		panic("app: ChapterSearchRoutes is required")
	}
	if deps.ClientErrorRoutes == nil {
		panic("app: ClientErrorRoutes is required")
	}
	router := chi.NewRouter()
	router.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	jobService := jobs.NewService(deps.JobsStore, auth.SystemClock{}, deps.JobTargetAccessResolver)
	authService := deps.Accounts.Session
	projectService := projects.NewService(deps.ProjectsStore, projects.NewSecureIDGenerator(), auth.SystemClock{})
	searchService := search.NewService(deps.SearchStore)
	projectsHandlers := projects.NewHandlers(
		projectService,
		authService,
		deps.AllowedOrigins,
	)
	libraryHandlers := library.NewHandlers(
		deps.LibraryService,
		authService,
		deps.AllowedOrigins,
	)
	jobHandlers := jobs.NewHandlers(
		jobService,
		authService,
		deps.AllowedOrigins,
	)
	indexingHandlers := indexing.NewHandlers(
		deps.IndexingService,
		authService,
		deps.AllowedOrigins,
	)
	searchHandlers := search.NewHandlers(
		searchService,
		authService,
		deps.AllowedOrigins,
	)
	currentUserID := func(r *http.Request) string {
		user, err := authService.CurrentUser(r.Context(), auth.SessionCookieValue(r))
		if err != nil {
			return ""
		}
		return user.ID
	}
	mountAPIHandlers := func(apiRouter chi.Router) {
		if deps.Accounts.Routes != nil {
			deps.Accounts.Routes(apiRouter)
		}
		deps.ClientErrorRoutes(apiRouter, deps.Accounts.CSRF, currentUserID)
		deps.AgentRoutes(apiRouter, authService, jobService)
		projectsHandlers.Mount(apiRouter)
		libraryHandlers.Mount(apiRouter)
		deps.BookCoverRoutes(apiRouter, authService)
		deps.NotesRoutes(apiRouter, authService)
		deps.AccountQuotaRoutes(apiRouter, authService)
		deps.TrashRoutes(apiRouter, authService)
		deps.ChapterSearchRoutes(apiRouter, authService)
		jobHandlers.Mount(apiRouter)
		indexingHandlers.Mount(apiRouter)
		searchHandlers.Mount(apiRouter)
		if deps.SiteRoutes != nil {
			deps.SiteRoutes(apiRouter)
		}
	}
	rateLimitMiddleware := httpapi.RateLimitMiddleware(deps.RateLimiter, httpapi.NewRateLimitKeyFunc(httpapi.RateLimitKeyOptions{
		PrincipalID: currentUserID,
	}))
	versionedRouter := chi.NewRouter()
	if deps.Accounts.Middleware != nil {
		versionedRouter.Use(deps.Accounts.Middleware)
	}
	versionedRouter.Use(rateLimitMiddleware)
	mountAPIHandlers(versionedRouter)
	router.Mount("/api/v1", versionedRouter)
	return router
}
