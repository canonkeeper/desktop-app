// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stubs are claude and codex of the test: they print their arguments, each ended with NUL.
func stubs(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\nfor argument in \"$@\"; do printf '%s\\0' \"$argument\"; done\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func argumentsOf(t *testing.T, shell []string, bin, command string) []string {
	t.Helper()
	run := exec.Command(shell[0], append(shell[1:], command)...)
	run.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "LANG=C.UTF-8"}
	out, err := run.Output()
	if err != nil {
		t.Fatalf("%s: %v", command, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

// specs/2026-11-15-desk-mcp.md MC3.2: the commands of Claude Code and Codex give the program its arguments as they are — a path
// with a space, the quotes of every kind, $, %, a backquote and Cyrillic — in bash on Linux and in PowerShell on Windows; no
// argument of the bridge begins with a dash; the removals name no path.
func TestTheCommandsGiveTheArgumentsAsTheyAre(t *testing.T) {
	bin := stubs(t)
	bridge := Bridge{Program: "/opt/Can'on Keeper/$HOME `x` 100%/canonkeeper-mcp", Args: []string{"mcp"}}
	dataDir := "/home/author/данные ‘саги’ ’; $(rm) `id` %PATH% \"q\""
	want := []string{bridge.Program, "mcp", dataDir}
	linux := Commands(bridge, dataDir, false)
	if got := argumentsOf(t, []string{"bash", "-c"}, bin, linux.ClaudeAdd); !slices.Equal(got, append([]string{"mcp", "add", "--scope", "user", "canonkeeper"}, want...)) {
		t.Fatalf("claude: %q", got)
	}
	if got := argumentsOf(t, []string{"bash", "-c"}, bin, linux.CodexAdd); !slices.Equal(got, append([]string{"mcp", "add", "canonkeeper", "--"}, want...)) {
		t.Fatalf("codex: %q", got)
	}
	if linux.ClaudeRemove != "claude mcp remove --scope user canonkeeper" || linux.CodexRemove != "codex mcp remove canonkeeper" {
		t.Fatalf("the removals: %+v", linux)
	}
	var file struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(linux.DesktopFile), &file); err != nil || file.Servers["canonkeeper"].Command != bridge.Program ||
		!slices.Equal(file.Servers["canonkeeper"].Args, []string{"mcp", dataDir}) {
		t.Fatalf("the file of Claude Desktop: %v %s", err, linux.DesktopFile)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte("{"+linux.DesktopEntry+"}"), &entry); err != nil || entry["canonkeeper"] == nil {
		t.Fatalf("the entry of Claude Desktop: %v %s", err, linux.DesktopEntry)
	}
	windows := Commands(Bridge{Program: `C:\Users\Тест\AppData\Local\CanonKeeper\canonkeeper-mcp.exe`}, `C:\Users\Тест\O'Brien ‘’‚‛\$x`, true)
	if windows.DesktopFile != "" || strings.Contains(windows.ClaudeAdd, " -") && !strings.Contains(windows.ClaudeAdd, "--scope user") {
		t.Fatalf("windows: %+v", windows)
	}
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Log("pwsh is not here: the command of Windows is checked by its form only")
		if !strings.Contains(windows.ClaudeAdd, `'C:\Users\Тест\O''Brien ‘‘’’‚‚‛‛\$x'`) {
			t.Fatalf("the quotes of PowerShell: %s", windows.ClaudeAdd)
		}
		return
	}
	got := argumentsOf(t, []string{pwsh, "-NoProfile", "-Command"}, bin, Commands(bridge, dataDir, true).ClaudeAdd)
	if !slices.Equal(got, append([]string{"mcp", "add", "--scope", "user", "canonkeeper"}, want...)) {
		t.Fatalf("pwsh: %q", got)
	}
}

// MC3.2: PowerShell takes each of ' ‘ ’ ‚ ‛ for a single quote, so a name between single quotes doubles every one of them —
// the table of the function, without pwsh at hand.
func TestPowerShellDoublesEveryKindOfASingleQuote(t *testing.T) {
	for text, want := range map[string]string{
		`O'Brien`:            `'O''Brien'`,
		"‘left’":             "'‘‘left’’'",
		"‚low‛":              "'‚‚low‛‛'",
		`"double" $x ` + "`": `'"double" $x ` + "`'",
		"":                   "''",
	} {
		if got := quotePowerShell(text); got != want {
			t.Errorf("quotePowerShell(%q) = %q, want %q", text, got, want)
		}
	}
}
