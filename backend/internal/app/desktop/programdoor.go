// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"canonkeeper/backend/internal/delivery/mcp/door"
	"canonkeeper/backend/internal/delivery/mcp/programtools"
	service "canonkeeper/backend/internal/services/desktop"
)

// The place of the local socket of the door (specs/2026-11-15-desk-mcp.md MC1.6): on Linux mcp.sock of the data directory
// while its path is at most 103 bytes, else a directory ck-mcp-* of its own in the temporary directory; on Windows a file of
// .canonkeeper in the home of the user, out of AppData, named by the data directory.
const (
	socketName      = "mcp.sock"
	spareSocketDirs = "ck-mcp-*"
	windowsSockets  = ".canonkeeper"
	// replaceTerm is how long a replace or a removal of mcp.json a reader of the file holds is tried again.
	replaceTerm = 2 * time.Second
)

// programDoor is the door of the app for the bridges of the programs of the author: it opens on the local socket only while
// the reading is on and the core is built, names itself in mcp.json after the socket listens and takes the file away before
// the socket closes; a start removes what a crash left (MC1.6, MC1.7). It implements the port of the service.
type programDoor struct {
	dataDir string
	logger  *log.Logger
	goos    string
	home    func() (string, error)
	// write replaces a file whole; the seam of a test fails it.
	write func(path string, content []byte) error

	mu      sync.Mutex
	door    *door.Door
	limits  door.Limits
	serving *door.Serving
	shut    []*door.Serving
	socket  string
	own     string
	failed  bool
}

var _ service.ProgramDoor = (*programDoor)(nil)

func newProgramDoor(dataDir string, logger *log.Logger) *programDoor {
	return &programDoor{dataDir: dataDir, logger: logger, goos: runtime.GOOS, home: os.UserHomeDir, write: replaceFile, limits: door.DefaultLimits}
}

// use gives the door its answers once the core is built.
func (doors *programDoor) use(answers *door.Door) {
	doors.mu.Lock()
	defer doors.mu.Unlock()
	doors.door = answers
}

// start opens the door when the backend serves and the reading is on (MC1.7).
func (doors *programDoor) start(enabled bool) {
	if !enabled {
		return
	}
	_ = doors.Open(context.Background())
}

func (doors *programDoor) doorFile() string {
	return filepath.Join(doors.dataDir, programtools.DoorFileName)
}

// socketOf is the path of the socket and the directory of the door to take away with it, if the door made it.
func (doors *programDoor) socketOf() (socket, own string, err error) {
	if doors.goos == "windows" {
		socket, err := doors.windowsSocket()
		if err != nil {
			return "", "", err
		}
		own = filepath.Dir(socket)
		if err := os.MkdirAll(own, 0o700); err != nil {
			return "", "", err
		}
		return socket, own, nil
	}
	socket = filepath.Join(doors.dataDir, socketName)
	if len(socket) <= maxSocketPath {
		return socket, "", nil
	}
	own, err = os.MkdirTemp("", spareSocketDirs)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(own, socketName), own, nil
}

