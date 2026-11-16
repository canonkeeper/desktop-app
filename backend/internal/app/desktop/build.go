// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	codexadapter "canonkeeper/backend/internal/adapters/codex"
	"canonkeeper/backend/internal/adapters/llmgateway"
	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/config"
	httpdesktop "canonkeeper/backend/internal/delivery/http/desktop"
	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	agentdomain "canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/aisettings"
	domain "canonkeeper/backend/internal/domain/desktop"
	readingdomain "canonkeeper/backend/internal/domain/readingconsent"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/ratelimit"
	appworkrepo "canonkeeper/backend/internal/repository/postgres/appwork"
	"canonkeeper/backend/internal/repository/postgres/modeljournal"
	readingrepo "canonkeeper/backend/internal/repository/postgres/readingconsent"
	aiservice "canonkeeper/backend/internal/services/aisettings"
	appworkservice "canonkeeper/backend/internal/services/appwork"
	service "canonkeeper/backend/internal/services/desktop"
	quotaservice "canonkeeper/backend/internal/services/quota"
	readingservice "canonkeeper/backend/internal/services/readingconsent"
	authstore "canonkeeper/backend/internal/store/auth"
	indexingstore "canonkeeper/backend/internal/store/indexing"
	projectstore "canonkeeper/backend/internal/store/projects"
	quotastore "canonkeeper/backend/internal/store/quota"

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

// desktopConfig is the configuration of the open core on the computer of the author: one origin of the window, the
// cluster of the data directory, the files, Codex homes and workspace under it, the codex-app-server and profile of the
// bundle — copied into the homes at every start (specs/2026-11-13-desk-home.md HM1) — and Codex of the chat on the local
// gateway of «Свой ИИ» with the window the author set, its token given to the app-server alone by value
// (specs/2026-11-13-desk-gateway.md GW2.1, GW2.2).
func desktopConfig(data DataDir, resources Resources, cluster *Cluster, listener net.Listener, gateway *llmgateway.Gateway, ai aisettings.Settings, retryPause time.Duration) config.Config {
	return config.Config{
		HTTPAddr: listener.Addr().String(), Environment: "desktop",
		DatabaseURL: cluster.ProductURL(), WorkerDatabaseURL: cluster.WorkerURL(), WorkersEnabled: true,
		AllowedOrigins: []string{"http://" + listener.Addr().String()},
		FileStorageDir: data.Files(),
		CodexPath:      resources.CodexAppServer(), CodexCWD: data.CodexWorkspace(),
		CodexRepositoryHome: resources.CodexHomes(), CodexHome: data.CodexHome(), CodexHomeCopies: true,
		CodexSQLiteHome:  data.CodexSQLite(),
		CodexPrivateHome: data.CodexPrivate(), CodexTempDir: data.CodexTemp(),
		CodexConfigOverrides: llmgateway.CodexOverrides(gateway.BaseURL(), ai.ContextWindow(), ai.CompactLimit()),
		CodexEnvironment:     []string{llmgateway.TokenVariable + "=" + gateway.Token()},
		// GW3.1: the calls of the core go to the gateway with its token by value and the Job of each call; the cards, the
		// register of names and scan read the books with the model of the reading (DA5.6). The places at once are the
		// author's and the gateway keeps them: the client takes as many as the settings allow at most.
		ModelAPI: config.ModelAPI{
			BaseURL: gateway.BaseURL(), Key: gateway.Token(), CarryJob: true, Stops: true, RetryPause: retryPause,
			Model: llmgateway.CardsModel, CardsModel: llmgateway.CardsModel, Parallel: aisettings.MaxParallel,
		},
	}
}

// ownAI is the helper of the app as the gateway reads it on every request (GW1.2): the settings of settings.json, the key
// of «Свой ИИ» the backend holds and the link with an account (specs/2026-11-13-desk-account-ai.md AI1.2).
type ownAI struct {
	settings *settings
	secrets  *shellSecrets
	link     linkHolder
}

