// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"canonkeeper/backend/internal/adapters/llmgateway"
	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/desktop"
	appworkservice "canonkeeper/backend/internal/services/appwork"
	service "canonkeeper/backend/internal/services/desktop"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	quotastore "canonkeeper/backend/internal/store/quota"
)

// Options are what the process gives a run: the control channel — stdin and the real stdout of the backend — and, when
// set, Logs, which hears the log file of the run: cmd/desktop sends there whatever else writes to stdout and the
// standard log, since stdout belongs to the channel. Version is the version of the app the build names
// (specs/2026-11-13-desk-settings.md ST4.2), empty for a build that does not. Site is the address of the site the app
// links with, which only the build sets (specs/2026-11-13-desk-account.md AC1.4).
type Options struct {
	In      io.Reader
	Control io.Writer
	Logs    func(*os.File)
	Version string
	// RetryPause is the unit of the pauses of the repeats of a call of the model; zero is the client's own. Only the
	// program of the process tests sets it (specs/2026-11-13-desk-consent.md CN3.4).
	RetryPause time.Duration
	Site       string
	// ResumeEvery is how often the reading of the helper of the account checks the pauses that go on by themselves; zero is
	// ten minutes. Only the program of the process tests sets it (specs/2026-11-13-desk-account-ai.md AI3.6).
	ResumeEvery time.Duration
}

// The reasons of fatal name the step that failed, never its content (DA3, «Types»).
const (
	reasonStart     = "start_expected"
	reasonDataDir   = "data_directory"
	reasonResources = "resources"
	reasonPostgres  = "postgres"
	reasonDatabase  = "database"
	// specs/2026-11-12-desk-process.md PR3.3, PR3.4: the copy before the migrations failed; a migration failed after
	// the copy; the database holds a migration newer than the program knows.
	reasonBackup      = "backup"
	reasonMigrate     = "migrate"
	reasonNewerSchema = "newer_schema"
	reasonAuthor      = "local_author"
	reasonListen      = "listen"
	reasonBackend     = "backend"
	// specs/2026-11-13-desk-upgrade.md UG2.1, UG2.4, UG4.6: the backend is another version than its shell; a migration
	// failed for want of place on the disk; a return of a copy found too little place, a damaged copy, or failed.
	reasonVersionMismatch = "version_mismatch"
	reasonMigrateSpace    = "migrate_space"
	reasonRestoreSpace    = "restore_space"
	reasonRestoreCopy     = "restore_copy"
	reasonRestore         = "restore"
)

// Timeouts of the stop: the chat turns are canceled, the core stops its HTTP, workers and Codex, then PostgreSQL.
const (
	stopTurnsTimeout = 5 * time.Second
	stopCoreTimeout  = 30 * time.Second
	stopClusterTime  = 90 * time.Second
)

