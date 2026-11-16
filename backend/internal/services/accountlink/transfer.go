// SPDX-License-Identifier: AGPL-3.0-only

package accountlink

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/accountlink"
	"canonkeeper/backend/internal/services/sagaarchive"
)

// The transfer of a saga from the site (specs/2026-11-16-desk-site-saga.md TR2, TR5): the app of a link announces a transfer
// under its pass with the challenge of a verifier of its own and waits for the return of the browser, the author chooses his
// saga on the page /transfer of the site, and the return spends the waiting once and starts the download in a term of its
// own — never the request of the browser —; the saga file opens as «Открыть файл саги» opens one, under the proof of the
// write of the session of the window that started the transfer. One transfer a run, only in memory; its verifier, state and
// code go into no journal and no answer.

// TransferSite is the site as the transfer needs it: the one client of the app (adapters/siteapi).
type TransferSite interface {
	RequestSagaTransfer(ctx context.Context, challenge string) (string, error)
	TakeSagaTransfer(ctx context.Context, code, verifier string) (io.ReadCloser, string, error)
}

// Opens open a saga file as a new saga: the open of the core (specs/2026-11-13-desk-sagafile.md SF3).
type Opens interface {
	Execute(ctx context.Context, input sagaarchive.OpenInput) (sagaarchive.Opened, error)
}

// Gate admits new work until an update closes it (specs/2026-11-13-desk-updates.md UB2.1): the gate of the new turns.
type Gate interface {
	Admit() (func(), error)
}

// TransferPorts are what the transfer takes from the run: the client to the site, the open of the core and its temporary
// files and the gate of the updates. Term, Silence and MaxBytes are the domain's unless a test shortens them.
type TransferPorts struct {
	Site     TransferSite
	Opens    Opens
	Temp     sagaarchive.TempFiles
	Gate     Gate
	Term     time.Duration
	Silence  time.Duration
	MaxBytes int64
}

// Transfer is the transfer as getSiteSagaTransfer says it (TR2.2): its id and phase; the address of /transfer, its term and
// the address of the account while it waits; the name of the file and the bytes taken while it downloads and opens; the
// code of a failure; the new saga and the report of its open once done.
type Transfer struct {
	ID        string
	Phase     domain.TransferPhase
	Address   string
	Expires   time.Time
	Email     string
	Name      string
	Bytes     int64
	Code      string
	ProjectID string
	Opened    *sagaarchive.Opened
}

// transferState is the one transfer of the run: its waiting, by the rules of the waiting of a link, and its run; life is the
// life of the transfers of this run, which the stop of the app ends.
type transferState struct {
	mu    sync.Mutex
	waits *domain.Waits
	run   *transferRun
	life  context.Context
	end   context.CancelFunc
	runs  sync.WaitGroup
}

func newTransferState(now func() time.Time) *transferState {
	life, end := context.WithCancel(context.Background())
	return &transferState{waits: domain.NewWaits(now), life: life, end: end}
}

// transferRun is one transfer; the lock of its state guards every field but bytes.
type transferRun struct {
	id        string
	phase     domain.TransferPhase
	address   string
	email     string
	expires   time.Time
	actor     string
	proof     auth.SessionWriteProof
	name      string
	bytes     atomic.Int64
	code      string
	projectID string
	opened    *sagaarchive.Opened
	stop      context.CancelFunc
}

// view is the transfer at the moment; a waiting that ran out fails for its term, and the journal of the run says so once.
// The lock is held.
func (link *shared) transferView() Transfer {
	state := link.transfer
	run := state.run
	if run == nil {
		return Transfer{Phase: domain.TransferNone}
	}
	if run.phase == domain.TransferWaiting {
		if _, ok := state.waits.Current(); !ok {
			run.phase, run.code = domain.TransferFailed, domain.TransferExpired
			link.log(`{"event":"desktop_saga_transfer_failed","code":"expired"}`)
		}
	}
	transfer := Transfer{ID: run.id, Phase: run.phase, Code: run.code}
	switch run.phase {
	case domain.TransferWaiting:
		transfer.Address, transfer.Expires, transfer.Email = run.address, run.expires, run.email
	case domain.TransferDownloading, domain.TransferOpening:
		transfer.Name, transfer.Bytes = run.name, run.bytes.Load()
	case domain.TransferDone:
		transfer.Name, transfer.ProjectID, transfer.Opened = run.name, run.projectID, run.opened
	}
	return transfer
}

