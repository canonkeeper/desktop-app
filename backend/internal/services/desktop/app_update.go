// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"

	domain "canonkeeper/backend/internal/domain/desktop"
)

// AppUpdates is the update of the app as its owner holds it (specs/2026-11-13-desk-updates.md UB1.7): the state the shell
// reported, the schedule and the setting of settings.json and the channel to the shell. The owner decides by the phase
// under its own lock; an operation the phase does not allow is domain.ErrUpdateState.
type AppUpdates interface {
	Update() domain.AppUpdate
	Check(ctx context.Context) error
	Install(ctx context.Context) error
	Cancel(ctx context.Context) error
	KeepDaily(daily bool) error
}

// ReadAppUpdate is getAppUpdate.
type ReadAppUpdate struct{ Updates AppUpdates }

func (useCase ReadAppUpdate) Execute(context.Context) domain.AppUpdate {
	return useCase.Updates.Update()
}

// CheckAppUpdate is checkAppUpdate: «Проверить сейчас».
type CheckAppUpdate struct{ Updates AppUpdates }

func (useCase CheckAppUpdate) Execute(ctx context.Context) (domain.AppUpdate, error) {
	if err := useCase.Updates.Check(ctx); err != nil {
		return domain.AppUpdate{}, err
	}
	return useCase.Updates.Update(), nil
}

// InstallAppUpdate is installAppUpdate: «Обновить и перезапустить» — the file is downloaded and checked, the update waits
// for the turns of the chat, then the shell installs it (UB2.1).
type InstallAppUpdate struct{ Updates AppUpdates }

func (useCase InstallAppUpdate) Execute(ctx context.Context) (domain.AppUpdate, error) {
	if err := useCase.Updates.Install(ctx); err != nil {
		return domain.AppUpdate{}, err
	}
	return useCase.Updates.Update(), nil
}

// CancelAppUpdate is cancelAppUpdate: the download or the wait for the turns stops, new turns are taken again.
type CancelAppUpdate struct{ Updates AppUpdates }

func (useCase CancelAppUpdate) Execute(ctx context.Context) (domain.AppUpdate, error) {
	if err := useCase.Updates.Cancel(ctx); err != nil {
		return domain.AppUpdate{}, err
	}
	return useCase.Updates.Update(), nil
}

// SetAppUpdateDaily is updateAppUpdateSettings: the check once a day, on or off; off makes no request at all (UB1.2).
type SetAppUpdateDaily struct{ Updates AppUpdates }

func (useCase SetAppUpdateDaily) Execute(_ context.Context, daily bool) (domain.AppUpdate, error) {
	if err := useCase.Updates.KeepDaily(daily); err != nil {
		return domain.AppUpdate{}, err
	}
	return useCase.Updates.Update(), nil
}
