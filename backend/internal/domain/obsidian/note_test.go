// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type noteCase struct {
	Name         string         `json:"name"`
	Path         string         `json:"path"`
	Text         string         `json:"text"`
	Hex          string         `json:"hex"`
	Repeat       int            `json:"repeat"`
	Skip         Skip           `json:"skip"`
	Front        map[string]any `json:"front"`
	FrontSkipped int            `json:"frontSkipped"`
	Body         string         `json:"body"`
}

func readTable[T any](t *testing.T, name string) []T {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var cases []T
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

// specs/2026-11-13-desk-obsidian.md OB3.1: a note is read by itself — its encoding by the rule of a text of a chapter, a
// note that does not read and a note over the size skipped by their reasons, the mark of the byte order, CRLF and the zero
// signs gone, the header read by its own reader, the notes of plugins skipped.
func TestANoteIsReadByItself(t *testing.T) {
	for _, test := range readTable[noteCase](t, "notes.json") {
		t.Run(test.Name, func(t *testing.T) {
			data := []byte(test.Text)
			if test.Hex != "" {
				decoded, err := hex.DecodeString(test.Hex)
				if err != nil {
					t.Fatal(err)
				}
				data = decoded
			}
			if test.Repeat > 0 {
				data = bytes.Repeat([]byte("а"), test.Repeat/2+1)
			}
			note, skip := ReadNote(test.Path, data)
			if skip != test.Skip {
				t.Fatalf("skip = %q, want %q", skip, test.Skip)
			}
			if skip != "" {
				return
			}
			if note.Body != test.Body {
				t.Fatalf("body = %q, want %q", note.Body, test.Body)
			}
			if test.Front != nil {
				got := map[string]any{}
				for _, key := range note.Front.Keys() {
					value, _ := note.Front.Get(key)
					if value.IsList {
						items := make([]any, len(value.List))
						for index, item := range value.List {
							items[index] = item
						}
						got[key] = items
					} else {
						got[key] = value.Text
					}
				}
				if !reflect.DeepEqual(got, test.Front) {
					t.Fatalf("front = %#v, want %#v", got, test.Front)
				}
			}
			if note.Front.Skipped != test.FrontSkipped {
				t.Fatalf("skipped keys = %d, want %d", note.Front.Skipped, test.FrontSkipped)
			}
		})
	}
}

type cleanCase struct {
	Name    string         `json:"name"`
	Record  bool           `json:"record"`
	Text    string         `json:"text"`
	Want    string         `json:"want"`
	Dropped map[string]int `json:"dropped"`
	Tags    []string       `json:"tags"`
	Fields  [][2]string    `json:"fields"`
	Rows    [][2]string    `json:"rows"`
}

// specs/2026-11-13-desk-obsidian.md OB3.2: the cleaning of a note for a book and for a record.
func TestTheCleaningOfANote(t *testing.T) {
	for _, test := range readTable[cleanCase](t, "clean.json") {
		t.Run(test.Name, func(t *testing.T) {
			cleaned := Clean(test.Text, test.Record)
			if cleaned.Text != test.Want {
				t.Fatalf("text = %q, want %q", cleaned.Text, test.Want)
			}
			dropped := map[string]int{
				"footnotes": cleaned.Dropped.Footnotes, "embeds": cleaned.Dropped.Embeds, "comments": cleaned.Dropped.Comments,
				"unpairedComments": cleaned.Dropped.UnpairedComments, "pluginBlocks": cleaned.Dropped.PluginBlocks, "tables": cleaned.Dropped.Tables,
			}
			for key, count := range dropped {
				if count != test.Dropped[key] {
					t.Fatalf("dropped %s = %d, want %d (%+v)", key, count, test.Dropped[key], cleaned.Dropped)
				}
			}
			if !reflect.DeepEqual(cleaned.Tags, test.Tags) || !reflect.DeepEqual(cleaned.Fields, test.Fields) || !reflect.DeepEqual(cleaned.Rows, test.Rows) {
				t.Fatalf("tags %q fields %q rows %q", cleaned.Tags, cleaned.Fields, cleaned.Rows)
			}
		})
	}
}

// The unclosed fence of the first note does not reach the second: each note is cleaned by itself.
func TestAnOpenFenceDoesNotSpoilTheNextNote(t *testing.T) {
	first := Clean("Текст.\n```\nкод", false)
	second := Clean("# Переправа\nПаром.", false)
	if first.Text != "Текст.\n```\nкод\n```" || second.Text != "# Переправа\nПаром." {
		t.Fatalf("first %q second %q", first.Text, second.Text)
	}
}

// specs/2026-11-13-desk-obsidian.md OB3.1: the end of a header stands no further than MaxFrontLines lines from its start;
// further on there is no header and the text is as it is.
func TestTheEndOfAHeaderStandsWithinItsLines(t *testing.T) {
	header := func(keys int) string {
		var text strings.Builder
		text.WriteString("---\n")
		for index := range keys {
			fmt.Fprintf(&text, "k%d: v\n", index)
		}
		text.WriteString("---\nТекст.\n")
		return text.String()
	}
	near, skip := ReadNote("Застава.md", []byte(header(MaxFrontLines-1)))
	if value, ok := near.Front.Get(fmt.Sprintf("k%d", MaxFrontLines-2)); skip != "" || !ok || value.Text != "v" || near.Body != "Текст.\n" {
		t.Fatalf("a header that ends on its last line = %q %q", skip, near.Body)
	}
	far, skip := ReadNote("Застава.md", []byte(header(MaxFrontLines)))
	if _, ok := far.Front.Get("k0"); skip != "" || ok || far.Body != header(MaxFrontLines) {
		t.Fatalf("a header that ends further on is read: %v", far.Front.Keys())
	}
}

// specs/2026-11-13-desk-obsidian.md OB3.2: the tags of a line are read in one pass over it, its code and links among them.
func TestTheTagsOfALongLineAreReadOnce(t *testing.T) {
	line := strings.Repeat("`a` #b [c](d) ", 100_000)
	started := time.Now()
	_, tags, _ := dropTags(line)
	if len(tags) != 100_000 || time.Since(started) > 5*time.Second {
		t.Fatalf("%d tags in %s", len(tags), time.Since(started))
	}
}
