// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// stop stops the backend the way the shell does and waits for it to exit.
func (process *backend) stop() {
	process.t.Helper()
	process.send(ControlMessage{Type: messageShutdown})
	process.wait(60 * time.Second)
}

// exited waits for a backend that sent fatal to exit; it exits with an error.
func (process *backend) exited(timeout time.Duration) {
	process.t.Helper()
	done := make(chan error, 1)
	go func() { done <- process.command.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			process.t.Fatal("the backend that sent fatal exited without an error")
		}
	case <-time.After(timeout):
		process.t.Fatalf("the backend did not exit in %s after fatal", timeout)
	}
}

// rememberedPort is the port settings.json of the data directory remembers.
func rememberedPort(t *testing.T, data string) (int, map[string]json.RawMessage) {
	t.Helper()
	values := readSettingsFile(t, DataDir{Root: data}.Settings())
	port, err := strconv.Atoi(string(values["port"]))
	if err != nil {
		t.Fatalf("settings.json port = %s", values["port"])
	}
	return port, values
}

// hostRequest asks the backend on its port with the Host header given.
func hostRequest(t *testing.T, port int, host string) int {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/healthz", nil)
	request.Host = host
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}

// specs/2026-11-12-desk-process.md PR1.1–PR1.4: the first start listens on a random port of 20000–29999 and remembers
// it in settings.json; the next start on the same data directory opens the window on the same address. A port a stranger
// took is given up after one more try 2 s later: the new port is remembered, ready says portChanged and the Host of the
// old address gets 421. Keys of settings.json the backend does not know live through the new port; a broken file is put
// aside and the start goes on with a new address.
func TestTheWindowKeepsItsAddressFromRunToRun(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")

	first := startBackend(t, binary, resources, data, nil)
	port := first.ready.Port
	if port < 20000 || port > 29999 || first.ready.PortChanged {
		t.Fatalf("the first start = port %d, portChanged %v", port, first.ready.PortChanged)
	}
	if remembered, _ := rememberedPort(t, data); remembered != port {
		t.Fatalf("settings.json remembers %d, the window is on %d", remembered, port)
	}
	if info, err := os.Stat(DataDir{Root: data}.Settings()); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("settings.json = %v, %v", info, err)
	}
	if runtime.GOOS == "linux" {
		if addresses := listening(t, first.command.Process.Pid); len(addresses) != 1 || addresses[0] != "127.0.0.1:"+strconv.Itoa(port) {
			t.Fatalf("the backend listens on %v", addresses)
		}
	}
	first.stop()

	_, values := rememberedPort(t, data)
	values["shell"] = json.RawMessage(`{"window":[800,600]}`)
	encoded, _ := json.Marshal(values)
	if err := os.WriteFile(DataDir{Root: data}.Settings(), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	second := startBackend(t, binary, resources, data, nil)
	if second.ready.Port != port || second.ready.PortChanged {
		t.Fatalf("the second start = port %d, portChanged %v; want %d", second.ready.Port, second.ready.PortChanged, port)
	}
	second.stop()

	stranger, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("the port of the stopped backend is not free: %v", err)
	}
	defer stranger.Close()
	third := startBackend(t, binary, resources, data, nil)
	moved := third.ready.Port
	if moved == port || moved < 20000 || moved > 29999 || !third.ready.PortChanged {
		t.Fatalf("a taken port = port %d, portChanged %v", moved, third.ready.PortChanged)
	}
	if third.took < 2*time.Second {
		t.Fatalf("the backend gave up the remembered port in %s, before trying it again", third.took)
	}
	remembered, values := rememberedPort(t, data)
	if remembered != moved || compact(t, values["shell"]) != `{"window":[800,600]}` {
		t.Fatalf("settings.json after the new port = %v", values)
	}
	if code := hostRequest(t, moved, "127.0.0.1:"+strconv.Itoa(port)); code != http.StatusMisdirectedRequest {
		t.Fatalf("the Host of the old address = %d", code)
	}
	if code := hostRequest(t, moved, "127.0.0.1:"+strconv.Itoa(moved)); code != http.StatusOK {
		t.Fatalf("the Host of the new address = %d", code)
	}
	third.stop()
	_ = stranger.Close()

	writeFile(t, DataDir{Root: data}.Settings(), `{"port": `)
	fourth := startBackend(t, binary, resources, data, nil)
	if !fourth.ready.PortChanged || fourth.ready.Port < 20000 || fourth.ready.Port > 29999 {
		t.Fatalf("a broken settings.json = port %d, portChanged %v", fourth.ready.Port, fourth.ready.PortChanged)
	}
	if remembered, _ := rememberedPort(t, data); remembered != fourth.ready.Port {
		t.Fatalf("settings.json after a broken one remembers %d, the window is on %d", remembered, fourth.ready.Port)
	}
	entries, _ := os.ReadDir(data)
	aside := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "settings.json.broken-") {
			aside++
			if raw, _ := os.ReadFile(filepath.Join(data, entry.Name())); string(raw) != `{"port": ` {
				t.Fatalf("the broken file put aside = %q", raw)
			}
		}
	}
	if aside != 1 {
		t.Fatalf("broken files put aside: %d", aside)
	}
	fourth.stop()
}
