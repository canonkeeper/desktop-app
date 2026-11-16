// SPDX-License-Identifier: AGPL-3.0-only

// Package accountlink holds the use cases of the link of the app with an account of the site on the side of the app
// (specs/2026-11-13-desk-account.md): start and cancel a link, finish it on the return of the browser, read its state
// with the account, unlink, and unlink before an erasure. The pass is held by the one client of the app to the site and
// kept by the shell as the record account:pass; nothing of the link is in PostgreSQL, and the local author does not
// change.
package accountlink

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"time"

	domain "canonkeeper/backend/internal/domain/accountlink"
	desktopdomain "canonkeeper/backend/internal/domain/desktop"
)

// AccountTerm is how long a read of the account waits for the site (AC2.1); EraseTerm how long the erasure waits for the
// site to unlink (AC2.5); UnlinkTerm how long «Отвязать» waits for it.
const (
	AccountTerm = 5 * time.Second
	EraseTerm   = 5 * time.Second
	UnlinkTerm  = 5 * time.Second
	// dropTerm bounds the removal of the record of a pass the site refused, which goes on after the answer.
	dropTerm = 30 * time.Second
)

// Site is the site as the link needs it: the one client of the app (adapters/siteapi), which holds the pass in its
// requests and drops it on its own refusal.
type Site interface {
	Exchange(ctx context.Context, exchange domain.Exchange) (string, domain.Account, error)
	Hold(pass string)
	Held() bool
	Account(ctx context.Context) (domain.Account, error)
	Unlink(ctx context.Context) bool
}

// Records are the secrets of the app as the link needs them: the pass is the record account:pass the shell keeps; Save
// and Delete are done once the shell answered ok (desktopdomain.ErrSecretNotKept otherwise).
type Records interface {
	Value(id string) (string, bool)
	Save(ctx context.Context, id, value string) error
	Delete(ctx context.Context, id string) error
	Store() desktopdomain.SecretsStore
}

// Leftovers keep, beside the data, the mark of a pass the app dropped while the shell may still keep its record — never
// the pass —, so the next start removes the record and is not linked (AC2.3).
type Leftovers interface {
	MarkDropped(pass string) error
	ForgetDropped() error
}

// Shell raises the window of the app.
type Shell interface {
	Focus() error
}

// Helpers hear the helper the site told for the app with an account it answered (specs/2026-11-13-desk-account-ai.md
// AI1.3): the build keeps its last window and threshold for Codex. Linked is a link made (AI1.2): the helper of the account
// is connected again, whatever helper the site told, and chosen by the intent the link was started with
// (specs/2026-11-15-desk-account-link-web.md AW3.2).
type Helpers interface {
	Heard(ctx context.Context, helper domain.Helper)
	Linked(ctx context.Context, helper domain.Helper, chose bool)
}

// Build is what the link knows of this build and this run: the address of the site of the build, the port of the window,
// the system, the version of the app and the name of the computer the site will see.
type Build struct {
	Site      Site
	Records   Records
	Leftovers Leftovers
	Shell     Shell
	Helpers   Helpers
	Random    io.Reader
	Now       func() time.Time
	Logger    *log.Logger

	SiteURL  string
	Port     int
	Platform domain.Platform
	Version  string
	Computer string
}

// Parts are the use cases of the link over the one waiting and the one state of the link of this run.
type Parts struct {
	Start  StartLink
	Cancel CancelLink
	Finish FinishLink
	Read   GetAccount
	Unlink UnlinkAccount

	build Build
	link  *shared
}

// shared is what the use cases of one run share: the waiting, the state of the link and the removals that go on after
// an answer.
type shared struct {
	build Build
	waits *domain.Waits
	state *domain.Link
	drops sync.WaitGroup
	// transfer is the one transfer of a saga from the site of this run (transfer.go).
	transfer *transferState
}

func NewParts(build Build) Parts {
	if build.Now == nil {
		build.Now = time.Now
	}
	link := &shared{build: build, waits: domain.NewWaits(build.Now), state: &domain.Link{}, transfer: newTransferState(build.Now)}
	return Parts{
		Start: StartLink{link: link}, Cancel: CancelLink{link: link}, Finish: FinishLink{link: link},
		Read: GetAccount{link: link}, Unlink: UnlinkAccount{link: link}, build: build, link: link,
	}
}

// Reaches says whether the app goes to the site now: it is linked or a link waits (AC2.7).
func (parts Parts) Reaches() bool {
	if parts.link.build.Site.Held() {
		return true
	}
	_, waiting := parts.link.waits.Current()
	return waiting || parts.link.state.Known().Exchanging != nil
}

