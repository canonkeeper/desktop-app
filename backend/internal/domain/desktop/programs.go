// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"strings"
)

// Programs is the reading of the sagas by the programs of the author (specs/2026-11-15-desk-mcp.md MC3.1): off by default;
// Ever says the author let it once, so the erasing of the data names what stays in the settings of his programs (MC3.4).
type Programs struct {
	Enabled bool `json:"enabled"`
	Ever    bool `json:"ever"`
}

// Bridge is how the program of the bridge is run, as the shell says it in start (MC3.2): the program, and before the data
// directory the arguments of the AppImage («mcp»).
type Bridge struct {
	Program string   `json:"program"`
	Args    []string `json:"args,omitempty"`
}

// ProgramCommands are what the author gives his programs (MC3.2, MC3.3): the commands of Claude Code and Codex — to add and to
// remove — for the shell of his system, and for Claude Desktop on Linux its entry and the whole file of its settings.
type ProgramCommands struct {
	ClaudeAdd    string
	ClaudeRemove string
	CodexAdd     string
	CodexRemove  string
	DesktopEntry string
	DesktopFile  string
}

// The removals: no path in them.
const (
	claudeRemove = "claude mcp remove --scope user canonkeeper"
	codexRemove  = "codex mcp remove canonkeeper"
)

// Commands are the commands of a bridge and a data directory: on Linux every path in single quotes with '\”, on Windows a
// command of PowerShell where every single quote of its kinds doubles; no argument of the bridge begins with a dash.
func Commands(bridge Bridge, dataDir string, windows bool) ProgramCommands {
	quote := quoteBash
	if windows {
		quote = quotePowerShell
	}
	run := append(append([]string{bridge.Program}, bridge.Args...), dataDir)
	quoted := make([]string, len(run))
	for index, part := range run {
		quoted[index] = quote(part)
	}
	line := strings.Join(quoted, " ")
	commands := ProgramCommands{
		ClaudeAdd: "claude mcp add --scope user canonkeeper " + line, ClaudeRemove: claudeRemove,
		CodexAdd: "codex mcp add canonkeeper -- " + line, CodexRemove: codexRemove,
	}
	if !windows {
		entry := map[string]any{"command": bridge.Program, "args": append(append([]string{}, bridge.Args...), dataDir)}
		encodedEntry, _ := json.MarshalIndent(map[string]any{"canonkeeper": entry}, "", "  ")
		encodedFile, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"canonkeeper": entry}}, "", "  ")
		// The entry goes inside "mcpServers": without the braces of its object.
		inner := strings.TrimSpace(string(encodedEntry))
		inner = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(inner, "{"), "}"))
		commands.DesktopEntry, commands.DesktopFile = unindent(inner), string(encodedFile)
	}
	return commands
}

// unindent takes off the two spaces the object of the entry gave each of its lines.
func unindent(text string) string {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimPrefix(line, "  ")
	}
	return strings.Join(lines, "\n")
}

func quoteBash(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

// quotePowerShell doubles every kind of a single quote PowerShell takes for one: ' ‘ ’ ‚ ‛.
func quotePowerShell(text string) string {
	var out strings.Builder
	out.WriteByte('\'')
	for _, letter := range text {
		out.WriteRune(letter)
		switch letter {
		case '\'', '‘', '’', '‚', '‛':
			out.WriteRune(letter)
		}
	}
	out.WriteByte('\'')
	return out.String()
}
