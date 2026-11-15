// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"context"
	"errors"
	"testing"

	domain "canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/desktop"
)

type keptSettings struct {
	settings domain.Settings
	writes   int
}

func (kept *keptSettings) AI() domain.Settings { return kept.settings }
func (kept *keptSettings) KeepAI(settings domain.Settings) error {
	kept.settings = settings
	kept.writes++
	return nil
}

type heldKeys struct {
	values map[string]string
	asked  []string
	refuse bool
}

func (keys *heldKeys) Has(id string) bool          { _, ok := keys.values[id]; return ok }
func (keys *heldKeys) Store() desktop.SecretsStore { return desktop.SecretsInFile }
func (keys *heldKeys) Save(_ context.Context, id, value string) error {
	keys.asked = append(keys.asked, "save "+id)
	if keys.refuse {
		return desktop.ErrSecretNotKept
	}
	keys.values[id] = value
	return nil
}
func (keys *heldKeys) Delete(_ context.Context, id string) error {
	keys.asked = append(keys.asked, "delete "+id)
	if keys.refuse {
		return desktop.ErrSecretNotKept
	}
	delete(keys.values, id)
	return nil
}

// specs/2026-11-13-desk-secrets.md SE2.3–SE2.5: a write replaces the settings whole as the domain checked them, one that
// is not valid changes nothing; the key goes to the shell under the record of «Свой ИИ» only when it is a key, and the
// state says whether it is held — never what it is.
func TestTheWindowReadsWritesAndHandsOverTheKey(t *testing.T) {
	ctx := context.Background()
	settings := &keptSettings{settings: domain.None()}
	keys := &heldKeys{values: map[string]string{}}
	read := Read{Settings: settings, Keys: keys}
	if state := stateOf(t, read); state.Settings.Source != domain.SourceNone || state.KeySaved || state.SecretsStore != desktop.SecretsInFile {
		t.Fatalf("the first state = %+v", state)
	}

	update := Update{Settings: settings, Read: read}
	state, err := update.Execute(ctx, domain.Draft{Source: "own", Own: &domain.OwnDraft{Preset: "openai"}})
	if err != nil || settings.writes != 1 || state.Settings.Own == nil || state.Settings.Own.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("update = %+v, %v, writes %d", state, err, settings.writes)
	}
	_, err = update.Execute(ctx, domain.Draft{Source: "own"})
	var field *domain.FieldError
	if !errors.As(err, &field) || field.Field != "own" || settings.writes != 1 || settings.settings.Own == nil {
		t.Fatalf("a draft that is not valid = %v, writes %d", err, settings.writes)
	}

	if err := (SetKey{Keys: keys}).Execute(ctx, "sk test"); !errors.Is(err, domain.ErrKeyInvalid) || len(keys.asked) != 0 {
		t.Fatalf("a key with a space = %v, asked %v", err, keys.asked)
	}
	if err := (SetKey{Keys: keys}).Execute(ctx, "key-test-0001"); err != nil || !stateOf(t, read).KeySaved {
		t.Fatalf("setAiKey = %v, state %+v", err, stateOf(t, read))
	}
	if err := (DeleteKey{Keys: keys}).Execute(ctx); err != nil || stateOf(t, read).KeySaved {
		t.Fatalf("deleteAiKey = %v, state %+v", err, stateOf(t, read))
	}
	keys.refuse = true
	if err := (SetKey{Keys: keys}).Execute(ctx, "key-test-0002"); !errors.Is(err, desktop.ErrSecretNotKept) || stateOf(t, read).KeySaved {
		t.Fatalf("a key the shell refused = %v", err)
	}
	if want := []string{"save provider:own", "delete provider:own", "save provider:own"}; len(keys.asked) != 3 ||
		keys.asked[0] != want[0] || keys.asked[1] != want[1] || keys.asked[2] != want[2] {
		t.Fatalf("the shell was asked %v", keys.asked)
	}
}

