// Package sagadraft holds the draft of a chapter as text (specs/2026-11-07-desktop-app.md, DA16.4): its one working
// text with the marks ⟦…⟧ of what the model wrote, the versions of that text, the edit of a chapter of the book and its
// way into the book. What the writing agent decided on the row — the case, the plan, the choices about the pack, the
// notes, the file of the author — it carries as data; the agent itself is domain/writingagent. It performs no I/O.
package sagadraft

import "strings"

// Lines are the lines of a text that are not blank, trimmed.
func Lines(text string) []string {
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
