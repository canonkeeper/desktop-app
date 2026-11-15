// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/delivery/mcp/door"
	"canonkeeper/backend/internal/delivery/mcp/programtools"
	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/services/programreads"
	researchservice "canonkeeper/backend/internal/services/researchchat"
)

// programDoorOfTheTest is the door for the programs of a build composed in a test: closed, over a data directory of the test.
func programDoorOfTheTest(t *testing.T) *programDoor {
	t.Helper()
	return newProgramDoor(t.TempDir(), log.New(io.Discard, "", 0))
}

// answersOfTheTest is a door that answers nothing of a saga: the tests of the place of the door do not call it.
func answersOfTheTest() *door.Door {
	return door.New(noReads{}, func(string, researchservice.Naming) door.Handler { return nil }, "1.4.0")
}

type noReads struct{}

func (noReads) Sagas(context.Context) ([]programreads.Saga, error) { return nil, nil }
func (noReads) Find(context.Context, string) (programreads.Saga, error) {
	return programreads.Saga{}, programreads.ErrNoSagas
}
func (noReads) Record(context.Context, programreads.Read) error { return nil }

func doorFileOf(t *testing.T, dataDir string) (programtools.DoorFile, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dataDir, programtools.DoorFileName))
	if errors.Is(err, os.ErrNotExist) {
		return programtools.DoorFile{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var file programtools.DoorFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	return file, true
}

func isSocket(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

// specs/2026-11-15-desk-mcp.md MC1.6: the door opens on mcp.sock of the data directory, names it in mcp.json after it listens,
// and takes both away when it closes; a data directory whose socket path is longer than 103 bytes opens it in a directory
// ck-mcp-* of its own, taken away with it; on Windows the socket is a file of .canonkeeper in the home, out of AppData.
func TestTheDoorOpensOnItsSocketAndTakesItAway(t *testing.T) {
	short := t.TempDir()
	long := filepath.Join(t.TempDir(), strings.Repeat("каталог-данных-", 6))
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	for name, place := range map[string]struct {
		dataDir string
		goos    string
		check   func(socket string) bool
	}{
		"short": {short, "linux", func(socket string) bool { return socket == filepath.Join(short, "mcp.sock") }},
		"long":  {long, "linux", func(socket string) bool { return strings.HasPrefix(filepath.Base(filepath.Dir(socket)), "ck-mcp-") }},
		"windows": {short, "windows", func(socket string) bool {
			return filepath.Dir(socket) == filepath.Join(home, ".canonkeeper") && len(filepath.Base(socket)) == 13
		}},
	} {
		doors := newProgramDoor(place.dataDir, log.New(io.Discard, "", 0))
		doors.goos, doors.home = place.goos, func() (string, error) { return home, nil }
		doors.use(answersOfTheTest())
		if err := doors.Open(context.Background()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		file, ok := doorFileOf(t, place.dataDir)
		if !ok || !place.check(file.Socket) || !isSocket(file.Socket) || file.Fingerprint != programtools.Fingerprint() || file.Version != "1.4.0" {
			t.Fatalf("%s: mcp.json %+v", name, file)
		}
		if conn, err := net.Dial("unix", file.Socket); err != nil {
			t.Fatalf("%s: the socket does not take a call: %v", name, err)
		} else {
			_ = conn.Close()
		}
		if state := doors.State(); !state.Open || state.Failed {
			t.Fatalf("%s: %+v", name, state)
		}
		doors.Close()
		if _, ok := doorFileOf(t, place.dataDir); ok || isSocket(file.Socket) {
			t.Fatalf("%s: the door left its file or its socket", name)
		}
		if name != "short" {
			if _, err := os.Stat(filepath.Dir(file.Socket)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s: the directory of the socket stayed", name)
			}
		}
		if state := doors.State(); state.Open {
			t.Fatalf("%s: closed and open", name)
		}
	}
}

// MC1.6, MC3.1: mcp.json that cannot be written leaves no door — no listener, no file, a state that says so, and one line of
// the log of the run.
func TestADoorWithoutItsFileIsNoDoor(t *testing.T) {
	dataDir := t.TempDir()
	var journal bytes.Buffer
	doors := newProgramDoor(dataDir, log.New(&journal, "", 0))
	doors.write = func(string, []byte) error { return errors.New("the disk is full") }
	doors.use(answersOfTheTest())
	if err := doors.Open(context.Background()); err == nil {
		t.Fatal("a door without its file opened")
	}
	if state := doors.State(); state.Open || !state.Failed {
		t.Fatalf("the state: %+v", state)
	}
	if isSocket(filepath.Join(dataDir, "mcp.sock")) {
		t.Fatal("the listener stayed")
	}
	if _, ok := doorFileOf(t, dataDir); ok {
		t.Fatal("mcp.json lies")
	}
	// One line of the log of the run with the code of the reason, without a path (MC3.1).
	if lines := strings.Split(strings.TrimSpace(journal.String()), "\n"); len(lines) != 1 || lines[0] != `{"event":"desktop_program_door_not_opened","reason":"file"}` {
		t.Fatalf("the log of the run: %q", journal.String())
	}
}

// MC1.6: a start removes what a crash left — the socket of mcp.json and its empty ck-mcp-* — and leaves a file of another kind
// and a directory with anything in it.
func TestTheStartRemovesWhatACrashLeft(t *testing.T) {
	leftSocket := func(t *testing.T, path string) {
		t.Helper()
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		listener.(*net.UnixListener).SetUnlinkOnClose(false)
		_ = listener.Close()
	}
	dataDir := t.TempDir()
	spare, err := os.MkdirTemp("", "ck-mcp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(spare) })
	leftSocket(t, filepath.Join(spare, "mcp.sock"))
	writeDoorFile := func(socket string) {
		encoded, _ := json.Marshal(programtools.DoorFile{Socket: socket, Fingerprint: "x", Version: "1.3.0"})
		if err := os.WriteFile(filepath.Join(dataDir, programtools.DoorFileName), encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeDoorFile(filepath.Join(spare, "mcp.sock"))
	newProgramDoor(dataDir, log.New(io.Discard, "", 0)).clean()
	if _, err := os.Stat(spare); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the socket and the directory a crash left stayed")
	}
	if _, ok := doorFileOf(t, dataDir); ok {
		t.Fatal("mcp.json a crash left stayed")
	}

	foreign := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(foreign, []byte("заметки"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeDoorFile(foreign)
	newProgramDoor(dataDir, log.New(io.Discard, "", 0)).clean()
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("a file of another kind was removed")
	}
	full, err := os.MkdirTemp("", "ck-mcp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(full) })
	leftSocket(t, filepath.Join(full, "mcp.sock"))
	if err := os.WriteFile(filepath.Join(full, "other"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	writeDoorFile(filepath.Join(full, "mcp.sock"))
	newProgramDoor(dataDir, log.New(io.Discard, "", 0)).clean()
	if _, err := os.Stat(filepath.Join(full, "other")); err != nil {
		t.Fatal("a directory with anything in it was removed")
	}
}

// MC1.6: a crash between the listen and mcp.json leaves the socket on its own path without the file — <data dir>/mcp.sock, on
// Windows .canonkeeper/<8 знаков>.sock: the start removes it when nobody listens on it, and the door opens; a socket somebody
// listens on stays, and the door does not open.
func TestTheStartRemovesTheSocketACrashLeftWithoutItsFile(t *testing.T) {
	home := t.TempDir()
	for _, goos := range []string{"linux", "windows"} {
		dataDir := t.TempDir()
		doors := newProgramDoor(dataDir, log.New(io.Discard, "", 0))
		doors.goos, doors.home = goos, func() (string, error) { return home, nil }
		socket, _, err := doors.socketOf()
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		listener.(*net.UnixListener).SetUnlinkOnClose(false)
		_ = listener.Close()
		doors.clean()
		doors.use(answersOfTheTest())
		if err := doors.Open(context.Background()); err != nil {
			t.Fatalf("%s: the door does not open over a socket a crash left without mcp.json: %v (state %+v)", goos, err, doors.State())
		}
		doors.Close()

		if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
			t.Fatal(err)
		}
		live, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		again := newProgramDoor(dataDir, log.New(io.Discard, "", 0))
		again.goos, again.home = goos, func() (string, error) { return home, nil }
		again.clean()
		if !isSocket(socket) {
			t.Fatalf("%s: the start removed a socket somebody listens on", goos)
		}
		again.use(answersOfTheTest())
		if err := again.Open(context.Background()); err == nil || again.State().Open {
			t.Fatalf("%s: the door opened over a socket somebody listens on", goos)
		}
		_ = live.Close()
	}
}

// MC3.3: the file of the extension carries the icon of the app.
func TestTheExtensionCarriesTheIconOfTheApp(t *testing.T) {
	icon, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "desktop", "src-tauri", "icons", "128x128.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(icon, extensionIcon) {
		t.Fatal("the icon of the extension is not the icon of the app")
	}
}

// MC1.7: the stop of the backend stays within the term of the shell — the turns and the door 5 s (the close of the door with its
// 2 s of mcp.json inside), the core 30 s, the cluster 90 s, less than STOP_TIMEOUT of the supervisor: a backend killed by the
// term would break the install of an update.
func TestTheStopOfTheBackendIsWithinTheTermOfTheShell(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "desktop", "src-tauri", "crates", "core", "src", "supervisor.rs"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`pub const STOP_TIMEOUT: Duration = Duration::from_secs\((\d+)\);`).FindSubmatch(source)
	if match == nil {
		t.Fatal("no STOP_TIMEOUT in the supervisor")
	}
	seconds, _ := strconv.Atoi(string(match[1]))
	if budget := stopTurnsTimeout + stopCoreTimeout + stopClusterTime; budget >= time.Duration(seconds)*time.Second {
		t.Fatalf("the stop of the backend %v is not less than the term of the shell %d s", budget, seconds)
	}
	// The close of the door, mcp.json tried again up to replaceTerm, is a part of the step of the turns, not a step of its own.
	if replaceTerm >= stopTurnsTimeout {
		t.Fatalf("the close of the door %v does not fit the step of the turns %v", replaceTerm, stopTurnsTimeout)
	}
}

// MC1.7: the close of the door — its mcp.json removal tried again up to 2 s included — and a handler that never ends do not
// take the stop of the backend past the term of the step of the turns: the step closes the door first, its calls hear
// «закрывается», and its handlers are waited for within the same term.
func TestADoorThatDoesNotEndKeepsTheStopInItsTerm(t *testing.T) {
	dataDir := t.TempDir()
	doors := newProgramDoor(dataDir, log.New(io.Discard, "", 0))
	started := make(chan struct{}, 1)
	doors.use(door.New(oneSaga{}, func(string, researchservice.Naming) door.Handler {
		return func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
			started <- struct{}{}
			select {}
		}
	}, "1.4.0"))
	if err := doors.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	file, _ := doorFileOf(t, dataDir)
	conn, err := net.Dial("unix", file.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	line, _ := json.Marshal(programtools.Request{Fingerprint: programtools.Fingerprint(), Version: "1.4.0", Kind: programtools.KindInstall, Tool: "read", Arguments: json.RawMessage(`{}`)})
	_, _ = conn.Write(append(line, '\n'))
	<-started
	// mcp.json cannot be removed: the close tries it again for its whole 2 s.
	if err := os.Chmod(dataDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0o700) })
	heard := make(chan string, 1)
	go func() {
		answer, _ := bufio.NewReader(conn).ReadBytes('\n')
		heard <- string(answer)
	}()
	began := time.Now()
	stopTurns(&build{programs: doors}, log.New(io.Discard, "", 0))
	if took := time.Since(began); took > stopTurnsTimeout+500*time.Millisecond {
		t.Fatalf("the stop of the turns with the close of the door took %v", took)
	}
	select {
	case answer := <-heard:
		if !strings.Contains(answer, "закрывается") {
			t.Fatalf("the call at the stop: %q", answer)
		}
	case <-time.After(time.Second):
		t.Fatal("the step of the turns did not close the door")
	}
}

// MC1.7: the words a call amid the close hears are the words of the step that closes the door — turned off by the author,
// «закрыт или выключено»; at a stop, an update or an erase, «закрывается».
func TestACallAmidTheCloseHearsWhyTheDoorCloses(t *testing.T) {
	for name, place := range map[string]struct {
		close func(*programDoor)
		words string
	}{
		"turned off": {(*programDoor).Close, programtools.WordsClosed},
		"a stop":     {(*programDoor).closing, programtools.WordsClosing},
	} {
		doors := newProgramDoor(t.TempDir(), log.New(io.Discard, "", 0))
		started := make(chan struct{}, 1)
		doors.use(door.New(oneSaga{}, func(string, researchservice.Naming) door.Handler {
			return func(ctx context.Context, _ agent.ToolCall) (agent.ToolResult, error) {
				started <- struct{}{}
				<-ctx.Done()
				return agent.ToolResult{}, ctx.Err()
			}
		}, "1.4.0"))
		if err := doors.Open(context.Background()); err != nil {
			t.Fatal(err)
		}
		file, _ := doorFileOf(t, doors.dataDir)
		conn, err := net.Dial("unix", file.Socket)
		if err != nil {
			t.Fatal(err)
		}
		line, _ := json.Marshal(programtools.Request{Fingerprint: programtools.Fingerprint(), Version: "1.4.0", Kind: programtools.KindInstall, Tool: "read", Arguments: json.RawMessage(`{}`)})
		_, _ = conn.Write(append(line, '\n'))
		<-started
		place.close(doors)
		raw, err := bufio.NewReader(conn).ReadBytes('\n')
		_ = conn.Close()
		var answer programtools.Answer
		if err != nil || json.Unmarshal(raw, &answer) != nil || answer.Text != place.words || !answer.Error {
			t.Fatalf("%s: the call amid the close heard %q %v", name, raw, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		doors.wait(ctx)
		cancel()
	}
}

// oneSaga is the readings of an author with one saga, for a door of the test.
type oneSaga struct{}

func (oneSaga) Sagas(context.Context) ([]programreads.Saga, error) {
	return []programreads.Saga{{ID: "11111111-0000-4000-8000-000000000000", Key: "11111111", Title: "Северный предел"}}, nil
}
func (oneSaga) Find(context.Context, string) (programreads.Saga, error) {
	return programreads.Saga{ID: "11111111-0000-4000-8000-000000000000", Key: "11111111"}, nil
}
func (oneSaga) Record(context.Context, programreads.Read) error { return nil }

// MC3.2: the backend reads start.bridge as the shell writes it (crates/core/src/control.rs): the file of an AppImage and mcp,
// or the program beside the shell; a start without it names none — and the state then has no commands.
func TestTheStartNamesTheBridgeOfTheShell(t *testing.T) {
	channel := newControl(strings.NewReader(
		`{"type":"start","dataDir":"/d","bridge":{"program":"/home/author/CanonKeeper.AppImage","args":["mcp"]}}`+"\n"+
			`{"type":"start","dataDir":"/d","bridge":{"program":"/usr/bin/canonkeeper-mcp"}}`+"\n"+`{"type":"start","dataDir":"/d"}`+"\n"), io.Discard)
	appimage, err := channel.receive()
	if err != nil || appimage.Bridge == nil || appimage.Bridge.Program != "/home/author/CanonKeeper.AppImage" || len(appimage.Bridge.Args) != 1 || appimage.Bridge.Args[0] != "mcp" {
		t.Fatalf("the bridge of an AppImage: %+v %v", appimage.Bridge, err)
	}
	beside, _ := channel.receive()
	if beside.Bridge == nil || beside.Bridge.Program != "/usr/bin/canonkeeper-mcp" || len(beside.Bridge.Args) != 0 {
		t.Fatalf("the bridge beside the shell: %+v", beside.Bridge)
	}
	if none, _ := channel.receive(); none.Bridge != nil {
		t.Fatalf("a start without the bridge: %+v", none.Bridge)
	}
}
