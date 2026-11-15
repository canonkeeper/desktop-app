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
	"time"

	"canonkeeper/backend/internal/app/desktop"
)

// version is the version of the app: scripts/desktop-backend.mjs sets it from the shell's Cargo.toml (-X main.version), so
// the window, the backend and the installer name one (specs/2026-11-13-desk-settings.md ST4.2).
var version string

// retryPause is the unit of the pauses of the repeats of a call of the model: empty in a release — the client's own; the
// process tests build a program with short pauses (-X main.retryPause=…, specs/2026-11-13-desk-consent.md CN3.4).
var retryPause string

// resumeEvery is how often the reading of the helper of the account checks the pauses that go on by themselves: empty in a
// release — ten minutes; the process tests build a program with a short one (-X main.resumeEvery=…,
// specs/2026-11-13-desk-account-ai.md AI3.6).
var resumeEvery string

// siteURL is the address of the site the app links with (specs/2026-11-13-desk-account.md AC1.4): only the build sets it
// (-X main.siteURL); neither the environment nor the settings change it.
var siteURL = "https://canonkeeper.ru"

func main() {
	if len(os.Args) != 1 {
		log.Fatal("desktop: no arguments: the shell sends start on stdin")
	}
	// Stdout is the control channel (DA3.2): whatever else writes to it goes to stderr until the log of the run opens.
	control := os.Stdout
	os.Stdout = os.Stderr
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pause, _ := time.ParseDuration(retryPause)
	every, _ := time.ParseDuration(resumeEvery)
	err := desktop.Run(ctx, desktop.Options{In: os.Stdin, Control: control, Version: version, Site: siteURL, RetryPause: pause, ResumeEvery: every, Logs: func(file *os.File) {
		os.Stdout = file
		log.SetOutput(file)
	}})
	if err != nil {
		os.Exit(1)
	}
}
