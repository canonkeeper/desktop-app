// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"io"
	"log"
	"testing"

	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	"canonkeeper/backend/internal/domain/aisettings"
	accountservice "canonkeeper/backend/internal/services/accountlink"

	obsidianservice "canonkeeper/backend/internal/services/obsidian"
)

// heldSite is the client of the site as the link of a unit test reads it: a pass held or not.
type heldSite struct{ held bool }

func (site heldSite) Exchange(context.Context, accountdomain.Exchange) (string, accountdomain.Account, error) {
	return "", accountdomain.Account{}, accountdomain.ErrUnreachable
}
func (heldSite) Hold(string)     {}
func (site heldSite) Held() bool { return site.held }
func (heldSite) Account(context.Context) (accountdomain.Account, error) {
	return accountdomain.Account{}, accountdomain.ErrUnreachable
}
func (heldSite) Unlink(context.Context) bool { return false }

func linkOfTheTest(held bool) accountLink {
	return accountLink{parts: accountservice.NewParts(accountservice.Build{Site: heldSite{held: held}})}
}

var helperOfTheSite = accountdomain.Helper{ChatModel: "gpt-north-chat", CardsModel: "gpt-north-cards", ContextWindow: 1_000_000, CompactLimit: 950_000}

// specs/2026-11-13-desk-account-ai.md AI1.3: the helper the site told is kept in the section ai of settings.json and read
// back at the next start — Codex of the app starts with its window and threshold before the site answers again; a helper
// the site changed while the source is the account is heard as a change of the settings, once.
func TestTheHelperOfTheSiteIsKeptForTheNextStart(t *testing.T) {
	data := DataDir{Root: t.TempDir()}
	saved, err := loadSettings(data.Settings(), settingsNow)
	if err != nil {
		t.Fatal(err)
	}
	changes := 0
	helpers := &siteHelpers{settings: saved, logger: log.New(io.Discard, "", 0)}
	helpers.hear(func(context.Context) { changes++ })
	helpers.Heard(context.Background(), helperOfTheSite)
	if told := saved.AI().Account; told == nil || told.ContextWindow != 1_000_000 || changes != 0 {
		t.Fatalf("the helper of the site with the source none = %+v, %d changes", told, changes)
	}
	if err := saved.KeepAI(aisettings.Settings{Source: aisettings.SourceAccount, Account: saved.AI().Account}); err != nil {
		t.Fatal(err)
	}
	helpers.Heard(context.Background(), helperOfTheSite)
	if changes != 0 {
		t.Fatal("the same helper was heard as a change")
	}
	changed := helperOfTheSite
	changed.ContextWindow, changed.CompactLimit = 400_000, 380_000
	helpers.Heard(context.Background(), changed)
	helpers.Heard(context.Background(), accountdomain.Helper{ChatModel: "x"})
	if changes != 1 || saved.AI().ContextWindow() != 400_000 || saved.AI().CompactLimit() != 380_000 {
		t.Fatalf("a changed helper: %d changes, window %d", changes, saved.AI().ContextWindow())
	}
	again, err := loadSettings(data.Settings(), settingsNow)
	if err != nil || again.AI().Source != aisettings.SourceAccount || again.AI().ContextWindow() != 400_000 || again.AI().CompactLimit() != 380_000 {
		t.Fatalf("the next start reads %+v, %v", again.AI(), err)
	}
	// AI1.2, GW4.1: a link made again connects the helper of the account — the catch-up of the reading starts once, the helper
	// of the site the same or none told —; with the source «Свой ИИ» it changes nothing.
	helpers.Linked(context.Background(), changed, false)
	helpers.Linked(context.Background(), accountdomain.Helper{}, false)
	if changes != 3 {
		t.Fatalf("two links made with the source account: %d changes", changes)
	}
	if err := saved.KeepAI(aisettings.Settings{Source: aisettings.SourceOwn, Account: saved.AI().Account}); err != nil {
		t.Fatal(err)
	}
	helpers.Linked(context.Background(), changed, false)
	if changes != 3 {
		t.Fatalf("a link made with the source «Свой ИИ» was heard as a change: %d", changes)
	}
}

