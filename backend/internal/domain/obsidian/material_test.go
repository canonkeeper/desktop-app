// SPDX-License-Identifier: AGPL-3.0-only

package obsidian_test

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/library"
)

type materialCase struct {
	Name       string               `json:"name"`
	Path       string               `json:"path"`
	FolderKind authormaterials.Kind `json:"folderKind"`
	Text       string               `json:"text"`
	Skip       obsidian.Skip        `json:"skip"`
	Want       struct {
		Name        string               `json:"name"`
		Kind        authormaterials.Kind `json:"kind"`
		Description string               `json:"description"`
		Section     string               `json:"section"`
		Properties  [][2]string          `json:"properties"`
		Aliases     []string             `json:"aliases"`
		Warnings    []obsidian.Warning   `json:"warnings"`
		Cut         int                  `json:"cut"`
		Left        int                  `json:"left"`
	} `json:"want"`
}

// specs/2026-11-13-desk-obsidian.md OB3.4: «заметка → запись» — the kind by the header, the tags and the folder; the name;
// «Другие имена»; the properties of the header, of «Ключ::» and of a table; the keys of the service; the limits cut and
// counted; the description without the signs, as a book reads its paragraphs; CRLF does not drop a record. A record the
// owner of the materials takes as it is.
func TestANoteIsARecordOfTheMaterials(t *testing.T) {
	data, err := os.ReadFile("testdata/materials.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []materialCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			read, skip := obsidian.ReadNote(test.Path, []byte(test.Text))
			if skip != "" {
				t.Fatalf("the note is skipped: %s", skip)
			}
			record, skip := obsidian.Material(read, test.FolderKind, library.MarkdownPlainLines)
			if skip != test.Skip {
				t.Fatalf("skip = %q, want %q", skip, test.Skip)
			}
			if skip != "" {
				return
			}
			var properties [][2]string
			for _, property := range record.Draft.Properties {
				properties = append(properties, [2]string{property.Key, property.Value})
			}
			want := test.Want
			if record.Draft.Name != want.Name || record.Draft.Kind != want.Kind || record.Draft.Description != want.Description ||
				record.Draft.Section != want.Section || !reflect.DeepEqual(properties, want.Properties) ||
				!reflect.DeepEqual(record.Aliases, want.Aliases) || !reflect.DeepEqual(record.Warnings, want.Warnings) ||
				record.Cut != want.Cut || record.Left != want.Left {
				t.Fatalf("record = %+v\nproperties %q aliases %q warnings %q cut %d left %d", record.Draft, properties, record.Aliases, record.Warnings, record.Cut, record.Left)
			}
			if _, ok := authormaterials.CleanDraft(record.Draft); !ok {
				t.Fatal("the owner refuses the record")
			}
		})
	}
}

// specs/2026-11-13-desk-obsidian.md OB3.4: the properties of a record stop at the limit of a record and the rest is
// counted; a description longer than a record holds ends at the end of a paragraph, and the cut is counted.
func TestTheLimitsOfARecord(t *testing.T) {
	var text strings.Builder
	text.WriteString("---\n")
	for index := range authormaterials.MaxProperties + 5 {
		fmt.Fprintf(&text, "свойство %02d: значение\n", index)
	}
	first, second := strings.Repeat("Ольга стояла на башне. ", 200), strings.Repeat("Мирон ждал у переправы. ", 200)
	text.WriteString("---\n" + first + "\n\n" + second + "\n")
	read, skip := obsidian.ReadNote("Персонажи/Ольга.md", []byte(text.String()))
	if skip != "" {
		t.Fatal(skip)
	}
	record, skip := obsidian.Material(read, authormaterials.KindCharacter, library.MarkdownPlainLines)
	if skip != "" || len(record.Draft.Properties) != authormaterials.MaxProperties || record.Left != 5 {
		t.Fatalf("%d properties, %d left, skip %q", len(record.Draft.Properties), record.Left, skip)
	}
	if record.Draft.Description != strings.TrimSpace(first) || record.Cut != 1 {
		t.Fatalf("the description of %d signs, cut %d", utf8.RuneCountInString(record.Draft.Description), record.Cut)
	}
	if _, ok := authormaterials.CleanDraft(record.Draft); !ok {
		t.Fatal("the owner refuses the record")
	}
}