// fail ends a run with its code, unless it ended already — a cancel and an unlinking end it at once. The lock is held.
func (run *transferRun) fail(code string) bool {
	if run.phase == domain.TransferFailed || run.phase == domain.TransferDone {
		return false
	}
	run.phase, run.code = domain.TransferFailed, code
	if run.stop != nil {
		run.stop()
	}
	return true
}

// abort ends a transfer that waits or downloads with the code: the author unlinked the app or erased its data (TR5.3). One
// that opens goes on: the saga is on the computer already.
func (link *shared) abortTransfer(code string) {
	state := link.transfer
	state.mu.Lock()
	defer state.mu.Unlock()
	run := state.run
	if run == nil || run.phase != domain.TransferWaiting && run.phase != domain.TransferDownloading {
		return
	}
	state.waits.Cancel()
	if run.fail(code) {
		link.log(`{"event":"desktop_saga_transfer_failed","code":"` + code + `"}`)
	}
}

// TransferParts are the use cases of the transfer of this run over the one transfer of the link.
type TransferParts struct {
	Start  StartTransfer
	Read   GetTransfer
	Cancel CancelTransfer
	Finish FinishTransfer
}

// Transfers are the use cases of the transfer over the ports of the run.
func (parts Parts) Transfers(ports TransferPorts) TransferParts {
	if ports.Term == 0 {
		ports.Term = domain.TransferTerm
	}
	if ports.Silence == 0 {
		ports.Silence = domain.TransferSilence
	}
	if ports.MaxBytes == 0 {
		ports.MaxBytes = domain.TransferMaxBytes
	}
	transfers := &transfers{link: parts.link, ports: ports}
	return TransferParts{Start: StartTransfer{transfers}, Read: GetTransfer{transfers}, Cancel: CancelTransfer{transfers}, Finish: FinishTransfer{transfers}}
}

// TransfersRunning counts the transfer that downloads or opens: an update waits for it as for a turn of the chat (TR5.3).
func (parts Parts) TransfersRunning(context.Context) (int, error) {
	state := parts.link.transfer
	state.mu.Lock()
	defer state.mu.Unlock()
	if run := state.run; run != nil && (run.phase == domain.TransferDownloading || run.phase == domain.TransferOpening) {
		return 1, nil
	}
	return 0, nil
}

// Transferring says whether the transfer goes to the site now: it waits for the author or downloads (TR5.4).
func (parts Parts) Transferring() bool {
	state := parts.link.transfer
	state.mu.Lock()
	defer state.mu.Unlock()
	transfer := parts.link.transferView()
	return transfer.Phase == domain.TransferWaiting || transfer.Phase == domain.TransferDownloading
}

