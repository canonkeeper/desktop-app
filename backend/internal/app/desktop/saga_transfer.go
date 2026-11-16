// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"

	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/auth"
	httpdesktop "canonkeeper/backend/internal/delivery/http/desktop"
	httpsagaarchive "canonkeeper/backend/internal/delivery/http/sagaarchive"
	accountservice "canonkeeper/backend/internal/services/accountlink"
	aiservice "canonkeeper/backend/internal/services/aisettings"

	"github.com/go-chi/chi/v5"
)

// A saga comes into the app by «Открыть файл саги» (specs/2026-11-13-desk-sagafile.md SF3.12) and by the transfer of a saga
// from the site (specs/2026-11-16-desk-site-saga.md TR2, TR5), which opens its file the same way. The transfer lives with the
// run: an update waits for it as for a turn of the chat, and the stop of the modules ends it before the database of the core
// closes; its return is a route of the root (serve).

// useTransfers builds the transfer over the core once compose has it — site is the one client of the app to the site — and
// makes an update wait for the turns of the chat and for a transfer that downloads or opens (TR5.3).
func (desktop *build) useTransfers(core app.Core, site accountservice.TransferSite, turns func(context.Context) (int, error)) {
	parts := desktop.account.parts
	desktop.account.transfers = parts.Transfers(accountservice.TransferPorts{
		Site: site, Opens: core.SagaArchiveOpen, Temp: app.SagaArchiveTemp(core), Gate: desktop.updates.gate,
	})
	desktop.updates.useTurns(workInProgress{turns: turns, transfers: parts.TransfersRunning}.count)
}

// mountSagaFiles mounts the operations by which a saga comes into the app: only the app opens a saga file, and only the app
// transfers one from the site.
func (desktop *build) mountSagaFiles(router chi.Router, core app.Core, sessions *auth.Service) {
	httpsagaarchive.MountOpen(router, core.SagaArchiveOpen, app.SagaArchiveTemp(core), sessions, core.Config.AllowedOrigins)
	transfers := desktop.account.transfers
	httpdesktop.SagaTransfer{
		Sessions: sessions, Start: transfers.Start, Read: transfers.Read, Cancel: transfers.Cancel, AllowedOrigins: core.Config.AllowedOrigins,
	}.Mount(router)
}

// stopModules is what the stop of the core ends of the modules of the build (app.Modules.Start), before the gateway and the
// database close: a check of the connection that runs is canceled and keeps nothing (specs/2026-11-13-desk-probes.md PB2.1),
// and a transfer of a saga stops — its download stops and its file goes, its open rolls back (TR5.3).
func (desktop *build) stopModules(checks *aiservice.Checks) func(context.Context) error {
	return func(ctx context.Context) error {
		checks.Stop()
		desktop.account.parts.StopTransfers(ctx)
		return nil
	}
}

// workInProgress is what an update waits for (specs/2026-11-13-desk-updates.md UB2.1): the turns of the chat in work and a
// transfer of a saga that downloads or opens (TR5.3).
type workInProgress struct {
	turns     func(context.Context) (int, error)
	transfers func(context.Context) (int, error)
}

func (work workInProgress) count(ctx context.Context) (int, error) {
	turns, err := work.turns(ctx)
	if err != nil {
		return 0, err
	}
	transfers, err := work.transfers(ctx)
	return turns + transfers, err
}