// linkHolder says whether the app is linked with an account now.
type linkHolder interface {
	Linked() bool
}

func (own ownAI) AI() aisettings.Settings { return own.settings.AI() }

func (own ownAI) Key() (string, bool) { return own.secrets.Value(domain.SecretOwnAI) }

// Connected is the helper of the app by the one rule of the domain (specs/2026-11-13-desk-gateway.md GW4.1): the status of
// a saga, the queue of the cards Job and the turn of the chat read it; until it holds, the reading of the saga waits for
// the helper and the chat asks nothing (specs/2026-11-12-desk-window.md WN3).
func (own ownAI) Connected(context.Context) bool {
	return own.settings.AI().Connected(own.held())
}

// held is what the backend holds for the helper: the key of «Свой ИИ» and the pass of the link.
func (own ownAI) held() aisettings.Held {
	return aisettings.Held{Key: own.secrets.Has(domain.SecretOwnAI), Pass: own.link != nil && own.link.Linked()}
}

// build is the desktop over the open core (DA1.8, DA2, DA3): the local author's sessions, opened by the one-time codes
// of the shell, and «кто автор» of the window (specs/2026-11-12-desk-window.md WN1.1); two common workers, two for chat
// turns, one for cards and one for the thread layer; the books read only with the author's consent; the root handler
// that checks Host and serves the embedded page.
type build struct {
	host     string
	authorID string
	codes    *domain.OpenCodes
	files    fs.FS
	// state reads what «Настройки» of the app show (specs/2026-11-13-desk-settings.md ST2.1).
	state service.ReadAppState
	// settings is the owner of settings.json, secrets the secrets of the app the shell kept: «Свой ИИ» reads and
	// writes through them (specs/2026-11-13-desk-secrets.md SE2).
	settings *settings
	secrets  *shellSecrets
	// gateway is the local gateway of «Свой ИИ» (specs/2026-11-13-desk-gateway.md GW1): Codex of the chat and the calls of
	// the core go to the provider of the author through it; codex are the app-servers of the core, set by compose.
	gateway *llmgateway.Gateway
	codex   *app.CodexClients
	logger  *log.Logger
	// catchUp queues the cards and the layer the helper is to read, as at the start (GW4.2; specs/2026-11-15-desk-reading-rest.md
	// RR1.8); set by compose.
	catchUp func(context.Context) (int, error)
	// shell is the shell as the erasure asks it (specs/2026-11-13-desk-erase.md ER1.2).
	shell eraseShell
	// version is the version of the app its saga files name (specs/2026-11-13-desk-sagafile.md SF1.1).
	version string
	// updates is an update of the app (specs/2026-11-13-desk-updates.md UB1, UB2.1): its operations, the count of the turns
	// it waits for and the gate of new turns it closes.
	updates *updater
	// account is the link of the app with an account of the site (specs/2026-11-13-desk-account.md); work is the count of
	// the work of the helper of the account (specs/2026-11-13-desk-account-ai.md).
	account accountLink
	work    *appworkservice.Work
	// site is the address of the site of the build, whose host the journal of the work of the account names.
	site string
	// resume lets the reading of the helper of the account go on by itself, every resumeEvery (AI3.6); set by compose.
	resume      *readingservice.Resume
	resumeEvery time.Duration
	// obsidian is the bridge with the vaults of Obsidian (specs/2026-11-13-desk-obsidian.md).
	obsidian *obsidianBridge
	// programs is the door for the programs of the author and bridge how the shell runs the program of the bridge
	// (specs/2026-11-15-desk-mcp.md MC1.6, MC3.2).
	programs *programDoor
	bridge   *domain.Bridge
	// ready tells the shell the backend serves; stopTurns cancels the chat turns of the author once compose built it —
	// a shutdown may come before.
	ready     func()
	mu        sync.Mutex
	stopTurns func(context.Context) (int, error)
}

