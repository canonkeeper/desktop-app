// SPDX-License-Identifier: AGPL-3.0-only

package accountlink

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"syscall"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/accountlink"
	archive "canonkeeper/backend/internal/domain/sagaarchive"
	"canonkeeper/backend/internal/services/sagaarchive"
)

// The download and the open of a transfer (specs/2026-11-16-desk-site-saga.md TR4.2, TR5.2, TR5.3): once the return spent the
// waiting, the saga of the code comes from the site into a temporary file and opens as «Открыть файл саги» opens one; every
// failure ends the transfer with its code.

// download takes the saga of the code and opens it (TR4.2, TR5.2): a busy site is waited for while the code lives, and a site
// that did not begin its answer is asked once more with the same code and verifier — the site spends the code just before it
// answers, so a break before is no spent code; the file goes to a temporary file with its SHA-256, no larger than the limit of
// a saga file and no silence longer than its term; then the open of the core under the proof of the start, its key of a
// repeat the id of the transfer.
func (transfers *transfers) download(ctx context.Context, run *transferRun, code, verifier string, from time.Time) {
	link, ports := transfers.link, transfers.ports
	state := link.transfer
	defer state.runs.Done()
	defer run.stop()
	codeEnds := from.Add(domain.TransferCodeLifetime)
	var body io.ReadCloser
	var name string
	retried := false
	for {
		var err error
		body, name, err = ports.Site.TakeSagaTransfer(ctx, code, verifier)
		var busy domain.SiteBusy
		if errors.As(err, &busy) {
			if link.build.Now().Add(busy.Wait).After(codeEnds) {
				// The busy saves of the site past the term of the code are «busy»; a Retry-After of the site past it, no answer.
				code := domain.TransferUnreachable
				if busy.Saves {
					code = domain.TransferBusy
				}
				transfers.failed(run, code)
				return
			}
			select {
			case <-time.After(busy.Wait):
				continue
			case <-ctx.Done():
				transfers.failed(run, transfers.stopped())
				return
			}
		}
		if errors.Is(err, domain.ErrUnreachable) && !retried && ctx.Err() == nil {
			retried = true
			link.log(`{"event":"desktop_saga_transfer_retried"}`)
			continue
		}
		if err != nil {
			transfers.failed(run, transfers.takeCode(ctx, err))
			return
		}
		break
	}
	state.mu.Lock()
	run.name = name
	state.mu.Unlock()
	file, sum, code, ok := transfers.receive(run, body)
	_ = body.Close()
	if !ok {
		transfers.failed(run, code)
		return
	}
	state.mu.Lock()
	if run.phase != domain.TransferDownloading {
		state.mu.Unlock()
		_ = file.Remove()
		return
	}
	run.phase = domain.TransferOpening
	state.mu.Unlock()
	link.log(`{"event":"desktop_saga_transfer_opening"}`)
	opened, err := ports.Opens.Execute(state.life, sagaarchive.OpenInput{
		OpenRequest: sagaarchive.OpenRequest{ActorUserID: run.actor, IdempotencyKey: run.id, FileSHA256: sum},
		Origin:      sagaarchive.FromSite, Proof: run.proof, File: file,
	})
	if err != nil {
		transfers.failed(run, openCode(state.life, err))
		return
	}
	state.mu.Lock()
	run.phase, run.projectID, run.opened = domain.TransferDone, opened.ProjectID, &opened
	state.mu.Unlock()
	link.log(`{"event":"desktop_saga_transfer_opened"}`)
}

// receive writes the stream to a temporary file with its SHA-256 and counts its bytes; a silence of the term cuts it.
func (transfers *transfers) receive(run *transferRun, body io.Reader) (sagaarchive.TempFile, string, string, bool) {
	ports := transfers.ports
	file, err := ports.Temp.Create()
	if err != nil {
		return nil, "", fileCode(err), false
	}
	// A silence of its term cuts the download: the stop of the run ends the read of the stream.
	timer := time.AfterFunc(ports.Silence, run.stop)
	defer timer.Stop()
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	for {
		read, err := body.Read(buffer)
		if read > 0 {
			timer.Reset(ports.Silence)
			if run.bytes.Add(int64(read)) > ports.MaxBytes {
				_ = file.Remove()
				return nil, "", domain.TransferTooLarge, false
			}
			hash.Write(buffer[:read])
			if _, err := file.Write(buffer[:read]); err != nil {
				_ = file.Remove()
				return nil, "", fileCode(err), false
			}
		}
		if errors.Is(err, io.EOF) {
			return file, hex.EncodeToString(hash.Sum(nil)), "", true
		}
		if err != nil {
			_ = file.Remove()
			return nil, "", transfers.stopped(), false
		}
	}
}

// failed ends a run with the code, once.
func (transfers *transfers) failed(run *transferRun, code string) {
	state := transfers.link.transfer
	state.mu.Lock()
	ended := run.fail(code)
	state.mu.Unlock()
	if ended {
		transfers.link.log(`{"event":"desktop_saga_transfer_failed","code":"` + code + `"}`)
	}
}

// stopped is why a download stopped: the app stopped, or it broke, went silent or ran out of its term. A cancel and an
// unlinking ended the run already with their own code.
func (transfers *transfers) stopped() string {
	if transfers.link.transfer.life.Err() != nil {
		return domain.TransferCanceled
	}
	return domain.TransferCut
}

// takeCode is the code of a download the site did not answer with a saga.
func (transfers *transfers) takeCode(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, domain.ErrTransferInvalid):
		return domain.TransferInvalid
	case errors.Is(err, domain.ErrTransferGone):
		return domain.TransferGone
	case errors.Is(err, domain.ErrPassInvalid), errors.Is(err, domain.ErrPassReplaced), errors.Is(err, domain.ErrNotHeld):
		return domain.TransferNotLinked
	case ctx.Err() != nil:
		return transfers.stopped()
	}
	return domain.TransferUnreachable
}

// fileCode is the code of a temporary file that could not be written.
func fileCode(err error) string {
	if errors.Is(err, syscall.ENOSPC) {
		return domain.TransferNoSpace
	}
	return domain.TransferUnexpected
}

// openCode is the code of an open of the saga file that failed: the window restarted since the start, the code of a refusal
// of the file, the stop of the app.
func openCode(life context.Context, err error) string {
	var refusal *archive.Refusal
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		return domain.TransferSessionEnded
	case errors.As(err, &refusal):
		return refusal.Code
	case life.Err() != nil:
		return domain.TransferCanceled
	}
	return domain.TransferUnexpected
}
