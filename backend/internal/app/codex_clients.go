package app

import (
	"context"
	"errors"
	"log"
	"path/filepath"

	codexadapter "canonkeeper/backend/internal/adapters/codex"
	"canonkeeper/backend/internal/config"
	agentdomain "canonkeeper/backend/internal/domain/agent"
	timelinedomain "canonkeeper/backend/internal/domain/agenttimeline"
	"canonkeeper/backend/internal/domain/quota"
	storedb "canonkeeper/backend/internal/store/db"
	quotastore "canonkeeper/backend/internal/store/quota"
)

var errInvalidCodexFlow = errors.New("invalid Codex flow")

// CodexClients are the app-servers of the flows, one each (specs/2026-11-02-image-workshop.md IW4.1): the research
// chat, the helper of the drawing workshop and the helper of the chapter workshop; a run, the reading of a thread, its cancellation and deletion go to
// the app-server of their flow.
type CodexClients struct {
	researchChat   *codexadapter.Client
	imageWorkshop  *codexadapter.Client
	writerWorkshop *codexadapter.Client
}

func codexClientConfig(cfg config.Config, flow agentdomain.Flow) (codexadapter.ClientConfig, error) {
	if !flow.Valid() {
		return codexadapter.ClientConfig{}, errInvalidCodexFlow
	}
	directory := string(flow)
	return codexadapter.ClientConfig{
		CodexPath: cfg.CodexPath, CodeModeHostPath: cfg.CodexCodeModeHost, CWD: cfg.CodexCWD,
		RepositoryHome:  filepath.Join(cfg.CodexRepositoryHome, directory),
		CodexHome:       filepath.Join(cfg.CodexHome, directory),
		CodexSQLiteHome: filepath.Join(cfg.CodexSQLiteHome, directory),
		PrivateHome:     filepath.Join(cfg.CodexPrivateHome, directory),
		TempDir:         filepath.Join(cfg.CodexTempDir, directory),
		ConfigOverrides: append([]string(nil), cfg.CodexConfigOverrides...),
		PassEnv:         append([]string(nil), cfg.CodexPassEnv...),
	}, nil
}

// set keeps the started app-server of a flow.
func (clients *CodexClients) set(flow agentdomain.Flow, client *codexadapter.Client) error {
	switch flow {
	case agentdomain.FlowResearchChat:
		clients.researchChat = client
	case agentdomain.FlowImageWorkshop:
		clients.imageWorkshop = client
	case agentdomain.FlowWriterWorkshop:
		clients.writerWorkshop = client
	default:
		return errInvalidCodexFlow
	}
	return nil
}

func (clients CodexClients) Client(flow agentdomain.Flow) (*codexadapter.Client, error) {
	var client *codexadapter.Client
	switch flow {
	case agentdomain.FlowResearchChat:
		client = clients.researchChat
	case agentdomain.FlowImageWorkshop:
		client = clients.imageWorkshop
	case agentdomain.FlowWriterWorkshop:
		client = clients.writerWorkshop
	}
	if client == nil {
		return nil, errInvalidCodexFlow
	}
	return client, nil
}

// started are the app-servers started so far, in the order of agent.Flows().
func (clients CodexClients) started() []*codexadapter.Client {
	var started []*codexadapter.Client
	for _, flow := range agentdomain.Flows() {
		if client, err := clients.Client(flow); err == nil {
			started = append(started, client)
		}
	}
	return started
}

func (clients CodexClients) Close() error {
	var err error
	for _, client := range clients.started() {
		err = errors.Join(err, client.Close())
	}
	return err
}

// RuntimeReady reports every app-server of the build ready to take a turn: the chat's, and the helpers' of a build
// that has them (the desktop has the chat alone, specs/2026-11-07-desktop-app.md DA1.4).
func (clients CodexClients) RuntimeReady() bool {
	started := clients.started()
	for _, client := range started {
		if !client.RuntimeReady() {
			return false
		}
	}
	return len(started) > 0 && clients.researchChat != nil
}

func (clients CodexClients) ThreadItems(ctx context.Context, flow agentdomain.Flow, rootThreadID string) ([]timelinedomain.RawItem, error) {
	client, err := clients.Client(flow)
	if err != nil {
		return nil, err
	}
	return client.ThreadItems(ctx, rootThreadID)
}

func (clients CodexClients) ThreadUpdates(ctx context.Context, flow agentdomain.Flow, rootThreadID string) (<-chan timelinedomain.Update, error) {
	client, err := clients.Client(flow)
	if err != nil {
		return nil, err
	}
	return client.ThreadUpdates(ctx, rootThreadID)
}

// StartRateLimitWatches keeps the readings of the usage window of every app-server (R9.1) for the site's operator: the
// flows share one model provider, and each app-server reports what its account sees. The desktop has no such watch
// (specs/2026-11-07-desktop-app.md DA1.8).
func StartRateLimitWatches(ctx context.Context, clients CodexClients, db *storedb.DB, logger *log.Logger) []<-chan struct{} {
	var done []<-chan struct{}
	for _, client := range clients.started() {
		done = append(done, startRateLimitWatch(ctx, client, db, logger))
	}
	return done
}

// startRateLimitWatch keeps readings of the model provider's usage window for the operator's
// usage report (specs/2026-10-28-writer-quotas-and-fair-queue.md R9.1).
func startRateLimitWatch(ctx context.Context, client *codexadapter.Client, db *storedb.DB, logger *log.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			// The readings are a report: a panic ends the watch, not the process.
			if recovered := recover(); recovered != nil {
				logger.Print(`{"event":"model_limit_watch_panicked"}`)
			}
		}()
		client.WatchRateLimits(ctx, func(ctx context.Context, sample quota.LimitSample) {
			if _, err := quotastore.RecordLimitSample(ctx, db.Pool, sample); err != nil && ctx.Err() == nil {
				logger.Print(`{"event":"model_limit_sample_failed"}`)
			}
		})
	}()
	return done
}
