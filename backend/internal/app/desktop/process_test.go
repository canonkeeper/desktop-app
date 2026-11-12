// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// resourcesVariable names the directory of the bundle the process tests run the backend with: postgres/ (our build of
// PostgreSQL 18), codex/codex-app-server of the go.mod version and codex-homes/. Without it the tests are skipped.
const resourcesVariable = "CANONKEEPER_DESKTOP_RESOURCES"

// backend is the built cmd/desktop run as the shell runs it: a child with the control channel on its stdin and stdout.
type backend struct {
	t       *testing.T
	command *exec.Cmd
	stdin   io.WriteCloser
	lines   *bufio.Scanner
	data    string
	ready   ControlMessage
	took    time.Duration
}

func desktopBinary(t *testing.T) (binary, resources string) {
	t.Helper()
	resources = os.Getenv(resourcesVariable)
	if resources == "" {
		t.Skipf("%s is not set", resourcesVariable)
	}
	return program(t, programOfTheTree), resources
}

func startBackend(t *testing.T, binary, resources, data string, secrets map[string]string) *backend {
	t.Helper()
	process := launchBackend(t, binary, resources, data, secrets)
	if process.ready.Type != messageReady || process.ready.Port == 0 || process.ready.Code == "" {
		t.Fatalf("the backend answered %+v instead of ready", process.ready)
	}
	return process
}

// launchBackend starts the backend and keeps its first answer to start in ready, whether ready or fatal.
func launchBackend(t *testing.T, binary, resources, data string, secrets map[string]string) *backend {
	t.Helper()
	command := exec.Command(binary)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	started := time.Now()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	process := &backend{t: t, command: command, stdin: stdin, lines: bufio.NewScanner(stdout), data: data}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	process.send(ControlMessage{Type: messageStart, DataDir: data, Resources: resources, Secrets: secrets})
	process.ready = process.receive(60 * time.Second)
	process.took = time.Since(started)
	return process
}

func (process *backend) send(message ControlMessage) {
	process.t.Helper()
	line, _ := json.Marshal(message)
	if _, err := process.stdin.Write(append(line, '\n')); err != nil {
		process.t.Fatal(err)
	}
}

func (process *backend) receive(timeout time.Duration) ControlMessage {
	process.t.Helper()
	received := make(chan ControlMessage, 1)
	go func() {
		var message ControlMessage
		if process.lines.Scan() {
			_ = json.Unmarshal(process.lines.Bytes(), &message)
		}
		received <- message
	}()
	select {
	case message := <-received:
		return message
	case <-time.After(timeout):
		process.t.Fatalf("no control message in %s", timeout)
		return ControlMessage{}
	}
}

func (process *backend) origin() string {
	return "http://127.0.0.1:" + strconv.Itoa(process.ready.Port)
}

// wait waits for the backend to exit after the shell stopped or closed the channel.
func (process *backend) wait(timeout time.Duration) {
	process.t.Helper()
	done := make(chan error, 1)
	go func() { done <- process.command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			process.t.Fatalf("the backend exited with %v", err)
		}
	case <-time.After(timeout):
		process.t.Fatalf("the backend did not exit in %s", timeout)
	}
}

// client is a browser of the window: one cookie jar, the Origin of the window on every request.
type client struct {
	t      *testing.T
	origin string
	http   *http.Client
	cookie string
}