// Linked says whether the app is linked now: the client holds a pass (specs/2026-11-13-desk-account-ai.md AI1.2) — no
// answer of the site does not undo it, a broken link and an unlinking do.
func (parts Parts) Linked() bool {
	return parts.link != nil && parts.link.build.Site != nil && parts.link.build.Site.Held()
}

// Refresh makes the next read of the account ask the site (specs/2026-11-13-desk-account-ai.md AI2.11): the one cache of
// what the site told, made stale by an open and a close of a permit.
func (parts Parts) Refresh() {
	if parts.link != nil {
		parts.link.state.Stale()
	}
}

// PassRefused is a refusal of the pass held an operation of the work of the helper heard (AI2.1): the link is broken at once
// and the record of the pass goes, as when a read of the account heard it.
func (parts Parts) PassRefused() {
	link := parts.link
	link.state.Broke()
	link.log(`{"event":"desktop_account_pass_refused"}`)
	link.drops.Add(1)
	go func() {
		defer link.drops.Done()
		ctx, cancel := context.WithTimeout(context.Background(), dropTerm)
		defer cancel()
		link.drop(ctx)
	}()
}

// heard hands the helper the site told to the build.
func (link *shared) heard(ctx context.Context, account domain.Account) {
	if link.build.Helpers != nil && account.Helper != (domain.Helper{}) {
		link.build.Helpers.Heard(ctx, account.Helper)
	}
}

// ForgetLeftover removes the record of a pass an earlier run dropped and the shell kept; the start holds no such pass
// (AC2.3). Once the shell removed it, the mark goes.
func (parts Parts) ForgetLeftover(ctx context.Context) error {
	if err := parts.link.build.Records.Delete(ctx, desktopdomain.SecretAccountPass); err != nil {
		return err
	}
	return parts.link.build.Leftovers.ForgetDropped()
}

// wait waits for the removals that go on after an answer.
func (parts Parts) wait() { parts.link.drops.Wait() }

// State is what getAccount says (AC2.1): the phase; the address of /link and its term while a link waits; why the last
// link failed; the account the site told last and when, and whether it did not answer since; the name of the computer
// the site sees, where the pass is kept and the pages of the site.
type State struct {
	Phase       domain.Phase
	Waiting     *domain.Waiting
	Failure     domain.Failure
	Account     *domain.Account
	At          time.Time
	Unreachable bool
	Computer    string
	Store       desktopdomain.SecretsStore
	Pages       domain.Pages
}

// stateOf is the state of the link at the moment, without asking the site. A waiting that ran out fails the link for its
// term (specs/2026-11-15-desk-account-link-web.md AW1.6) until a new one starts. The living waiting is asked first: a term
// that passes between the two questions is then told by the second, not lost to «nothing waits».
func (link *shared) stateOf() State {
	state := State{
		Phase: domain.PhaseNone, Computer: link.build.Computer, Pages: domain.PagesOf(link.build.SiteURL),
	}
	if link.build.Records != nil {
		state.Store = link.build.Records.Store()
	}
	if waiting, ok := link.waits.Current(); ok {
		state.Phase, state.Waiting = domain.PhaseWaiting, shown(waiting)
		return state
	}
	if link.waits.Lapsed() {
		link.state.Failed(domain.FailedExpired)
		link.log(`{"event":"desktop_account_link_failed","failure":"expired"}`)
	}
	known := link.state.Known()
	switch {
	case known.Exchanging != nil:
		state.Phase, state.Waiting = domain.PhaseWaiting, shown(*known.Exchanging)
	case link.build.Site.Held():
		state.Phase, state.Account, state.At, state.Unreachable = domain.PhaseLinked, known.Account, known.At, known.Unreachable
	case known.Broken:
		state.Phase = domain.PhaseBroken
	case known.Failure != "":
		state.Phase, state.Failure = domain.PhaseFailed, known.Failure
	}
	return state
}

// shown is a waiting as the page sees it: the address of /link and its term, not the verifier.
func shown(waiting domain.Waiting) *domain.Waiting {
	return &domain.Waiting{Address: waiting.Address, Expires: waiting.Expires}
}

// drop stops using the pass the site refused or the author unlinked and removes its record; a record the shell did not
// remove is marked for the next start (AC2.3). It reports whether the record went.
func (link *shared) drop(ctx context.Context) bool {
	pass, kept := link.build.Records.Value(desktopdomain.SecretAccountPass)
	if err := link.build.Records.Delete(ctx, desktopdomain.SecretAccountPass); err != nil {
		if kept {
			link.mark(pass)
		}
		return false
	}
	return true
}

