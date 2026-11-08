package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	codexadapter "canonkeeper/backend/internal/adapters/codex"
	coverfiles "canonkeeper/backend/internal/adapters/library"
	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/config"
	codexresearch "canonkeeper/backend/internal/delivery/codex/researchchat"
	httptimeline "canonkeeper/backend/internal/delivery/http/agenttimeline"
	httpauthormaterials "canonkeeper/backend/internal/delivery/http/authormaterials"
	httpauthormemory "canonkeeper/backend/internal/delivery/http/authormemory"
	"canonkeeper/backend/internal/delivery/http/clienterrors"
	httpindexing "canonkeeper/backend/internal/delivery/http/indexing"
	httplibrary "canonkeeper/backend/internal/delivery/http/library"
	httpnotes "canonkeeper/backend/internal/delivery/http/notes"
	httpquota "canonkeeper/backend/internal/delivery/http/quota"
	httpresearch "canonkeeper/backend/internal/delivery/http/researchchat"
	httpsagadraft "canonkeeper/backend/internal/delivery/http/sagadraft"
	httpsaga "canonkeeper/backend/internal/delivery/http/sagareference"
	workerauthormaterials "canonkeeper/backend/internal/delivery/worker/authormaterials"
	workerresearch "canonkeeper/backend/internal/delivery/worker/researchchat"
	workersagalayer "canonkeeper/backend/internal/delivery/worker/sagalayer"
	workersagamemory "canonkeeper/backend/internal/delivery/worker/sagamemory"
	agentdomain "canonkeeper/backend/internal/domain/agent"
	timelinedomain "canonkeeper/backend/internal/domain/agenttimeline"
	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/indexing"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/ratelimit"
	agentjobsrepo "canonkeeper/backend/internal/repository/postgres/agentjobs"
	authormaterialsrepo "canonkeeper/backend/internal/repository/postgres/authormaterials"
	authormemoryrepo "canonkeeper/backend/internal/repository/postgres/authormemory"
	deletionrepo "canonkeeper/backend/internal/repository/postgres/deletion"
	indexingrepo "canonkeeper/backend/internal/repository/postgres/indexing"
	coverrepo "canonkeeper/backend/internal/repository/postgres/library"
	notesrepo "canonkeeper/backend/internal/repository/postgres/notes"
	researchrepo "canonkeeper/backend/internal/repository/postgres/researchchat"
	sagadraftrepo "canonkeeper/backend/internal/repository/postgres/sagadraft"
	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	"canonkeeper/backend/internal/retention"
	timelineservice "canonkeeper/backend/internal/services/agenttimeline"
	authormaterialsservice "canonkeeper/backend/internal/services/authormaterials"
	authormemoryservice "canonkeeper/backend/internal/services/authormemory"
	deletionservice "canonkeeper/backend/internal/services/deletion"
	indexingservice "canonkeeper/backend/internal/services/indexing"
	coverservice "canonkeeper/backend/internal/services/library"
	notesservice "canonkeeper/backend/internal/services/notes"
	quotaservice "canonkeeper/backend/internal/services/quota"
	researchservice "canonkeeper/backend/internal/services/researchchat"
	sagadraftservice "canonkeeper/backend/internal/services/sagadraft"
	sagalayerservice "canonkeeper/backend/internal/services/sagalayer"
	sagamemoryservice "canonkeeper/backend/internal/services/sagamemory"
	sagareferenceservice "canonkeeper/backend/internal/services/sagareference"
	canonstore "canonkeeper/backend/internal/store/canon"
	storedb "canonkeeper/backend/internal/store/db"
	indexingstore "canonkeeper/backend/internal/store/indexing"
	jobstore "canonkeeper/backend/internal/store/jobs"
	librarystore "canonkeeper/backend/internal/store/library"
	projectstore "canonkeeper/backend/internal/store/projects"
	quotastore "canonkeeper/backend/internal/store/quota"
	retentionstore "canonkeeper/backend/internal/store/retention"

	"github.com/go-chi/chi/v5"
)

const gracefulShutdownTimeout = 10 * time.Second

type researchIDGenerator struct{}

func (researchIDGenerator) NewID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	value[6], value[8] = value[6]&0x0f|0x40, value[8]&0x3f|0x80
	raw := hex.EncodeToString(value[:])
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
}

