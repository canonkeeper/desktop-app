// SPDX-License-Identifier: AGPL-3.0-only

package accountlink

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/accountlink"
	archive "canonkeeper/backend/internal/domain/sagaarchive"
	"canonkeeper/backend/internal/services/sagaarchive"
)

const codeOfTheTransfer = "code-of-the-transfer-north-0000000000000000"

// answer is how the site of a test answers a download.
type answer func(ctx context.Context) (io.ReadCloser, string, error)

// transferSite is the site of a transfer: the challenges it was announced with and the answers of its downloads.
type transferSite struct {
	mu         sync.Mutex
	challenges []string
	request    error
	takes      []struct{ code, verifier string }
	answers    []answer
}

func (site *transferSite) RequestSagaTransfer(_ context.Context, challenge string) (string, error) {
	site.mu.Lock()
	defer site.mu.Unlock()
	site.challenges = append(site.challenges, challenge)
	return "request-north", site.request
}

func (site *transferSite) TakeSagaTransfer(ctx context.Context, code, verifier string) (io.ReadCloser, string, error) {
	site.mu.Lock()
	site.takes = append(site.takes, struct{ code, verifier string }{code, verifier})
	answer := site.answers[0]
	if len(site.answers) > 1 {
		site.answers = site.answers[1:]
	}
	site.mu.Unlock()
	return answer(ctx)
}

func (site *transferSite) taken() int {
	site.mu.Lock()
	defer site.mu.Unlock()
	return len(site.takes)
}

func saga(body string) answer {
	return func(context.Context) (io.ReadCloser, string, error) {
		return io.NopCloser(strings.NewReader(body)), "Северный предел.cksaga", nil
	}
}

// stream is a body the test writes, which the end of the request ends as the body of an answer of HTTP.
func stream() (answer, *io.PipeWriter) {
	reader, writer := io.Pipe()
	return func(ctx context.Context) (io.ReadCloser, string, error) {
		go func() {
			<-ctx.Done()
			_ = writer.CloseWithError(ctx.Err())
		}()
		return reader, "", nil
	}, writer
}

func refused(err error) answer {
	return func(context.Context) (io.ReadCloser, string, error) { return nil, "", err }
}

// opens is the open of the core of a test: what it was given and what it answers.
type opens struct {
	mu     sync.Mutex
	inputs []sagaarchive.OpenInput
	files  []string
	err    error
	ctx    context.Context
}

func (opens *opens) Execute(ctx context.Context, input sagaarchive.OpenInput) (sagaarchive.Opened, error) {
	defer func() { _ = input.File.Remove() }()
	size, _ := input.File.Size()
	content := make([]byte, size)
	_, _ = input.File.ReadAt(content, 0)
	opens.mu.Lock()
	defer opens.mu.Unlock()
	opens.inputs, opens.files, opens.ctx = append(opens.inputs, input), append(opens.files, string(content)), ctx
	if opens.err != nil {
		return sagaarchive.Opened{}, opens.err
	}
	return sagaarchive.Opened{ProjectID: "saga-of-the-app", Name: "Северный предел", Report: sagaarchive.Report{Books: 1}}, nil
}

type temp struct {
	mu    sync.Mutex
	files []*tempFile
}

func (temp *temp) Create() (sagaarchive.TempFile, error) {
	temp.mu.Lock()
	defer temp.mu.Unlock()
	file := &tempFile{}
	temp.files = append(temp.files, file)
	return file, nil
}

// received waits for the file of the transfer to take n bytes: the download counts the bytes of a part before it writes
// them, so the count says them once the file has them.
func (temp *temp) received(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		temp.mu.Lock()
		taken := 0
		for _, file := range temp.files {
			size, _ := file.Size()
			taken += int(size)
		}
		temp.mu.Unlock()
		if taken >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the file of the transfer took %d bytes, not %d", taken, n)
		}
		time.Sleep(time.Millisecond)
	}
}

func (temp *temp) removed() bool {
	temp.mu.Lock()
	defer temp.mu.Unlock()
	for _, file := range temp.files {
		if !file.gone() {
			return false
		}
	}
	return true
}

type tempFile struct {
	mu      sync.Mutex
	content bytes.Buffer
	removed bool
}

