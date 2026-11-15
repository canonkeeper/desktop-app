// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"context"
	"errors"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/aisettings"
)

// siteOfTheTest is the helper of the site as the choice of the source asks it.
type siteOfTheTest struct {
	helper domain.Account
	err    error
	asked  int
}

func (site *siteOfTheTest) Helper(context.Context) (domain.Account, error) {
	site.asked++
	return site.helper, site.err
}

// specs/2026-11-13-desk-account-ai.md AI1.1: the source «Аккаунт CanonKeeper» is chosen only by a linked app whose site
// answered with its helper — the choice takes it itself —; otherwise a refusal of its own and the settings stay. The
// profile of «Свой ИИ» stays beside it, and the helper the site told is kept for every source.
func TestTheSourceOfTheAccountIsChosenOnlyWithTheHelperOfTheSite(t *testing.T) {
	ctx := context.Background()
	own, err := domain.Draft{Source: "own", Own: &domain.OwnDraft{Preset: "ollama", ChatModel: text("a"), CardsModel: text("b")}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	told := domain.Account{ChatModel: "gpt-north-chat", CardsModel: "gpt-north-cards", ContextWindow: 1_000_000, CompactLimit: 950_000}
	for _, refusal := range []struct {
		what string
		err  error
	}{{"not linked", ErrAccountUnlinked}, {"no answer of the site", ErrAccountUnreachable}} {
		settings := &keptSettings{settings: own}
		changes := &heardChanges{}
		site := &siteOfTheTest{err: refusal.err}
		update := Update{Settings: settings, Read: Read{Settings: settings, Keys: &heldKeys{values: map[string]string{}}}, Changed: changes, Account: site}
		if _, err := update.Execute(ctx, domain.Draft{Source: "account"}); !errors.Is(err, refusal.err) {
			t.Fatalf("%s: %v", refusal.what, err)
		}
		if settings.writes != 0 || changes.heard != 0 || settings.settings.Source != domain.SourceOwn {
			t.Fatalf("%s changed the settings: %+v", refusal.what, settings.settings)
		}
	}
	for _, unfit := range []domain.Account{{}, {ChatModel: "a", CardsModel: "b", ContextWindow: 100_000, CompactLimit: 100_000}} {
		settings := &keptSettings{settings: own}
		update := Update{Settings: settings, Read: Read{Settings: settings, Keys: &heldKeys{values: map[string]string{}}}, Account: &siteOfTheTest{helper: unfit}}
		if _, err := update.Execute(ctx, domain.Draft{Source: "account"}); !errors.Is(err, ErrAccountUnreachable) || settings.writes != 0 {
			t.Fatalf("a helper Codex cannot run with: %v, %d writes", err, settings.writes)
		}
	}
	settings := &keptSettings{settings: own}
	changes := &heardChanges{}
	site := &siteOfTheTest{helper: told}
	update := Update{Settings: settings, Read: Read{Settings: settings, Keys: &heldKeys{values: map[string]string{}}}, Changed: changes, Account: site}
	state, err := update.Execute(ctx, domain.Draft{Source: "account", Own: &domain.OwnDraft{Preset: "ollama", ChatModel: text("a"), CardsModel: text("b")}})
	if err != nil || state.Settings.Source != domain.SourceAccount || state.Settings.Account == nil || *state.Settings.Account != told ||
		state.Settings.Own == nil || state.Settings.Own.ChatModel != "a" || changes.heard != 1 || site.asked != 1 {
		t.Fatalf("the source of the account = %+v, %v", state.Settings, err)
	}
	// Back to «Свой ИИ»: the helper the site told stays for the page and for the next choice.
	state, err = update.Execute(ctx, domain.Draft{Source: "own", Own: &domain.OwnDraft{Preset: "ollama", ChatModel: text("a"), CardsModel: text("b")}})
	if err != nil || state.Settings.Source != domain.SourceOwn || state.Settings.Account == nil || site.asked != 1 {
		t.Fatalf("back to own = %+v, %v", state.Settings, err)
	}
}

// AI1.6: «Проверить связь» and the list of the models are not for the source of the account: a refusal of their own, and
// the state of the settings carries no check.
func TestTheChecksOfTheProviderAreNotForTheAccount(t *testing.T) {
	told := domain.Account{ChatModel: "gpt-north-chat", CardsModel: "gpt-north-cards", ContextWindow: 1_000_000, CompactLimit: 950_000}
	account := &keptSettings{settings: domain.Settings{Source: domain.SourceAccount, Account: &told}}
	prober := &fakeProber{}
	reports := &keptReports{report: &domain.Report{At: time.Now()}}
	if _, err := checksOf(account, prober, reports).Start(context.Background()); !errors.Is(err, ErrNotForSource) {
		t.Fatalf("a check of the account = %v", err)
	}
	if _, err := (ListModels{Settings: account, Keys: &heldKeys{values: map[string]string{}}}).Execute(context.Background()); !errors.Is(err, ErrNotForSource) {
		t.Fatalf("the models of the account = %v", err)
	}
	if state, err := (Read{Settings: account, Keys: &heldKeys{values: map[string]string{}}, Reports: reports}).Execute(context.Background()); err != nil || state.Check != nil {
		t.Fatalf("the state of the account carries a check: %+v, %v", state.Check, err)
	}
}

func text(value string) *string { return &value }
