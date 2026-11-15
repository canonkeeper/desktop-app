// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import "testing"

// specs/2026-11-13-desk-updates.md UB1.7: the phase the page reads of the one the shell reported, and what each allows.
func TestThePhaseOfAnUpdateIsWhatTheShellReportedAndAllowsItsOperations(t *testing.T) {
	for _, case_ := range []struct {
		shell      ShellUpdate
		kind       UpdateKind
		installing bool
		phase      UpdatePhase
		found      UpdateFound
	}{
		{ShellUpdate{}, UpdateDeb, false, UpdateIdle, UpdateNeverChecked},
		{ShellUpdate{Phase: "idle", Found: "available", Version: "1.3.0"}, UpdateDeb, false, UpdateIdle, UpdateAvailable},
		{ShellUpdate{Phase: "idle", Found: "offline"}, UpdateNSIS, false, UpdateIdle, UpdateOffline},
		{ShellUpdate{Phase: "idle", Found: "x"}, UpdateNSIS, false, UpdateIdle, UpdateNeverChecked},
		{ShellUpdate{Phase: "downloading"}, UpdateAppImage, false, UpdateDownloading, ""},
		{ShellUpdate{Phase: "downloaded"}, UpdateAppImage, false, UpdateWaitingTurn, ""},
		{ShellUpdate{Phase: "downloaded"}, UpdateAppImage, true, UpdateInstalling, ""},
		{ShellUpdate{Phase: "install_refused"}, UpdateRPM, false, UpdateInstallRefused, ""},
		// A build updated by hand has no update whatever is reported.
		{ShellUpdate{Phase: "idle", Found: "available"}, UpdateNone, false, UpdateIdle, UpdateNeverChecked},
	} {
		if phase, found := UpdateOf(case_.shell, case_.kind, case_.installing); phase != case_.phase || found != case_.found {
			t.Fatalf("%+v %s = %s %s; want %s %s", case_.shell, case_.kind, phase, found, case_.phase, case_.found)
		}
	}
	available := AppUpdate{Kind: UpdateDeb, Phase: UpdateIdle, Found: UpdateAvailable, Available: &UpdateRelease{Version: "1.3.0"}}
	if !MayInstall(available) || !MayCheck(UpdateIdle, UpdateDeb) || MayCancel(UpdateIdle) {
		t.Fatal("a version found")
	}
	for _, phase := range []UpdatePhase{UpdateDownloadFailed, UpdateVerifyFailed, UpdateInstallRefused, UpdateInstallFailed} {
		again := available
		again.Phase = phase
		if !MayInstall(again) || !MayCheck(phase, UpdateDeb) {
			t.Fatalf("after %s «Обновить и перезапустить» and a check are allowed", phase)
		}
	}
	for _, phase := range []UpdatePhase{UpdateChecking, UpdateDownloading, UpdateWaitingTurn, UpdateInstalling} {
		busy := available
		busy.Phase = phase
		if MayInstall(busy) || MayCheck(phase, UpdateDeb) {
			t.Fatalf("%s allows a new check or installation", phase)
		}
	}
	if !MayCancel(UpdateDownloading) || !MayCancel(UpdateWaitingTurn) || MayCancel(UpdateInstalling) {
		t.Fatal("only the download and the wait are canceled")
	}
	if ParseUpdateKind("msi") != UpdateNone || ParseUpdateKind("appimage") != UpdateAppImage || MayCheck(UpdateIdle, UpdateNone) {
		t.Fatal("the kinds")
	}
}
