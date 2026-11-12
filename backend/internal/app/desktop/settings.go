// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// The port of the window (specs/2026-11-12-desk-process.md PR1): the page keeps the place of reading, the theme and
// what is not saved in the storage of its origin, and the origin holds the port, so the backend listens on the same
// port from run to run. The first port is random of 20000–29999 — under the dynamic ports of Windows (49152+) and Linux
// (32768+), and two users of one computer get different ones.
const (
	firstWindowPort = 20000
	lastWindowPort  = 29999
	// windowPortTries new random ports are tried when the remembered one is taken.
	windowPortTries = 20
	// windowPortRetry is the wait before the remembered port is tried again: the backend of the previous run may still
	// be closing it.
	windowPortRetry = 2 * time.Second
	// settingsPort is the key of settings.json the backend owns; the shell and later slices keep their keys beside it.
	settingsPort = "port"
)

// settings is settings.json of the data directory: the settings of the app that live with the data of the author.
type settings struct {
	path   string
	values map[string]json.RawMessage
	// port is the remembered port of the window, 0 when there is none; lost says the address the window had cannot be
	// told: the file was broken or unreadable, or its port was not a port.
	port int
	lost bool
}

// loadSettings reads settings.json; there is none at the first start. A file that cannot be read or is not a JSON
// object does not stop the start (PR1.4): it is put aside as settings.json.broken-<UTC time> as it was, and the start
// goes on with new settings.
func loadSettings(path string, now time.Time) (*settings, error) {
	current := &settings{path: path, values: map[string]json.RawMessage{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return current, nil
	}
	if err != nil || json.Unmarshal(raw, &current.values) != nil || current.values == nil {
		current.values, current.lost = map[string]json.RawMessage{}, true
		return current, os.Rename(path, path+".broken-"+now.UTC().Format("20060102T150405Z"))
	}
	if remembered, ok := current.values[settingsPort]; ok {
		port, err := strconv.Atoi(string(remembered))
		if err != nil || port < 1 || port > 65535 {
			current.lost = true
			return current, nil
		}
		current.port = port
	}
	return current, nil
}

// keep remembers the port the window listens on (PR1.1) and says whether the address of the window changed — the page
// then finds the storage of its origin empty (PR1.2, PR1.4). The file is replaced whole: written beside it, then renamed
// over it, so a stop in the middle leaves the old file or the new one; the keys the backend does not know stay.
func (current *settings) keep(port int) (bool, error) {
	changed := current.lost || current.port != 0 && current.port != port
	if current.port == port && !current.lost {
		return false, nil
	}
	values := make(map[string]json.RawMessage, len(current.values)+1)
	for key, value := range current.values {
		values[key] = value
	}
	values[settingsPort] = json.RawMessage(strconv.Itoa(port))
	encoded, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return changed, err
	}
	if err := replaceFile(current.path, append(encoded, '\n')); err != nil {
		return changed, err
	}
	current.values, current.port, current.lost = values, port, false
	return changed, nil
}

// replaceFile writes a file of the user alone (0600) beside path and renames it over path.
func replaceFile(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	written := false
	defer func() {
		if !written {
			_ = os.Remove(file.Name())
		}
	}()
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	written = true
	return nil
}

// windowPorts are how the window gets its listener: listen on a port of 127.0.0.1, a random port of the range, a wait.
type windowPorts struct {
	listen func(port int) (net.Listener, error)
	random func() int
	wait   func(time.Duration)
}

// systemWindowPorts listen only on 127.0.0.1 (PR1.3, DA3.1): never 0.0.0.0, :: or localhost.
var systemWindowPorts = windowPorts{
	listen: func(port int) (net.Listener, error) {
		return net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	},
	random: func() int { return firstWindowPort + rand.IntN(lastWindowPort-firstWindowPort+1) },
	wait:   time.Sleep,
}

// open listens on the remembered port, on it again after windowPortRetry when it is busy or the system refused it, then
// on up to windowPortTries random ports (PR1.2); without a remembered port it starts with the random ones. When every
// attempt fails, the error is the listener's last.
func (ports windowPorts) open(remembered int) (net.Listener, error) {
	var err error
	if remembered != 0 {
		var listener net.Listener
		if listener, err = ports.listen(remembered); err == nil {
			return listener, nil
		}
		ports.wait(windowPortRetry)
		if listener, err = ports.listen(remembered); err == nil {
			return listener, nil
		}
	}
	for range windowPortTries {
		var listener net.Listener
		if listener, err = ports.listen(ports.random()); err == nil {
			return listener, nil
		}
	}
	return nil, err
}
