// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"context"
	"errors"

	domain "canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/desktop"
)

// ModelLister asks the provider of the saved settings for its models: the local gateway, the one reader of the key.
type ModelLister interface {
	Models(ctx context.Context) domain.Models
}

// ErrNotSet is a question to a provider the settings do not name: no profile of «Свой ИИ», or a variant that answers only
// with the key the backend does not hold.
var ErrNotSet = errors.New("aisettings: no provider to ask")

// ListModels is listAiModels (specs/2026-11-13-desk-probes.md PB1): the provider of the saved settings, asked with his
// key, gives the names of its models, says it gives none, or the reason it failed. The list is kept nowhere.
type ListModels struct {
	Settings SettingsReader
	Keys     KeyHolder
	Provider ModelLister
}

func (list ListModels) Execute(ctx context.Context) (domain.Models, error) {
	settings := list.Settings.AI()
	if settings.Source == domain.SourceAccount {
		// The models of the helper of the account are the site's (specs/2026-11-13-desk-account-ai.md AI1.6).
		return domain.Models{}, ErrNotForSource
	}
	own := settings.Own
	if own == nil || own.Preset.NeedsKey() && !list.Keys.Has(desktop.SecretOwnAI) {
		return domain.Models{}, ErrNotSet
	}
	return list.Provider.Models(ctx), nil
}