// specs/2026-11-15-desk-account-link-web.md AW3.2: a link made chooses the helper of the account when it was started to choose
// it or no helper is chosen yet — kept in settings.json and heard as a change of the settings, as a choice of the author —;
// «Свой ИИ» chosen without the intent stays and nothing is heard. The profile of «Свой ИИ» stays in every case.
func TestALinkMadeChoosesTheHelperOfTheAccountByItsIntent(t *testing.T) {
	chat, window, currency := "deepseek-chat", 64_000, "RUB"
	drafted, err := aisettings.Draft{Source: "own", Own: &aisettings.OwnDraft{
		Preset: "deepseek", ChatModel: &chat, CardsModel: &chat, Advanced: aisettings.AdvancedDraft{ContextWindow: &window, Currency: &currency},
	}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	profile := drafted.Own
	for _, test := range []struct {
		name    string
		source  aisettings.Source
		chose   bool
		after   aisettings.Source
		changes int
	}{
		{"«Свой ИИ» with the intent", aisettings.SourceOwn, true, aisettings.SourceAccount, 1},
		{"«Свой ИИ» without the intent", aisettings.SourceOwn, false, aisettings.SourceOwn, 0},
		{"none without the intent", aisettings.SourceNone, false, aisettings.SourceAccount, 1},
	} {
		data := DataDir{Root: t.TempDir()}
		saved, err := loadSettings(data.Settings(), settingsNow)
		if err != nil {
			t.Fatal(err)
		}
		if err := saved.KeepAI(aisettings.Settings{Source: test.source, Own: profile}); err != nil {
			t.Fatal(err)
		}
		changes := 0
		helpers := &siteHelpers{settings: saved, logger: log.New(io.Discard, "", 0)}
		helpers.hear(func(context.Context) { changes++ })
		helpers.Linked(context.Background(), helperOfTheSite, test.chose)
		again, err := loadSettings(data.Settings(), settingsNow)
		if err != nil {
			t.Fatal(err)
		}
		kept := again.AI()
		if kept.Source != test.after || changes != test.changes || kept.Own == nil || *kept.Own != *profile || kept.Account == nil {
			t.Errorf("%s: %+v, %d changes", test.name, kept, changes)
		}
	}
}

// AI1.2: the helper of the account is connected while the app is linked; a broken link and an unlinking undo it at once,
// a new link brings it back without a new choice.
func TestTheHelperOfTheAccountFollowsTheLink(t *testing.T) {
	data := DataDir{Root: t.TempDir()}
	saved, err := loadSettings(data.Settings(), settingsNow)
	if err != nil {
		t.Fatal(err)
	}
	told := helperOf(helperOfTheSite)
	if err := saved.KeepAI(aisettings.Settings{Source: aisettings.SourceAccount, Account: &told}); err != nil {
		t.Fatal(err)
	}
	site := &switchedSite{}
	helper := ownAI{settings: saved, secrets: newShellSecrets(nil, "", nil, 0), link: accountservice.NewParts(accountservice.Build{Site: site})}
	for _, step := range []struct {
		held, want bool
	}{{true, true}, {false, false}, {true, true}} {
		site.held = step.held
		if got := helper.Connected(context.Background()); got != step.want {
			t.Fatalf("linked %v: connected %v", step.held, got)
		}
	}
}

// specs/2026-11-13-desk-obsidian.md OB4.4 with the helper of the account (AI1.2): the preview of a vault names who reads
// the books for the app — nobody while no helper is connected, the helper of the account while the app is linked, never
// «Свой ИИ» for it.
func TestThePreviewOfAVaultNamesTheHelperOfTheAccount(t *testing.T) {
	data := DataDir{Root: t.TempDir()}
	saved, err := loadSettings(data.Settings(), settingsNow)
	if err != nil {
		t.Fatal(err)
	}
	site := &switchedSite{held: true}
	helper := ownAI{settings: saved, secrets: newShellSecrets(nil, "", nil, 0), link: accountservice.NewParts(accountservice.Build{Site: site})}
	if got := helper.reads(context.Background()); got != obsidianservice.HelperNone {
		t.Fatalf("no helper chosen: %q", got)
	}
	told := helperOf(helperOfTheSite)
	if err := saved.KeepAI(aisettings.Settings{Source: aisettings.SourceAccount, Account: &told}); err != nil {
		t.Fatal(err)
	}
	if got := helper.reads(context.Background()); got != obsidianservice.HelperAccount {
		t.Fatalf("the helper of the account, linked: %q", got)
	}
	site.held = false
	if got := helper.reads(context.Background()); got != obsidianservice.HelperNone {
		t.Fatalf("the helper of the account, the link broken: %q", got)
	}
}

type switchedSite struct {
	heldSite
	held bool
}

func (site *switchedSite) Held() bool { return site.held }

// AI1.7: the line of the site in «куда ходит приложение» names the questions to the helper and the reading of the books
// beside the link while the source is the account; there is no second line.
func TestTheLineOfTheSiteNamesTheHelperOfTheAccount(t *testing.T) {
	data := DataDir{Root: t.TempDir()}
	saved, err := loadSettings(data.Settings(), settingsNow)
	if err != nil {
		t.Fatal(err)
	}
	site := "https://site.example.test"
	link := linkOfTheTest(true)
	if uses := networkUses(link, site, link.network(saved, site))(); len(uses) != 1 || uses[0].Purpose != "связь с аккаунтом: имя, тариф и остатки" {
		t.Fatalf("the source none: %+v", uses)
	}
	told := helperOf(helperOfTheSite)
	if err := saved.KeepAI(aisettings.Settings{Source: aisettings.SourceAccount, Account: &told}); err != nil {
		t.Fatal(err)
	}
	uses := networkUses(link, site, link.network(saved, site))()
	if len(uses) != 1 || uses[0].Host != "site.example.test" ||
		uses[0].Purpose != "связь с аккаунтом: имя, тариф и остатки; вопросы помощнику и чтение книг" {
		t.Fatalf("the source account: %+v", uses)
	}
	unlinked := linkOfTheTest(false)
	if uses := networkUses(unlinked, site, unlinked.network(saved, site))(); len(uses) != 0 {
		t.Fatalf("an app not linked: %+v", uses)
	}
}
