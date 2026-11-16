// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"strconv"
	"strings"
)

// The links of the references to a place in the book (specs/2026-11-16-desk-links-writer.md LW1): «абзац N» and the line that
// opens a chapter are links canonkeeper:// that open the app at that chapter and paragraph (specs/2026-11-16-desk-app-links.md
// AL1.1). testdata/app-links.json holds what the writer writes for the test of this package and for the test of the core of the
// shell, which parses every link of it to the same place.

// appLink is a link without its parameters: the scheme and the one action of the shell.
const appLink = "canonkeeper://open"

// lastParagraph is the greatest number of a paragraph the shell takes.
const lastParagraph = 99_999

// openLink is the link that opens the app at a chapter of a saga — at its paragraph from one, at the chapter itself for
// paragraph 0 —, built of the ids and the number alone, without the revision: a revision changes with every edit of the
// chapter, and the target of the link is part of the fingerprint of a reference (LW1.1). false for a value the shell does not
// take (AL1.1) — an id not canonical, a number out of 1…99999 —: the caption stays text.
func openLink(saga, chapter string, paragraph int) (string, bool) {
	saga, sagaOK := canonicalID(saga)
	chapter, chapterOK := canonicalID(chapter)
	if !sagaOK || !chapterOK || paragraph < 0 || paragraph > lastParagraph {
		return "", false
	}
	link := appLink + "?saga=" + saga + "&chapter=" + chapter
	if paragraph > 0 {
		link += "&p=" + strconv.Itoa(paragraph)
	}
	return link, true
}

// canonicalID is an id as the shell takes it (AL1.1): 36 characters, hex in groups of 8-4-4-4-12; the upper case goes to the
// lower, the form the shell gives the page.
func canonicalID(value string) (string, bool) {
	if len(value) != 36 {
		return "", false
	}
	for at := range len(value) {
		switch sign := value[at]; at {
		case 8, 13, 18, 23:
			if sign != '-' {
				return "", false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", rune(sign)) {
				return "", false
			}
		}
	}
	return strings.ToLower(value), true
}