func (browser *client) do(method, path, csrf string, body any) (*http.Response, []byte) {
	browser.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, _ := http.NewRequest(method, browser.origin+path, reader)
	request.Header.Set("Origin", browser.origin)
	request.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	if browser.cookie != "" {
		request.AddCookie(&http.Cookie{Name: "ck_session", Value: browser.cookie})
	}
	response, err := browser.http.Do(request)
	if err != nil {
		browser.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response, raw
}

func (browser *client) csrf() string {
	browser.t.Helper()
	response, body := browser.do(http.MethodGet, "/api/v1/auth/csrf", "", nil)
	var token struct {
		CSRFToken string `json:"csrfToken"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &token) != nil || token.CSRFToken == "" {
		browser.t.Fatalf("csrf = %d %s", response.StatusCode, body)
	}
	return token.CSRFToken
}

// open exchanges a code for the session of the window (DA3.3).
func (browser *client) open(code string) *http.Response {
	browser.t.Helper()
	response, body := browser.do(http.MethodPost, "/api/v1/desktop/session", browser.csrf(), map[string]string{"code": code})
	for _, cookie := range response.Cookies() {
		if cookie.Name == "ck_session" {
			browser.cookie = cookie.Value
		}
	}
	if response.StatusCode == http.StatusOK && !strings.Contains(string(body), "local@desktop.invalid") {
		browser.t.Fatalf("the session names %s", body)
	}
	return response
}

// database connects to the cluster of the data directory as the product.
func database(t *testing.T, data string) *pgx.Conn {
	t.Helper()
	cluster := runningCluster(t, data)
	connection, err := pgx.Connect(context.Background(), cluster.url(productRole, cluster.secrets.Product, databaseName, url.Values{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close(context.Background()) })
	return connection
}

// runningCluster is the cluster a backend runs in the data directory: its passwords and how this run listens.
func runningCluster(t *testing.T, data string) *Cluster {
	t.Helper()
	secrets, err := loadClusterSecrets(DataDir{Root: data}.Secret())
	if err != nil {
		t.Fatal(err)
	}
	settings, err := os.ReadFile(filepath.Join(data, "pg", runSettings))
	if err != nil {
		t.Fatal(err)
	}
	cluster := &Cluster{data: DataDir{Root: data}, secrets: secrets}
	for _, line := range strings.Split(string(settings), "\n") {
		name, value, _ := strings.Cut(line, " = ")
		value = strings.Trim(value, "'")
		switch name {
		case "unix_socket_directories":
			if value != "" {
				cluster.host = value
			}
		case "listen_addresses":
			if value != "" {
				cluster.host = value
			}
		case "port":
			cluster.port, _ = strconv.Atoi(value)
		}
	}
	return cluster
}

// family are the processes of a run: the backend, its descendants (Codex) and the PostgreSQL of its data directory,
// which pg_ctl started apart from the backend. Linux only: it reads /proc.
func family(t *testing.T, pid int, data string) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	parents := map[int]int{}
	var members []int
	for _, entry := range entries {
		child, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
		parents[child], _ = strconv.Atoi(fields[1])
		if cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline")); err == nil && strings.Contains(string(cmdline), data) {
			members = append(members, child)
		}
	}
	for child := range parents {
		for ancestor := parents[child]; ancestor > 1; ancestor = parents[ancestor] {
			if ancestor == pid {
				members = append(members, child)
				break
			}
		}
	}
	if pid > 0 {
		members = append(members, pid)
	}
	return members
}

// listening are the TCP addresses a process listens on (Linux: its socket inodes in /proc/net/tcp and tcp6).
func listening(t *testing.T, pid int) []string {
	t.Helper()
	inodes := map[string]bool{}
	descriptors, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(pid), "fd"))
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "fd", descriptor.Name()))
		if err == nil && strings.HasPrefix(target, "socket:[") {
			inodes[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = true
		}
	}
	var addresses []string
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		raw, err := os.ReadFile(table)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" || !inodes[fields[9]] {
				continue
			}
			host, port, _ := strings.Cut(fields[1], ":")
			number, _ := strconv.ParseInt(port, 16, 32)
			if len(host) == 8 {
				value, _ := strconv.ParseUint(host, 16, 32)
				host = fmt.Sprintf("%d.%d.%d.%d", value&0xff, value>>8&0xff, value>>16&0xff, value>>24)
			}
			addresses = append(addresses, host+":"+strconv.FormatInt(number, 10))
		}
	}
	return addresses
}

// specs/2026-11-07-desktop-app.md DA1.3, DA1.6, DA1.9, DA3.1–DA3.4, DA3.6: a cold start on an empty data directory with
// Cyrillic in a path too long for a socket prepares the cluster by DA1.3 and is ready within 30 s; the backend listens
// only on 127.0.0.1:P; the code and the keys are in no argument or environment of its processes; a code opens the window
// once; a closed stdin stops the backend, Codex and PostgreSQL and leaves no process.
func TestAColdStartServesTheWindowAndAClosedChannelLeavesNoProcess(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "Данные автора", strings.Repeat("длинный-каталог-", 4))
	const key = "provider-key-of-the-author-for-the-test-0000"
	process := startBackend(t, binary, resources, data, map[string]string{"openai": key})
	if process.took > 30*time.Second {
		t.Fatalf("the first start took %s, over the 30 s of DA1.9", process.took)
	}
	t.Logf("first start to ready: %s", process.took)

	connection := database(t, data)
	var lower, stem, provider, locale, collate, ctype, vector string
	if err := connection.QueryRow(context.Background(), `SELECT lower('ЁЖ'), ts_lexize('russian_stem', 'кутузова')::text,
		to_tsvector('russian', '«Война и мир» — роман…')::text`).Scan(&lower, &stem, &vector); err != nil {
		t.Fatal(err)
	}
	if err := connection.QueryRow(context.Background(), `SELECT datlocprovider::text, datlocale, datcollate, datctype
		FROM pg_database WHERE datname = current_database()`).Scan(&provider, &locale, &collate, &ctype); err != nil {
		t.Fatal(err)
	}
	if lower != "ёж" || stem != "{кутузов}" || strings.ContainsAny(vector, "«»—…") {
		t.Fatalf("lower = %q, stem = %q, vector = %q", lower, stem, vector)
	}
	if provider != "b" || locale != "C.UTF-8" || collate != "C" || ctype != systemUTF8Locale() {
		t.Fatalf("database locale = %s %s %s %s", provider, locale, collate, ctype)
	}
	_ = connection.Close(context.Background())

	browser := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	response := browser.open(process.ready.Code)
	if response.StatusCode != http.StatusOK || browser.cookie == "" {
		t.Fatalf("the first code = %d", response.StatusCode)
	}
	cookie := response.Header.Get("Set-Cookie")
	if !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Strict") || strings.Contains(cookie, "Secure") ||
		strings.Contains(cookie, "Expires") || strings.Contains(cookie, "Max-Age") {
		t.Fatalf("the cookie of the window = %q", cookie)
	}
	if response.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("an answer without the headers of the page")
	}
	stranger := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	if response := stranger.open(process.ready.Code); response.StatusCode != http.StatusForbidden {
		t.Fatalf("the code used twice = %d", response.StatusCode)
	}
	process.send(ControlMessage{Type: messageOpenCode})
	next := process.receive(10 * time.Second)
	if next.Type != messageOpenCode || next.Code == "" || next.Code == process.ready.Code {
		t.Fatalf("open-code = %+v", next)
	}
	if response := stranger.open(next.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("the next code = %d", response.StatusCode)
	}
	if response, _ := browser.do(http.MethodGet, "/api/v1/projects", "", nil); response.StatusCode != http.StatusOK {
		t.Fatalf("the projects of the window = %d", response.StatusCode)
	}

	pid := process.command.Process.Pid
	if runtime.GOOS == "linux" {
		if addresses := listening(t, pid); len(addresses) != 1 || addresses[0] != fmt.Sprintf("127.0.0.1:%d", process.ready.Port) {
			t.Fatalf("the backend listens on %v", addresses)
		}
		members := family(t, pid, data)
		if len(members) < 3 {
			t.Fatalf("the processes of the run = %v; want the backend, Codex and PostgreSQL", members)
		}
		for _, member := range members {
			for _, file := range []string{"cmdline", "environ"} {
				raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(member), file))
				if err != nil {
					continue
				}
				if bytes.Contains(raw, []byte(key)) || bytes.Contains(raw, []byte(process.ready.Code)) || bytes.Contains(raw, []byte(next.Code)) {
					t.Fatalf("/proc/%d/%s holds a code or a key", member, file)
				}
			}
		}
	}

	_ = process.stdin.Close()
	process.wait(60 * time.Second)
	if _, err := os.Stat(filepath.Join(data, "pg", "postmaster.pid")); err == nil {
		t.Fatal("PostgreSQL is still running after the backend stopped")
	}
	if runtime.GOOS == "linux" {
		if left := family(t, 0, data); len(left) != 0 {
			t.Fatalf("processes left after the stop: %v", left)
		}
	}
}

// DA1.6, DA1.7: a backend killed mid-run leaves PostgreSQL running; the next start stops it by the postmaster.pid of its
// cluster, starts its own and finds what the author made, and a shutdown stops everything.
func TestAKilledBackendLeavesNothingTheNextStartCannotTakeOver(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	first := startBackend(t, binary, resources, data, nil)
	browser := &client{t: t, origin: first.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	if response := browser.open(first.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	if response, body := browser.do(http.MethodPost, "/api/v1/projects", browser.csrf(), map[string]string{"title": "Северный предел"}); response.StatusCode != http.StatusCreated {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	pidFile := filepath.Join(data, "pg", "postmaster.pid")
	before, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = first.command.Wait()
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatal("PostgreSQL did not outlive the killed backend; the test proves nothing")
	}

	second := startBackend(t, binary, resources, data, nil)
	after, err := os.ReadFile(pidFile)
	if err != nil || strings.Split(string(after), "\n")[0] == strings.Split(string(before), "\n")[0] {
		t.Fatalf("the cluster left by the crash was not replaced: %v", err)
	}
	browser = &client{t: t, origin: second.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	if response := browser.open(second.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open after the crash = %d", response.StatusCode)
	}
	if response, body := browser.do(http.MethodGet, "/api/v1/projects", "", nil); response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Северный предел") {
		t.Fatalf("the saga after the crash = %d %s", response.StatusCode, body)
	}
	second.send(ControlMessage{Type: messageShutdown})
	second.wait(60 * time.Second)
	if _, err := os.Stat(pidFile); err == nil {
		t.Fatal("PostgreSQL is still running after shutdown")
	}
	if runtime.GOOS == "linux" {
		if left := family(t, 0, data); len(left) != 0 {
			t.Fatalf("processes left after shutdown: %v", left)
		}
	}
}
