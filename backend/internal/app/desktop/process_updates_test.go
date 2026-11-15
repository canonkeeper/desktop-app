// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// programWithFastUpdates is the program of the tree with a day of the daily check that lasts a second and a half
// (specs/2026-11-13-desk-updates.md UB1.2): the fake clock of the process tests.
var programWithFastUpdates = programVariant{name: "fast-updates", version: "1.2.0", fastUpdates: `// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import "time"

var (
	updateFirstCheck  = 300 * time.Millisecond
	updateDay         = 1500 * time.Millisecond
	updateLook        = 100 * time.Millisecond
	updateTurnsLook   = 200 * time.Millisecond
	updateTurnsWithin = 10 * time.Second
)
`}

// shellOfAnUpdate plays the shell after ready: it reads every line of the backend into a channel.
type shellOfAnUpdate struct {
	process *backend
	lines   chan ControlMessage
}

func playShell(process *backend) *shellOfAnUpdate {
	shell := &shellOfAnUpdate{process: process, lines: make(chan ControlMessage, 64)}
	go func() {
		defer close(shell.lines)
		for {
			line, err := process.process.Line(time.Minute)
			if err != nil {
				continue
			}
			if line == nil {
				return
			}
			var message ControlMessage
			if json.Unmarshal(line, &message) == nil {
				shell.lines <- message
			}
		}
	}()
	return shell
}

// during collects the messages of a kind the backend writes for a while; the shell answers a check with the phase.
func (shell *shellOfAnUpdate) during(kind string, wait time.Duration, answer *ControlMessage) []time.Time {
	var seen []time.Time
	deadline := time.After(wait)
	for {
		select {
		case message, open := <-shell.lines:
			if !open {
				return seen
			}
			if message.Type == kind {
				seen = append(seen, time.Now())
				if answer != nil {
					shell.process.send(*answer)
				}
			}
		case <-deadline:
			return seen
		}
	}
}

// next waits for the next message of a kind.
func (shell *shellOfAnUpdate) next(kind string, wait time.Duration) ControlMessage {
	shell.process.t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case message, open := <-shell.lines:
			if !open {
				shell.process.t.Fatalf("the backend ended before %s", kind)
			}
			if message.Type == kind {
				return message
			}
		case <-deadline:
			shell.process.t.Fatalf("no %s in %s", kind, wait)
		}
	}
}

func (browser *client) appUpdate() map[string]any {
	browser.t.Helper()
	var update map[string]any
	browser.read("/api/v1/desktop/update", &update)
	return update
}

func (browser *client) updateCommand(path string) (int, []byte) {
	browser.t.Helper()
	response, body := browser.do(http.MethodPost, "/api/v1/desktop/update/"+path, browser.csrf(), nil)
	return response.StatusCode, body
}

