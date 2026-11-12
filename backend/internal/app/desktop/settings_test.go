// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

var settingsNow = time.Date(2026, 11, 12, 20, 35, 12, 0, time.UTC)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readSettingsFile(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatalf("settings.json = %q: %v", raw, err)
	}
	return values
}

func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// specs/2026-11-12-desk-process.md PR1.1: settings.json remembers the port of the window; writing it keeps every key the
// backend does not know, the file is 0600 and is replaced whole — no temporary file is left beside it.
func TestTheSettingsKeepTheKeysTheBackendDoesNotKnow(t *testing.T) {
	data := DataDir{Root: t.TempDir()}
	writeFile(t, data.Settings(), `{"port": 21000, "shell": {"window": [800, 600]}, "providers": [{"id": "openai"}]}`)
	saved, err := loadSettings(data.Settings(), settingsNow)
	if err != nil {
		t.Fatal(err)
	}
	if saved.port != 21000 || saved.lost {
		t.Fatalf("the remembered port = %d, lost %v", saved.port, saved.lost)
	}
	changed, err := saved.keep(21000)
	if err != nil || changed {
		t.Fatalf("keeping the same port: changed %v, %v", changed, err)
	}
	changed, err = saved.keep(23456)
	if err != nil || !changed {
		t.Fatalf("keeping a new port: changed %v, %v", changed, err)
	}
	values := readSettingsFile(t, data.Settings())
	if compact(t, values["port"]) != "23456" || compact(t, values["shell"]) != `{"window":[800,600]}` ||
		compact(t, values["providers"]) != `[{"id":"openai"}]` || len(values) != 3 {
		t.Fatalf("settings.json after the new port = %v", values)
	}
	if info, err := os.Stat(data.Settings()); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("settings.json mode = %v, %v", info.Mode(), err)
	}
	entries, _ := os.ReadDir(data.Root)
	if len(entries) != 1 {
		t.Fatalf("the data directory after the write holds %v", entries)
	}
}

// PR1.1: the first start has no settings.json and no remembered port; remembering the port makes the file.
func TestTheFirstStartHasNoRememberedPort(t *testing.T) {
	data := DataDir{Root: t.TempDir()}
	saved, err := loadSettings(data.Settings(), settingsNow)
	if err != nil || saved.port != 0 || saved.lost {
		t.Fatalf("no settings.json = %+v, %v", saved, err)
	}
	if changed, err := saved.keep(24680); err != nil || changed {
		t.Fatalf("the first port: changed %v, %v", changed, err)
	}
	if values := readSettingsFile(t, data.Settings()); compact(t, values["port"]) != "24680" || len(values) != 1 {
		t.Fatalf("settings.json of the first start = %v", values)
	}
}

// PR1.4: a settings.json that is not a JSON object or cannot be read does not stop the start: it is put aside as
// settings.json.broken-<time> as it was, the start goes on with new settings, and the address of the window is new.
// A port that is not a port loses the address too, but the file and its other keys stay.
func TestABrokenSettingsFileIsPutAsideAndTheStartGoesOn(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON": `{"port": 21000`, "not an object": `[21000]`, "empty": ``, "a string": `"port"`,
	} {
		t.Run(name, func(t *testing.T) {
			data := DataDir{Root: t.TempDir()}
			writeFile(t, data.Settings(), content)
			saved, err := loadSettings(data.Settings(), settingsNow)
			if err != nil || saved.port != 0 || !saved.lost {
				t.Fatalf("a broken settings.json = %+v, %v", saved, err)
			}
			aside := data.Settings() + ".broken-20261112T203512Z"
			if raw, err := os.ReadFile(aside); err != nil || string(raw) != content {
				t.Fatalf("the broken file put aside = %q, %v", raw, err)
			}
			if changed, err := saved.keep(25000); err != nil || !changed {
				t.Fatalf("the port after a broken file: changed %v, %v", changed, err)
			}
			if values := readSettingsFile(t, data.Settings()); compact(t, values["port"]) != "25000" || len(values) != 1 {
				t.Fatalf("settings.json after a broken file = %v", values)
			}
		})
	}
	t.Run("unreadable", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("the mode of a file does not stop its owner here")
		}
		data := DataDir{Root: t.TempDir()}
		writeFile(t, data.Settings(), `{"port": 21000}`)
		if err := os.Chmod(data.Settings(), 0); err != nil {
			t.Fatal(err)
		}
		saved, err := loadSettings(data.Settings(), settingsNow)
		if err != nil || saved.port != 0 || !saved.lost {
			t.Fatalf("an unreadable settings.json = %+v, %v", saved, err)
		}
		if _, err := os.Stat(data.Settings() + ".broken-20261112T203512Z"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("not a port", func(t *testing.T) {
		data := DataDir{Root: t.TempDir()}
		writeFile(t, data.Settings(), `{"port": "21000", "shell": true}`)
		saved, err := loadSettings(data.Settings(), settingsNow)
		if err != nil || saved.port != 0 || !saved.lost {
			t.Fatalf("a port that is not a port = %+v, %v", saved, err)
		}
		if changed, err := saved.keep(26000); err != nil || !changed {
			t.Fatalf("changed %v, %v", changed, err)
		}
		if values := readSettingsFile(t, data.Settings()); compact(t, values["port"]) != "26000" || compact(t, values["shell"]) != "true" {
			t.Fatalf("settings.json = %v", values)
		}
	})
}