// Run is the life of the backend (DA1.6): start from the shell → data directory → PostgreSQL (the one a crash left
// stops first) → the copy and the migrations of a new version → local author → HTTP on 127.0.0.1 on the remembered port
// → ready with the port and the first code. Then it answers open-code and hears secret-result until shutdown, a closed
// stdin or ctx ends it, and stops: the chat turns of the author are canceled the standard way, the core stops taking
// Jobs and stops its HTTP, workers and Codex — the other running Jobs go back to the queue — and PostgreSQL stops fast. A
// step that fails sends fatal with its reason. A backend that could not stop its cluster ends with an error: the shell
// installs an update only after a backend that stopped by itself and cleanly (specs/2026-11-13-desk-updates.md UB2.3).
func Run(ctx context.Context, options Options) (err error) {
	channel := newControl(options.In, options.Control)
	start, err := channel.receive()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil || start.Type != messageStart {
		return fail(channel, reasonStart, errors.New("desktop: the first message is not start"))
	}
	// specs/2026-11-13-desk-upgrade.md UG2.1: the shell and its backend are one version; another one is an installation cut
	// short, and it is said before anything reads the data directory.
	if start.Version != options.Version {
		_ = channel.send(ControlMessage{Type: messageFatal, Reason: reasonVersionMismatch, Version: options.Version, Shell: start.Version})
		return errors.New("desktop: the backend is another version than its shell")
	}
	// specs/2026-11-13-desk-secrets.md SE1.3: start says where the shell keeps the secrets it hands over.
	store, ok := domain.ParseSecretsStore(start.SecretsStore)
	if !ok {
		return fail(channel, reasonStart, errors.New("desktop: start does not say where the shell keeps the secrets"))
	}
	appSecrets := newShellSecrets(channel.send, store, start.Secrets, secretAnswerTime)
	// PR2.2: a path of the shell of Windows loses its prefix \\?\ before any use.
	if start.DataDir, err = plainPath(start.DataDir); err != nil {
		return fail(channel, reasonDataDir, err)
	}
	if start.Resources, err = plainPath(start.Resources); err != nil {
		return fail(channel, reasonResources, err)
	}
	if start.DataDir == "" {
		if start.DataDir, err = DefaultDataDir(); err != nil {
			return fail(channel, reasonDataDir, err)
		}
	}
	data := DataDir{Root: filepath.Clean(start.DataDir)}
	if err := data.Prepare(); err != nil {
		return fail(channel, reasonDataDir, err)
	}
	unfinished := removeUnfinishedBackups(data.Backups())
	if !filepath.IsAbs(start.Resources) {
		return fail(channel, reasonResources, errors.New("desktop: the resources of the bundle must be an absolute path"))
	}
	resources := Resources{Root: filepath.Clean(start.Resources)}
	logFile, err := openLog(data.Logs(), time.Now())
	if err != nil {
		return fail(channel, reasonDataDir, err)
	}
	defer logFile.Close()
	if options.Logs != nil {
		options.Logs(logFile)
	}
	logger := log.New(logFile, "", log.LstdFlags|log.LUTC)
	if unfinished != nil {
		logger.Printf(`{"event":"desktop_unfinished_backup_not_removed","error":%q}`, unfinished.Error())
	}
	// UG2.2: settings.json is read before the cluster — the version that opened the data speaks for a refusal of the data.
	saved, err := loadSettings(data.Settings(), time.Now())
	if err != nil {
		return failLogged(channel, logger, reasonDataDir, err)
	}
	previous := saved.Version()
	secrets, err := loadClusterSecrets(data.Secret())
	if err != nil {
		return failLogged(channel, logger, reasonDataDir, err)
	}
	cluster := newCluster(resources.PostgresBin(), data, secrets)
	cluster.version = options.Version
	// specs/2026-11-13-desk-upgrade.md UG4.2: a return an earlier start did not end is undone before the cluster starts.
	if err := cluster.undoRestore(ctx); err != nil {
		return failLogged(channel, logger, reasonRestore, err)
	}
	// UG4.1: the shell asked to bring the data back from the copy this backend names; then the start goes on as usual.
	if start.Restore {
		step := func(message ControlMessage) { _ = channel.send(message) }
		if failure := cluster.restore(ctx, step, diskFree, time.Now()); failure != nil {
			return failWith(channel, logger, failure.fatal(), failure)
		}
		logger.Print(`{"event":"desktop_restored"}`)
	}
	if err := cluster.Start(ctx); err != nil {
		if errors.Is(err, errNewerSchema) {
			return failWith(channel, logger, cluster.newerSchemaFatal(ctx, previous), err)
		}
		return failLogged(channel, logger, reasonPostgres, err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), stopClusterTime)
		defer cancel()
		if stopErr := cluster.Stop(stopCtx); stopErr != nil {
			logger.Printf(`{"event":"desktop_postgres_stop_failed","error":%q}`, stopErr.Error())
			if err == nil {
				err = stopErr
			}
		}
	}()
	backup, err := cluster.Bootstrap(ctx, bootstrapHooks{
		step: func(step string) {
			_ = channel.send(ControlMessage{Type: messageStep, Step: step, Version: options.Version})
		},
		migrated: func() {
			if _, err := saved.keepVersion(options.Version); err != nil {
				logger.Printf(`{"event":"desktop_version_not_kept","error":%q}`, err.Error())
			}
		},
	})
	if backup != "" {
		// PR3.1, PR3.2, DT13.6: the copy is named in the log of the run; it and the newest one before it stay, whether the
		// migrations went through or failed — a migration that fails start after start makes no third copy.
		logger.Printf(`{"event":"desktop_backup_made","backup":%q}`, backup)
		if err := pruneBackups(data.Backups(), backupsKept, backup); err != nil {
			logger.Printf(`{"event":"desktop_backups_not_pruned","error":%q}`, err.Error())
		}
	}
	if errors.Is(err, errNewerSchema) {
		return failWith(channel, logger, cluster.newerSchemaFatal(ctx, previous), err)
	}
	if err != nil {
		return failWith(channel, logger, bootstrapFailure(err, previous), err)
	}
	authorID, err := openLocalAuthor(ctx, cluster.ProductURL())
	if err != nil {
		return failLogged(channel, logger, reasonAuthor, err)
	}
	// PR1: the window listens on the port settings.json remembers, only on 127.0.0.1.
	listener, err := systemWindowPorts.open(saved.port)
	if err != nil {
		return failLogged(channel, logger, reasonListen, err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	portChanged, err := saved.keep(port)
	if err != nil {
		_ = listener.Close()
		return failLogged(channel, logger, reasonDataDir, err)
	}
	if portChanged {
		logger.Printf(`{"event":"desktop_window_port_changed","port":%d}`, port)
	}

	codes := domain.NewOpenCodes(time.Now)
	// specs/2026-11-13-desk-updates.md UB1: the schedule and the state of an update; the shell says how the app was
	// installed.
	updates := newUpdater(channel.send, saved, options.Version, domain.ParseUpdateKind(start.UpdateKind), logger)
	updatesCtx, stopUpdates := context.WithCancel(context.Background())
	defer stopUpdates()
	// specs/2026-11-13-desk-account.md: the link with an account of the site of the build, over the record of its pass.
	account, err := newAccountLink(options.Site, port, options.Version, appSecrets, saved, eraseShell{send: channel.send}, logger)
	if err != nil {
		_ = listener.Close()
		return failLogged(channel, logger, reasonBackend, err)
	}
	// specs/2026-11-13-desk-account-ai.md AI2: the count of the work of the helper of the account goes through the one client
	// of the app to the site; the refs of its permits are derived with the salt of this data directory. A refusal of the pass
	// an operation of the work hears breaks the link at once.
	salt, err := saved.WorkSalt()
	if err != nil {
		_ = listener.Close()
		return failLogged(channel, logger, reasonDataDir, errors.Join(errNoSalt, err))
	}
	work := appworkservice.New(account.client, nil, salt, account.parts, logger)
	account.client.WhenDropped(account.parts.PassRefused)
	// specs/2026-11-13-desk-gateway.md GW1.1: the local gateway of the helper listens on 127.0.0.1 with a token of this run;
	// the token lives in memory and goes to the app-server and the client of the core by value. It reads the link of the
	// account as the helper «Аккаунт CanonKeeper» (specs/2026-11-13-desk-account-ai.md AI1.2).
	gateway, err := llmgateway.Open(ownAI{settings: saved, secrets: appSecrets, link: account.parts}, logger)
	if err != nil {
		_ = listener.Close()
		return failLogged(channel, logger, reasonListen, err)
	}
	defer gateway.Close()
	desktop := &build{
		host: listener.Addr().String(), authorID: authorID, codes: codes, files: webFiles(), version: options.Version,
		state: appState(data, options.Version, saved, networkUses(account, options.Site, account.network(saved, options.Site), ownNetwork(saved.AI), updates.network),
			proxyOfTheNetwork(os.Getenv, account, options.Site, saved.AI)),
		settings: saved, secrets: appSecrets, shell: eraseShell{send: channel.send}, gateway: gateway, updates: updates, logger: logger, account: *account,
		work: work, site: options.Site, resumeEvery: options.ResumeEvery,
		obsidian: newObsidianBridge(channel.send, logger),
		programs: newProgramDoor(data.Root, logger), bridge: start.Bridge,
	}
	// specs/2026-11-15-desk-mcp.md MC1.6, MC1.7, MC3.5: what a crash left of the door goes before it opens; the door closes on
	// every way out once it may have opened; «О программе» says whether the programs may read.
	desktop.programs.clean()
	defer desktop.programs.closing()
	desktop.state.Programs = func() bool { return saved.Programs().Enabled }
	desktop.ready = func() {
		// UG2.2, UG2.5: the start went well — this version opened the data; ready names the version before an update once,
		// and the notes of the release now running when the app installed it (UB2.8).
		updated, err := saved.keepVersion(options.Version)
		if err != nil {
			logger.Printf(`{"event":"desktop_version_not_kept","error":%q}`, err.Error())
		}
		message := ControlMessage{
			Type: messageReady, Port: port, PortChanged: portChanged, GatewayPort: gateway.Port(), Code: codes.Issue(), UpdatedFrom: updated,
			ReleaseNotes: saved.releaseNotesOf(options.Version),
		}
		if err := channel.send(message); err != nil {
			logger.Print(`{"event":"desktop_ready_not_sent"}`)
			return
		}
		if err := saved.forgetUpdate(); err != nil {
			logger.Printf(`{"event":"desktop_update_note_not_taken","error":%q}`, err.Error())
		}
		if err := saved.forgetNotes(options.Version); err != nil {
			logger.Printf(`{"event":"desktop_release_notes_not_taken","error":%q}`, err.Error())
		}
		go updates.run(updatesCtx)
		account.served()
		// MC1.7: the door for the programs opens once the core serves, when the reading is on.
		desktop.programs.start(saved.Programs().Enabled)
		// AI3.6: the reading of the helper of the account goes on by itself from the pauses that are due.
		go desktop.keepReading(updatesCtx, updated != "")
	}
	// AI1.3: a helper the site tells while the source is the account restarts the app-server by the rule of the gateway.
	account.helpers.hear(desktop.AIChanged)
	coreCtx, stopCore := context.WithCancel(context.Background())
	defer stopCore()
	coreDone := make(chan error, 1)
	go func() {
		coreDone <- app.Run(coreCtx, desktopConfig(data, resources, cluster, listener, gateway, saved.AI(), options.RetryPause), logger, desktop.extension(listener))
	}()

	messages := make(chan ControlMessage)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		for {
			message, err := channel.receive()
			if errors.Is(err, errControlMessage) {
				logger.Print(`{"event":"desktop_control_message_refused"}`)
				continue
			}
			if err != nil {
				return
			}
			select {
			case messages <- message:
			case <-coreCtx.Done():
				return
			}
		}
	}()

	for {
		select {
		case message := <-messages:
			switch message.Type {
			case messageOpenCode:
				if err := channel.send(ControlMessage{Type: messageOpenCode, Code: codes.Issue()}); err != nil {
					logger.Print(`{"event":"desktop_open_code_not_sent"}`)
				}
				continue
			case messageSecretResult:
				appSecrets.answered(message.Request, message.OK)
				continue
			case messageUpdateState:
				updates.heard(message)
				continue
			case messageFolderPicked:
				// OB1.3: the check of the folder runs outside this loop.
				desktop.obsidian.picked(message)
				continue
			case messageShutdown:
			default:
				logger.Printf(`{"event":"desktop_control_message_unknown","type":%q}`, message.Type)
				continue
			}
		case <-ended:
		case <-ctx.Done():
		case err := <-coreDone:
			// The core stopped by itself: Codex or the HTTP server failed; the shell restarts the backend.
			if err == nil {
				err = errors.New("desktop: the core stopped")
			}
			return failLogged(channel, logger, reasonBackend, err)
		}
		break
	}
	stopUpdates()
	// MC1.7: the step of the turns closes the door of the programs first; its handlers end within the same term.
	stopTurns(desktop, logger)
	stopCore()
	select {
	case err := <-coreDone:
		if err != nil {
			logger.Printf(`{"event":"desktop_core_stop_failed","error":%q}`, err.Error())
		}
	case <-time.After(stopCoreTimeout):
		logger.Print(`{"event":"desktop_core_stop_timed_out"}`)
	}
	return nil
}

