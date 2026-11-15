// SPDX-License-Identifier: AGPL-3.0-only

package door

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/delivery/mcp/programtools"
)

// The bound of one page in bytes of UTF-8 with its lines of the pages (specs/2026-11-15-desk-mcp.md MC2.5): Claude Code and
// Claude Desktop take a larger answer; every other program — Codex with a model not of the catalog of OpenAI cuts the middle
// of an answer past 12 000 bytes — the small one.
const (
	largePage = 36_000
	smallPage = 11_000
)

// PageBound is the bound of a page of the program that called itself so.
func PageBound(client string) int {
	switch client {
	case "claude-code", "claude-ai":
		return largePage
	default:
		return smallPage
	}
}

// pageAsked is a page the program asks for: its number and the mark of the answer it read before; none — the first.
type pageAsked struct {
	number int
	mark   string
}

var pageForm = regexp.MustCompile(`^([1-9][0-9]{0,5}):([0-9a-f]{8})$`)

var (
	errPageForm = errors.New("door: the value of page is not of its form")
	errChanged  = errors.New("door: the answer changed since the page before")
	errNoPage   = errors.New("door: no such page")
)

func parsePage(value string) (pageAsked, error) {
	if value == "" {
		return pageAsked{number: 1}, nil
	}
	match := pageForm.FindStringSubmatch(value)
	if match == nil {
		return pageAsked{}, errPageForm
	}
	number, _ := strconv.Atoi(match[1])
	return pageAsked{number: number, mark: match[2]}, nil
}

// markOf is the mark of a whole answer: 8 signs of its SHA-256.
func markOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:4])
}

// paged is a page of an answer: its text, its number and the number of the pages.
type paged struct {
	text   string
	number int
	pages  int
}

// pageOf cuts an answer into pages of at most bound bytes with their lines and gives the one asked: an answer of one page goes
// whole, without the lines; the pages put together without their first and last lines are the answer to the sign. A mark
// that is not the answer's is errChanged, a page past the last errNoPage. Nothing is kept between the calls.
func pageOf(text string, bound int, asked pageAsked) (paged, string, error) {
	mark := markOf(text)
	if asked.mark != "" && asked.mark != mark {
		return paged{}, "", errChanged
	}
	if len(text) <= bound {
		if asked.number != 1 {
			return paged{}, "", errNoPage
		}
		return paged{text: text, number: 1, pages: 1}, mark, nil
	}
	// The lines of a page are at most this long: six digits a number, the mark of eight signs.
	room := bound - 2*len(programtools.PageLine(999_998, 999_999, mark)) - 2
	chunks := cut(text, room)
	if asked.number > len(chunks) {
		return paged{}, "", errNoPage
	}
	line := programtools.PageLine(asked.number, len(chunks), mark)
	return paged{text: line + "\n" + chunks[asked.number-1] + "\n" + line, number: asked.number, pages: len(chunks)}, mark, nil
}

// cut cuts a text into chunks of at most room bytes, after a line where it can, else on a border of a sign.
func cut(text string, room int) []string {
	var chunks []string
	for len(text) > room {
		end := strings.LastIndexByte(text[:room], '\n') + 1
		if end <= 0 {
			end = room
			for end > 0 && !utf8.RuneStart(text[end]) {
				end--
			}
		}
		chunks = append(chunks, text[:end])
		text = text[end:]
	}
	return append(chunks, text)
}