// StopTransfers ends the transfers of this run as the app stops, before its core (TR5.3): a download stops and its file goes,
// an open rolls its transaction back; it waits for them within ctx.
func (parts Parts) StopTransfers(ctx context.Context) {
	state := parts.link.transfer
	state.end()
	done := make(chan struct{})
	go func() {
		state.runs.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

type transfers struct {
	link  *shared
	ports TransferPorts
}

// StartTransfer is startSiteSagaTransfer (TR2.1): while nothing downloads or opens, no update closes the app and the app is
// linked, a verifier and a state of 32 random bytes, the announcement of the transfer on the site with the challenge of the
// verifier, and a waiting of 10 minutes with the proof of the write of the session of the window. A new start replaces a
// transfer that waits; a site that did not take the announcement leaves the transfer failed with its code.
type StartTransfer struct{ transfers *transfers }

func (useCase StartTransfer) Execute(ctx context.Context, actor string, proof auth.SessionWriteProof) (Transfer, error) {
	link, ports := useCase.transfers.link, useCase.transfers.ports
	state, build := link.transfer, link.build
	state.mu.Lock()
	running := state.run != nil && (state.run.phase == domain.TransferDownloading || state.run.phase == domain.TransferOpening)
	current := link.transferView()
	state.mu.Unlock()
	if running {
		return current, domain.ErrTransferRunning
	}
	release, err := ports.Gate.Admit()
	if err != nil {
		return current, domain.ErrUpdating
	}
	release()
	if !build.Site.Held() {
		return current, domain.ErrNotLinked
	}
	verifier, err := domain.NewSecret(build.Random)
	if err != nil {
		return Transfer{}, err
	}
	secret, err := domain.NewSecret(build.Random)
	if err != nil {
		return Transfer{}, err
	}
	request, err := ports.Site.RequestSagaTransfer(ctx, domain.Challenge(verifier))
	state.mu.Lock()
	defer state.mu.Unlock()
	if run := state.run; run != nil && (run.phase == domain.TransferDownloading || run.phase == domain.TransferOpening) {
		return link.transferView(), domain.ErrTransferRunning
	}
	state.waits.Cancel()
	run := &transferRun{id: auth.NewUUID(), actor: actor, proof: proof}
	state.run = run
	if err != nil {
		run.phase, run.code = domain.TransferFailed, requestCode(err)
		link.log(`{"event":"desktop_saga_transfer_failed","code":"` + run.code + `"}`)
		return link.transferView(), nil
	}
	if known := link.state.Known(); known.Account != nil {
		run.email = known.Account.Email
	}
	run.phase, run.expires = domain.TransferWaiting, build.Now().Add(domain.WaitLifetime)
	run.address = domain.TransferAddress(build.SiteURL, request, secret, build.Port)
	state.waits.Begin(domain.Waiting{Verifier: verifier, State: secret, Address: run.address, Expires: run.expires})
	link.log(`{"event":"desktop_saga_transfer_waiting"}`)
	return link.transferView(), nil
}

// requestCode is the code of an announcement the site did not take.
func requestCode(err error) string {
	if errors.Is(err, domain.ErrPassInvalid) || errors.Is(err, domain.ErrPassReplaced) || errors.Is(err, domain.ErrNotHeld) {
		return domain.TransferNotLinked
	}
	return domain.TransferUnreachable
}

// GetTransfer is getSiteSagaTransfer (TR2.2).
type GetTransfer struct{ transfers *transfers }

func (useCase GetTransfer) Execute() Transfer {
	link := useCase.transfers.link
	state := link.transfer
	state.mu.Lock()
	defer state.mu.Unlock()
	return link.transferView()
}

// CancelTransfer is cancelSiteSagaTransfer (TR2.2) of the transfer of its id: one that waits or downloads stops as canceled —
// a download removes its file —, one that opens is domain.ErrTransferOpening, and a done or failed one goes. Another id — a
// transfer put away or replaced meanwhile — changes nothing: the answer is the transfer as it is.
type CancelTransfer struct{ transfers *transfers }

func (useCase CancelTransfer) Execute(id string) (Transfer, error) {
	link := useCase.transfers.link
	state := link.transfer
	state.mu.Lock()
	defer state.mu.Unlock()
	transfer := link.transferView()
	if transfer.ID == "" || transfer.ID != id {
		return transfer, nil
	}
	switch transfer.Phase {
	case domain.TransferWaiting, domain.TransferDownloading:
		state.waits.Cancel()
		state.run.fail(domain.TransferCanceled)
		link.log(`{"event":"desktop_saga_transfer_failed","code":"canceled"}`)
	case domain.TransferOpening:
		return transfer, domain.ErrTransferOpening
	case domain.TransferDone, domain.TransferFailed:
		state.run = nil
	}
	return link.transferView(), nil
}

// FinishTransfer is the return of the browser (TR5.1, TR5.2): a code of the shape of the site and the state waiting spend
// the waiting once, and the download starts in the term of the transfer and the life of the run; the window is raised. A
// return after an update closed the app to new work fails the transfer as updating. The gate is asked before the state of
// the transfer is locked: the count of an update locks the state under the gate.
type FinishTransfer struct{ transfers *transfers }

func (useCase FinishTransfer) Execute(code, secret string) error {
	transfers := useCase.transfers
	link, ports := transfers.link, transfers.ports
	state := link.transfer
	if !domain.CodeShape(code) {
		return domain.ErrStateInvalid
	}
	release, closed := ports.Gate.Admit()
	if release != nil {
		defer release()
	}
	state.mu.Lock()
	waiting, err := state.waits.Spend(secret)
	run := state.run
	if err != nil || run == nil || run.phase != domain.TransferWaiting {
		state.mu.Unlock()
		return domain.ErrStateInvalid
	}
	if closed != nil {
		run.fail(domain.TransferUpdating)
		state.mu.Unlock()
		link.log(`{"event":"desktop_saga_transfer_failed","code":"updating"}`)
		return nil
	}
	ctx, stop := context.WithTimeout(state.life, ports.Term)
	run.phase, run.stop = domain.TransferDownloading, stop
	state.runs.Add(1)
	state.mu.Unlock()
	link.log(`{"event":"desktop_saga_transfer_downloading"}`)
	go transfers.download(ctx, run, code, waiting.Verifier, link.build.Now())
	if err := link.build.Shell.Focus(); err != nil {
		link.log(`{"event":"desktop_window_not_raised"}`)
	}
	return nil
}
