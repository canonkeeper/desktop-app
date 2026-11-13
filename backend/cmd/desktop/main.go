// SPDX-License-Identifier: AGPL-3.0-only

// Command desktop is the backend of the desktop app, the sidecar of its shell (specs/2026-11-07-desktop-app.md DA1.2):
// the shell starts it, talks to it over its stdin and stdout and stops it. It is built from the open code alone.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"canonkeeper/backend/internal/app/desktop"
)

// version is the version of the app: scripts/desktop-backend.mjs sets it from the shell's Cargo.toml (-X main.version), so
// the window, the backend and the installer name one (specs/2026-11-13-desk-settings.md ST4.2).
var version string

func main() {
	if len(os.Args) != 1 {
		log.Fatal("desktop: no arguments: the shell sends start on stdin")
	}
	// Stdout is the control channel (DA3.2): whatever else writes to it goes to stderr until the log of the run opens.
	control := os.Stdout
	os.Stdout = os.Stderr
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := desktop.Run(ctx, desktop.Options{In: os.Stdin, Control: control, Version: version, Logs: func(file *os.File) {
		os.Stdout = file
		log.SetOutput(file)
	}})
	if err != nil {
		os.Exit(1)
	}
}
