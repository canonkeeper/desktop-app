// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"crypto/rand"
	"log"
	"os"
	"runtime"
	"sync"
	"time"

	"canonkeeper/backend/internal/adapters/siteapi"
	httpdesktop "canonkeeper/backend/internal/delivery/http/desktop"
	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	"canonkeeper/backend/internal/domain/aisettings"
	domain "canonkeeper/backend/internal/domain/desktop"
	accountservice "canonkeeper/backend/internal/services/accountlink"
	aiservice "canonkeeper/backend/internal/services/aisettings"
	service "canonkeeper/backend/internal/services/desktop"
)

// leftoverTerm bounds the removal at the start of a record of a pass an earlier run dropped (AC2.3).
const leftoverTerm = time.Minute

// accountLink is the link of the app with an account of the site in this run (specs/2026-11-13-desk-account.md): the one
// client to the site of the build, holding the pass the shell handed over in start — unless settings.json marks it as
// dropped —, and the use cases of the link over the record account:pass. leftover says the record of start is a dropped
// pass the shell still keeps: it is removed once the backend serves, and the app is not linked.
type accountLink struct {
	parts    accountservice.Parts
	leftover bool
	logger   *log.Logger
	// client is the one client of the app to the site; helpers hears the helper the site tells.
	client  *siteapi.Client
	helpers *siteHelpers
	// transfers are the use cases of the transfer of a saga from the site over the link (specs/2026-11-16-desk-site-saga.md),
	// set by compose (saga_transfer.go).
	transfers accountservice.TransferParts
}

func newAccountLink(site string, port int, version string, secrets *shellSecrets, saved *settings, shell eraseShell, logger *log.Logger) (*accountLink, error) {
	client, err := siteapi.New(site)
	if err != nil {
		return nil, err
	}
	link := &accountLink{logger: logger, client: client, helpers: &siteHelpers{settings: saved, logger: logger}}
	if pass, ok := secrets.Value(domain.SecretAccountPass); ok {
		if saved.Dropped(pass) {
			link.leftover = true
		} else {
			client.Hold(pass)
		}
	}
	platform, _ := accountdomain.PlatformOf(runtime.GOOS)
	host, _ := os.Hostname()
	link.parts = accountservice.NewParts(accountservice.Build{
		Site: client, Records: secrets, Leftovers: saved, Shell: shell, Helpers: link.helpers, Random: rand.Reader, Now: time.Now, Logger: logger,
		SiteURL: site, Port: port, Platform: platform, Version: version, Computer: accountdomain.ComputerName(host),
	})
	return link, nil
}

// handlers are the operations of the link for the window.
func (link accountLink) handlers() httpdesktop.Account {
	parts := link.parts
	return httpdesktop.Account{Start: parts.Start, Cancel: parts.Cancel, Read: parts.Read, Unlink: parts.Unlink}
}

// callback is the return of the browser at the root of the window.
func (link accountLink) callback() httpdesktop.LinkCallback {
	return httpdesktop.LinkCallback{Finish: link.parts.Finish, Term: httpdesktop.CallbackTerm}
}

// served removes the record of a dropped pass once the shell answers on the channel.
func (link accountLink) served() {
	if !link.leftover {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), leftoverTerm)
		defer cancel()
		if err := link.parts.ForgetLeftover(ctx); err != nil {
			link.logger.Print(`{"event":"desktop_account_leftover_not_removed"}`)
		}
	}()
}

// Reaches says whether the app goes to the site now (AC2.7).
func (link accountLink) Reaches() bool { return link.parts.Reaches() }

// Helper is the helper of the site asked now for the choice of the source of the account
// (specs/2026-11-13-desk-account-ai.md AI1.1): the app is linked and the site answered with it.
func (link accountLink) Helper(ctx context.Context) (aisettings.Account, error) {
	if !link.parts.Linked() {
		return aisettings.Account{}, aiservice.ErrAccountUnlinked
	}
	state := link.parts.Read.Execute(ctx, true)
	switch {
	case state.Phase != accountdomain.PhaseLinked:
		return aisettings.Account{}, aiservice.ErrAccountUnlinked
	case state.Unreachable || state.Account == nil:
		return aisettings.Account{}, aiservice.ErrAccountUnreachable
	}
	return helperOf(state.Account.Helper), nil
}