// helper is the helper of the app as the build reads it: the settings, the key and the link.
func (desktop *build) helper() ownAI {
	return ownAI{settings: desktop.settings, secrets: desktop.secrets, link: desktop.account.parts}
}

// readingSettings are the settings the reading of the books reads: the helper, the site of the account and the link.
func (desktop *build) readingSettings() readingSettings {
	return readingSettings{ownAI: desktop.helper(), site: desktop.site, account: &desktop.account}
}

// turnsStopper is what cancels the chat turns of the author, nil before compose.
func (desktop *build) turnsStopper() func(context.Context) (int, error) {
	desktop.mu.Lock()
	defer desktop.mu.Unlock()
	return desktop.stopTurns
}

func (desktop *build) extension(listener net.Listener) app.Extension {
	extension := app.Extension{
		// The thread layer of the app has its one worker (specs/2026-11-12-desktop-target.md DT4.6; GW3.4).
		Workers:  app.Workers{Common: 2, Chat: 2, Cards: 1, Layer: 1},
		Listener: listener,
		Compose:  desktop.compose,
	}
	if desktop.work != nil {
		// specs/2026-11-13-desk-account-ai.md AI1.4, AI2.1: a turn and an upload of materials keep their source at their
		// start, and the work of the account is opened at its site as the last step.
		extension.Charges = charges{settings: desktop.settings, link: desktop.account.parts, work: desktop.work}
	}
	return extension
}

