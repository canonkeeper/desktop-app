// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"crypto/rand"
	"io"
	"net/url"
	"testing"
	"time"

	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/auth"
	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	domain "canonkeeper/backend/internal/domain/desktop"
	accountservice "canonkeeper/backend/internal/services/accountlink"
	aiservice "canonkeeper/backend/internal/services/aisettings"
)

// downloadingSite is the site of a transfer whose download goes until its context ends: a saga the site sends slowly.
type downloadingSite struct{ asked chan struct{} }

func (downloadingSite) RequestSagaTransfer(context.Context, string) (string, error) {
	return "request-north", nil
}

func (site downloadingSite) TakeSagaTransfer(ctx context.Context, _, _ string) (io.ReadCloser, string, error) {
	close(site.asked)
	<-ctx.Done()
	return nil, "", ctx.Err()
}

// desktopWithATransfer is a build whose link holds a pass and whose transfer of a saga downloads, composed over a core that
// connects nothing as compose composes it; turns counts the turns of the chat for the updates.
func desktopWithATransfer(t *testing.T, updates *updater, turns func(context.Context) (int, error)) *build {
	t.Helper()
	desktop := &build{updates: updates, account: accountLink{parts: accountservice.NewParts(accountservice.Build{
		Site: heldSite{held: true}, Shell: eraseShell{}, Random: rand.Reader, Now: time.Now, SiteURL: "https://site.example.test", Port: 24567,
	})}}
	t.Cleanup(func() { desktop.account.parts.StopTransfers(context.Background()) })
	site := downloadingSite{asked: make(chan struct{})}
	desktop.useTransfers(app.Core{}, site, turns)
	transfers := desktop.account.transfers
	waiting, err := transfers.Start.Execute(context.Background(), "author", auth.SessionWriteProof{SessionHash: "session", CSRFSecretHash: "csrf"})
	if err != nil || waiting.Phase != accountdomain.TransferWaiting {
		t.Fatalf("the start of a transfer = %+v %v", waiting, err)
	}
	address, err := url.Parse(waiting.Address)
	if err != nil {
		t.Fatal(err)
	}
	if err := transfers.Finish.Execute("code-of-the-transfer-north-0000000000000000", address.Query().Get("state")); err != nil {
		t.Fatal(err)
	}
	<-site.asked
	if downloading := transfers.Read.Execute(); downloading.Phase != accountdomain.TransferDownloading {
		t.Fatalf("the transfer = %+v", downloading)
	}
	return desktop
}

// specs/2026-11-16-desk-site-saga.md TR5.3: the stop of the modules of the build ends a transfer of a saga that downloads as
// canceled and waits for it, within the stop of the core.
func TestTheStopOfTheModulesEndsATransferOfASaga(t *testing.T) {
	desktop := desktopWithATransfer(t, updaterOfTheBuild(), func(context.Context) (int, error) { return 0, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := desktop.stopModules(&aiservice.Checks{})(ctx); err != nil {
		t.Fatal(err)
	}
	if stopped := desktop.account.transfers.Read.Execute(); stopped.Phase != accountdomain.TransferFailed || stopped.Code != accountdomain.TransferCanceled {
		t.Fatalf("a transfer as the app stops = %+v", stopped)
	}
}

// specs/2026-11-16-desk-site-saga.md TR5.3; specs/2026-11-13-desk-updates.md UB2.1: «Обновить и перезапустить» waits for a
// transfer that downloads as for a turn of the chat — its gate stays open and the shell is not asked —, and installs once the
// transfer ended.
func TestAnUpdateWaitsForATransferOfASaga(t *testing.T) {
	updates, channel, _ := updaterOfTheTest(t, domain.UpdateAppImage)
	desktop := desktopWithATransfer(t, updates, func(context.Context) (int, error) { return 0, nil })
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "available", Version: "1.3.0"})
	if err := updates.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	updates.heard(ControlMessage{Type: messageUpdateState, Phase: "downloaded", Version: "1.3.0"})
	updates.whenNoTurns(context.Background())
	if update := updates.Update(); update.Phase != domain.UpdateWaitingTurn || updates.gate.isClosed() {
		t.Fatalf("an update over a transfer that downloads = %+v, the gate closed %v", update, updates.gate.isClosed())
	}
	if _, err := desktop.account.transfers.Cancel.Execute(desktop.account.transfers.Read.Execute().ID); err != nil {
		t.Fatal(err)
	}
	updates.whenNoTurns(context.Background())
	if update := updates.Update(); update.Phase != domain.UpdateInstalling {
		t.Fatalf("an update once the transfer ended = %+v", update)
	}
	if got := channel.kinds(); got[len(got)-1] != "update-install 1.3.0" {
		t.Fatalf("the backend wrote %v", got)
	}
}