// Extension is what a build adds to the open core (specs/2026-11-07-desktop-app.md DA16.4): cmd/api passes the site's,
// cmd/desktop the desktop's. It names the Codex flows of the build's helpers besides the chat (one app-server each),
// the check of the seams of a piece taken from a version of a draft, the sizes of the open Job pools, whether the cards
// Job asks the author's consent before it calls the model, the listener the build opened itself, and how the build
// composes its modules over the open core.
type Extension struct {
	Flows      []agentdomain.Flow
	DraftSeams func(*jobstore.Store) sagadraftrepo.SeamCheck
	Workers    Workers
	// CardConsent makes the cards Job call the model only for a saga whose author allowed it (DA8.1); the site builds
	// cards without asking.
	CardConsent bool
	// Listener is the build's own listener (the desktop's 127.0.0.1:0, DA3.1); without one the core listens on
	// cfg.HTTPAddr.
	Listener net.Listener
	Compose  func(Core) (Modules, error)
}

// Core is what the open composition hands the modules of a build, built once: the configuration and the stores of the
// process, its model access, the saga, the chat and the drafts. API serves the root API handler once it is built.
type Core struct {
	Config          config.Config
	Logger          *log.Logger
	DB              *storedb.DB
	Files           *os.Root
	Codex           *CodexClients
	Model           *modelapi.Client
	Usage           *quotastore.UsageRecorder
	IDs             interface{ NewID() string }
	Jobs            *jobstore.Store
	Library         *librarystore.Store
	LibraryService  *library.Service
	Saga            *sagamemoryservice.Saga
	SagaStore       *sagamemoryrepo.Repository
	Chat            *researchrepo.Repository
	ChatUseCases    ChatUseCases
	AuthorMemory    *authormemoryrepo.Repository
	AuthorMaterials *authormaterialsrepo.Repository
	Drafts          *sagadraftrepo.Repository
	Covers          Covers
	// JobService is the shared Job lifecycle with the chat's cancel: the desktop cancels the turns of its author with it
	// when it stops (specs/2026-11-07-desktop-app.md DA1.6).
	JobService *jobs.Service
	API        http.Handler
}

// ChatUseCases are the use cases of the chat other modules ask it through, and the deletion of the native tree of a
// session.
type ChatUseCases struct {
	Create     *researchservice.CreateSession
	Get        *researchservice.GetSession
	Submit     *researchservice.SubmitTurn
	Steer      *researchservice.SteerTurn
	DeleteTree func(context.Context, researchdomain.DeleteTarget) error
}

// Covers are the book covers: their store, their upload and the book response a cover operation answers with.
type Covers struct {
	Store        *coverrepo.Repository
	Upload       *coverservice.UploadCover
	BookResponse httplibrary.BookResponseFunc
}

// Modules are the closed modules of a build the open core runs with.
type Modules struct {
	// Accounts are the sessions of the build's users.
	Accounts Accounts
	// ChatTools are the tools the build adds to the chat (specs/2026-11-07-desktop-app.md DA9.2).
	ChatTools []codexresearch.Tool
	// Helpers are the bundles of the helpers' sessions by their kind, over the handler of the chat's tools.
	Helpers func(chat func(context.Context, agentdomain.ToolCall) (agentdomain.ToolResult, error)) map[string]researchservice.Bundle
	// Routes mount the build's operations after the open ones; RateLimits are their budgets.
	Routes     func(chi.Router)
	RateLimits map[string]ratelimit.Rule
	// Pools are the build's Job workers; ProjectDirectories are its directories of project files the deletion of a
	// project removes.
	Pools              []JobPool
	ProjectDirectories []func(projectID string) string
	// Start runs the build's background work once the HTTP server is built: what it waits for at shutdown and how it
	// stops what it serves.
	Start func(context.Context) ([]<-chan struct{}, func(context.Context) error)
	// Serve is the root handler of the build around the API and the readiness checks; without it the process serves
	// them alone (the site: nginx serves the page). The desktop checks Host and serves the embedded page (DA3.4, DA3.6).
	Serve func(api http.Handler) http.Handler
}

// Accounts are how the build's users have sessions: the session service the open handlers check, the routes of their
// accounts, the CSRF check of the session's and the anonymous token, and the middleware every API request passes
// before the rate limit.
type Accounts struct {
	Session    *auth.Service
	Routes     func(chi.Router)
	CSRF       func(http.Handler) http.Handler
	Middleware func(http.Handler) http.Handler
}

// JobPool is a pool of Job workers of a build.
type JobPool struct {
	Name     string
	Handlers []jobworker.Handler
	Workers  int
}

type productApplication struct {
	httpHandler     http.Handler
	routes          chi.Router
	libraryStore    *librarystore.Store
	libraryService  *library.Service
	indexingService *indexing.Service
	chatHandler     jobworker.Handler
	cardsHandler    jobworker.Handler
	layerHandler    jobworker.Handler
	materialHandler jobworker.Handler
	missingCards    catchUpQueue
	missingLayers   catchUpQueue
	coverDeletion   coverDeletionRunner
	modules         Modules
}

type coverDeletionRunner func(context.Context) (bool, error)