func (file *tempFile) Write(p []byte) (int, error) {
	file.mu.Lock()
	defer file.mu.Unlock()
	return file.content.Write(p)
}

func (file *tempFile) ReadAt(p []byte, off int64) (int, error) {
	file.mu.Lock()
	defer file.mu.Unlock()
	return bytes.NewReader(file.content.Bytes()).ReadAt(p, off)
}

func (file *tempFile) Size() (int64, error) {
	file.mu.Lock()
	defer file.mu.Unlock()
	return int64(file.content.Len()), nil
}

func (file *tempFile) Remove() error {
	file.mu.Lock()
	defer file.mu.Unlock()
	file.removed = true
	return nil
}

func (file *tempFile) gone() bool {
	file.mu.Lock()
	defer file.mu.Unlock()
	return file.removed
}

// gate is the gate of the updates of a test.
type gate struct{ closed bool }

func (gate *gate) Admit() (func(), error) {
	if gate.closed {
		return nil, errors.New("an update is installed")
	}
	return func() {}, nil
}

// transferStand is the transfer of a test over a linked stand.
type transferStand struct {
	*stand
	transferSite *transferSite
	opens        *opens
	temp         *temp
	gate         *gate
	transfers    TransferParts
	proof        auth.SessionWriteProof
}

func newTransferStand(t *testing.T) *transferStand {
	t.Helper()
	stand := newStand()
	stand.site.Hold(passOfTheTest)
	transfer := &transferStand{
		stand: stand, transferSite: &transferSite{answers: []answer{saga("PK-the-saga")}},
		opens: &opens{}, temp: &temp{}, gate: &gate{}, proof: auth.SessionWriteProof{SessionHash: "session", CSRFSecretHash: "csrf"},
	}
	transfer.transfers = stand.link.Transfers(TransferPorts{
		Site: transfer.transferSite, Opens: transfer.opens, Temp: transfer.temp, Gate: transfer.gate,
		Silence: 200 * time.Millisecond,
	})
	t.Cleanup(func() { transfer.link.StopTransfers(context.Background()) })
	return transfer
}

func (stand *transferStand) start(t *testing.T) (Transfer, url.Values) {
	t.Helper()
	transfer, err := stand.transfers.Start.Execute(context.Background(), "author", stand.proof)
	if err != nil || transfer.Phase != domain.TransferWaiting || transfer.ID == "" || !transfer.Expires.Equal(stand.now.Add(domain.WaitLifetime)) {
		t.Fatalf("Start = %+v, %v", transfer, err)
	}
	address, err := url.Parse(transfer.Address)
	if err != nil || !strings.HasPrefix(transfer.Address, "https://site.example.test/transfer?") {
		t.Fatalf("the address = %q", transfer.Address)
	}
	return transfer, address.Query()
}

