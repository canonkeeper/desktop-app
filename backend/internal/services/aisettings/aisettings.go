// SPDX-License-Identifier: AGPL-3.0-only

// Package aisettings is what the window of the desktop app does with «Свой ИИ» (specs/2026-11-13-desk-secrets.md SE2):
// reads the settings, replaces them, gives the shell the key to keep and has it removed. The key goes one way: the
// backend holds it in memory once the shell kept it and never gives it back.
package aisettings

import (
	"context"
	"errors"

	domain "canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/desktop"
)

var (
	// ErrAccountUnlinked is a choice of the helper of the account by an app that is not linked
	// (specs/2026-11-13-desk-account-ai.md AI1.1); ErrAccountUnreachable one whose site did not answer with a helper Codex
	// can run with. Neither changes the settings.
	ErrAccountUnlinked    = errors.New("aisettings: the app is not linked with an account")
	ErrAccountUnreachable = errors.New("aisettings: the site did not tell its helper")
	// ErrNotForSource is «Проверить связь» or the list of the models of the helper of the account, which has neither (AI1.6).
	ErrNotForSource = errors.New("aisettings: not for the source of the settings")
)

// AccountHelper is the helper of the site of the link, asked now (AI1.1): ErrAccountUnlinked or ErrAccountUnreachable.
type AccountHelper interface {
	Helper(ctx context.Context) (domain.Account, error)
}

// SettingsReader reads the settings of the AI the data directory keeps.
type SettingsReader interface {
	AI() domain.Settings
}

// SettingsKeeper replaces the settings of the AI the data directory keeps, whole.
type SettingsKeeper interface {
	KeepAI(domain.Settings) error
}

// KeyHolder says whether the backend holds a secret of the app and where the shell keeps them.
type KeyHolder interface {
	Has(id string) bool
	Store() desktop.SecretsStore
}

// KeySaver asks the shell to keep a secret of the app; desktop.ErrSecretNotKept — the shell refused or was silent.
type KeySaver interface {
	Save(ctx context.Context, id, value string) error
}

// KeyRemover asks the shell to remove a secret of the app; desktop.ErrSecretNotKept — the shell refused or was silent.
type KeyRemover interface {
	Delete(ctx context.Context, id string) error
}

// ChatRuntime is the helper of the chat as the window hears of it (specs/2026-11-13-desk-gateway.md GW2.3): a new window of
// the chat waits for the answer of the turn that runs.
type ChatRuntime interface {
	AfterAnswer() bool
}

// Changed hears every change of the settings or of the key that went through (GW2.3, GW4.2): the build gives the helper
// of the chat its new window and, once the helper is connected, queues the cards of the books.
type Changed interface {
	AIChanged(ctx context.Context)
}

// MoneyStops counts the sagas of the author read under a consent that acts with a stop sum in money of the currency the
// settings price in now — a change of the currency or the removal of the prices pauses them (specs/2026-11-13-desk-consent-web.md
// RW3.1). The service of the consent counts them; the settings know only this port.
type MoneyStops interface {
	Count(ctx context.Context) (int, error)
}

// State is what the window reads of «Свой ИИ» (SE2.4): the settings, whether the key is held, where the shell keeps the
// secrets, whether a new window of the chat waits for an answer and the last check with what it says now
// (specs/2026-11-13-desk-probes.md PB2.5), and how many sagas a change of the prices would pause (RW3.1). The key itself is
// not in it.
type State struct {
	Settings           domain.Settings
	KeySaved           bool
	SecretsStore       desktop.SecretsStore
	AppliesAfterAnswer bool
	Check              *Verdicts
	MoneyStops         int
}

// Read is getAiSettings.
type Read struct {
	Settings SettingsReader
	Keys     KeyHolder
	Runtime  ChatRuntime
	Reports  ReportReader
	// Stops counts the sagas under a sum in money; none — a build without the reading by «Свой ИИ» says none.
	Stops MoneyStops
}

func (read Read) Execute(ctx context.Context) (State, error) {
	settings := read.Settings.AI()
	state := State{
		Settings: settings, KeySaved: read.Keys.Has(desktop.SecretOwnAI), SecretsStore: read.Keys.Store(),
		AppliesAfterAnswer: read.Runtime != nil && read.Runtime.AfterAnswer(),
	}
	// The helper of the account has no check of its own (specs/2026-11-13-desk-account-ai.md AI1.6).
	if read.Reports != nil && settings.Source != domain.SourceAccount {
		if report, ok := read.Reports.Report(); ok {
			state.Check = verdictsOf(report, settings)
		}
	}
	if read.Stops != nil {
		count, err := read.Stops.Count(ctx)
		if err != nil {
			return State{}, err
		}
		state.MoneyStops = count
	}
	return state, nil
}

// KeyWatcher hears that the key changed: the check made with the key before is stale (PB2.5).
type KeyWatcher interface {
	KeyChanged(ctx context.Context)
}

func heard(changed Changed, ctx context.Context) {
	if changed != nil {
		changed.AIChanged(ctx)
	}
}

// Update is updateAiSettings (SE2.3, SE2.4): the draft is checked by the rules of the domain and replaces the settings
// whole; a field that is not valid changes nothing (*domain.FieldError). The helper of the account is chosen with what
// its site tells now — a linked app whose site answered with a helper Codex can run with —, and the helper the site told
// last stays beside every source (specs/2026-11-13-desk-account-ai.md AI1.1, AI1.3).
type Update struct {
	Settings SettingsKeeper
	Read     Read
	Changed  Changed
	Account  AccountHelper
}

func (update Update) Execute(ctx context.Context, draft domain.Draft) (State, error) {
	previous := update.Read.Settings.AI()
	// The currency and the price of the cache the write left out stay (specs/2026-11-13-desk-consent.md CN1.1).
	settings, err := draft.SettingsOver(previous)
	if err != nil {
		return State{}, err
	}
	settings.Account = previous.Account
	if settings.Source == domain.SourceAccount {
		if update.Account == nil {
			return State{}, ErrAccountUnlinked
		}
		helper, err := update.Account.Helper(ctx)
		if err != nil {
			return State{}, err
		}
		if !helper.Valid() {
			return State{}, ErrAccountUnreachable
		}
		settings.Account = &helper
	}
	if err := update.Settings.KeepAI(settings); err != nil {
		return State{}, err
	}
	heard(update.Changed, ctx)
	return update.Read.Execute(ctx)
}

// SetKey is setAiKey (SE2.5): a key by the rules of the domain goes to the shell, and the backend holds it once the shell
// kept it.
type SetKey struct {
	Keys    KeySaver
	Changed Changed
	Checks  KeyWatcher
}

func (set SetKey) Execute(ctx context.Context, key string) error {
	if err := domain.CheckKey(key); err != nil {
		return err
	}
	if err := set.Keys.Save(ctx, desktop.SecretOwnAI, key); err != nil {
		return err
	}
	if set.Checks != nil {
		set.Checks.KeyChanged(ctx)
	}
	heard(set.Changed, ctx)
	return nil
}

// DeleteKey is deleteAiKey: the shell removes the key, and the backend stops holding it once the shell did.
type DeleteKey struct {
	Keys    KeyRemover
	Changed Changed
	Checks  KeyWatcher
}

func (remove DeleteKey) Execute(ctx context.Context) error {
	if err := remove.Keys.Delete(ctx, desktop.SecretOwnAI); err != nil {
		return err
	}
	if remove.Checks != nil {
		remove.Checks.KeyChanged(ctx)
	}
	heard(remove.Changed, ctx)
	return nil
}
