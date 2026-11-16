// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// appLinks is testdata/app-links.json: the links the writer writes by the values of a place, shared with the test of the core
// of the shell, which parses them.
type appLinks struct {
	Examples []struct {
		Saga, Chapter string
		P             *int
		Link          string
	} `json:"examples"`
}

// specs/2026-11-16-desk-links-writer.md LW1.3: one function writes the link of a place from the ids of the saga and of the
// chapter and the number of the paragraph — the links of the table, and no link for a value the shell does not take (an id not
// canonical, a number out of 1…99999); the shell parses the same table (desktop/src-tauri/crates/core/src/link.rs).
func TestTheLinkOfAPlaceIsTheTableTheShellParses(t *testing.T) {
	data, err := os.ReadFile("testdata/app-links.json")
	if err != nil {
		t.Fatal(err)
	}
	var table appLinks
	if err := json.Unmarshal(data, &table); err != nil {
		t.Fatal(err)
	}
	links, none := 0, 0
	for _, example := range table.Examples {
		paragraph := 0
		if example.P != nil {
			if *example.P == 0 {
				t.Fatalf("%+v: p 0 is no paragraph to the writer, the table names none instead", example)
			}
			paragraph = *example.P
		}
		got, ok := openLink(example.Saga, example.Chapter, paragraph)
		if got != example.Link || ok != (example.Link != "") {
			t.Errorf("openLink(%q, %q, %d) = %q, %v; want %q", example.Saga, example.Chapter, paragraph, got, ok, example.Link)
		}
		if ok {
			links++
		} else {
			none++
		}
	}
	if links == 0 || none == 0 {
		t.Fatalf("the table has %d links and %d values without one", links, none)
	}
}

// LW1.4, OE2.5: the target of a link to the book is part of the fingerprint of a reference, as the target of a link of the wiki
// is not: a reference written before the links reads as changed content, and a link the author changed is a change of his.
func TestTheTargetOfALinkToTheBookIsInTheFingerprint(t *testing.T) {
	link, _ := openLink(sagaID, ch1ID, 3)
	other, _ := openLink(sagaID, ch1ID, 4)
	line := "- Впервые: [[CanonKeeper/Главы/1 Застава|Книга 1, гл. 1]], [абзац 3](" + link + ")\n"
	fingerprint := ExportPrint([]byte(line))
	if fingerprint == ExportPrint([]byte("- Впервые: [[CanonKeeper/Главы/1 Застава|Книга 1, гл. 1]], абзац 3\n")) ||
		fingerprint == ExportPrint([]byte(strings.Replace(line, link, other, 1))) {
		t.Fatal("a link to the book is not in the fingerprint")
	}
	if fingerprint != ExportPrint([]byte(strings.Replace(line, "[[CanonKeeper/Главы/1 Застава|", "[[1 Застава|", 1))) {
		t.Fatal("the target of a link of the wiki is in the fingerprint")
	}
}