type deletionTargetAndCoverRunner struct {
	targets deletionTargetRunner
	covers  coverDeletionRunner
}

func (runner deletionTargetAndCoverRunner) RunOnceDiagnostic(ctx context.Context) (deletionservice.RunDiagnostic, error) {
	diagnostic, targetErr := runner.targets.RunOnceDiagnostic(ctx)
	_, coverErr := runner.covers(ctx)
	return diagnostic, errors.Join(targetErr, coverErr)
}

type coverBookReader interface {
	GetBook(context.Context, library.Actor, string) (library.Book, error)
}

func coverBookResponse(reader coverBookReader) httplibrary.BookResponseFunc {
	return func(ctx context.Context, actor domainlibrary.Actor, bookID string, coverURL *string) (any, error) {
		book, err := reader.GetBook(ctx, library.Actor{UserID: actor.UserID}, bookID)
		if err != nil {
			switch {
			case errors.Is(err, library.ErrNotFound), errors.Is(err, library.ErrConflict):
				return nil, domainlibrary.ErrConflict
			case errors.Is(err, library.ErrForbidden):
				return nil, domainlibrary.ErrForbidden
			default:
				return nil, err
			}
		}
		book.CoverURL = coverURL
		return library.BookResponseFromBook(book), nil
	}
}

// lateHandler serves the root API handler once it is built.
type lateHandler struct{ handler http.Handler }

func (late *lateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	late.handler.ServeHTTP(w, r)
}