// UB1.1, UB1.2: the backend keeps the schedule and the setting: without the answer to «Добро пожаловать» and with the check
// off it asks nothing for days of the fake clock; «Проверить сейчас» at once; with the check on, once a day from the last
// check. What the shell reports is what the page reads. Only the windows of «nothing» are of the clock of the test, and a
// slow machine makes them only quieter.
func TestTheBackendChecksOnceADayOnlyAsTheAuthorChoseAndReadsThePhaseOfTheShell(t *testing.T) {
	_, resources := desktopBinary(t)
	binary := program(t, programWithFastUpdates)
	data := filepath.Join(t.TempDir(), "data")
	process := launchWith(t, binary, ControlMessage{
		Type: messageStart, DataDir: data, Resources: resources, SecretsStore: "file", Version: versionOfProgram(binary), UpdateKind: "appimage",
	})
	if process.ready.Type != messageReady {
		t.Fatalf("the backend answered %+v", process.ready)
	}
	window := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 60 * time.Second}}
	if response := window.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	shell := playShell(process)
	process.send(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "never"})
	latest := ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "latest"}
	if checks := shell.during(messageUpdateCheck, 3500*time.Millisecond, &latest); len(checks) != 0 {
		t.Fatalf("«Добро пожаловать» is not answered, and the backend checked %d times", len(checks))
	}
	if response, body := window.do(http.MethodPost, "/api/v1/desktop/welcome", window.csrf(), map[string]bool{"checkUpdates": false}); response.StatusCode != http.StatusNoContent {
		t.Fatalf("dismissWelcome = %d %s", response.StatusCode, body)
	}
	if checks := shell.during(messageUpdateCheck, 3500*time.Millisecond, &latest); len(checks) != 0 {
		t.Fatalf("the check is off, and the backend checked %d times", len(checks))
	}
	// «Проверить сейчас» asks at once, whatever the day: with the daily check off, the check that comes is this one.
	if status, body := window.updateCommand("check"); status != http.StatusOK {
		t.Fatalf("checkAppUpdate = %d %s", status, body)
	}
	shell.next(messageUpdateCheck, 15*time.Second)
	if status, body := window.updateCommand("check"); status != http.StatusConflict || !strings.Contains(string(body), "desktop.update_state") {
		t.Fatalf("a second check while one goes on = %d %s", status, body)
	}
	process.send(latest)
	waitPhase(t, window, "idle")
	// Once a day from the last check, by the times the backend keeps in settings.json — the day of the fake clock lasts a
	// second and a half —, however late the lines of the channel reach the shell.
	checked := []time.Time{checkedAfter(t, data, time.Time{})}
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/update/settings", window.csrf(), map[string]bool{"daily": true}); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAppUpdateSettings = %d %s", response.StatusCode, body)
	}
	for len(checked) < 3 {
		shell.next(messageUpdateCheck, 15*time.Second)
		checked = append(checked, checkedAfter(t, data, checked[len(checked)-1]))
		process.send(latest)
	}
	for at := 1; at < len(checked); at++ {
		if gap := checked[at].Sub(checked[at-1]); gap < 1500*time.Millisecond {
			t.Fatalf("two checks %s apart by the clock of the backend", gap)
		}
	}
	// A backend started again hears the phase from the shell after ready: here the shell tells a download.
	process.send(ControlMessage{Type: messageUpdateState, Phase: "downloading", Version: "1.3.0", Notes: "Что нового", Percent: 40})
	deadline := time.Now().Add(15 * time.Second)
	for {
		update := window.appUpdate()
		if update["phase"] == "downloading" && update["percent"] == float64(40) && update["kind"] == "appimage" {
			if available, _ := update["available"].(map[string]any); available["version"] != "1.3.0" {
				t.Fatalf("the update = %v", update)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the update = %v", update)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// checkedAfter is the time of the last check settings.json keeps once it is later than after.
func checkedAfter(t *testing.T, data string, after time.Time) time.Time {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var saved struct {
			Updates struct {
				Checked time.Time `json:"checked"`
			} `json:"updates"`
		}
		if text, err := os.ReadFile(DataDir{Root: data}.Settings()); err == nil && json.Unmarshal(text, &saved) == nil && saved.Updates.Checked.After(after) {
			return saved.Updates.Checked
		}
		if time.Now().After(deadline) {
			t.Fatalf("settings.json keeps no check after %s", after)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// UB2.1: the file is downloaded and checked first; a turn that began during the download ends before the backend asks to
// install; the step that finds no turn closes the taking of new ones — a question is then desktop.updating and stays —, and
// the stop for the installation cancels no turn. A cancel opens the taking again.
func TestAnUpdateWaitsForTheTurnsOfTheChatAndThenTakesNoNewOne(t *testing.T) {
	_, resources := desktopBinary(t)
	binary := program(t, programWithFastUpdates)
	data := filepath.Join(t.TempDir(), "data")
	provider := &providerOfTheTest{}
	stub := newProviderServer(t, provider)
	process := launchWith(t, binary, ControlMessage{
		Type: messageStart, DataDir: data, Resources: resources, SecretsStore: "file", Version: versionOfProgram(binary), UpdateKind: "deb",
	})
	if process.ready.Type != messageReady {
		t.Fatalf("the backend answered %+v", process.ready)
	}
	window := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 60 * time.Second}}
	if response := window.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	shell := playShell(process)
	chats := chatsWithTheHelper(t, window, stub, data)

	// A later version is found; «Обновить и перезапустить» asks the shell to download it — and a cancel stops it.
	process.send(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "available", Version: "1.3.0", Notes: "Что нового"})
	waitPhase(t, window, "idle")
	if status, body := window.updateCommand("install"); status != http.StatusOK {
		t.Fatalf("installAppUpdate = %d %s", status, body)
	}
	if asked := shell.next(messageUpdateDownload, 5*time.Second); asked.Version != "1.3.0" {
		t.Fatalf("update-download = %+v", asked)
	}
	if status, body := window.updateCommand("cancel"); status != http.StatusOK {
		t.Fatalf("cancelAppUpdate = %d %s", status, body)
	}
	if asked := shell.next(messageUpdateCancel, 5*time.Second); asked.Version != "1.3.0" {
		t.Fatalf("update-cancel = %+v", asked)
	}
	process.send(ControlMessage{Type: messageUpdateState, Phase: "idle", Found: "available", Version: "1.3.0", Notes: "Что нового"})
	waitPhase(t, window, "idle")

	// Again: a long turn begins during the download.
	if status, body := window.updateCommand("install"); status != http.StatusOK {
		t.Fatalf("installAppUpdate = %d %s", status, body)
	}
	shell.next(messageUpdateDownload, 5*time.Second)
	process.send(ControlMessage{Type: messageUpdateState, Phase: "downloading", Version: "1.3.0", Notes: "Что нового", Percent: 40})
	long, status, body := window.ask(chats[0], longQuestion, "the long question during the download")
	if status != http.StatusAccepted {
		t.Fatalf("a question during the download = %d %s", status, body)
	}
	provider.holding(t, 2*time.Minute)
	process.send(ControlMessage{Type: messageUpdateState, Phase: "downloaded", Version: "1.3.0", Notes: "Что нового"})
	waitPhase(t, window, "waiting_turn")
	// While it waits, new turns are taken — in another chat: a new question of the same chat would interrupt its turn.
	short, status, body := window.ask(chats[1], "Где застава?", "a short question during the wait")
	if status != http.StatusAccepted {
		t.Fatalf("a question during the wait = %d %s", status, body)
	}
	install := shell.next(messageUpdateInstall, 2*time.Minute)
	if install.Version != "1.3.0" {
		t.Fatalf("update-install = %+v", install)
	}
	for _, turn := range []string{long, short} {
		if ended := window.jobEnded(turn, 5*time.Second); ended != "succeeded" {
			var job map[string]any
			window.read("/api/v1/jobs/"+turn, &job)
			t.Fatalf("a turn begun before the installation = %s: %v", ended, job["error"])
		}
	}
	if update := window.appUpdate(); update["phase"] != "installing" {
		t.Fatalf("after update-install = %v", update)
	}
	if _, status, body := window.ask(chats[1], "А перевал?", "a question during the installation"); status != http.StatusConflict || !containsCode(body, "desktop.updating") {
		t.Fatalf("a question during the installation = %d %s", status, body)
	}
	if status, body := window.updateCommand("cancel"); status != http.StatusConflict {
		t.Fatalf("a cancel of an installation asked for = %d %s", status, body)
	}
	// The stop for the installation cancels no turn and ends cleanly: the shell installs after it.
	process.send(ControlMessage{Type: messageShutdown})
	process.wait(2 * time.Minute)
	logs, err := os.ReadDir(DataDir{Root: data}.Logs())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range logs {
		text, _ := os.ReadFile(filepath.Join(DataDir{Root: data}.Logs(), entry.Name()))
		if strings.Contains(string(text), "desktop_chat_turns_stopped") {
			t.Fatal("the stop for the installation canceled a turn")
		}
	}
}

// UB2.3: a backend that could not stop the cluster of its data directory exits with an error — the shell installs nothing
// after it. The postmaster.pid set aside makes pg_ctl stop fail at once while the server runs.
func TestABackendThatCouldNotStopItsClusterExitsWithAnError(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	process := startBackend(t, binary, resources, data, nil)
	pidFile := filepath.Join(data, "pg", "postmaster.pid")
	aside := pidFile + ".aside"
	if err := os.Rename(pidFile, aside); err != nil {
		t.Fatal(err)
	}
	// Registered after the backend, so it runs before its end, which stops the cluster by its postmaster.pid.
	t.Cleanup(func() { _ = os.Rename(aside, pidFile) })
	process.send(ControlMessage{Type: messageShutdown})
	exited := make(chan error, 1)
	go func() { exited <- process.command.Wait() }()
	select {
	case err := <-exited:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() == 0 {
			t.Fatalf("the backend that could not stop its cluster exited with %v", err)
		}
	case <-time.After(stopClusterTime + 30*time.Second):
		t.Fatal("the backend did not exit")
	}
	logs, err := os.ReadDir(DataDir{Root: data}.Logs())
	if err != nil {
		t.Fatal(err)
	}
	said := false
	for _, entry := range logs {
		text, _ := os.ReadFile(filepath.Join(DataDir{Root: data}.Logs(), entry.Name()))
		said = said || strings.Contains(string(text), "desktop_postgres_stop_failed")
	}
	if !said {
		t.Fatal("the log of the run does not say the cluster did not stop")
	}
}

func newProviderServer(t *testing.T, provider *providerOfTheTest) *httptest.Server {
	t.Helper()
	stub := httptest.NewServer(provider)
	t.Cleanup(stub.Close)
	return stub
}

// chatsWithTheHelper makes a saga with the book of «Северный предел», connects «Свой ИИ» at the stub and opens two chats.
func chatsWithTheHelper(t *testing.T, window *client, stub *httptest.Server, data string) [2]string {
	t.Helper()
	response, body := window.do(http.MethodPost, "/api/v1/projects", window.csrf(), map[string]string{"title": "Северный предел"})
	var project struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &project) != nil {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	window.importFile(project.ID, "Застава у перевала.md", northernBook)
	deadline := time.Now().Add(90 * time.Second)
	for {
		if waits, ok := window.waiting(project.ID); ok && waits == "helper" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the reading never waited for the helper")
		}
		time.Sleep(200 * time.Millisecond)
	}
	settings := map[string]any{"source": "own", "own": map[string]any{
		"preset": "custom", "baseUrl": stub.URL + "/v1", "protocol": "chat", "chatModel": "north-chat", "cardsModel": "north-cards",
		"advanced": map[string]any{"parallel": 2},
	}}
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), settings); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings = %d %s", response.StatusCode, body)
	}
	// The helper is connected: the reading waits for the consent once the text is indexed; the catch-up queues no cards Job
	// for a saga without a card (specs/2026-11-15-desk-reading-queue.md RQ2.1).
	readingSettled(t, data, project.ID, 90*time.Second)
	if waits, _ := window.waiting(project.ID); waits != "consent" {
		t.Fatalf("the reading with the helper waits for %q", waits)
	}
	var chats [2]string
	for at := range chats {
		response, body = window.post("/api/v1/projects/"+project.ID+"/chats", map[string]string{"sourceView": "published", "scope": "project"}, fmt.Sprintf("chat-of-the-update-%d", at))
		var chat struct {
			ID string `json:"id"`
		}
		if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &chat) != nil {
			t.Fatalf("createChatSession = %d %s", response.StatusCode, body)
		}
		chats[at] = chat.ID
	}
	return chats
}

// waitPhase waits for the phase the page reads.
func waitPhase(t *testing.T, window *client, phase string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		update := window.appUpdate()
		if update["phase"] == phase {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the update = %v; want %s", update, phase)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
