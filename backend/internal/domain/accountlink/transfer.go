// SPDX-License-Identifier: AGPL-3.0-only

package accountlink

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/sagaarchive"
)

// The transfer of a saga of the site to the app (specs/2026-11-16-desk-site-saga.md TR2, TR4, TR5): the app announces it on
// the site under its pass with the challenge of a verifier of its own, the author chooses his saga on the page /transfer
// under his own sign-in, the browser comes back to the window with a one-time code, and the app downloads the saga with
// the code and the verifier and opens it as a new saga. One transfer a run, only in memory.

const (
	// TransferCodeLifetime is how long the site keeps a code for the app (the site's applink.TransferLifetime): a busy site
	// is waited for within it, counted from the return of the browser.
	TransferCodeLifetime = 10 * time.Minute
	// TransferTerm bounds the whole download: the site prepares a saga within its save of 30 minutes (TR4.2).
	TransferTerm = 35 * time.Minute
	// TransferSilence is how long a download waits for its next byte before it is cut (TR4.2).
	TransferSilence = 2 * time.Minute
	// TransferMaxBytes is the largest saga file the app takes: the limit of a saga file.
	TransferMaxBytes = sagaarchive.MaxFileBytes
)

// TransferPhase is the state of the transfer as getSiteSagaTransfer says it (TR2.2).
type TransferPhase string

const (
	TransferNone        TransferPhase = "none"
	TransferWaiting     TransferPhase = "waiting"
	TransferDownloading TransferPhase = "downloading"
	TransferOpening     TransferPhase = "opening"
	TransferDone        TransferPhase = "done"
	TransferFailed      TransferPhase = "failed"
)

// The codes of a transfer that failed (TR2.2); a refusal of the open of the saga file fails with its own code
// saga_archive.*.
const (
	// TransferExpired: the request ran out without a return of the browser.
	TransferExpired = "expired"
	// TransferCanceled: the author stopped it, or the app erased its data.
	TransferCanceled = "canceled"
	// TransferInvalid: the site refused the code — out of its term, spent, of another app.
	TransferInvalid = "transfer_invalid"
	// TransferGone: the saga went from the site or is not the author's any more.
	TransferGone = "gone"
	// TransferBusy: the saves of the site were busy beyond the term of the code.
	TransferBusy = "busy"
	// TransferUnreachable: the site did not answer as it answers, or asked to wait beyond the term of the code.
	TransferUnreachable = "unreachable"
	// TransferNotLinked: the app is not linked any more, or the site refused its pass.
	TransferNotLinked = "not_linked"
	// TransferCut: the download broke, went silent for TransferSilence or ran out of TransferTerm.
	TransferCut = "download_cut"
	// TransferTooLarge: the file is over TransferMaxBytes.
	TransferTooLarge = "too_large"
	// TransferNoSpace: no place on the disk for the file.
	TransferNoSpace = "no_space"
	// TransferSessionEnded: the window was restarted since the transfer began, and its proof of a write is gone.
	TransferSessionEnded = "session_ended"
	// TransferUpdating: an update closed the app to new work before the download began.
	TransferUpdating = "updating"
	// TransferUnexpected: a failure of this computer the other codes do not name.
	TransferUnexpected = "unexpected"
)

var (
	// ErrTransferRunning is a start while a transfer downloads or opens (TR2.1).
	ErrTransferRunning = errors.New("accountlink: a transfer runs")
	// ErrTransferOpening is a cancel of a transfer that opens: the open is not stopped half-way (TR2.2).
	ErrTransferOpening = errors.New("accountlink: the saga already opens")
	// ErrNotLinked is a start of a transfer of an app that is not linked (TR2.1).
	ErrNotLinked = errors.New("accountlink: the app is not linked")
	// ErrUpdating is a start of a transfer while an update closes the app to new work (TR2.1).
	ErrUpdating = errors.New("accountlink: an update is installed")
	// ErrTransferInvalid is app.transfer_invalid of the site; ErrTransferGone saga_transfer.gone.
	ErrTransferInvalid = errors.New("accountlink: the site refused the code of the transfer")
	ErrTransferGone    = errors.New("accountlink: the saga of the transfer went from the site")
)

// SiteBusy is saga_transfer.busy of the site, or a Retry-After of the site the client still waits out: the download may be
// asked again after the wait.
type SiteBusy struct {
	Wait time.Duration
	// Saves says the saves of the site are busy (saga_transfer.busy); otherwise the client waits out a Retry-After.
	Saves bool
}

func (busy SiteBusy) Error() string { return "accountlink: the site asked to wait" }

// TransferAddress is the page /transfer of the site with what the app asks it: the number of its request, the state and
// the port of its return — nothing of the author (TR2.1).
func TransferAddress(site, request, state string, port int) string {
	query := url.Values{"request": {request}, "state": {state}, "port": {strconv.Itoa(port)}}
	return strings.TrimRight(site, "/") + "/transfer?" + query.Encode()
}