func Run(ctx context.Context, cfg config.Config, logger *log.Logger, extension Extension) (runErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cfg.ValidateCore(); err != nil {
		return err
	}
	if cfg.DatabaseURL == "" {
		return errors.New("api: product database is required")
	}
	if extension.Compose == nil {
		return errors.New("api: the modules of the build are required")
	}
	if cfg.WorkersEnabled && !extension.Workers.valid() {
		return errors.New("api: the build has no Job workers")
	}
	db, err := storedb.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return errors.New("api: failed to open product database")
	}
	defer db.Close()
	fileRoot, err := openFileRoot(cfg.FileStorageDir)
	if err != nil {
		return err
	}
	defer fileRoot.Close()
	coverFiles, err := coverfiles.NewBookCoverFiles(fileRoot)
	if err != nil {
		return errors.New("api: book cover storage unavailable")
	}

	// One app-server per Codex flow (specs/2026-11-02-image-workshop.md IW4.1): the chat and the helpers of the build;
	// one that fails to start leaves the started ones to Close.
	clients := &CodexClients{}
	defer func() { runErr = errors.Join(runErr, clients.Close()) }()
	for _, flow := range append([]agentdomain.Flow{agentdomain.FlowResearchChat}, extension.Flows...) {
		clientConfig, err := codexClientConfig(cfg, flow)
		var client *codexadapter.Client
		if err == nil {
			clientConfig.Logger = logger
			client, err = codexadapter.NewClient(ctx, clientConfig)
		}
		if err == nil {
			err = clients.set(flow, client)
		}
		if err != nil {
			return fmt.Errorf("api: invalid Codex runtime configuration: %w", err)
		}
	}

	voiceBrainModel, voiceBrainEffort := cfg.ModelAPI.Brain()
	model, err := modelapi.New(modelapi.Config{
		BaseURL: cfg.ModelAPI.BaseURL, KeyEnv: cfg.ModelAPI.KeyEnv, Model: cfg.ModelAPI.Model, Effort: cfg.ModelAPI.Effort,
		CardsModel: cfg.ModelAPI.CardsModel, CardsEffort: cfg.ModelAPI.CardsEffort,
		ImageModel: cfg.ModelAPI.ImageModel, ImageQuality: cfg.ModelAPI.ImageQuality, CheckModel: cfg.ModelAPI.CheckModel,
		WritingModel: cfg.ModelAPI.WritingModel, WritingEffort: cfg.ModelAPI.WritingEffort, WritingCheckModel: cfg.ModelAPI.WritingCheckModel,
		Parallel: cfg.ModelAPI.Parallel, VoiceBrain: voiceBrainModel, VoiceBrainEffort: voiceBrainEffort,
	})
	if err != nil {
		return errors.New("api: model API unavailable")
	}

	canonStore := canonstore.New(db)
	jobsStore := jobstore.New(db)
	libraryStore := librarystore.New(db)
	product, err := composeProduct(Core{
		Config: cfg, Logger: logger, DB: db, Files: fileRoot, Codex: clients, Model: model, Jobs: jobsStore, Library: libraryStore,
	}, canonStore, coverFiles, extension)
	if err != nil {
		return err
	}
	var workerDB *storedb.DB
	var workerJobs workerJobStore
	var importExpiryStore importStagingExpiryStore
	var retentionWork retentionRunner
	var deletionWork deletionTargetRunner
	if cfg.WorkersEnabled {
		workerDB, err = storedb.Open(ctx, cfg.WorkerDatabaseURL)
		if err != nil {
			return errors.New("api: failed to open worker discovery database")
		}
		defer workerDB.Close()
		if err := jobstore.ValidateWorkerDiscoveryDatabaseIdentities(ctx, db, workerDB); err != nil {
			return errors.New("api: invalid worker discovery database identity")
		}
		claims, err := jobstore.NewWorkerClaimAuthority(ctx, workerDB)
		if err != nil {
			return errors.New("api: failed to read worker discovery database clock")
		}
		workerJobs = workerJobStore{claims: claims, product: jobsStore}
		importExpiryStore = workerImportExpiryStore{claims: claims, product: libraryStore}

		deletions := deletionrepo.New(db, deletionrepo.WithSourceDeletedModelJobTombstoner(jobsStore))
		nativeTarget, targetErr := deletionservice.NewNativeTarget(deletions, deletionservice.NativeTreeDeleterFunc(
			func(ctx context.Context, selector deletiondomain.RootSelector) (string, error) {
				client, err := clients.Client(selector.Flow)
				if err != nil {
					return "", err
				}
				return client.DeleteTree(ctx, codexadapter.RootSelector{CorrelationID: selector.CorrelationID, ThreadID: selector.ThreadID})
			},
		))
		if targetErr != nil {
			return errors.New("api: native deletion target unavailable")
		}
		filesystemTarget, targetErr := deletionservice.NewFilesystemTarget(fileRoot, deletions, product.modules.ProjectDirectories...)
		if targetErr != nil {
			return errors.New("api: filesystem deletion target unavailable")
		}
		postgresTarget, targetErr := deletionservice.NewPostgresTarget(deletions)
		if targetErr != nil {
			return errors.New("api: postgres deletion target unavailable")
		}
		coordinator, coordinatorErr := deletionservice.NewCoordinator(deletionservice.CoordinatorConfig{
			RuntimeTargetEnabled: true, Claims: claims, Store: deletions,
			Handlers: deletionservice.TargetHandlers{
				"runtime": nativeTarget, "filesystem": filesystemTarget, "postgresql": postgresTarget,
			},
		})
		if coordinatorErr != nil {
			return errors.New("api: deletion coordinator unavailable")
		}
		retentionStore := retentionstore.New(db)
		retentionWork = retention.NewService(retentionStore, libraryStore, claims, retentionStore, coordinator)
		deletionWork = deletionTargetAndCoverRunner{targets: coordinator, covers: product.coverDeletion}
	}

	listener := extension.Listener
	if listener == nil {
		if listener, err = net.Listen("tcp", cfg.HTTPAddr); err != nil {
			return errors.New("api: failed to open HTTP listener")
		}
	}
	defer listener.Close()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make([]<-chan struct{}, 0, 7)
	if cfg.WorkersEnabled {
		done = append(done, startImportStagingExpiryRunner(runCtx, importExpiryStore, logger), startRetentionRunner(runCtx, retentionWork, deletionWork, logger))
		common := []jobworker.Handler{
			library.NewImportHandler(libraryStore, libraryStore),
			indexing.NewTextIndexHandler(product.indexingService),
			product.materialHandler,
		}
		workers := extension.Workers
		done = append(done, startJobWorkerPool(runCtx, workerJobs, common, workers.Common, "job", logger)...)
		// Chat turns mostly wait on the model; workers of their own keep answers independent of imports and text indexes.
		done = append(done, startJobWorkerPool(runCtx, workerJobs, []jobworker.Handler{product.chatHandler}, workers.Chat, "chat", logger)...)
		// Chapter cards have their own workers, so long builds never hold chat, import or index
		// slots; a cards Job yields after each portion and a project runs one at a time (UC12).
		done = append(done, startJobWorkerPool(runCtx, workerJobs, []jobworker.Handler{product.cardsHandler}, workers.Cards, "cards", logger)...)
		for _, pool := range product.modules.Pools {
			done = append(done, startJobWorkerPool(runCtx, workerJobs, pool.Handlers, pool.Workers, pool.Name, logger)...)
		}
		done = append(done, startCatchUp(runCtx, product.missingCards, "chapter cards", "cards_catch_up_failed", logger))
		if workers.Layer > 0 {
			// The thread layer has its own workers: a long build holds nothing else (specs/2026-11-01-writer-assistant-index.md, R2.1).
			done = append(done, startJobWorkerPool(runCtx, workerJobs, []jobworker.Handler{product.layerHandler}, workers.Layer, "layer", logger)...)
			done = append(done, startCatchUp(runCtx, product.missingLayers, "saga layer", "layer_catch_up_failed", logger))
		}
	}
	var workerDatabasePing databasePing
	if workerDB != nil {
		workerDatabasePing = workerDB.Pool.Ping
	}
	handler := withReadiness(product.httpHandler, db.Pool.Ping, workerDatabasePing, clients.RuntimeReady)
	if product.modules.Serve != nil {
		handler = product.modules.Serve(handler)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	stopModules := func(context.Context) error { return nil }
	if product.modules.Start != nil {
		var moduleDone []<-chan struct{}
		moduleDone, stopModules = product.modules.Start(runCtx)
		done = append(done, moduleDone...)
	}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.Serve(listener) }()
	logger.Print("canonkeeper backend listening")
	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = errors.New("api: HTTP server stopped")
		}
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
	defer shutdownCancel()
	shutdownServers := func(ctx context.Context) error { return errors.Join(server.Shutdown(ctx), stopModules(ctx)) }
	return errors.Join(runErr, waitForShutdown(shutdownCtx, shutdownServers, func(ctx context.Context) error {
		return waitForBackground(ctx, done)
	}))
}