func (link *shared) mark(pass string) {
	if err := link.build.Leftovers.MarkDropped(pass); err != nil {
		link.log(`{"event":"desktop_account_mark_not_kept"}`)
	}
}

func (link *shared) log(line string) {
	if link.build.Logger != nil {
		link.build.Logger.Print(line)
	}
}

// StartLink is startAccountLink (AC1.1): while the app is not linked, no link waits and none is exchanged, and the site
// can return to this build, a new waiting of 10 minutes — a verifier and a state of 32 random bytes — and the address
// of /link with the challenge of the verifier, the state, the port of the window and the system. The intent to choose
// the helper of the account lives with the waiting, not in the address (specs/2026-11-15-desk-account-link-web.md AW3.2).
type StartLink struct{ link *shared }

func (useCase StartLink) Execute(_ context.Context, chooseHelper bool) (State, error) {
	link, build := useCase.link, useCase.link.build
	if _, waiting := link.waits.Current(); waiting || build.Site.Held() || link.state.Known().Exchanging != nil {
		return link.stateOf(), domain.ErrNotNow
	}
	if err := domain.Startable(build.Port, build.Platform, build.Version); err != nil {
		return link.stateOf(), err
	}
	verifier, err := domain.NewSecret(build.Random)
	if err != nil {
		return State{}, err
	}
	state, err := domain.NewSecret(build.Random)
	if err != nil {
		return State{}, err
	}
	link.state.Started()
	link.waits.Begin(domain.Waiting{
		Verifier: verifier, State: state, Expires: build.Now().Add(domain.WaitLifetime), ChooseHelper: chooseHelper,
		Address: domain.LinkAddress(build.SiteURL, domain.Challenge(verifier), state, build.Port, build.Platform),
	})
	return link.stateOf(), nil
}

// CancelLink is cancelAccountLink: the waiting ends; its return is refused.
type CancelLink struct{ link *shared }

func (useCase CancelLink) Execute(context.Context) {
	useCase.link.waits.Cancel()
}

// Outcome is how a return ended once its state was spent.
type Outcome int

const (
	// Linked: the shell keeps the pass; the app is linked.
	Linked Outcome = iota
	// ExchangeFailed: the site did not change the code for a pass.
	ExchangeFailed
	// NotSaved: the shell did not keep the pass; the site was told to unlink it.
	NotSaved
)

// FinishLink is the return of the browser (AC1.2, AC1.3): a code of the shape of the site and the state waiting spend the
// waiting once; the code goes to the site with the verifier, the name of the computer and the version — a refusal once,
// a lost answer once more with the same code and verifier —, the pass is kept by the shell as account:pass and held only
// then, and the window is raised. A pass the shell did not keep is unlinked with itself and its record removed. The
// caller runs it in a term of its own, not the request of the browser.
type FinishLink struct{ link *shared }

func (useCase FinishLink) Execute(ctx context.Context, code, state string) (Outcome, error) {
	link, build := useCase.link, useCase.link.build
	if !domain.CodeShape(code) {
		return ExchangeFailed, domain.ErrStateInvalid
	}
	waiting, err := link.waits.Spend(state)
	if err != nil {
		return ExchangeFailed, err
	}
	link.state.Exchanging(waiting)
	defer link.state.Exchanged()
	exchange := domain.Exchange{Code: code, Verifier: waiting.Verifier, Name: build.Computer, AppVersion: build.Version}
	pass, account, err := build.Site.Exchange(ctx, exchange)
	if errors.Is(err, domain.ErrAnswerLost) {
		// The site may have made the device and its answer got lost: the same code again links it or has the site
		// unlink the device it made (AC1.3).
		pass, account, err = build.Site.Exchange(ctx, exchange)
	}
	if err != nil {
		link.state.Failed(domain.FailedExchange)
		link.log(`{"event":"desktop_account_link_failed","failure":"exchange"}`)
		return ExchangeFailed, nil
	}
	build.Site.Hold(pass)
	if err := build.Records.Save(ctx, desktopdomain.SecretAccountPass, pass); err != nil {
		build.Site.Unlink(ctx)
		if err := build.Records.Delete(ctx, desktopdomain.SecretAccountPass); err != nil {
			link.mark(pass)
		}
		link.state.Failed(domain.FailedKeyring)
		link.log(`{"event":"desktop_account_link_failed","failure":"keyring"}`)
		return NotSaved, nil
	}
	if err := build.Leftovers.ForgetDropped(); err != nil {
		link.log(`{"event":"desktop_account_mark_not_forgotten"}`)
	}
	link.state.Linked(account, build.Now())
	if build.Helpers != nil {
		build.Helpers.Linked(ctx, account.Helper, waiting.ChooseHelper)
	}
	link.log(`{"event":"desktop_account_linked"}`)
	if err := build.Shell.Focus(); err != nil {
		link.log(`{"event":"desktop_window_not_raised"}`)
	}
	return Linked, nil
}