func (desktop *build) compose(core app.Core) (app.Modules, error) {
	sessions := auth.NewService(authstore.New(core.DB), auth.NewSecureIDGenerator(), auth.SystemClock{})
	sessionHandlers := auth.NewHandlers(sessions, false)
	// The journal of the gateway is written once the database of the run is open (GW1.7): until then nothing is sent
	// (specs/2026-11-14-desk-journal.md JR1.1).
	journal := modeljournal.New(core.DB)
	desktop.gateway.UseJournal(journal)
	// «Проверить связь» (specs/2026-11-13-desk-probes.md PB2): the probes go through the gateway, the report into settings.json.
	checks := &aiservice.Checks{
		Settings: desktop.settings, Keys: desktop.secrets, Prober: desktop.gateway, Reports: desktop.settings,
		Limit: aiservice.CheckTime, Now: time.Now, Logger: desktop.logger,
	}
	readings := readingrepo.New(core.DB).WithSamples(indexingstore.New(core.DB))
	reading := desktop.readingSettings()
	// One policy of the reading for the builders, the queues, the status, the estimate and the preview of a vault (CN3.1;
	// specs/2026-11-13-desk-obsidian.md OB4.4; specs/2026-11-15-desk-reading-rest.md RR1.4).
	readingPolicy := sagaReading{readingservice.Policy{Settings: reading, Consents: readings, Marks: readings}}
	toRead := sagaToRead{saga: core.SagaStore, reading: readings, policy: readingPolicy.Policy}
	samples := readingservice.Samples{
		Settings: reading, Owners: readings, Consents: readings, Saga: toRead, Store: readings,
		Cards: sampleCards{saga: core.SagaStore, memory: core.Saga, reading: readings}, Cancel: cancelJob{jobs: core.JobService}, Now: time.Now,
	}
	// specs/2026-11-13-desk-consent.md CN3.3: every request of the reading is checked at the gateway against the consent of
	// its saga, and its count is written with its row of the journal.
	desktop.gateway.UseReading(gatewayReading{admissions: &readingservice.Admissions{
		Settings: reading, Jobs: readings, Ledger: readings, Now: time.Now,
	}})
	readAI := aiservice.Read{
		Settings: desktop.settings, Keys: desktop.secrets, Runtime: desktop, Reports: desktop.settings,
		// specs/2026-11-13-desk-consent-web.md RW3.1: the sagas a change of the prices would pause, counted by the consent.
		Stops: readingservice.MoneyStops{Settings: reading, Consents: readings, Author: desktop.authorID},
	}
	open := httpdesktop.Handlers{
		Open:     service.OpenSession{Codes: desktop.codes, Sessions: sessions, LocalAuthorID: desktop.authorID, Now: time.Now},
		Sessions: sessions,
		State:    desktop.state,
		AI: httpdesktop.AI{
			Read: readAI, Update: aiservice.Update{Settings: desktop.settings, Read: readAI, Changed: desktop, Account: desktop.account},
			SetKey:    aiservice.SetKey{Keys: desktop.secrets, Changed: desktop, Checks: checks},
			DeleteKey: aiservice.DeleteKey{Keys: desktop.secrets, Changed: desktop, Checks: checks},
			// specs/2026-11-13-desk-probes.md PB1: the models of the provider, asked through the gateway, the reader of the key.
			Models: aiservice.ListModels{Settings: desktop.settings, Keys: desktop.secrets, Provider: desktop.gateway},
			Check:  checks,
		},
		// specs/2026-11-13-desk-consent.md: the reading of the books by «Свой ИИ» of the saga of its owner.
		Reading: httpdesktop.Reading{
			Estimate: readingservice.GetEstimate{
				Settings: reading, Owners: readings,
				Saga: toRead, Samples: samples,
			},
			Consent: readingservice.GetConsent{Settings: reading, Owners: readings, Consents: readings},
			SetConsent: readingservice.SetConsent{
				Settings: reading, Owners: readings, Store: readings, Now: time.Now,
				Work: readingWork{queue: core.QueueReading, live: readings, jobs: core.JobService},
			},
			Sample: samples,
			Unit: func() readingdomain.Unit {
				if own := desktop.settings.AI().Own; own != nil {
					return readingdomain.UnitOf(own.Advanced)
				}
				return readingdomain.UnitTokens
			},
		},
		Erase: service.EraseData{Shell: desktop.shell, Account: desktop.account.parts.Unlink},
		// specs/2026-11-14-desk-journal.md JR2: the journal of what went out, read, saved and cleared by «Настройки → Журнал».
		Log:     service.SendLog{Store: journal, Clears: desktop.settings, Checks: checks, Now: time.Now},
		Account: desktop.account.handlers(),
		Welcome: service.DismissWelcome{Settings: desktop.settings},
		Updates: httpdesktop.Updates{
			Read: service.ReadAppUpdate{Updates: desktop.updates}, Check: service.CheckAppUpdate{Updates: desktop.updates},
			Install: service.InstallAppUpdate{Updates: desktop.updates}, Cancel: service.CancelAppUpdate{Updates: desktop.updates},
			SetDaily: service.SetAppUpdateDaily{Updates: desktop.updates},
		},
		CSRF:           sessionHandlers.CSRFMiddleware,
		AllowedOrigins: core.Config.AllowedOrigins,
	}
	sagas := projects.NewService(projectstore.New(core.DB, projectstore.WithQuota(core.Quota)), projects.NewSecureIDGenerator(), auth.SystemClock{})
	stop := service.StopChatTurns{Projects: sagas, Jobs: core.JobService, LocalAuthorID: desktop.authorID}
	if desktop.work != nil {
		// The rows of the work a site counts live in the database of the run (specs/2026-11-13-desk-account-ai.md AI2.3).
		desktop.work.Rows = appworkrepo.New(core.DB)
		stop.Counted = desktop.work
		// The gateway takes the work of the account to its site (specs/2026-11-13-desk-account-ai.md AI2.4, AI3.1).
		desktop.gateway.UseAccount(gatewayAccount{
			link: desktop.account.parts, host: accountdomain.HostOf(desktop.site), work: desktop.work, client: desktop.account.client,
		})
	}
	// specs/2026-11-15-desk-mcp.md: the door for the programs of the author, once the core is built.
	open.Programs = desktop.useProgramDoor(core, sagas, journal)
	stopTurns := stop.Execute
	desktop.useTransfers(core, desktop.account.client, service.CountChatTurns{Projects: sagas, Jobs: core.JobService, LocalAuthorID: desktop.authorID}.Execute)
	desktop.mu.Lock()
	desktop.stopTurns, desktop.codex, desktop.catchUp = stopTurns, core.Codex, core.CatchUpReading
	desktop.resume = &readingservice.Resume{
		Settings: reading, Store: readings, Now: time.Now,
		Work: readingWork{queue: core.QueueReading, live: readings, jobs: core.JobService},
	}
	desktop.mu.Unlock()
	modules := app.Modules{
		Accounts: app.Accounts{
			Session: sessions,
			Routes: func(router chi.Router) {
				sessionHandlers.Mount(router)
				open.Mount(router)
				// «Открыть файл саги» and «Перенести сагу с сайта»: only the app opens a saga file and transfers one from the site.
				desktop.mountSagaFiles(router, core, sessions)
				// The bridge with a vault of Obsidian (specs/2026-11-13-desk-obsidian.md): only the app reads a folder of the author.
				desktop.obsidian.handlers(core, desktop.helper().reads, readingPolicy.allowed, sessions).Mount(router)
			},
			CSRF: sessionHandlers.CSRFMiddleware,
		},
		RateLimits: windowRateLimits,
		// The helper is «Свой ИИ» of the author: none until he connects one (specs/2026-11-12-desk-window.md WN3; GW4.1).
		Helper: desktop.helper(),
		// The books are read only under the consent of the saga (specs/2026-11-13-desk-consent.md CN3.1).
		Reading: readingPolicy.Policy,
		// The status says the reading of a saga by one port: what it waits for and its state (CN3.8).
		ReadingStatus: readingservice.Status{
			Settings: reading, Consents: readings, Saga: toRead, Pace: readings,
			Samples: samples, Now: time.Now,
		},
		// The turn names its thread and its Job to the gateway while it runs (GW2.4).
		TurnThreads: desktop.gateway.Threads(),
		// What a turn gives Codex is named by its thread for the record of its next sending (specs/2026-11-14-desk-journal.md
		// JR1.4).
		TurnNames: journal,
		// No new turn while an update is installed (specs/2026-11-13-desk-updates.md UB2.1).
		TurnGate: desktop.updates.gate,

		SagaArchive: app.SagaArchive{CreatedBy: "CanonKeeper " + desktop.version},
		Serve:       desktop.serve,
		Start: func(ctx context.Context) ([]<-chan struct{}, func(context.Context) error) {
			// The drafts of the bridge a fallen load left are canceled before the shell hears the backend serves
			// (specs/2026-11-14-desk-obsidian-books.md OK1.4).
			desktop.obsidian.cancelDrafts(ctx)
			if desktop.ready != nil {
				desktop.ready()
			}
			return nil, desktop.stopModules(checks)
		},
	}
	if desktop.work != nil {
		// specs/2026-11-13-desk-account-ai.md AI2.3, AI2.7, AI2.9, AI2.11: the turn takes, names and closes the work the site
		// counts; getJob of a turn says whether its question came back; getAccountQuota tells the counters of the site.
		modules.TurnWork, modules.Questions = desktop.work, questions{work: desktop.work}
		// AI3.2, AI3.8: the end of a Job of the reading or of an upload closes its permit.
		modules.WorkEnded = workEnded{work: desktop.work}
		modules.AccountQuota = accountQuota{
			local: quotaservice.GetAccountQuota{Reader: quotastore.New(core.DB, core.Quota), Now: time.Now}, settings: desktop.settings,
			link: desktop.account, work: desktop.work,
		}
	}
	return modules, nil
}

