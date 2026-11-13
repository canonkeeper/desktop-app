// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"time"
)

// AppState is what «Настройки» of the app show (specs/2026-11-13-desk-settings.md ST2.1, ST4.2, ST4.3): the version of
// the app, where its data live as the author sees the path, the place they take, the copies before an update and where
// the app goes in the network.
type AppState struct {
	// Version is the one version of the window, the backend and the installer; empty when the build did not name it.
	Version string
	DataDir string
	Usage   DiskUsage
	Backups []Backup
	Network []NetworkUse
}

// DiskUsage is the place the data directory takes. While a count goes on, Counting is set and Bytes is the last known
// place — Known is false before the first count ended.
type DiskUsage struct {
	Bytes    int64
	Known    bool
	Counting bool
}

// Backup is a copy before the migrations of a new version: when it was made and the version of the app that made it,
// empty for a copy made before the copies named it.
type Backup struct {
	Made    time.Time
	Version string
}

// NetworkUse is a host the app goes to in this build and these settings, and what for.
type NetworkUse struct {
	Host    string
	Purpose string
}

// UsageMeter counts the place of the data directory within the limit of a request.
type UsageMeter interface {
	Measure(ctx context.Context) DiskUsage
}

// BackupList lists the copies before an update, the newest first.
type BackupList interface {
	List() ([]Backup, error)
}

// ReadAppState is getDesktopState: one read for the parts «Данные» and «О программе».
type ReadAppState struct {
	Version string
	DataDir string
	Usage   UsageMeter
	Backups BackupList
	Network func() []NetworkUse
}

// Execute reads the state; a copy that cannot be listed fails the read, the place answers within its limit.
func (useCase ReadAppState) Execute(ctx context.Context) (AppState, error) {
	backups, err := useCase.Backups.List()
	if err != nil {
		return AppState{}, err
	}
	return AppState{
		Version: useCase.Version,
		DataDir: useCase.DataDir,
		Usage:   useCase.Usage.Measure(ctx),
		Backups: backups,
		Network: useCase.Network(),
	}, nil
}
