// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"errors"
	"time"
)

// An update of the app (specs/2026-11-13-desk-updates.md UB1, UB2): the backend keeps the schedule, the setting and the
// state; the shell checks, downloads, checks the signatures and installs. The page reads the state and asks the backend,
// the backend asks the shell only by the version the shell itself reported.

// UpdateKind is how this copy of the app was installed (UB1.7): it updates itself only as an installer of Windows, a
// package of Linux or an AppImage that runs from its file; any other build is updated by hand.
type UpdateKind string

const (
	UpdateNSIS     UpdateKind = "nsis"
	UpdateDeb      UpdateKind = "deb"
	UpdateRPM      UpdateKind = "rpm"
	UpdateAppImage UpdateKind = "appimage"
	UpdateNone     UpdateKind = "none"
)

// ParseUpdateKind reads the kind start names; anything else updates by hand.
func ParseUpdateKind(kind string) UpdateKind {
	switch UpdateKind(kind) {
	case UpdateNSIS, UpdateDeb, UpdateRPM, UpdateAppImage:
		return UpdateKind(kind)
	}
	return UpdateNone
}

// UpdatePhase is where an update stands as getAppUpdate shows it.
type UpdatePhase string

const (
	UpdateIdle           UpdatePhase = "idle"
	UpdateChecking       UpdatePhase = "checking"
	UpdateDownloading    UpdatePhase = "downloading"
	UpdateWaitingTurn    UpdatePhase = "waiting_turn"
	UpdateInstalling     UpdatePhase = "installing"
	UpdateDownloadFailed UpdatePhase = "download_failed"
	UpdateVerifyFailed   UpdatePhase = "verify_failed"
	UpdateInstallRefused UpdatePhase = "install_refused"
	UpdateInstallFailed  UpdatePhase = "install_failed"
)

// UpdateFound is what the last check found, while nothing else goes on.
type UpdateFound string

const (
	UpdateNeverChecked UpdateFound = "never"
	UpdateLatest       UpdateFound = "latest"
	UpdateAvailable    UpdateFound = "available"
	UpdateOffline      UpdateFound = "offline"
)

// UpdateRefusal is why the system did not install a package (UB2.6): the window of the password was closed, the system
// did not ask for it or gave no right, or another installation of programs holds a lock of Debian
// (specs/2026-11-17-desk-update-linux.md UL2.1, UL2.2) — then no package is kept, and the author presses again once it ended.
type UpdateRefusal string

const (
	UpdateDeclined    UpdateRefusal = "declined"
	UpdateUnavailable UpdateRefusal = "unavailable"
	UpdateBusy        UpdateRefusal = "busy"
)

// UpdateRelease is a later version the shell proved by its signature, with the plain text of its notes.
type UpdateRelease struct {
	Version string
	Notes   string
}

// AppUpdate is getAppUpdate: the version of the program, whether it checks once a day, how it was installed, the phase
// and what goes with it.
type AppUpdate struct {
	Version   string
	Daily     bool
	Kind      UpdateKind
	Phase     UpdatePhase
	Found     UpdateFound
	Available *UpdateRelease
	Percent   int
	Refusal   UpdateRefusal
	Package   string
	CheckedAt time.Time
}

// ShellUpdate is the last update-state of the shell.
type ShellUpdate struct {
	Phase   string
	Found   string
	Version string
	Notes   string
	Percent int
	Refusal string
	Package string
}

// ErrUpdateState refuses an operation the phase of the update does not allow: a check while one goes on, an installation
// without a version to install, a cancel of what cannot be canceled.
var ErrUpdateState = errors.New("desktop: the update is in another phase")

// UpdateOf is what the page reads of the update the shell reported (UB1.7): the file downloaded and checked waits for the
// turns of the chat until the backend asked to install it, then it installs.
func UpdateOf(shell ShellUpdate, kind UpdateKind, installing bool) (UpdatePhase, UpdateFound) {
	if kind == UpdateNone {
		return UpdateIdle, UpdateNeverChecked
	}
	switch shell.Phase {
	case "checking":
		return UpdateChecking, ""
	case "downloading":
		return UpdateDownloading, ""
	case "downloaded":
		if installing {
			return UpdateInstalling, ""
		}
		return UpdateWaitingTurn, ""
	case "installing":
		return UpdateInstalling, ""
	case "download_failed":
		return UpdateDownloadFailed, ""
	case "verify_failed":
		return UpdateVerifyFailed, ""
	case "install_refused":
		return UpdateInstallRefused, ""
	case "install_failed":
		return UpdateInstallFailed, ""
	}
	switch UpdateFound(shell.Found) {
	case UpdateLatest, UpdateAvailable, UpdateOffline:
		return UpdateIdle, UpdateFound(shell.Found)
	}
	return UpdateIdle, UpdateNeverChecked
}

// MayCheck says whether a check may start in a phase: not while one goes on, a download or an installation.
func MayCheck(phase UpdatePhase, kind UpdateKind) bool {
	if kind == UpdateNone {
		return false
	}
	switch phase {
	case UpdateChecking, UpdateDownloading, UpdateWaitingTurn, UpdateInstalling:
		return false
	}
	return true
}

// MayInstall says whether «Обновить и перезапустить» may start: a later version is known, and nothing goes on.
func MayInstall(update AppUpdate) bool {
	if update.Kind == UpdateNone || update.Available == nil || update.Available.Version == "" {
		return false
	}
	switch update.Phase {
	case UpdateIdle:
		return update.Found == UpdateAvailable
	case UpdateDownloadFailed, UpdateVerifyFailed, UpdateInstallRefused, UpdateInstallFailed:
		return true
	}
	return false
}

// MayCancel says whether the download or the wait for the turns may stop: an installation the backend asked for does not.
func MayCancel(phase UpdatePhase) bool {
	return phase == UpdateDownloading || phase == UpdateWaitingTurn
}
