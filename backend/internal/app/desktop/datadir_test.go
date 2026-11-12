// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// specs/2026-11-12-desk-process.md PR2.1, PR2.3: on Windows the data of the author live in
// %LOCALAPPDATA%\ru.canonkeeper.desktop\data — the directory of the identifier of the app, which the uninstaller removes
// only when the author ticks «remove the data», never the directory the program is installed in; Linux and macOS keep
// their directories.
func TestTheDataOfTheAuthorLiveApartFromTheProgram(t *testing.T) {
	home := func() (string, error) { return "/home/author", nil }
	for _, test := range []struct {
		system      string
		environment map[string]string
		want        string
	}{
		{"windows", map[string]string{"LOCALAPPDATA": `C:\Users\Тест\AppData\Local`}, filepath.Join(`C:\Users\Тест\AppData\Local`, "ru.canonkeeper.desktop", "data")},
		{"linux", map[string]string{"XDG_DATA_HOME": "/srv/xdg"}, "/srv/xdg/canonkeeper"},
		{"linux", map[string]string{"XDG_DATA_HOME": "relative"}, "/home/author/.local/share/canonkeeper"},
		{"linux", nil, "/home/author/.local/share/canonkeeper"},
		{"darwin", nil, "/home/author/Library/Application Support/CanonKeeper"},
	} {
		got, err := defaultDataDir(test.system, func(name string) string { return test.environment[name] }, home)
		if err != nil || got != test.want {
			t.Errorf("%s %v = %q, %v; want %q", test.system, test.environment, got, err, test.want)
		}
	}
	if _, err := defaultDataDir("windows", func(string) string { return "" }, home); err == nil {
		t.Fatal("Windows without LOCALAPPDATA has a data directory")
	}
}

// PR2.2: a path the shell of Windows gives with the prefix \\?\ (the resource directory of Tauri does) loses it before
// any use — the programs of PostgreSQL do not find themselves by it; a network path \\?\UNC\… is refused.
func TestAnExtendedPathOfWindowsBecomesAPlainOne(t *testing.T) {
	for path, want := range map[string]string{
		`\\?\C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop\data`: `C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop\data`,
		`\\?\D:\Program Files\CanonKeeper`:                            `D:\Program Files\CanonKeeper`,
		`C:\Users\Тест\data`:                                          `C:\Users\Тест\data`,
		`/home/author/.local/share/canonkeeper`:                       `/home/author/.local/share/canonkeeper`,
		``:                                                            ``,
	} {
		if got, err := plainPath(path); err != nil || got != want {
			t.Errorf("plainPath(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
	for _, path := range []string{`\\?\UNC\server\share\data`, `\\?\unc\server\share`} {
		if got, err := plainPath(path); !errors.Is(err, errNetworkPath) || got != "" {
			t.Errorf("plainPath(%q) = %q, %v; want a refusal", path, got, err)
		}
	}
}

// PR2.2: a network path in start stops the backend before anything is made: the data directory with the reason of the
// data directory, the bundle with the reason of the bundle.
func TestANetworkPathInStartIsRefused(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	for _, test := range []struct {
		start  ControlMessage
		reason string
	}{
		{ControlMessage{Type: messageStart, DataDir: `\\?\UNC\server\share\data`, Resources: "/opt/canonkeeper"}, reasonDataDir},
		{ControlMessage{Type: messageStart, DataDir: data, Resources: `\\?\UNC\server\share\canonkeeper`}, reasonResources},
	} {
		line, _ := json.Marshal(test.start)
		var control bytes.Buffer
		if err := Run(context.Background(), Options{In: bytes.NewReader(append(line, '\n')), Control: &control}); err == nil {
			t.Fatalf("%+v started", test.start)
		}
		var fatal ControlMessage
		if json.Unmarshal(control.Bytes(), &fatal) != nil || fatal.Type != messageFatal || fatal.Reason != test.reason {
			t.Fatalf("%+v answered %q; want fatal %s", test.start, control.String(), test.reason)
		}
	}
	if _, err := os.Stat(data); err == nil {
		t.Fatal("the data directory was made before the bundle was checked")
	}
}