func helperOf(helper accountdomain.Helper) aisettings.Account {
	return aisettings.Account{
		ChatModel: helper.ChatModel, CardsModel: helper.CardsModel,
		ContextWindow: int(min(helper.ContextWindow, 1<<31-1)), CompactLimit: int(min(helper.CompactLimit, 1<<31-1)),
	}
}

// siteHelpers keeps the helper the site told last in the section ai of settings.json (AI1.3): Codex of the app starts with
// its window and threshold, and a change the site made while the source is the account is heard as a change of the
// settings — the app-server restarts by the rule of the gateway.
type siteHelpers struct {
	settings *settings
	logger   *log.Logger

	mu      sync.Mutex
	changed func(context.Context)
}

// hear makes changed hear a change of the helper once the build is composed.
func (helpers *siteHelpers) hear(changed func(context.Context)) {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	helpers.changed = changed
}

func (helpers *siteHelpers) Heard(ctx context.Context, told accountdomain.Helper) {
	if helpers.keep(told) {
		helpers.change(ctx)
	}
}

// Linked hears a link made (specs/2026-11-13-desk-account-ai.md AI1.2, GW4.1): the helper of the account is chosen by the
// rule of the domain — the intent of the start, or no helper chosen yet (specs/2026-11-15-desk-account-link-web.md AW3.2) —
// and while the source is the account its helper is connected again: the build starts once what a change of the settings
// starts — the catch-up of the reading, the window of the site for Codex —, the helper of the site the same or not.
func (helpers *siteHelpers) Linked(ctx context.Context, told accountdomain.Helper, chose bool) {
	helpers.keep(told)
	helpers.choose(chose)
	helpers.change(ctx)
}

// choose keeps the source a link made chooses; the profile of «Свой ИИ» and the helper of the site stay.
func (helpers *siteHelpers) choose(chose bool) {
	current := helpers.settings.AI()
	next := current.Linked(chose)
	if next.Source == current.Source {
		return
	}
	if err := helpers.settings.KeepAI(next); err != nil {
		helpers.logger.Print(`{"event":"desktop_account_helper_not_chosen"}`)
	}
}

// keep keeps a helper the site told that differs from the one kept; it says whether it changed.
func (helpers *siteHelpers) keep(told accountdomain.Helper) bool {
	helper := helperOf(told)
	if !helper.Valid() {
		return false
	}
	current := helpers.settings.AI()
	if current.Account != nil && *current.Account == helper {
		return false
	}
	next := current
	next.Account = &helper
	if err := helpers.settings.KeepAI(next); err != nil {
		helpers.logger.Print(`{"event":"desktop_account_helper_not_kept"}`)
		return false
	}
	return true
}

// change is a change of the settings heard while the source is the account.
func (helpers *siteHelpers) change(ctx context.Context) {
	helpers.mu.Lock()
	changed := helpers.changed
	helpers.mu.Unlock()
	if changed != nil && helpers.settings.AI().Source == aisettings.SourceAccount {
		changed(ctx)
	}
}

// network is what the app goes to the site for beside its link (specs/2026-11-13-desk-account-ai.md AI1.7): the questions to
// the helper and the reading of the books, while the source is the account and the app is linked. networkUses puts it in
// the one line of the host of the site.
func (link accountLink) network(settings *settings, site string) func() []service.NetworkUse {
	return func() []service.NetworkUse {
		if settings.AI().Source != aisettings.SourceAccount || !link.parts.Linked() {
			return nil
		}
		return []service.NetworkUse{{Host: accountdomain.HostOf(site), Purpose: "вопросы помощнику и чтение книг"}}
	}
}

// transferNetwork is what the app goes to the site for while a transfer of a saga from it waits for the author or downloads
// (specs/2026-11-16-desk-site-saga.md TR5.4); networkUses puts it in the one line of the host of the site.
func (link accountLink) transferNetwork(site string) func() []service.NetworkUse {
	return func() []service.NetworkUse {
		if !link.parts.Transferring() {
			return nil
		}
		return []service.NetworkUse{{Host: accountdomain.HostOf(site), Purpose: "перенос саги с сайта"}}
	}
}
