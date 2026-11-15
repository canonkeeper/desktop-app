// SPDX-License-Identifier: AGPL-3.0-only

package programtools

import "encoding/json"

// The kinds of a bridge: the program of the installation, or the copy inside the extension of Claude Desktop, whose words
// it carries in itself (MC1.4).
const (
	KindInstall   = "install"
	KindExtension = "extension"
)

// Request is the one line a bridge sends the door over the local socket (MC1.4): the fingerprint of its set, its version and
// kind, the name and the title of the program from initialize, and the tool with its arguments. Hello is the first word of
// a program on initialize (MC1.5): no tool, nothing of the saga.
type Request struct {
	Fingerprint string          `json:"fingerprint"`
	Version     string          `json:"version"`
	Kind        string          `json:"kind"`
	Client      string          `json:"client,omitempty"`
	Title       string          `json:"title,omitempty"`
	Hello       bool            `json:"hello,omitempty"`
	Tool        string          `json:"tool,omitempty"`
	Arguments   json.RawMessage `json:"arguments,omitempty"`
}

// Answer is the one line of the door: the text of the tool, Error when it is the error of the tool.
type Answer struct {
	Text  string `json:"text"`
	Error bool   `json:"error,omitempty"`
}

// DoorFileName is the file of the data directory that names the door while it is open (MC1.4, MC1.6).
const DoorFileName = "mcp.json"

// DoorFile is that file: the path of the local socket, the fingerprint of the set of the door and the version of the app.
type DoorFile struct {
	Socket      string `json:"socket"`
	Fingerprint string `json:"fingerprint"`
	Version     string `json:"version"`
}