// Routes is the router of a build over its core — the open operations and the build's — without starting anything: the
// contract of the desktop is what the desktop's mounts (specs/2026-11-07-desktop-app.md DA16.4).
func Routes(core Core, extension Extension) (chi.Router, error) {
	covers, err := coverfiles.NewBookCoverFiles(core.Files)
	if err != nil {
		return nil, err
	}
	product, err := composeProduct(core, canonstore.New(core.DB), covers, extension)
	if err != nil {
		return nil, err
	}
	return product.routes, nil
}

// composeProduct builds the open domains over the core and composes the modules of the build with them.
func composeProduct(
	core Core, canonStore *canonstore.Store, coverFiles coverservice.CoverObjectStore, extension Extension,
) (productApplication, error) {
	db, cfg, clients, model, logger := core.DB, core.Config, core.Codex, core.Model, core.Logger
	jobsStore, libraryStore := core.Jobs, core.Library
	clock := jobs.SystemClock{}
	agentJobs := agentjobsrepo.New(db)

	indexingService := indexingservice.NewService(indexingrepo.New(db))

	researchRepository := researchrepo.New(db)
	sagaRepository := sagamemoryrepo.New(db)
	// The saga memory is read per call from PostgreSQL; nothing of a saga stays in the process
	// (specs/2026-10-28-saga-search-in-postgres.md).
	saga := sagamemoryservice.NewSaga(sagaRepository, sagaRepository)
	// One gate of model_api.parallel places serves both: a scan waiting for a place gets it
	// before chapter cards (quota spec UC11).
	usage := quotastore.NewUsageRecorder(db, logger)
	layerQueue := sagalayerservice.NewQueueDue(sagaRepository)
	// The author's materials (specs/2026-11-04-saga-map.md, UC9) are read by the tools and the map.
	authorMaterials := authormaterialsrepo.New(db, jobsStore)
	// Successful cards count for the thread layer of a build that has one (specs/2026-11-07-desktop-app.md DA8.8).
	var cardsDone sagamemoryservice.CardsDone
	if extension.Workers.Layer > 0 {
		cardsDone = func(ctx context.Context, version sagamemoryservice.TextVersion) {
			if _, err := layerQueue.Execute(ctx, version.ProjectID, version.View); err != nil {
				logLayerFailure(logger, version.ProjectID, "queue", err)
			}
		}
	}
	cardsBuilder := sagamemoryservice.NewBuildCards(
		sagaRepository, sagaRepository, model.Background(), usage, model.CardsModel(), cfg.ModelAPI.Parallel,
		func(projectID, stage string, err error) { logNamesFailure(logger, projectID, stage, err) }, cardsDone,
	).WithMap(sagamemoryservice.NewMapBuilder(saga, sagaRepository, authorMaterials), func(projectID, stage string, err error) { logMapFailure(logger, projectID, stage, err) })
	if extension.CardConsent {
		// The author pays for the cards: the model is called only for a saga he allowed (DA8.1).
		cardsBuilder = cardsBuilder.WithPolicy(sagaRepository)
	}
	cardsHandler := workersagamemory.NewHandler(cardsBuilder)
	// The same gate and cards model: a chat call waiting for a place goes first (R2.1).
	layerHandler := workersagalayer.NewHandler(sagalayerservice.NewBuildLayer(
		saga, sagaRepository, model.Background(), usage, model.CardsModel(), cfg.ModelAPI.Parallel,
		func(projectID, stage string, err error) { logLayerFailure(logger, projectID, stage, err) },
	))
	researchReads := researchservice.NewReadTools(researchRepository, saga, model.Interactive(), usage, researchservice.ScanSettings{
		Parallel: cfg.ModelAPI.Parallel, Model: cfg.ModelAPI.Model,
	}, sagaRepository).WithMaterials(authorMaterials).WithAttachments(researchRepository)
	researchMemory := researchservice.NewAuthorMemoryTools(researchRepository, saga)
	authorMemory := authormemoryrepo.New(db)
	// Nobody stands by the Job of an import, so it runs below the chat.
	materialHandler := workerauthormaterials.NewHandler(authormaterialsservice.RunImport{
		Store: authorMaterials, Model: model.Background(), ModelName: model.CardsModel(), Usage: usage,
	})
	researchCancel := researchservice.NewCancelTurn(researchRepository, func(ctx context.Context, selector researchservice.RootSelector) error {
		client, err := clients.Client(selector.Flow)
		if err != nil {
			return err
		}
		_, err = client.CancelRoot(ctx, codexadapter.RootSelector{CorrelationID: selector.CorrelationID, ThreadID: selector.ThreadID})
		return err
	})
	researchFail := researchservice.NewFailTurn(researchRepository)
	researchCreate := researchservice.NewCreateSession(researchRepository, researchIDGenerator{}, clock)
	researchList := researchservice.NewListSessions(researchRepository)
	researchGet := researchservice.NewGetSession(researchRepository)
	researchChapterKey := researchservice.NewResolveChapterKey(researchRepository, saga)
	researchUpdate := researchservice.NewUpdateSession(researchRepository)
	deleteTree := func(ctx context.Context, target researchdomain.DeleteTarget) error {
		client, err := clients.Client(target.Flow)
		if err != nil {
			return err
		}
		_, err = client.DeleteTree(ctx, codexadapter.RootSelector{CorrelationID: target.ChatID, ThreadID: target.RootThreadID})
		return err
	}
	researchDelete := researchservice.NewDeleteSession(researchRepository, deleteTree)
	// The session of the helper of a draft goes with its draft; one deleted already is no error.
	draftSessions := researchservice.NewDeleteDraftSession(researchRepository, deleteTree)
	deleteDraftSession := func(ctx context.Context, actorUserID, chatID string) error {
		err := draftSessions.Execute(ctx, researchdomain.Actor{UserID: actorUserID}, chatID)
		if errors.Is(err, researchdomain.ErrNotFound) {
			return nil
		}
		return err
	}
	researchSubmit := researchservice.NewSubmitTurn(researchRepository, clock)
	researchSteer := researchservice.NewSteerTurn(researchRepository, func(ctx context.Context, request researchservice.TurnSteerRequest) error {
		client, err := clients.Client(request.Flow)
		if err != nil {
			return err
		}
		return client.SteerTurn(ctx, codexadapter.SteerRequest{
			ThreadID: request.RootThreadID, TurnID: request.TurnID, ClientMessageID: request.ClientMessageID, Input: request.Input,
		})
	}, clock)
	var draftSeams sagadraftrepo.SeamCheck
	if extension.DraftSeams != nil {
		draftSeams = extension.DraftSeams(jobsStore)
	}
	chapterDrafts := sagadraftrepo.New(db, libraryStore, draftSeams)
	libraryService := library.NewService(libraryStore, library.NewSecureIDGenerator(), clock)
	coverRepository := coverrepo.New(db)
	coverUpload := coverservice.NewUploadCover(coverRepository, coverFiles, coverservice.NewSecureCoverIdentity)
	coverGet := coverservice.NewGetCover(coverRepository, coverFiles)
	coverDelete := coverservice.NewDeleteCover(coverRepository)
	coverDeletion := coverservice.NewCoverDeletionReconciler(coverRepository, coverFiles)
	api := &lateHandler{}
	jobTargets := newJobTargetResolver(indexing.NewJobTargetAccessResolver(canonStore), researchCancel)

	core.Usage, core.IDs, core.LibraryService = usage, researchIDGenerator{}, libraryService
	core.JobService = jobs.NewService(jobsStore, auth.SystemClock{}, jobTargets)
	core.Saga, core.SagaStore, core.Chat = saga, sagaRepository, researchRepository
	core.ChatUseCases = ChatUseCases{Create: researchCreate, Get: researchGet, Submit: researchSubmit, Steer: researchSteer, DeleteTree: deleteTree}
	core.AuthorMemory, core.AuthorMaterials, core.Drafts = authorMemory, authorMaterials, chapterDrafts
	core.Covers = Covers{Store: coverRepository, Upload: coverUpload, BookResponse: coverBookResponse(libraryService)}
	core.API = api
	modules, err := extension.Compose(core)
	if err != nil {
		return productApplication{}, err
	}
	if modules.Accounts.Session == nil || modules.Accounts.CSRF == nil {
		return productApplication{}, errors.New("api: the build has no sessions")
	}
	researchBundle := codexresearch.Bundle(researchReads, researchMemory, modules.ChatTools...)
	bundles := map[string]researchservice.Bundle{agentdomain.SessionResearch: researchBundle}
	if modules.Helpers != nil {
		for kind, bundle := range modules.Helpers(researchBundle.Handler) {
			bundles[kind] = bundle
		}
	}
	researchRunner := researchservice.AgentRunnerFunc(func(ctx context.Context, request researchservice.AgentRunRequest) (agentdomain.RunOutcome, error) {
		client, err := clients.Client(request.Flow)
		if err != nil {
			return agentdomain.RunOutcome{}, err
		}
		return client.Run(ctx, codexadapter.RunRequest{
			CorrelationID: request.CorrelationID, TurnCorrelationID: request.TurnCorrelationID,
			ExistingRootThreadID: request.ExistingRootThreadID, ExistingTurnID: request.ExistingTurnID,
			ContinueFailedTurn: request.ContinueFailedTurn, BundleID: request.BundleID,
			SkillName: request.SkillName, InitialInput: request.InitialInput,
			Tools: request.Tools, BindRoot: request.BindRoot, BindTurn: request.BindTurn, Handler: request.Handler,
		})
	})
	researchRun := researchservice.NewRunTurn(researchRepository, researchRunner, bundles)

	timeline := timelineservice.New(func(ctx context.Context, projectID, jobID string) (timelineservice.ThreadBinding, error) {
		binding, err := agentJobs.JobBinding(ctx, agentjobsrepo.JobKey{ProjectID: projectID, JobID: jobID})
		if errors.Is(err, agentjobsrepo.ErrNotFound) {
			return timelineservice.ThreadBinding{}, timelinedomain.ErrNotFound
		}
		return timelineservice.ThreadBinding{Flow: binding.Flow, RootThreadID: binding.RootThreadID, TurnID: binding.TurnID}, err
	}, clients)

	legacyIndexingStore := indexingstore.New(db)
	legacyIndexingService := indexing.NewService(legacyIndexingStore, indexing.NewSecureIDGenerator(), clock)
	projectsStore := projectstore.New(db)
	router := newRouter(HTTPDependencies{
		ProjectsStore: projectsStore, LibraryStore: libraryStore,
		JobsStore: jobsStore, LibraryService: libraryService,
		IndexingService: legacyIndexingService, JobTargetAccessResolver: jobTargets,
		SearchStore: canonStore, AllowedOrigins: cfg.AllowedOrigins, RequestLogWriter: os.Stdout,
		RateLimiter: ratelimit.NewMemoryLimiter(RateLimits(modules.RateLimits)),
		Accounts:    modules.Accounts,
		NotesRoutes: func(router chi.Router, currentUser *auth.Service) {
			repository := notesrepo.New(db)
			httpnotes.Handlers{
				List: notesservice.List{Repository: repository}, Get: notesservice.Get{Repository: repository},
				Create: notesservice.Create{Repository: repository}, Update: notesservice.Update{Repository: repository},
				Delete:  notesservice.Delete{Repository: repository},
				Resolve: notesservice.Resolve{Repository: repository, Native: clients},
				Auth:    currentUser, AllowedOrigins: cfg.AllowedOrigins,
			}.Mount(router)
		},
		AccountQuotaRoutes: func(router chi.Router, currentUser *auth.Service) {
			httpquota.Handlers{
				AccountQuota: quotaservice.GetAccountQuota{Reader: quotastore.New(db), Now: time.Now},
				Auth:         currentUser,
			}.Mount(router)
		},
		BookCoverRoutes: func(router chi.Router, currentUser *auth.Service) {
			httplibrary.NewHandlers(
				coverUpload, coverGet, coverDelete, coverBookResponse(libraryService), currentUser, cfg.AllowedOrigins,
			).Mount(router)
			httplibrary.MountBookExport(router, coverservice.NewExportBook(coverRepository, coverGet, time.Now), currentUser)
		},
		ClientErrorRoutes: func(router chi.Router, csrf func(http.Handler) http.Handler, currentUserID func(*http.Request) string) {
			clienterrors.NewHandler(log.New(os.Stdout, "", 0), currentUserID).Mount(router, csrf, cfg.AllowedOrigins...)
		},
		ChapterSearchRoutes: func(router chi.Router, currentUser *auth.Service) {
			httplibrary.MountChapterSearch(router, coverservice.NewSearchChapters(coverRepository), currentUser, cfg.AllowedOrigins)
		},
		AgentRoutes: func(router chi.Router, currentUser *auth.Service, jobService *jobs.Service) {
			httpresearch.NewHandlers(researchCreate, researchList, researchGet, researchChapterKey, researchUpdate, researchDelete, researchSubmit, researchSteer, researchservice.NewGetAttachment(researchRepository), currentUser, cfg.AllowedOrigins).Mount(router)
			httpindexing.NewStatusHandler(indexingService, currentUser).Mount(router)
			httpsaga.NewHandlers(
				sagareferenceservice.NewListGlossary(sagaRepository, saga),
				sagareferenceservice.NewGetMap(sagaRepository, saga, sagaRepository, sagaRepository),
				sagareferenceservice.NewGetMapNode(sagaRepository, sagaRepository),
				sagareferenceservice.NewSearchMap(sagaRepository, sagaRepository),
				sagareferenceservice.NewGetContents(sagaRepository, saga),
				httpsaga.MapHiddenUseCases{
					List:    sagareferenceservice.NewListMapHidden(sagaRepository, sagaRepository),
					Hide:    sagareferenceservice.NewHideOnMap(sagaRepository, sagaRepository, sagaRepository),
					Restore: sagareferenceservice.NewRestoreOnMap(sagaRepository),
				},
				currentUser,
				cfg.AllowedOrigins,
			).Mount(router)
			httpsagadraft.NewHandlers(httpsagadraft.UseCases{
				List:         sagadraftservice.List{Store: chapterDrafts},
				Get:          sagadraftservice.Get{Store: chapterDrafts},
				Create:       sagadraftservice.Create{Store: chapterDrafts, Now: time.Now},
				Update:       sagadraftservice.Update{Store: chapterDrafts},
				Delete:       sagadraftservice.Delete{Store: chapterDrafts, DeleteSession: deleteDraftSession},
				AddToBook:    sagadraftservice.AddToBook{Store: chapterDrafts},
				OpenEdit:     sagadraftservice.OpenChapterEdit{Store: chapterDrafts},
				Replace:      sagadraftservice.ReplaceChapter{Store: chapterDrafts},
				Revert:       sagadraftservice.RevertChapter{Store: chapterDrafts},
				Reopen:       sagadraftservice.ReopenChapter{Store: chapterDrafts},
				ListVersions: sagadraftservice.ListVersions{Store: chapterDrafts},
				GetVersion:   sagadraftservice.GetVersion{Store: chapterDrafts},
				TakeVersion:  sagadraftservice.TakeVersion{Store: chapterDrafts},
			}, currentUser, cfg.AllowedOrigins).Mount(router)
			httpauthormaterials.NewHandlers(httpauthormaterials.UseCases{
				List:      authormaterialsservice.List{Store: authorMaterials},
				Update:    authormaterialsservice.Update{Store: authorMaterials},
				Delete:    authormaterialsservice.Delete{Store: authorMaterials},
				Start:     authormaterialsservice.Start{Store: authorMaterials, Now: time.Now},
				GetImport: authormaterialsservice.GetImport{Store: authorMaterials, Names: sagaRepository},
				Confirm:   authormaterialsservice.Confirm{Store: authorMaterials},
				Discard:   authormaterialsservice.Discard{Store: authorMaterials},
			}, currentUser, cfg.AllowedOrigins).Mount(router)
			httpauthormemory.NewHandlers(httpauthormemory.UseCases{
				List:   authormemoryservice.List{Store: authorMemory, Chapters: saga},
				Create: authormemoryservice.Create{Access: authorMemory, Store: authorMemory, Chapters: saga, Now: time.Now},
				Update: authormemoryservice.Update{Access: authorMemory, Store: authorMemory, Chapters: saga},
				Delete: authormemoryservice.Delete{Access: authorMemory, Store: authorMemory},
			}, currentUser, cfg.AllowedOrigins).Mount(router)
			httptimeline.NewHandlers(timeline, currentUser, jobService, cfg.AllowedOrigins).Mount(router)
		},
		SiteRoutes: modules.Routes,
	})
	handler := httpapi.RequestLogger(os.Stdout)(router)
	api.handler = handler
	return productApplication{
		httpHandler: handler, routes: router, libraryStore: libraryStore, libraryService: libraryService,
		indexingService: legacyIndexingService, coverDeletion: coverDeletion.ReconcileOne, cardsHandler: cardsHandler,
		layerHandler: layerHandler, materialHandler: materialHandler,
		missingCards:  sagamemoryservice.NewQueueMissingCards(sagaRepository, sagaRepository, sagaRepository, sagaRepository),
		missingLayers: sagalayerservice.NewQueueMissingLayers(sagaRepository, layerQueue),
		chatHandler:   workerresearch.NewHandler(researchRun, researchCancel, researchFail),
		modules:       modules,
	}, nil
}
