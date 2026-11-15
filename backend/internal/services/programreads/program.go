// SPDX-License-Identifier: AGPL-3.0-only

package programreads

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// knownNames are the programs that give a name without a title (specs/2026-11-15-desk-mcp.md MC4.3).
var knownNames = map[string]string{"claude-code": "Claude Code", "claude-ai": "Claude Desktop", "codex-mcp-client": "Codex"}

// maxProgramName bounds the name of a program in characters, as the journal keeps it.
const maxProgramName = 200

// Unnamed is a program that gave no name.
const Unnamed = "программа без имени"

// ProgramName is the name of a program as the journal and the page say it (MC4.3): the title it gave on initialize, without
// one its name by the table of the known names, else the name as it is — in NFC, without the signs of the categories Cc and
// Cf, its spaces made one, up to 200 characters. It is what the program says of itself, not a check.
func ProgramName(name, title string) string {
	if cleaned := clean(title); cleaned != "" {
		return cleaned
	}
	if known, ok := knownNames[name]; ok {
		return known
	}
	if cleaned := clean(name); cleaned != "" {
		return cleaned
	}
	return Unnamed
}

func clean(text string) string {
	var kept strings.Builder
	for _, letter := range norm.NFC.String(text) {
		switch {
		case unicode.Is(unicode.Cc, letter) || unicode.Is(unicode.Cf, letter):
			kept.WriteRune(' ')
		default:
			kept.WriteRune(letter)
		}
	}
	runes := []rune(strings.Join(strings.Fields(kept.String()), " "))
	if len(runes) > maxProgramName {
		runes = []rune(strings.TrimSpace(string(runes[:maxProgramName])))
	}
	return string(runes)
}
