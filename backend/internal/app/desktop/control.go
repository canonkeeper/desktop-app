// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sync"

	domain "canonkeeper/backend/internal/domain/desktop"
)

// ControlMessage is one line of JSON between the shell and the backend on the stdin and stdout of the backend
// (specs/2026-11-07-desktop-app.md DA3, «Types»; specs/2026-11-13-desk-secrets.md SE1.4; specs/2026-11-13-desk-erase.md
// ER1.2; specs/2026-11-13-desk-upgrade.md UG2, UG3; specs/2026-11-13-desk-updates.md UB1.1). The shell writes start,
// open-code, secret-result, update-state — what an update of the app came to, after every change and every ready — and
// shutdown; the backend writes step while a long step of the start goes, ready, open-code with the next code,
// secret-save, secret-delete, erase — the author asked to erase all the data of the app, which the shell does once the
// backend stopped —, focus — raise the window —, update-check, update-download, update-install, update-cancel, fatal,
// and the choice of the folder of an Obsidian vault: the backend writes folder-choice and folder-result, the shell
// folder-picked. The channel is known only to the two processes: no code and no key goes into the arguments or
// the environment of a process, which other users of the computer can see (DA3.2). A message is never written to a log:
// it may carry a secret.
type ControlMessage struct {
	Type string `json:"type"`
	// DataDir is the data directory of the user, Resources the directory of the bundle's PostgreSQL, codex-app-server
	// and Codex profile; both come with start.
	DataDir   string `json:"dataDir,omitempty"`
	Resources string `json:"resources,omitempty"`
	// Bridge is how the program of the bridge for the programs of the author runs (specs/2026-11-15-desk-mcp.md MC3.2): the
	// program beside the program of the app, or the file of the AppImage and mcp; none without the program (start).
	Bridge *domain.Bridge `json:"bridge,omitempty"`
	// Secrets are the records of the secrets of the app the shell keeps, by id, and SecretsStore where it keeps them
	// (start). ID and Value are the record the backend asks the shell to keep (secret-save) or to remove
	// (secret-delete, no value); Request names the answer, and OK says whether the shell did it (secret-result).
	Secrets      map[string]string `json:"secrets,omitempty"`
	SecretsStore string            `json:"secretsStore,omitempty"`
	ID           string            `json:"id,omitempty"`
	Value        string            `json:"value,omitempty"`
	Request      string            `json:"request,omitempty"`
	OK           bool              `json:"ok,omitempty"`
	// Port and GatewayPort are the listeners of the backend (ready); Code opens the window once (ready, open-code).
	// PortChanged says the window is on another address than at the previous start: the page finds the storage of its
	// origin empty (ready; specs/2026-11-12-desk-process.md PR1.2, PR1.4).
	Port        int    `json:"port,omitempty"`
	PortChanged bool   `json:"portChanged,omitempty"`
	GatewayPort int    `json:"gatewayPort,omitempty"`
	Code        string `json:"code,omitempty"`
	// Reason says why the backend stops, without content (fatal); Backup names the copy of backups/ made before a
	// migration that failed (fatal migrate; specs/2026-11-12-desk-process.md PR3.3) — its name, not its content.
	Reason string `json:"reason,omitempty"`
	Backup string `json:"backup,omitempty"`
	// The start after an update (specs/2026-11-13-desk-upgrade.md UG2–UG4). Version is the version of the app: of the
	// shell in start, of the program in step and in fatal version_mismatch, where Shell is the version start named.
	// Restore asks for a start that brings the data back from the copy the backend names as the candidate (start). Step
	// names a long step before ready — backup, migrate, restore (step). Opened is the version that opened the data before
	// (fatal newer_schema, migrate, backup); Copy is the copy a fatal or a step speaks of — the candidate of a return, the
	// copy a return brings back or failed with; Needed is the bytes a return still needs on the disk (fatal
	// restore_space). UpdatedFrom is the version the data were opened with before this one, once after an update (ready).
	Version string      `json:"version,omitempty"`
	Shell   string      `json:"shell,omitempty"`
	Restore bool        `json:"restore,omitempty"`
	Step    string      `json:"step,omitempty"`
	Opened  string      `json:"opened,omitempty"`
	Copy    *BackupCopy `json:"copy,omitempty"`
	// Before are the folders «до возврата» earlier returns left, which the question before a return names (fatal
	// newer_schema; UG4.4).
	Before      []BeforeRestore `json:"before,omitempty"`
	Needed      int64           `json:"needed,omitempty"`
	UpdatedFrom string          `json:"updatedFrom,omitempty"`
	// The updates of the app (UB1.1, UB2.8). UpdateKind is how the app was installed (start). Phase, Found, Percent,
	// Refusal, Package, Cause and Notes are what update-state says of an update, Version its version — the backend names
	// only that version in update-download, update-install and update-cancel. ReleaseNotes are the notes of the release
	// now running, once after the app installed it (ready).
	UpdateKind   string `json:"updateKind,omitempty"`
	Phase        string `json:"phase,omitempty"`
	Found        string `json:"found,omitempty"`
	Percent      int    `json:"percent,omitempty"`
	Refusal      string `json:"refusal,omitempty"`
	Package      string `json:"package,omitempty"`
	Cause        string `json:"cause,omitempty"`
	Notes        string `json:"notes,omitempty"`
	ReleaseNotes string `json:"releaseNotes,omitempty"`
	// The choice of the folder of an Obsidian vault (specs/2026-11-13-desk-obsidian.md OB1.1, OB1.2). Request is the
	// number of the choice and Saga the title of its saga (folder-choice); Path is the folder the author picked in the
	// system window (folder-picked); Outcome is how the backend ended the choice — linked, a reason of a refusal, stale
	// (folder-result).
	Saga    string `json:"saga,omitempty"`
	Path    string `json:"path,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

const (
	messageStart        = "start"
	messageOpenCode     = "open-code"
	messageSecretResult = "secret-result"
	messageShutdown     = "shutdown"
	messageReady        = "ready"
	messageSecretSave   = "secret-save"
	messageSecretDelete = "secret-delete"
	messageErase        = "erase"
	messageFatal        = "fatal"
	messageStep         = "step"
	// specs/2026-11-13-desk-updates.md UB1.1.
	messageUpdateState    = "update-state"
	messageUpdateCheck    = "update-check"
	messageUpdateDownload = "update-download"
	messageUpdateInstall  = "update-install"
	messageUpdateCancel   = "update-cancel"
	// messageFocus asks the shell to raise the window: the link with the account went through in the browser
	// (specs/2026-11-13-desk-account.md AC1.3).
	messageFocus = "focus"
	// specs/2026-11-13-desk-obsidian.md OB1.1: the backend declares a choice of a folder, the shell names the folder the
	// author picked for it, the backend says how the choice ended.
	messageFolderChoice = "folder-choice"
	messageFolderPicked = "folder-picked"
	messageFolderResult = "folder-result"
)

// maxControlLine bounds one message: a start with every key of the author is a few kilobytes.
const maxControlLine = 1 << 20

var errControlMessage = errors.New("desktop: a control message is not one line of JSON")

// control is the channel: one reader of the shell's lines and a writer that sends whole lines one at a time.
type control struct {
	scanner *bufio.Scanner
	mu      sync.Mutex
	out     io.Writer
}

func newControl(in io.Reader, out io.Writer) *control {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64<<10), maxControlLine)
	return &control{scanner: scanner, out: out}
}

// receive reads the next message; io.EOF is the shell that closed stdin or died.
func (channel *control) receive() (ControlMessage, error) {
	if !channel.scanner.Scan() {
		if err := channel.scanner.Err(); err != nil {
			return ControlMessage{}, err
		}
		return ControlMessage{}, io.EOF
	}
	var message ControlMessage
	if json.Unmarshal(channel.scanner.Bytes(), &message) != nil || message.Type == "" {
		return ControlMessage{}, errControlMessage
	}
	return message, nil
}

func (channel *control) send(message ControlMessage) error {
	line, err := json.Marshal(message)
	if err != nil {
		return err
	}
	channel.mu.Lock()
	defer channel.mu.Unlock()
	_, err = channel.out.Write(append(line, '\n'))
	return err
}