// stopTurns closes the door of the programs, cancels the chat turns of the author before the core stops (DA1.6), and waits
// for the handlers of the shut door within the same term (specs/2026-11-15-desk-mcp.md MC1.7): the stop of the backend stays
// within the term of the shell.
func stopTurns(desktop *build, logger *log.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), stopTurnsTimeout)
	defer cancel()
	if desktop.programs != nil {
		// The door stops taking calls before the turns stop — its mcp.json removal, tried again up to 2 s, is in the term.
		desktop.programs.closing()
		defer desktop.programs.wait(ctx)
	}
	stop := desktop.turnsStopper()
	if stop == nil {
		return
	}
	canceled, err := stop(ctx)
	if err != nil {
		logger.Printf(`{"event":"desktop_chat_turns_not_stopped","canceled":%d}`, canceled)
		return
	}
	if canceled > 0 {
		logger.Printf(`{"event":"desktop_chat_turns_stopped","canceled":%d}`, canceled)
	}
}

// openLocalAuthor prepares the local author through a pool of its own that closes before the core opens its pools.
func openLocalAuthor(ctx context.Context, productURL string) (string, error) {
	db, err := storedb.Open(ctx, productURL)
	if err != nil {
		return "", err
	}
	defer db.Close()
	return prepareLocalAuthor(ctx, db)
}

