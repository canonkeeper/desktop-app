// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"path/filepath"
	"testing"
)

// specs/2026-11-13-desk-upgrade.md UG2.2, UG2.5: settings.json keeps the version that opened the data; an update names the
// version before it until ready took it — a start that fell after its migrations names it at the next start — and a
// return to an earlier version names nothing. Another key of the file stays.
func TestSettingsKeepTheVersionAndTheUpdateUntilReadyNamesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{"port": 24117, "shell": {"x": 1}}`)
	load := func() *settings {
		t.Helper()
		saved, err := loadSettings(path, settingsNow)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	keep := func(saved *settings, version, want string) {
		t.Helper()
		updated, err := saved.keepVersion(version)
		if err != nil || updated != want {
			t.Fatalf("keepVersion(%s) = %q, %v; want %q", version, updated, err, want)
		}
	}
	saved := load()
	keep(saved, "1.2.0", "")
	keep(load(), "1.3.0", "1.2.0")
	// The start fell before ready: the next one names the update.
	again := load()
	if again.Version() != "1.3.0" {
		t.Fatalf("the version = %q", again.Version())
	}
	keep(again, "1.3.0", "1.2.0")
	if err := again.forgetUpdate(); err != nil {
		t.Fatal(err)
	}
	keep(load(), "1.3.0", "")
	keep(load(), "1.2.0", "")
	keep(load(), "", "")
	values := readSettingsFile(t, path)
	if compact(t, values["version"]) != `"1.2.0"` || values["updatedFrom"] != nil || compact(t, values["shell"]) != `{"x":1}` {
		t.Fatalf("settings.json = %v", values)
	}
	// A version that is no version is not read.
	writeFile(t, path, `{"version": "1.2.0 и ещё"}`)
	if load().Version() != "" {
		t.Fatal("a version with spaces was read")
	}
}

func TestVersionsCompareByTheirNumbers(t *testing.T) {
	for _, pair := range [][2]string{{"1.3.0", "1.2.0"}, {"1.10.0", "1.9.9"}, {"v2.0.0", "1.99.0"}, {"1.3.0-rc.1", "1.2.9"}} {
		if !newerVersion(pair[0], pair[1]) || newerVersion(pair[1], pair[0]) {
			t.Fatalf("%s against %s", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{{"1.3.0", ""}, {"", "1.3.0"}, {"next", "1.3.0"}, {"1.3.0", "1.3.0"}} {
		if newerVersion(pair[0], pair[1]) {
			t.Fatalf("%q is newer than %q", pair[0], pair[1])
		}
	}
	if latestVersion("", "1.3.0") != "1.3.0" || latestVersion("1.3.0", "1.2.0") != "1.3.0" || latestVersion("dev", "1.2.0") != "1.2.0" {
		t.Fatal("the latest of two versions")
	}
}
