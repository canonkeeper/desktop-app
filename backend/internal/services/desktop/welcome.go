// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import "context"

// WelcomeSettings are settings.json of the app as «Добро пожаловать» reads it (specs/2026-11-13-desk-upgrade.md UG5.1):
// whether the welcome is still to be shown and whether the app checks for a new version once a day.
type WelcomeSettings interface {
	Welcome() (show bool, checkUpdates bool)
}

// WelcomeKeeper marks the welcome as seen with the choice of the author, in settings.json: the mark lives through a new
// port of the window and a new installation of the program; «Стереть все данные» takes it with the data.
type WelcomeKeeper interface {
	DismissWelcome(checkUpdates bool) error
}

// DismissWelcome is dismissWelcome: the author closed «Добро пожаловать» with the choice to check for a new version once
// a day, or not.
type DismissWelcome struct {
	Settings WelcomeKeeper
}

func (useCase DismissWelcome) Execute(_ context.Context, checkUpdates bool) error {
	return useCase.Settings.DismissWelcome(checkUpdates)
}