// prepareLocalAuthor finds or creates the local author with the limits at their largest and no session of an earlier
// run (DA2.2, DA3.3).
func prepareLocalAuthor(ctx context.Context, db *storedb.DB) (string, error) {
	authors := authstore.New(db)
	return service.PrepareLocalAuthor{
		Authors:  authors,
		Users:    auth.NewService(authors, auth.NewSecureIDGenerator(), auth.SystemClock{}),
		Defaults: quotastore.New(db, quotastore.Fence{}),
		Now:      time.Now,
	}.Execute(ctx)
}

// fail tells the shell why the backend stops and returns the error for the process.
func fail(channel *control, reason string, err error) error {
	_ = channel.send(ControlMessage{Type: messageFatal, Reason: reason})
	return err
}

func failLogged(channel *control, logger *log.Logger, reason string, err error) error {
	return failWith(channel, logger, ControlMessage{Type: messageFatal, Reason: reason}, err)
}

// failWith sends a fatal that names more than its reason: the copy of a failed migration, the versions of the data.
func failWith(channel *control, logger *log.Logger, fatal ControlMessage, err error) error {
	logger.Printf(`{"event":"desktop_fatal","reason":%q,"backup":%q,"error":%q}`, fatal.Reason, fatal.Backup, err.Error())
	_ = channel.send(fatal)
	return err
}

// newerSchemaFatal is the fatal of data a newer program opened, with the copy a return would bring back (UG2.3).
func (cluster *Cluster) newerSchemaFatal(ctx context.Context, opened string) ControlMessage {
	major, err := cluster.bundleMajor(ctx)
	if err != nil {
		major = ""
	}
	return newerSchema(opened, cluster.data, major)
}