// GetAccount is getAccount (AC2.1–AC2.3): the state of the link; for a linked app the account the site tells, asked at
// most once a minute or when the page asks, within AccountTerm. No answer keeps what the site told in this run with «нет
// связи»; the refusal of the pass held breaks the link and its record goes; a refusal of a pass held before, or a pass
// the author unlinked before the read reached the site, changes nothing.
type GetAccount struct{ link *shared }

func (useCase GetAccount) Execute(ctx context.Context, fresh bool) State {
	link, build := useCase.link, useCase.link.build
	if link.stateOf().Phase != domain.PhaseLinked || !link.state.Due(build.Now(), fresh) {
		return link.stateOf()
	}
	useCase.ask(ctx)
	return link.stateOf()
}

// Held is the state of the link the app holds now, never waiting for the site (specs/2026-11-13-desk-account-ai.md AI3.7):
// the account the site told last and when it told it. Once a read is due (FreshEvery) it goes aside within AccountTerm, and a
// later call holds what the site told.
func (useCase GetAccount) Held() State {
	link := useCase.link
	state := link.stateOf()
	if state.Phase == domain.PhaseLinked && link.state.Due(link.build.Now(), false) {
		link.drops.Add(1)
		go func() {
			defer link.drops.Done()
			useCase.ask(context.Background())
		}()
	}
	return state
}

// ask reads the account at the site within AccountTerm.
func (useCase GetAccount) ask(ctx context.Context) {
	link, build := useCase.link, useCase.link.build
	asked, cancel := context.WithTimeout(ctx, AccountTerm)
	defer cancel()
	account, err := build.Site.Account(asked)
	switch {
	case err == nil:
		link.state.Read(account, build.Now())
		link.heard(ctx, account)
	case errors.Is(err, domain.ErrPassInvalid):
		link.state.Broke()
		link.log(`{"event":"desktop_account_pass_refused"}`)
		link.drops.Add(1)
		go func() {
			defer link.drops.Done()
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dropTerm)
			defer cancel()
			link.drop(ctx)
		}()
	case errors.Is(err, domain.ErrPassReplaced), errors.Is(err, domain.ErrNotHeld):
	default:
		link.state.Unreachable()
	}
}

// Unlinked says what unlinking reached (AC2.4): whether the site heard it and whether the record of the pass went from
// the computer.
type Unlinked struct {
	Site     bool
	Computer bool
}

// UnlinkAccount is unlinkAccount (AC2.4): the site is told when it answers within UnlinkTerm; the app stops using the
// pass in any case and its record goes — one the shell did not remove is marked for the next start. A transfer of a saga
// that waits or downloads ends as not_linked (specs/2026-11-16-desk-site-saga.md TR5.3). Nothing else on the computer
// changes.
type UnlinkAccount struct{ link *shared }

func (useCase UnlinkAccount) Execute(ctx context.Context) Unlinked {
	link, build := useCase.link, useCase.link.build
	link.abortTransfer(domain.TransferNotLinked)
	asked, cancel := context.WithTimeout(ctx, UnlinkTerm)
	told := build.Site.Unlink(asked)
	cancel()
	link.state.Unlinked()
	removed := link.drop(ctx)
	link.log(`{"event":"desktop_account_unlinked"}`)
	return Unlinked{Site: told, Computer: removed}
}

// BeforeErase tries to unlink the app on the site before «Стереть все данные» within EraseTerm (AC2.5); the erasure goes
// on whatever it answers, and the shell removes every record. A transfer of a saga that waits or downloads ends as canceled
// (specs/2026-11-16-desk-site-saga.md TR5.3).
func (useCase UnlinkAccount) BeforeErase(ctx context.Context) {
	useCase.link.abortTransfer(domain.TransferCanceled)
	asked, cancel := context.WithTimeout(ctx, EraseTerm)
	defer cancel()
	useCase.link.build.Site.Unlink(asked)
	useCase.link.state.Unlinked()
}
