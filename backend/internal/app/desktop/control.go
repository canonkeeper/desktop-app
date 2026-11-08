// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// ControlMessage is one line of JSON between the shell and the backend on the stdin and stdout of the backend
// (specs/2026-11-07-desktop-app.md DA3, «Types»). The shell writes start, secret, open-code and shutdown; the backend
// writes ready, open-code with the next code and fatal. The channel is known only to the two processes: no code and no
// key goes into the arguments or the environment of a process, which other users of the computer can see (DA3.2).
type ControlMessage struct {
	Type string `json:"type"`
	// DataDir is the data directory of the user, Resources the directory of the bundle's PostgreSQL, codex-app-server
	// and Codex profile; both come with start.
	DataDir   string `json:"dataDir,omitempty"`
	Resources string `json:"resources,omitempty"`
	// Secrets are the keys of the providers by keyRef (start); ID and Value change one of them (secret, no value
	// deletes it).
	Secrets map[string]string `json:"secrets,omitempty"`
	ID      string            `json:"id,omitempty"`
	Value   *string           `json:"value,omitempty"`
	// Port and GatewayPort are the listeners of the backend (ready); Code opens the window once (ready, open-code).
	Port        int    `json:"port,omitempty"`
	GatewayPort int    `json:"gatewayPort,omitempty"`
	Code        string `json:"code,omitempty"`
	// Reason says why the backend stops, without content (fatal).
	Reason string `json:"reason,omitempty"`
}

const (
	messageStart    = "start"
	messageSecret   = "secret"
	messageOpenCode = "open-code"
	messageShutdown = "shutdown"
	messageReady    = "ready"
	messageFatal    = "fatal"
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