// fakePorts are the system the window listens on: busy ports refuse as many times as they are counted (-1 — always),
// every attempt and every wait is recorded.
type fakePorts struct {
	busy     map[int]int
	random   []int
	attempts []int
	waits    []time.Duration
}

type fakeListener struct {
	net.Listener
	port int
}

func (ports *fakePorts) system() windowPorts {
	return windowPorts{
		listen: func(port int) (net.Listener, error) {
			ports.attempts = append(ports.attempts, port)
			if left := ports.busy[port]; left != 0 {
				ports.busy[port] = left - 1
				return nil, errors.New("bind: address already in use")
			}
			return fakeListener{port: port}, nil
		},
		random: func() int {
			next := ports.random[0]
			ports.random = ports.random[1:]
			return next
		},
		wait: func(duration time.Duration) { ports.waits = append(ports.waits, duration) },
	}
}

// PR1.1, PR1.2: the window listens on the remembered port; a busy one is tried once more after 2 s — the backend of the
// previous run may still be closing it — then new random ports of 20000–29999, at most 20; the first start takes a
// random port at once; when every attempt fails the error of the listener says why.
func TestTheWindowListensOnTheRememberedPortOrANewOne(t *testing.T) {
	for _, test := range []struct {
		name       string
		remembered int
		busy       map[int]int
		random     []int
		port       int
		attempts   []int
		waits      int
	}{
		{name: "first start", random: []int{25000}, port: 25000, attempts: []int{25000}},
		{name: "first start on a busy port", busy: map[int]int{25000: -1}, random: []int{25000, 26000}, port: 26000, attempts: []int{25000, 26000}},
		{name: "remembered", remembered: 21000, port: 21000, attempts: []int{21000}},
		{name: "remembered, free after 2 s", remembered: 21000, busy: map[int]int{21000: 1}, port: 21000, attempts: []int{21000, 21000}, waits: 1},
		{name: "remembered, taken", remembered: 21000, busy: map[int]int{21000: -1}, random: []int{22000}, port: 22000, attempts: []int{21000, 21000, 22000}, waits: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.busy == nil {
				test.busy = map[int]int{}
			}
			ports := &fakePorts{busy: test.busy, random: test.random}
			listener, err := ports.system().open(test.remembered)
			if err != nil || listener.(fakeListener).port != test.port {
				t.Fatalf("open = %v, %v; want %d", listener, err, test.port)
			}
			if !slices.Equal(ports.attempts, test.attempts) || len(ports.waits) != test.waits {
				t.Fatalf("attempts %v, waits %v; want %v and %d", ports.attempts, ports.waits, test.attempts, test.waits)
			}
			for _, wait := range ports.waits {
				if wait != 2*time.Second {
					t.Fatalf("waited %s", wait)
				}
			}
		})
	}
	random := make([]int, 20)
	busy := map[int]int{21000: -1}
	for index := range random {
		random[index] = 22000 + index
		busy[random[index]] = -1
	}
	ports := &fakePorts{busy: busy, random: random}
	if listener, err := ports.system().open(21000); err == nil || listener != nil || len(ports.attempts) != 22 {
		t.Fatalf("every port busy = %v, %v after %d attempts", listener, err, len(ports.attempts))
	}
}

// PR1.1: the random ports are those of 20000–29999 — under the dynamic ports of Windows and Linux.
func TestTheRandomPortOfTheWindowIsUnderTheDynamicPorts(t *testing.T) {
	for range 1000 {
		if port := systemWindowPorts.random(); port < 20000 || port > 29999 {
			t.Fatalf("random port %d", port)
		}
	}
	if filepath.Base(DataDir{Root: "/data"}.Settings()) != "settings.json" {
		t.Fatal("the settings are not settings.json of the data directory")
	}
}