// stateOf is getAiSettings of the test.
func stateOf(t *testing.T, read Read) State {
	t.Helper()
	state, err := read.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// countedStops is the port of the sagas under a sum in money: what the service of the consent counts, or its failure.
type countedStops struct {
	count int
	err   error
}

func (stops countedStops) Count(context.Context) (int, error) { return stops.count, stops.err }

// specs/2026-11-13-desk-consent-web.md RW3.1: getAiSettings says how many sagas a change of the prices would pause by the port
// of the service of the consent — none without the port — and fails with it; a write answers the same state.
func TestTheStateSaysTheSagasUnderASumInMoney(t *testing.T) {
	ctx := context.Background()
	settings := &keptSettings{settings: domain.None()}
	keys := &heldKeys{values: map[string]string{}}
	if state := stateOf(t, Read{Settings: settings, Keys: keys}); state.MoneyStops != 0 {
		t.Fatalf("without the port = %d", state.MoneyStops)
	}
	read := Read{Settings: settings, Keys: keys, Stops: countedStops{count: 2}}
	if state := stateOf(t, read); state.MoneyStops != 2 {
		t.Fatalf("two sagas under a sum in money = %d", state.MoneyStops)
	}
	if state, err := (Update{Settings: settings, Read: read}).Execute(ctx, domain.Draft{Source: "own", Own: &domain.OwnDraft{Preset: "openai"}}); err != nil || state.MoneyStops != 2 {
		t.Fatalf("the state of a write = %+v, %v", state, err)
	}
	broken := errors.New("the consents did not read")
	if _, err := (Read{Settings: settings, Keys: keys, Stops: countedStops{err: broken}}).Execute(ctx); !errors.Is(err, broken) {
		t.Fatalf("a failure of the count = %v", err)
	}
}

// chatRuntime is the app-server of the chat as the window hears of it.
type chatRuntime struct{ waits bool }

func (runtime chatRuntime) AfterAnswer() bool { return runtime.waits }

// heardChanges counts what the build heard of the changes.
type heardChanges struct{ heard int }

func (changes *heardChanges) AIChanged(context.Context) { changes.heard++ }

// specs/2026-11-13-desk-gateway.md GW2.3, GW4.2: the state says a new window waits for the answer of the turn that runs,
// and the build hears every change of the settings and of the key that went through — not one that was refused.
func TestTheBuildHearsEveryChangeOfTheAI(t *testing.T) {
	ctx := context.Background()
	settings := &keptSettings{settings: domain.None()}
	keys := &heldKeys{values: map[string]string{}}
	changes := &heardChanges{}
	read := Read{Settings: settings, Keys: keys, Runtime: chatRuntime{waits: true}}
	if !stateOf(t, read).AppliesAfterAnswer || stateOf(t, Read{Settings: settings, Keys: keys}).AppliesAfterAnswer {
		t.Fatal("the state does not say what the runtime of the chat says")
	}
	update := Update{Settings: settings, Read: read, Changed: changes}
	if _, err := update.Execute(ctx, domain.Draft{Source: "own"}); err == nil || changes.heard != 0 {
		t.Fatalf("a refused draft was heard: %v, %d", err, changes.heard)
	}
	if _, err := update.Execute(ctx, domain.Draft{Source: "own", Own: &domain.OwnDraft{Preset: "openai"}}); err != nil || changes.heard != 1 {
		t.Fatalf("an update was not heard: %v, %d", err, changes.heard)
	}
	if err := (SetKey{Keys: keys, Changed: changes}).Execute(ctx, "key-test-heard-0001"); err != nil || changes.heard != 2 {
		t.Fatalf("a key was not heard: %v, %d", err, changes.heard)
	}
	if err := (DeleteKey{Keys: keys, Changed: changes}).Execute(ctx); err != nil || changes.heard != 3 {
		t.Fatalf("a removed key was not heard: %v, %d", err, changes.heard)
	}
	keys.refuse = true
	if err := (SetKey{Keys: keys, Changed: changes}).Execute(ctx, "key-test-heard-0002"); err == nil || changes.heard != 3 {
		t.Fatalf("a key the shell refused was heard: %v, %d", err, changes.heard)
	}
}