// chatRuntime is the app-server of the chat, once compose set the clients of Codex.
func (desktop *build) chatRuntime() *codexadapter.Client {
	desktop.mu.Lock()
	clients := desktop.codex
	desktop.mu.Unlock()
	if clients == nil {
		return nil
	}
	client, err := clients.Client(agentdomain.FlowResearchChat)
	if err != nil {
		return nil
	}
	return client
}

// eraseShell asks the shell to erase all the data of the app: the line erase of the control channel
// (specs/2026-11-13-desk-erase.md ER1.2). The backend removes nothing itself; the shell stops it, then erases. Focus asks
// it to raise the window, the line focus (specs/2026-11-13-desk-account.md AC1.3).
type eraseShell struct{ send func(ControlMessage) error }

func (shell eraseShell) AskErase() error {
	if shell.send == nil {
		return errors.New("desktop: no control channel")
	}
	return shell.send(ControlMessage{Type: messageErase})
}

func (shell eraseShell) Focus() error {
	if shell.send == nil {
		return errors.New("desktop: no control channel")
	}
	return shell.send(ControlMessage{Type: messageFocus})
}

// AfterAnswer says that a new window of the chat waits for the answer of the turn that runs (GW2.3).
func (desktop *build) AfterAnswer() bool {
	chat := desktop.chatRuntime()
	return chat != nil && chat.Pending()
}

