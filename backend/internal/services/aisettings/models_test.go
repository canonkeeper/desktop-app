// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"context"
	"errors"
	"testing"

	domain "canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/desktop"
)

type askedProvider struct{ asked int }

func (provider *askedProvider) Models(context.Context) domain.Models {
	provider.asked++
	return domain.Models{Outcome: domain.ModelsUnlisted}
}

// specs/2026-11-13-desk-probes.md PB1.1: the models are asked of the provider the saved settings name — a variant that
// answers only with a key is not asked without one, a local server is asked without any — and nothing else is.
func TestTheModelsAreAskedOfTheProviderOfTheSettings(t *testing.T) {
	ctx := context.Background()
	settings := &keptSettings{settings: domain.None()}
	keys := &heldKeys{values: map[string]string{}}
	provider := &askedProvider{}
	list := ListModels{Settings: settings, Keys: keys, Provider: provider}
	if _, err := list.Execute(ctx); !errors.Is(err, ErrNotSet) {
		t.Fatalf("without settings = %v", err)
	}
	set := func(preset string) {
		t.Helper()
		value, err := domain.Draft{Source: "own", Own: &domain.OwnDraft{Preset: preset}}.Settings()
		if err != nil {
			t.Fatal(err)
		}
		settings.settings = value
	}
	set("deepseek")
	if _, err := list.Execute(ctx); !errors.Is(err, ErrNotSet) || provider.asked != 0 {
		t.Fatalf("DeepSeek without its key = %v, asked %d", err, provider.asked)
	}
	keys.values[desktop.SecretOwnAI] = "key-test-models-0001"
	if models, err := list.Execute(ctx); err != nil || models.Outcome != domain.ModelsUnlisted || provider.asked != 1 {
		t.Fatalf("DeepSeek with its key = %+v, %v", models, err)
	}
	delete(keys.values, desktop.SecretOwnAI)
	set("ollama")
	if _, err := list.Execute(ctx); err != nil || provider.asked != 2 {
		t.Fatalf("a local server = %v, asked %d", err, provider.asked)
	}
}
