// SPDX-License-Identifier: AGPL-3.0-only

// Command desktop-mcp is canonkeeper-mcp, the program of the bridge for the programs of the author
// (specs/2026-11-15-desk-mcp.md MC1): Claude Code, Claude Desktop or Codex of the author runs it with the data directory of
// the app; it speaks MCP over stdio and takes a call to the door of the running app over its local socket.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"canonkeeper/backend/internal/delivery/mcp/bridge"
)

// version is the version of the release, set by the build as the version of the backend (-X main.version).
var version = "dev"

// silence is how long the bridge waits for the first message after the start; the build of a test sets it shorter.
var silence = "90s"

func main() {
	dataDir, kind, ok := bridge.Arguments(os.Args[1:])
	if !ok {
		fmt.Fprintln(os.Stderr, "canonkeeper-mcp: the arguments are the absolute path of the data directory of CanonKeeper and, in the extension of Claude Desktop, extension")
		os.Exit(2)
	}
	wait, err := time.ParseDuration(silence)
	if err != nil {
		wait = bridge.Silence
	}
	os.Exit(bridge.Run(context.Background(), os.Stdin, os.Stdout, bridge.Config{DataDir: dataDir, Kind: kind, Version: version, Silence: wait}))
}