// Open opens the door: the socket listens, then mcp.json names it. Anything of it that does not open leaves no door — the
// listener closed, the state a refusal — and one line of the log of the run with the code of the reason, without a path.
func (doors *programDoor) Open(context.Context) error {
	doors.mu.Lock()
	defer doors.mu.Unlock()
	if doors.serving != nil {
		return nil
	}
	if doors.door == nil {
		return doors.notOpened("core", errors.New("desktop: the door opens after the core"))
	}
	socket, own, err := doors.socketOf()
	if err != nil {
		return doors.notOpened("socket", err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		doors.removeOwn(own)
		return doors.notOpened("listen", err)
	}
	content, err := json.Marshal(programtools.DoorFile{Socket: socket, Fingerprint: programtools.Fingerprint(), Version: doors.door.Version()})
	if err == nil {
		err = retried(func() error { return doors.write(doors.doorFile(), content) })
	}
	if err != nil {
		_ = listener.Close()
		doors.removeOwn(own)
		return doors.notOpened("file", err)
	}
	doors.serving, doors.socket, doors.own, doors.failed = doors.door.Serve(listener, doors.limits), socket, own, false
	return nil
}

// notOpened is a door that did not open: the state says so, and the log of the run keeps the code of the reason — the error
// names a path, so it stays out.
func (doors *programDoor) notOpened(reason string, err error) error {
	doors.failed = true
	doors.logger.Printf(`{"event":"desktop_program_door_not_opened","reason":%q}`, reason)
	return err
}

// Close takes mcp.json away, stops the door taking calls and ends the calls with the words — «закрыт или выключено» when the
// author turns the reading off, «закрывается» at a stop, an update or an erase — then takes the empty directory of the
// socket away. It does not wait for the handlers: wait does, within the term of the stop of the turns.
func (doors *programDoor) Close() { doors.close(programtools.WordsClosed) }

func (doors *programDoor) closing() { doors.close(programtools.WordsClosing) }

func (doors *programDoor) close(words string) {
	doors.mu.Lock()
	defer doors.mu.Unlock()
	if doors.serving == nil {
		return
	}
	if err := retried(func() error {
		err := os.Remove(doors.doorFile())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}); err != nil {
		doors.logger.Print(`{"event":"desktop_program_door_file_kept"}`)
	}
	doors.serving.Shut(words)
	doors.removeOwn(doors.own)
	doors.shut = append(doors.shut, doors.serving)
	doors.serving, doors.socket, doors.own = nil, "", ""
}

// wait waits for the handlers of the door until ctx ends.
func (doors *programDoor) wait(ctx context.Context) {
	doors.mu.Lock()
	shut := append([]*door.Serving(nil), doors.shut...)
	doors.mu.Unlock()
	for _, serving := range shut {
		serving.Wait(ctx)
	}
}

// removeOwn takes away the directory of the socket the door made — only empty.
func (doors *programDoor) removeOwn(own string) {
	if own != "" {
		_ = os.Remove(own)
	}
}

// State is the door as the state of the reading tells it: open, failed to open, and the programs that turned to it.
func (doors *programDoor) State() service.ProgramDoorState {
	doors.mu.Lock()
	state := service.ProgramDoorState{Open: doors.serving != nil, Failed: doors.failed}
	answers := doors.door
	doors.mu.Unlock()
	if answers != nil {
		for _, program := range answers.Programs() {
			state.Programs = append(state.Programs, service.ProgramSeen{Name: program.Name, At: program.At, Matches: program.Matches, Extension: program.Extension})
		}
	}
	return state
}

// windowsSocket is the socket of the door on Windows: the file of this data directory in .canonkeeper of the home.
func (doors *programDoor) windowsSocket() (string, error) {
	home, err := doors.home()
	if err != nil || home == "" {
		return "", errors.Join(errors.New("desktop: no home of the user for the socket of the door"), err)
	}
	sum := sha256.Sum256([]byte(doors.dataDir))
	return filepath.Join(home, windowsSockets, hex.EncodeToString(sum[:])[:8]+".sock"), nil
}

// clean removes what a crash left before the door opens (MC1.6): mcp.json and the socket it names, and — a crash between the
// listen and mcp.json — the socket on the own lasting path of the door without the file (<data dir>/mcp.sock; on Windows
// .canonkeeper/<8 знаков>.sock). A socket goes only when it is a socket of the door — on Windows only the file of this data
// directory — and nobody listens on it; its directory only when it is an empty ck-mcp-* (or .canonkeeper). A file of another
// kind, a socket somebody listens on and a directory with anything in it stay.
func (doors *programDoor) clean() {
	if raw, err := os.ReadFile(doors.doorFile()); err == nil {
		var left programtools.DoorFile
		if json.Unmarshal(raw, &left) == nil && left.Socket != "" {
			doors.removeStale(left.Socket)
		}
		_ = os.Remove(doors.doorFile())
	}
	if doors.goos == "windows" {
		if socket, err := doors.windowsSocket(); err == nil {
			doors.removeStale(socket)
		}
	} else if socket := filepath.Join(doors.dataDir, socketName); len(socket) <= maxSocketPath {
		doors.removeStale(socket)
	}
}

// removeStale removes a socket of the door nobody listens on, and its directory when it is an empty ck-mcp-* or .canonkeeper.
func (doors *programDoor) removeStale(socket string) {
	info, err := os.Lstat(socket)
	if err != nil || info.IsDir() {
		return
	}
	if doors.goos == "windows" {
		if own, err := doors.windowsSocket(); err != nil || socket != own {
			return
		}
	} else if info.Mode()&os.ModeSocket == 0 {
		return
	}
	if conn, err := net.DialTimeout("unix", socket, time.Second); err == nil {
		_ = conn.Close()
		return
	}
	_ = os.Remove(socket)
	directory := filepath.Dir(socket)
	if matched, _ := filepath.Match(spareSocketDirs, filepath.Base(directory)); matched || filepath.Base(directory) == windowsSockets {
		_ = os.Remove(directory)
	}
}

// retried tries a replace or a removal of mcp.json again while a reader of the file holds it, up to replaceTerm.
func retried(do func() error) error {
	deadline := time.Now().Add(replaceTerm)
	for {
		err := do()
		if err == nil || time.Now().After(deadline) || !strings.Contains(strings.ToLower(err.Error()), "used by another process") && !errors.Is(err, os.ErrPermission) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