// catchUpTime bounds the catch-up of the cards a connected helper starts.
const catchUpTime = 2 * time.Minute

// AIChanged hears a change of the settings or of the key that went through. The app-server of the chat takes the window
// the author set — at once when no turn runs, after the answer when one does (GW2.3); the models and the effort need no
// restart, the gateway reads them on every request. A helper that is connected now gets the cards it is to read queued
// at once, without a restart of the app (GW4.2): the catch-up of the start queues only what is missing. The journal says
// how each catch-up ended — the one sign of it, which the tests of the process wait for.
func (desktop *build) AIChanged(ctx context.Context) {
	if chat := desktop.chatRuntime(); chat != nil {
		ai := desktop.settings.AI()
		overrides := llmgateway.CodexOverrides(desktop.gateway.BaseURL(), ai.ContextWindow(), ai.CompactLimit())
		if _, err := chat.Configure(ctx, overrides); err != nil {
			desktop.logger.Print(`{"event":"desktop_chat_window_not_applied"}`)
		}
	}
	desktop.mu.Lock()
	catchUp := desktop.catchUp
	desktop.mu.Unlock()
	if catchUp == nil || !desktop.helper().Connected(ctx) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), catchUpTime)
		defer cancel()
		queued, err := catchUp(ctx)
		if err != nil {
			desktop.logger.Print(`{"event":"desktop_cards_catch_up_failed"}`)
			return
		}
		desktop.logger.Printf(`{"event":"desktop_cards_catch_up_done","queued":%d}`, queued)
	}()
}

// serve is the root handler (DA3.4, DA3.6): a Host other than 127.0.0.1:P gets 421, the API and the readiness checks go
// to the core, the returns of the pages /link and /transfer of the site are the two routes without the session of the
// window (specs/2026-11-13-desk-account.md AC1.2; specs/2026-11-16-desk-site-saga.md TR5.1), every other path is the
// embedded page, and every answer carries the headers of the page.
func (desktop *build) serve(api http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", api)
	mux.Handle("/healthz", api)
	mux.Handle("/readyz", api)
	mux.Handle("/desktop/link/callback", desktop.account.callback())
	mux.Handle("/desktop/transfer/callback", httpdesktop.TransferCallback{Finish: desktop.account.transfers.Finish})
	mux.Handle("/", page(desktop.files))
	return httpapi.RequireHost(desktop.host)(withPageHeaders(mux))
}

