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
	"sync"
	"time"

	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/desktop"
	service "canonkeeper/backend/internal/services/desktop"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	quotastore "canonkeeper/backend/internal/store/quota"
)

// Options are what the process gives a run: the control channel — stdin and the real stdout of the backend — and, when
// set, Logs, which hears the log file of the run: cmd/desktop sends there whatever else writes to stdout and the
// standard log, since stdout belongs to the channel. Version is the version of the app the build names
// (specs/2026-11-13-desk-settings.md ST4.2), empty for a build that does not.
type Options struct {
	In      io.Reader
	Control io.Writer
	Logs    func(*os.File)
	Version string
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
)

// Timeouts of the stop: the chat turns are canceled, the core stops its HTTP, workers and Codex, then PostgreSQL.
const (
	stopTurnsTimeout = 5 * time.Second
	stopCoreTimeout  = 30 * time.Second
	stopClusterTime  = 90 * time.Second
)

// keys are the keys of the providers the shell handed over (DA4.2); they live in memory only, until the backend exits.
type keys struct {
	mu     sync.Mutex
	values map[string]string
}

func (store *keys) set(id string, value *string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.values == nil {
		store.values = map[string]string{}
	}
	if value == nil {
		delete(store.values, id)
		return
	}
	store.values[id] = *value
}

// Run is the life of the backend (DA1.6): start from the shell → data directory → PostgreSQL (the one a crash left
// stops first) → the copy and the migrations of a new version → local author → HTTP on 127.0.0.1 on the remembered port
// → ready with the port and the first code. Then it answers open-code and secret until shutdown, a closed stdin or ctx
// ends it, and stops: the chat turns of the author are canceled the standard way, the core stops taking Jobs and stops
// its HTTP, workers and Codex — the other running Jobs go back to the queue — and PostgreSQL stops fast. A step that
// fails sends fatal with its reason.
func Run(ctx context.Context, options Options) error {
	channel := newControl(options.In, options.Control)
	start, err := channel.receive()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil || start.Type != messageStart {
		return fail(channel, reasonStart, errors.New("desktop: the first message is not start"))
	}
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
	providerKeys := &keys{}
	for id, value := range start.Secrets {
		providerKeys.set(id, &value)
	}

	secrets, err := loadClusterSecrets(data.Secret())
	if err != nil {
		return failLogged(channel, logger, reasonDataDir, err)
	}
	cluster := newCluster(resources.PostgresBin(), data, secrets)
	cluster.version = options.Version
	if err := cluster.Start(ctx); err != nil {
		return failLogged(channel, logger, reasonPostgres, err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), stopClusterTime)
		defer cancel()
		if err := cluster.Stop(stopCtx); err != nil {
			logger.Printf(`{"event":"desktop_postgres_stop_failed","error":%q}`, err.Error())
		}
	}()
	backup, err := cluster.Bootstrap(ctx)
	if backup != "" {
		// PR3.1, PR3.2, DT13.6: the copy is named in the log of the run; it and the newest one before it stay, whether the
		// migrations went through or failed — a migration that fails start after start makes no third copy.
		logger.Printf(`{"event":"desktop_backup_made","backup":%q}`, backup)
		if err := pruneBackups(data.Backups(), backupsKept, backup); err != nil {
			logger.Printf(`{"event":"desktop_backups_not_pruned","error":%q}`, err.Error())
		}
	}
	if err != nil {
		return failWith(channel, logger, bootstrapFailure(err), err)
	}
	authorID, err := openLocalAuthor(ctx, cluster.ProductURL())
	if err != nil {
		return failLogged(channel, logger, reasonAuthor, err)
	}
	// PR1: the window listens on the port settings.json remembers, only on 127.0.0.1.
	saved, err := loadSettings(data.Settings(), time.Now())
	if err != nil {
		return failLogged(channel, logger, reasonDataDir, err)
	}
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
	desktop := &build{host: listener.Addr().String(), authorID: authorID, codes: codes, files: webFiles(), state: appState(data, options.Version)}
	desktop.ready = func() {
		if err := channel.send(ControlMessage{Type: messageReady, Port: port, PortChanged: portChanged, Code: codes.Issue()}); err != nil {
			logger.Print(`{"event":"desktop_ready_not_sent"}`)
		}
	}
	coreCtx, stopCore := context.WithCancel(context.Background())
	defer stopCore()
	coreDone := make(chan error, 1)
	go func() {
		coreDone <- app.Run(coreCtx, desktopConfig(data, resources, cluster, listener), logger, desktop.extension(listener))
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
			case messageSecret:
				providerKeys.set(message.ID, message.Value)
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

// stopTurns cancels the chat turns of the author before the core stops (DA1.6).
func stopTurns(desktop *build, logger *log.Logger) {
	stop := desktop.turnsStopper()
	if stop == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopTurnsTimeout)
	defer cancel()
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

// failWith sends a fatal that names more than its reason: the copy of a failed migration.
func failWith(channel *control, logger *log.Logger, fatal ControlMessage, err error) error {
	logger.Printf(`{"event":"desktop_fatal","reason":%q,"backup":%q,"error":%q}`, fatal.Reason, fatal.Backup, err.Error())
	_ = channel.send(fatal)
	return err
}