// until waits for the transfer to reach a phase.
func (stand *transferStand) until(t *testing.T, phase domain.TransferPhase) Transfer {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		transfer := stand.transfers.Read.Execute()
		if transfer.Phase == phase {
			return transfer
		}
		if time.Now().After(deadline) {
			t.Fatalf("the transfer stays %+v, not %s", transfer, phase)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TR2.1, TR5.1, TR5.2: a transfer announces the challenge of its verifier on the site and waits with the address of
// /transfer — the request, the state and the port, nothing of the author —; a return with another state is refused and
// spends nothing, the right one spends the waiting once, raises the window and downloads with the verifier of the challenge;
// the file opens under the proof of the start with the id of the transfer as its key, and the transfer is done with the
// new saga. The verifier never leaves but to the download.
func TestATransferWaitsDownloadsAndOpensTheSaga(t *testing.T) {
	stand := newTransferStand(t)
	stand.link.link.state.Linked(domain.Account{Name: "Северный", Email: "a…@example.test"}, stand.now)
	transfer, query := stand.start(t)
	if query.Get("request") != "request-north" || query.Get("port") != "24567" || len(query.Get("state")) != 43 || len(query) != 3 ||
		transfer.Email != "a…@example.test" {
		t.Fatalf("the waiting = %+v %v", transfer, query)
	}
	if !stand.link.Transferring() {
		t.Fatal("a waiting transfer does not reach the site")
	}
	if err := stand.transfers.Finish.Execute(codeOfTheTransfer, "another-state"); !errors.Is(err, domain.ErrStateInvalid) {
		t.Fatalf("another state = %v", err)
	}
	if err := stand.transfers.Finish.Execute("short", query.Get("state")); !errors.Is(err, domain.ErrStateInvalid) {
		t.Fatalf("a code of another shape = %v", err)
	}
	if err := stand.transfers.Finish.Execute(codeOfTheTransfer, query.Get("state")); err != nil {
		t.Fatal(err)
	}
	if err := stand.transfers.Finish.Execute(codeOfTheTransfer, query.Get("state")); !errors.Is(err, domain.ErrStateInvalid) {
		t.Fatalf("a second return = %v", err)
	}
	done := stand.until(t, domain.TransferDone)
	if done.ID != transfer.ID || done.ProjectID != "saga-of-the-app" || done.Opened == nil || done.Opened.Report.Books != 1 || done.Name != "Северный предел.cksaga" {
		t.Fatalf("done = %+v", done)
	}
	if stand.shell.focused != 1 || stand.link.Transferring() {
		t.Fatalf("focused %d, reaches %v", stand.shell.focused, stand.link.Transferring())
	}
	site := stand.transferSite
	if len(site.challenges) != 1 || len(site.takes) != 1 || site.takes[0].code != codeOfTheTransfer || domain.Challenge(site.takes[0].verifier) != site.challenges[0] {
		t.Fatalf("the site was asked %+v %+v", site.challenges, site.takes)
	}
	input := stand.opens.inputs[0]
	sum := sha256.Sum256([]byte("PK-the-saga"))
	if input.ActorUserID != "author" || input.IdempotencyKey != transfer.ID || input.Proof != stand.proof || stand.opens.files[0] != "PK-the-saga" ||
		input.FileSHA256 != hex.EncodeToString(sum[:]) || input.Origin != sagaarchive.FromSite {
		t.Fatalf("the open = %+v %q", input, stand.opens.files)
	}
	if running, _ := stand.link.TransfersRunning(context.Background()); running != 0 || !stand.temp.removed() {
		t.Fatalf("running %d, the file removed %v", running, stand.temp.removed())
	}
	// The outcome goes once the window put it away.
	if gone, err := stand.transfers.Cancel.Execute(transfer.ID); err != nil || gone.Phase != domain.TransferNone {
		t.Fatalf("Cancel after done = %+v %v", gone, err)
	}
}

// TR2.1, TR2.2: no start without a link, while an update is installed or while a transfer downloads; a start the site did
// not take fails with its code; a waiting that runs out fails as expired; a new start replaces a waiting one.
func TestATransferStartsOnlyWhenItMay(t *testing.T) {
	stand := newTransferStand(t)
	stand.gate.closed = true
	if _, err := stand.transfers.Start.Execute(context.Background(), "author", stand.proof); !errors.Is(err, domain.ErrUpdating) {
		t.Fatalf("while an update is installed = %v", err)
	}
	stand.gate.closed = false
	stand.site.Unlink(context.Background())
	if _, err := stand.transfers.Start.Execute(context.Background(), "author", stand.proof); !errors.Is(err, domain.ErrNotLinked) {
		t.Fatalf("without a link = %v", err)
	}
	stand.site.Hold(passOfTheTest)
	if len(stand.transferSite.challenges) != 0 {
		t.Fatal("a refused start asked the site")
	}
	stand.transferSite.request = domain.ErrPassInvalid
	if failed, err := stand.transfers.Start.Execute(context.Background(), "author", stand.proof); err != nil || failed.Phase != domain.TransferFailed || failed.Code != domain.TransferNotLinked {
		t.Fatalf("a refused pass = %+v %v", failed, err)
	}
	stand.transferSite.request = domain.ErrUnreachable
	if failed, _ := stand.transfers.Start.Execute(context.Background(), "author", stand.proof); failed.Code != domain.TransferUnreachable {
		t.Fatalf("no answer = %+v", failed)
	}
	stand.transferSite.request = nil
	first, firstQuery := stand.start(t)
	second, _ := stand.start(t)
	if first.ID == second.ID {
		t.Fatal("a new start keeps the transfer")
	}
	if err := stand.transfers.Finish.Execute(codeOfTheTransfer, firstQuery.Get("state")); !errors.Is(err, domain.ErrStateInvalid) {
		t.Fatalf("the return of a replaced waiting = %v", err)
	}
	stand.now = stand.now.Add(domain.WaitLifetime)
	if expired := stand.transfers.Read.Execute(); expired.Phase != domain.TransferFailed || expired.Code != domain.TransferExpired || expired.ID != second.ID {
		t.Fatalf("a waiting that ran out = %+v", expired)
	}
	stand.now = stand.now.Add(-domain.WaitLifetime)

	_, query := stand.start(t)
	block := make(chan struct{})
	stand.transferSite.answers = []answer{func(context.Context) (io.ReadCloser, string, error) {
		<-block
		return io.NopCloser(strings.NewReader("PK")), "", nil
	}}
	if err := stand.transfers.Finish.Execute(codeOfTheTransfer, query.Get("state")); err != nil {
		t.Fatal(err)
	}
	if running, _ := stand.link.TransfersRunning(context.Background()); running != 1 {
		t.Fatalf("a download counts %d for the updates", running)
	}
	if _, err := stand.transfers.Start.Execute(context.Background(), "author", stand.proof); !errors.Is(err, domain.ErrTransferRunning) {
		t.Fatalf("a start while one downloads = %v", err)
	}
	close(block)
	stand.until(t, domain.TransferDone)
}

// TR2.2, TR5.3: a cancel stops a waiting and a download as canceled and removes the file; a transfer that opens refuses the
// cancel; «Отвязать» ends a waiting and a download as not_linked; a return after an update closed the app fails as updating.
func TestATransferStopsAsTheAuthorAndTheAppSay(t *testing.T) {
	stand := newTransferStand(t)
	waiting, _ := stand.start(t)
	if canceled, err := stand.transfers.Cancel.Execute(waiting.ID); err != nil || canceled.Phase != domain.TransferFailed || canceled.Code != domain.TransferCanceled {
		t.Fatalf("a canceled waiting = %+v %v", canceled, err)
	}

	stand.downloading(t)
	if canceled, err := stand.transfers.Cancel.Execute(stand.transfers.Read.Execute().ID); err != nil || canceled.Code != domain.TransferCanceled {
		t.Fatalf("a canceled download = %+v %v", canceled, err)
	}
	stand.removed(t)
	if len(stand.opens.inputs) != 0 {
		t.Fatalf("a canceled download opened %d", len(stand.opens.inputs))
	}

	_, query := stand.start(t)
	stand.link.Unlink.Execute(context.Background())
	if unlinked := stand.transfers.Read.Execute(); unlinked.Code != domain.TransferNotLinked {
		t.Fatalf("an unlinked waiting = %+v", unlinked)
	}
	if err := stand.transfers.Finish.Execute(codeOfTheTransfer, query.Get("state")); !errors.Is(err, domain.ErrStateInvalid) {
		t.Fatalf("the return of an unlinked waiting = %v", err)
	}

	stand.site.Hold(passOfTheTest)
	_, query = stand.start(t)
	stand.gate.closed = true
	if err := stand.transfers.Finish.Execute(codeOfTheTransfer, query.Get("state")); err != nil {
		t.Fatal(err)
	}
	if updating := stand.transfers.Read.Execute(); updating.Code != domain.TransferUpdating || stand.transferSite.taken() != 1 {
		t.Fatalf("a return while an update is installed = %+v, %d downloads", updating, stand.transferSite.taken())
	}
	stand.gate.closed = false

	opening, query := stand.start(t)
	stand.opens.err = errors.New("unexpected")
	stand.transferSite.answers = []answer{saga("PK")}
	stand.opens.mu.Lock()
	if err := stand.transfers.Finish.Execute(codeOfTheTransfer, query.Get("state")); err != nil {
		t.Fatal(err)
	}
	stand.until(t, domain.TransferOpening)
	if _, err := stand.transfers.Cancel.Execute(opening.ID); !errors.Is(err, domain.ErrTransferOpening) {
		t.Fatalf("a cancel while the saga opens = %v", err)
	}
	stand.opens.mu.Unlock()
	if failed := stand.until(t, domain.TransferFailed); failed.Code != domain.TransferUnexpected {
		t.Fatalf("an open that failed = %+v", failed)
	}
}

// TR2.2: a cancel names its transfer. The page puts away the outcome it said and stops the transfer it shows; a request that
// comes after another start — the outcome of the last transfer put away late, a stop of a window of a replaced waiting —
// changes nothing and answers the transfer as it is.
func TestACancelStopsOnlyItsOwnTransfer(t *testing.T) {
	stand := newTransferStand(t)
	first, _ := stand.start(t)
	if same, err := stand.transfers.Cancel.Execute("00000000-0000-4000-8000-000000000999"); err != nil || same.ID != first.ID || same.Phase != domain.TransferWaiting {
		t.Fatalf("a cancel of another transfer = %+v %v", same, err)
	}
	if canceled, err := stand.transfers.Cancel.Execute(first.ID); err != nil || canceled.Phase != domain.TransferFailed {
		t.Fatalf("a cancel of the waiting = %+v %v", canceled, err)
	}
	second, _ := stand.start(t)
	if same, err := stand.transfers.Cancel.Execute(first.ID); err != nil || same.ID != second.ID || same.Phase != domain.TransferWaiting {
		t.Fatalf("the outcome of the first put away after the second started = %+v %v", same, err)
	}
	if read := stand.transfers.Read.Execute(); read.ID != second.ID || read.Phase != domain.TransferWaiting {
		t.Fatalf("the second after the late cancel = %+v", read)
	}
	if canceled, err := stand.transfers.Cancel.Execute(second.ID); err != nil || canceled.Phase != domain.TransferFailed {
		t.Fatalf("a cancel of the second = %+v %v", canceled, err)
	}
	if gone, err := stand.transfers.Cancel.Execute(second.ID); err != nil || gone.Phase != domain.TransferNone {
		t.Fatalf("its outcome put away = %+v %v", gone, err)
	}
	if none, err := stand.transfers.Cancel.Execute(second.ID); err != nil || none.Phase != domain.TransferNone {
		t.Fatalf("a cancel once it is put away = %+v %v", none, err)
	}
}

// TR4.1, TR4.2, TR5.2: the busy saves of the site are waited for while the code lives and then fail as busy; a site that did
// not begin its answer is asked once more with the same code and verifier, and the second answer ends the transfer — a busy
// wait between them is not that retry —, while a stream that broke after the site began to answer is not asked again; the
// refusals of the site fail with their codes; a silence cuts the download; a file over the limit fails as too_large; an open the window
// restarted since fails as session_ended, and a refusal of the file with its own code.
func TestATransferFailsWithItsCode(t *testing.T) {
	silence, _ := stream()
	cases := []struct {
		name    string
		answers []answer
		open    error
		max     int64
		code    string
		takes   int
	}{
		{name: "busy past the code", answers: []answer{refused(domain.SiteBusy{Wait: time.Hour, Saves: true})}, code: domain.TransferBusy, takes: 1},
		{name: "a wait of the client past the code", answers: []answer{refused(domain.SiteBusy{Wait: time.Hour})}, code: domain.TransferUnreachable, takes: 1},
		{name: "busy, then the saga", answers: []answer{refused(domain.SiteBusy{Wait: 10 * time.Millisecond, Saves: true}), saga("PK")}, code: "", takes: 2},
		{name: "the code refused", answers: []answer{refused(domain.ErrTransferInvalid)}, code: domain.TransferInvalid, takes: 1},
		{name: "the saga went", answers: []answer{refused(domain.ErrTransferGone)}, code: domain.TransferGone, takes: 1},
		{name: "the pass refused", answers: []answer{refused(domain.ErrPassInvalid)}, code: domain.TransferNotLinked, takes: 1},
		{name: "no answer twice", answers: []answer{refused(domain.ErrUnreachable), refused(domain.ErrUnreachable)}, code: domain.TransferUnreachable, takes: 2},
		{name: "no answer, then the saga", answers: []answer{refused(domain.ErrUnreachable), saga("PK")}, code: "", takes: 2},
		{name: "no answer, then the code refused", answers: []answer{refused(domain.ErrUnreachable), refused(domain.ErrTransferInvalid)}, code: domain.TransferInvalid, takes: 2},
		{name: "busy, no answer, then the saga", answers: []answer{refused(domain.SiteBusy{Wait: 10 * time.Millisecond, Saves: true}), refused(domain.ErrUnreachable), saga("PK")}, code: "", takes: 3},
		{name: "no answer, busy, no answer", answers: []answer{refused(domain.ErrUnreachable), refused(domain.SiteBusy{Wait: 10 * time.Millisecond, Saves: true}), refused(domain.ErrUnreachable)}, code: domain.TransferUnreachable, takes: 3},
		{name: "a cut after a part", answers: []answer{cut("PK-part"), saga("PK")}, code: domain.TransferCut, takes: 1},
		{name: "silence", answers: []answer{silence}, code: domain.TransferCut, takes: 1},
		{name: "too large", answers: []answer{saga("PK-over-the-limit")}, max: 4, code: domain.TransferTooLarge, takes: 1},
		{name: "the window restarted", answers: []answer{saga("PK")}, open: auth.ErrUnauthenticated, code: domain.TransferSessionEnded, takes: 1},
		{name: "a file refused", answers: []answer{saga("PK")}, open: &archive.Refusal{Code: archive.CodeDamaged}, code: archive.CodeDamaged, takes: 1},
	}
	for _, tc := range cases {
		stand := newTransferStand(t)
		if tc.max > 0 {
			stand.transfers = stand.link.Transfers(TransferPorts{
				Site: stand.transferSite, Opens: stand.opens, Temp: stand.temp, Gate: stand.gate, Silence: 200 * time.Millisecond, MaxBytes: tc.max,
			})
		}
		stand.transferSite.answers, stand.opens.err = tc.answers, tc.open
		_, query := stand.start(t)
		if err := stand.transfers.Finish.Execute(codeOfTheTransfer, query.Get("state")); err != nil {
			t.Fatal(err)
		}
		want := domain.TransferFailed
		if tc.code == "" {
			want = domain.TransferDone
		}
		if ended := stand.until(t, want); ended.Code != tc.code || stand.transferSite.taken() != tc.takes {
			t.Fatalf("%s: %+v after %d downloads", tc.name, ended, stand.transferSite.taken())
		}
		if !stand.temp.removed() {
			t.Fatalf("%s: the file stays", tc.name)
		}
		for _, take := range stand.transferSite.takes {
			if take != stand.transferSite.takes[0] {
				t.Fatalf("%s: a retry asked another code or verifier", tc.name)
			}
		}
	}
}

// cut is a download whose stream breaks after the part: the site began to answer, so the code is spent.
func cut(part string) answer {
	return func(context.Context) (io.ReadCloser, string, error) {
		return io.NopCloser(io.MultiReader(strings.NewReader(part), errorReader{io.ErrUnexpectedEOF})), "", nil
	}
}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

// stalledOpens is an open of the core that goes on until its context ends, as a transaction the end rolls back; release lets
// it go when a test fails before.
type stalledOpens struct {
	started chan struct{}
	release chan struct{}
	ended   chan error
}

func (opens *stalledOpens) Execute(ctx context.Context, input sagaarchive.OpenInput) (sagaarchive.Opened, error) {
	defer func() { _ = input.File.Remove() }()
	close(opens.started)
	select {
	case <-ctx.Done():
	case <-opens.release:
	}
	opens.ended <- ctx.Err()
	return sagaarchive.Opened{}, errors.Join(errors.New("the open rolled back"), ctx.Err())
}

// downloading brings a transfer of the stand to its download: the stream the site answers with, one part written.
func (stand *transferStand) downloading(t *testing.T) *io.PipeWriter {
	t.Helper()
	_, query := stand.start(t)
	body, writer := stream()
	t.Cleanup(func() { _ = writer.CloseWithError(io.ErrClosedPipe) })
	stand.transferSite.answers = []answer{body}
	if err := stand.transfers.Finish.Execute(codeOfTheTransfer, query.Get("state")); err != nil {
		t.Fatal(err)
	}
	for stand.transferSite.taken() == 0 {
		time.Sleep(time.Millisecond)
	}
	if _, err := writer.Write([]byte("PK-part")); err != nil {
		t.Fatal(err)
	}
	// The part is taken when the file has it, not when the phase is downloading: the phase is that from the return on.
	stand.temp.received(t, 7)
	if downloading := stand.transfers.Read.Execute(); downloading.Phase != domain.TransferDownloading || downloading.Bytes != 7 {
		t.Fatalf("downloading = %+v", downloading)
	}
	return writer
}

// removed waits for the files of the stand to go.
func (stand *transferStand) removed(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !stand.temp.removed() {
		if time.Now().After(deadline) {
			t.Fatal("the file of the transfer stays")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TR5.3: the stop of the app ends the transfer before its core: a download stops, its file goes and nothing opens; an open is
// given the end of the life of the transfers — its transaction rolls back —; both end as canceled, and the stop waits for
// them. An update counts a transfer that opens as one that downloads.
func TestTheStopOfTheAppEndsATransfer(t *testing.T) {
	stand := newTransferStand(t)
	stand.downloading(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stand.link.StopTransfers(ctx)
	if stopped := stand.transfers.Read.Execute(); stopped.Phase != domain.TransferFailed || stopped.Code != domain.TransferCanceled {
		t.Fatalf("a download as the app stops = %+v", stopped)
	}
	if !stand.temp.removed() || len(stand.opens.inputs) != 0 {
		t.Fatalf("a download the stop ended left its file %v or opened %d", !stand.temp.removed(), len(stand.opens.inputs))
	}

	stand = newTransferStand(t)
	opens := &stalledOpens{started: make(chan struct{}), release: make(chan struct{}), ended: make(chan error, 1)}
	t.Cleanup(func() { close(opens.release) })
	stand.transfers = stand.link.Transfers(TransferPorts{Site: stand.transferSite, Opens: opens, Temp: stand.temp, Gate: stand.gate})
	_, query := stand.start(t)
	if err := stand.transfers.Finish.Execute(codeOfTheTransfer, query.Get("state")); err != nil {
		t.Fatal(err)
	}
	<-opens.started
	if running, _ := stand.link.TransfersRunning(context.Background()); running != 1 {
		t.Fatalf("a transfer that opens counts %d for the updates", running)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stand.link.StopTransfers(ctx)
	select {
	case err := <-opens.ended:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the open was given %v", err)
		}
	default:
		t.Fatal("the stop of the app did not wait for the open, or did not end it")
	}
	if stopped := stand.transfers.Read.Execute(); stopped.Phase != domain.TransferFailed || stopped.Code != domain.TransferCanceled {
		t.Fatalf("an open as the app stops = %+v", stopped)
	}
	stand.removed(t)
}

// TR5.3: «Стереть все данные» ends a transfer that waits and one that downloads as canceled, «Отвязать» one that downloads as
// not_linked; the file of a download goes.
func TestTheErasureAndTheUnlinkingEndATransfer(t *testing.T) {
	stand := newTransferStand(t)
	stand.start(t)
	stand.link.Unlink.BeforeErase(context.Background())
	if erased := stand.transfers.Read.Execute(); erased.Phase != domain.TransferFailed || erased.Code != domain.TransferCanceled {
		t.Fatalf("a waiting before the erasure = %+v", erased)
	}

	stand = newTransferStand(t)
	stand.downloading(t)
	stand.link.Unlink.BeforeErase(context.Background())
	if erased := stand.transfers.Read.Execute(); erased.Phase != domain.TransferFailed || erased.Code != domain.TransferCanceled {
		t.Fatalf("a download before the erasure = %+v", erased)
	}
	stand.removed(t)

	stand = newTransferStand(t)
	stand.downloading(t)
	stand.link.Unlink.Execute(context.Background())
	if unlinked := stand.transfers.Read.Execute(); unlinked.Phase != domain.TransferFailed || unlinked.Code != domain.TransferNotLinked {
		t.Fatalf("a download as the app is unlinked = %+v", unlinked)
	}
	stand.removed(t)
	if len(stand.opens.inputs) != 0 {
		t.Fatal("an ended download opened its saga")
	}
}