// A window opens a session a few times a day; a page that guesses codes gets thirty tries a minute. The key of
// «Свой ИИ» is handed over or removed a few times at most (specs/2026-11-13-desk-secrets.md SE2.5); all the data are
// erased once (specs/2026-11-13-desk-erase.md ER1.2).
// The contract of the window names each limit in the words of its operation (TestTheLimitsOfTheWindowAreTheContracts).
var windowRateLimits = map[string]ratelimit.Rule{
	"openDesktopSession": {Limit: 30, Window: time.Minute},
	"setAiKey":           {Limit: 30, Window: time.Minute},
	"deleteAiKey":        {Limit: 30, Window: time.Minute},
	"eraseDesktopData":   {Limit: 5, Window: time.Minute},
	// specs/2026-11-15-desk-mcp.md MC3.1, MC3.3: the reading by the programs turns on and off; the file of the extension is built
	// on every call.
	"updateProgramAccess":       {Limit: 30, Window: time.Minute},
	"getClaudeDesktopExtension": {Limit: 10, Window: time.Minute},
	// The models of the provider are asked when the author opens the list (specs/2026-11-13-desk-probes.md PB1.2).
	"listAiModels": {Limit: 30, Window: time.Minute},
	// A check takes minutes and one runs at a time; ten starts and thirty stops a minute are more than a window needs.
	"startAiCheck":  {Limit: 10, Window: time.Minute},
	"cancelAiCheck": {Limit: 30, Window: time.Minute},
	// «Добро пожаловать» is closed once (specs/2026-11-13-desk-upgrade.md UG5.1).
	"dismissWelcome": {Limit: 30, Window: time.Minute},
	// The consent of the reading changes a few times a reading (specs/2026-11-13-desk-consent.md CN3.2).
	"setReadingConsent": {Limit: 60, Window: time.Minute},
	// A sample is pressed once or twice a saga (specs/2026-11-13-desk-consent.md CN2).
	"startReadingSample":  {Limit: 30, Window: time.Minute},
	"cancelReadingSample": {Limit: 60, Window: time.Minute},
	// An update is checked, installed or canceled by a button a few times a day (specs/2026-11-13-desk-updates.md
	// UB1.7).
	"checkAppUpdate":          {Limit: 10, Window: time.Minute},
	"installAppUpdate":        {Limit: 10, Window: time.Minute},
	"cancelAppUpdate":         {Limit: 10, Window: time.Minute},
	"updateAppUpdateSettings": {Limit: 30, Window: time.Minute},
	// The folder of a vault is chosen by the system window a few times; its tree is looked over and its link read as
	// the page opens (specs/2026-11-13-desk-obsidian.md OB0.1) — the gate keeps one reading of a saga at a time.
	"beginObsidianVaultChoice": {Limit: 10, Window: time.Minute},
	"unlinkObsidianVault":      {Limit: 10, Window: time.Minute},
	"scanObsidianVault":        {Limit: 30, Window: time.Minute},
	"previewObsidianImport":    {Limit: 20, Window: time.Minute},
	// The books of the folders are loaded and updated by a button after a preview, a book linked with a folder by hand
	// (specs/2026-11-14-desk-obsidian-books.md).
	"importObsidianBooks":          {Limit: 10, Window: time.Minute},
	"updateBookFromObsidian":       {Limit: 20, Window: time.Minute},
	"linkBookToObsidianFolder":     {Limit: 20, Window: time.Minute},
	"unlinkBookFromObsidianFolder": {Limit: 20, Window: time.Minute},
	// The records of its notes are loaded by a button after a preview (specs/2026-11-15-desk-obsidian-notes.md).
	"importObsidianMaterials": {Limit: 10, Window: time.Minute},
	// The map and the notes of the chapters are exported by a button (specs/2026-11-15-desk-obsidian-export.md OE1.1).
	"exportObsidianSaga": {Limit: 10, Window: time.Minute},
	// A transfer of a saga from the site starts and stops by a button (specs/2026-11-16-desk-site-saga.md TR2).
	"startSiteSagaTransfer":  {Limit: 10, Window: time.Minute},
	"cancelSiteSagaTransfer": {Limit: 30, Window: time.Minute},
	// The journal is cleared and saved to a file by a button (specs/2026-11-14-desk-journal.md JR2.5, JR2.6); the file reads
	// the whole choice, the heaviest read of the window.
	"clearSendLog":   {Limit: 10, Window: time.Minute},
	"getSendLogFile": {Limit: 10, Window: time.Minute},
}
