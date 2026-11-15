package codex

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"sync"
	"time"

	"canonkeeper/backend/internal/domain/agent"

	sdk "github.com/openai/codex/sdk/go"
	"github.com/openai/codex/sdk/go/protocol"
)

var ErrInvalid = errors.New("invalid Codex adapter request")

type ClientConfig struct {
	Transport        Transport
	transportFactory func(context.Context) (Transport, error)
	sdkLimits        sdk.ClientLimits
	now              func() time.Time
	CodexPath        string
	CodeModeHostPath string
	CWD              string
	RepositoryHome   string
	CodexHome        string
	CodexSQLiteHome  string
	PrivateHome      string
	TempDir          string
	ConfigOverrides  []string
	PassEnv          []string
	// Environment are NAME=value pairs for the app-server alone (transportConfig.Environment).
	Environment []string
	// HomeCopies puts the profile into the home by copy, not by links: the way of the app (CopyHome); the site links it
	// (PrepareHome).
	HomeCopies bool
	// Logger receives the stderr tail of an app-server that exits unexpectedly or fails to start.
	Logger *log.Logger
}

type Client struct {
	sdk            *sdk.Client
	dispatcher     *Dispatcher
	cwd            string
	repositoryHome string
	runtimeDone    <-chan struct{}
	runtimeExit    func() error
	logger         *log.Logger
	supervisor     *clientSupervisor
	closeOnce      sync.Once
	closeErr       error
	// provider is the model provider this app-server is configured with, read once per process.
	providerMu sync.Mutex
	provider   string
}

func NewClient(ctx context.Context, config ClientConfig) (*Client, error) {
	if ctx == nil || !filepath.IsAbs(config.CWD) {
		return nil, ErrInvalid
	}
	if config.RepositoryHome != "" || config.CodexHome != "" {
		prepare := PrepareHome
		if config.HomeCopies {
			prepare = CopyHome
		}
		if err := prepare(config.CodexHome, config.RepositoryHome); err != nil {
			return nil, err
		}
	}
	if config.Transport != nil && config.transportFactory == nil {
		return newRuntimeClient(ctx, config, config.Transport)
	}
	now := config.now
	if now == nil {
		now = time.Now
	}
	supervisor := &clientSupervisor{now: now, logger: config.Logger, overrides: append([]string(nil), config.ConfigOverrides...)}
	transportFactory := config.transportFactory
	if transportFactory == nil {
		// The factory runs under the lock of the supervisor: an app-server starts with the settings it holds then
		// (specs/2026-11-13-desk-gateway.md GW2.3).
		transportFactory = func(ctx context.Context) (Transport, error) {
			return startTransport(ctx, transportConfig{
				CodexPath: config.CodexPath, CodeModeHostPath: config.CodeModeHostPath, CWD: config.CWD,
				CodexHome: config.CodexHome, CodexSQLiteHome: config.CodexSQLiteHome,
				PrivateHome: config.PrivateHome, TempDir: config.TempDir,
				ConfigOverrides: supervisor.overrides, PassEnv: config.PassEnv, Environment: config.Environment, Logger: config.Logger,
			})
		}
	}
	supervisor.factory = func(ctx context.Context) (*Client, error) {
		transport, err := transportFactory(ctx)
		if err != nil {
			return nil, err
		}
		return newRuntimeClient(ctx, config, transport)
	}
	// Сбой запуска не останавливает backend: среда поднимется при следующем ходе (R3.1).
	_, _ = supervisor.runtime(ctx)
	return &Client{supervisor: supervisor}, nil
}

func newRuntimeClient(ctx context.Context, config ClientConfig, transport Transport) (*Client, error) {
	var native *sdk.Client
	dispatcher := newDispatcher(func(ctx context.Context, threadID string) (agent.ToolCaller, error) {
		if native == nil {
			return agent.ToolCaller{}, ErrDenied
		}
		return resolveCaller(ctx, func(ctx context.Context, id string) (protocol.Thread, error) {
			response, err := native.Threads.Read(ctx, protocol.ThreadReadParams{ThreadID: id})
			return response.Thread, err
		}, threadID)
	})
	client, err := sdk.NewClient(ctx, sdk.ClientConfig{
		ClientName: "canonkeeper", ClientVersion: "1", ProtocolMode: sdk.ProtocolModeExperimental,
		Mode: sdk.ClientModeHighLevel, Compatibility: sdk.CompatibilityStrict, Transport: transport,
		Limits:   config.sdkLimits,
		Handlers: sdk.ServerHandlers{DynamicTools: sdk.DynamicToolsItemToolCallFunc(dispatcher.handle)},
	})
	if err != nil {
		if process, ok := transport.(interface{ reportStartFailure() }); ok {
			process.reportStartFailure()
		} else {
			_ = transport.Close()
		}
		return nil, err
	}
	native = client
	var runtimeDone <-chan struct{}
	if lifecycle, ok := transport.(interface{ RuntimeDone() <-chan struct{} }); ok {
		runtimeDone = lifecycle.RuntimeDone()
	}
	var runtimeExit func() error
	if lifecycle, ok := transport.(interface{ RuntimeExitError() error }); ok {
		runtimeExit = lifecycle.RuntimeExitError
	}
	return &Client{
		sdk: client, dispatcher: dispatcher, cwd: config.CWD,
		repositoryHome: config.RepositoryHome, runtimeDone: runtimeDone, runtimeExit: runtimeExit,
		logger: config.Logger,
	}, nil
}

func (client *Client) Close() error {
	if client == nil {
		return nil
	}
	if client.supervisor != nil {
		return client.supervisor.close()
	}
	client.closeOnce.Do(func() { client.closeErr = client.sdk.Close() })
	return client.closeErr
}

func (client *Client) runtime(ctx context.Context) (*Client, error) {
	if client == nil || ctx == nil {
		return nil, ErrInvalid
	}
	if client.supervisor == nil {
		return client, nil
	}
	return client.supervisor.runtime(ctx)
}

func runtimeStopped(done <-chan struct{}) bool {
	if done == nil {
		return false
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// Configure gives the app-server new settings (specs/2026-11-13-desk-gateway.md GW2.3): when no turn runs a new app-server
// starts with them at once and Configure says so; while a turn runs they wait, Pending says so, and the app-server is
// replaced when the last turn ends. The threads stay in the homes and go on with the new app-server.
func (client *Client) Configure(ctx context.Context, overrides []string) (bool, error) {
	if client == nil || client.supervisor == nil || ctx == nil || !agent.ValidConfigOverrides(overrides) {
		return false, ErrInvalid
	}
	return client.supervisor.configure(ctx, overrides)
}

// Pending says that new settings wait for the end of the turns that run.
func (client *Client) Pending() bool {
	return client != nil && client.supervisor != nil && client.supervisor.waiting()
}

// RuntimeReady сообщает, работает ли процесс app-server, не пытаясь его запустить.
func (client *Client) RuntimeReady() bool {
	if client == nil {
		return false
	}
	if client.supervisor == nil {
		return !runtimeStopped(client.runtimeDone)
	}
	return client.supervisor.ready()
}

func (client *Client) runtimeExitError() error {
	if client == nil || !runtimeStopped(client.runtimeDone) {
		return nil
	}
	if client.runtimeExit != nil {
		return client.runtimeExit()
	}
	return agent.ErrRuntimeProcessExited
}
