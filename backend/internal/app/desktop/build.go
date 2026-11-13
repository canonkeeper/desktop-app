// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/config"
	httpdesktop "canonkeeper/backend/internal/delivery/http/desktop"
	domain "canonkeeper/backend/internal/domain/desktop"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/ratelimit"
	service "canonkeeper/backend/internal/services/desktop"
	authstore "canonkeeper/backend/internal/store/auth"
	projectstore "canonkeeper/backend/internal/store/projects"

	"github.com/go-chi/chi/v5"
)

// Resources is the directory of the bundle the shell names in start (DA1.3, DA1.4, DA17): postgres/ holds our build of
// PostgreSQL 18 (bin, lib, share), codex/ the codex-app-server of the fork's release, codex-homes/ the profile of the
// chat.
type Resources struct {
	Root string
}

func (resources Resources) PostgresBin() string {
	return filepath.Join(resources.Root, "postgres", "bin")
}

func (resources Resources) CodexAppServer() string {
	name := "codex-app-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(resources.Root, "codex", name)
}

func (resources Resources) CodexHomes() string { return filepath.Join(resources.Root, "codex-homes") }

// The models the build asks for by role (DA4): the gateway of the author's provider maps them to his models (S3). Until
// the author connects an AI the model API has no address and calls nothing (DA2.6).
const (
	chatModel  = "canonkeeper-chat"
	cardsModel = "canonkeeper-cards"
)

// desktopConfig is the configuration of the open core on the computer of the author: one origin of the window, the
// cluster of the data directory, the files, Codex homes and workspace under it, the codex-app-server and profile of the
// bundle — copied into the homes at every start (specs/2026-11-13-desk-home.md HM1) — and no model address yet.
func desktopConfig(data DataDir, resources Resources, cluster *Cluster, listener net.Listener) config.Config {
	return config.Config{
		HTTPAddr: listener.Addr().String(), Environment: "desktop",
		DatabaseURL: cluster.ProductURL(), WorkerDatabaseURL: cluster.WorkerURL(), WorkersEnabled: true,
		AllowedOrigins: []string{"http://" + listener.Addr().String()},
		FileStorageDir: data.Files(),
		CodexPath:      resources.CodexAppServer(), CodexCWD: data.CodexWorkspace(),
		CodexRepositoryHome: resources.CodexHomes(), CodexHome: data.CodexHome(), CodexHomeCopies: true,
		CodexSQLiteHome:  data.CodexSQLite(),
		CodexPrivateHome: data.CodexPrivate(), CodexTempDir: data.CodexTemp(),
		ModelAPI: config.ModelAPI{Model: chatModel, CardsModel: cardsModel},
	}
}

// build is the desktop over the open core (DA1.8, DA2, DA3): the local author's sessions, opened by the one-time codes
// of the shell, and «кто автор» of the window (specs/2026-11-12-desk-window.md WN1.1); two common workers, two for chat turns and one for cards, no thread layer; cards only with the author's
// consent; the root handler that checks Host and serves the embedded page.
type build struct {
	host     string
	authorID string
	codes    *domain.OpenCodes
	files    fs.FS
	// state reads what «Настройки» of the app show (specs/2026-11-13-desk-settings.md ST2.1).
	state service.ReadAppState
	// ready tells the shell the backend serves; stopTurns cancels the chat turns of the author once compose built it —
	// a shutdown may come before.
	ready     func()
	mu        sync.Mutex
	stopTurns func(context.Context) (int, error)
}

// turnsStopper is what cancels the chat turns of the author, nil before compose.
func (desktop *build) turnsStopper() func(context.Context) (int, error) {
	desktop.mu.Lock()
	defer desktop.mu.Unlock()
	return desktop.stopTurns
}

func (desktop *build) extension(listener net.Listener) app.Extension {
	return app.Extension{
		Workers:     app.Workers{Common: 2, Chat: 2, Cards: 1},
		CardConsent: true,
		Listener:    listener,
		Compose:     desktop.compose,
	}
}

func (desktop *build) compose(core app.Core) (app.Modules, error) {
	sessions := auth.NewService(authstore.New(core.DB), auth.NewSecureIDGenerator(), auth.SystemClock{})
	sessionHandlers := auth.NewHandlers(sessions, false)
	open := httpdesktop.Handlers{
		Open:           service.OpenSession{Codes: desktop.codes, Sessions: sessions, LocalAuthorID: desktop.authorID, Now: time.Now},
		Sessions:       sessions,
		State:          desktop.state,
		CSRF:           sessionHandlers.CSRFMiddleware,
		AllowedOrigins: core.Config.AllowedOrigins,
	}
	stopTurns := service.StopChatTurns{
		Projects:      projects.NewService(projectstore.New(core.DB, projectstore.WithQuota(core.Quota)), projects.NewSecureIDGenerator(), auth.SystemClock{}),
		Jobs:          core.JobService,
		LocalAuthorID: desktop.authorID,
	}.Execute
	desktop.mu.Lock()
	desktop.stopTurns = stopTurns
	desktop.mu.Unlock()
	return app.Modules{
		Accounts: app.Accounts{
			Session: sessions,
			Routes: func(router chi.Router) {
				sessionHandlers.Mount(router)
				open.Mount(router)
			},
			CSRF: sessionHandlers.CSRFMiddleware,
		},
		// A window opens a session a few times a day; a page that guesses codes gets thirty tries a minute.
		RateLimits: map[string]ratelimit.Rule{"openDesktopSession": {Limit: 30, Window: time.Minute}},
		// The helper is the model of the build: none until the author connects an AI (specs/2026-11-12-desk-window.md WN3).
		Helper: helperOf{model: core.Model},
		Serve:  desktop.serve,
		Start: func(context.Context) ([]<-chan struct{}, func(context.Context) error) {
			if desktop.ready != nil {
				desktop.ready()
			}
			return nil, func(context.Context) error { return nil }
		},
	}, nil
}

// helperOf says whether the helper of the app is connected: the model API of the build has an address (DA2.6). Until it
// has, the status of a saga says the reading waits for the helper and no cards Job is queued (specs/2026-11-12-desk-window.md
// WN3.1, WN3.4).
type helperOf struct{ model *modelapi.Client }

func (helper helperOf) Connected(context.Context) bool {
	return helper.model != nil && helper.model.Connected()
}

// serve is the root handler (DA3.4, DA3.6): a Host other than 127.0.0.1:P gets 421, the API and the readiness checks go
// to the core, every other path is the embedded page, and every answer carries the headers of the page.
func (desktop *build) serve(api http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", api)
	mux.Handle("/healthz", api)
	mux.Handle("/readyz", api)
	mux.Handle("/", page(desktop.files))
	return httpapi.RequireHost(desktop.host)(withPageHeaders(mux))
}
