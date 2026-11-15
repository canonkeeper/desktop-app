// SPDX-License-Identifier: AGPL-3.0-only

package modeljournal

import (
	"encoding/json"
	"os"
	"testing"

	"canonkeeper/backend/internal/domain/disclosure"
)

// JR2.3: the words «что ушло» are one function by the table of examples of testdata/what-went.json.
func TestTheWordsOfWhatWentAreTheTable(t *testing.T) {
	raw, err := os.ReadFile("testdata/what-went.json")
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Places Places `json:"places"`
		Cases  []struct {
			Name  string           `json:"name"`
			Kind  Kind             `json:"kind"`
			Named disclosure.Named `json:"named"`
			Page  int              `json:"page"`
			Pages int              `json:"pages"`
			Paged bool             `json:"paged"`
			Want  string           `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Cases) < 30 {
		t.Fatalf("the table has %d cases", len(table.Cases))
	}
	for _, example := range table.Cases {
		named := disclosure.Merge(disclosure.Named{}, example.Named)
		if got := WhatWent(example.Kind, named, table.Places, Paging{Page: example.Page, Pages: example.Pages, Paged: example.Paged}); got != example.Want {
			t.Errorf("%s:\n got %s\nwant %s", example.Name, got, example.Want)
		}
	}
}

// JR2.3: a move of the books changes «Книга N» — the place is named as it stands now.
func TestAMoveOfTheBooksChangesTheBookOfThePlace(t *testing.T) {
	named := disclosure.Named{Chapters: []disclosure.Chapter{{ID: "c", Precision: disclosure.Whole}}}
	before := Places{Chapters: map[string]ChapterPlace{"c": {State: PlaceLive, BookID: "b", Book: 2, Number: "12", Title: "Белый порт"}}}
	after := Places{Chapters: map[string]ChapterPlace{"c": {State: PlaceLive, BookID: "b", Book: 1, Number: "12", Title: "Белый порт"}}}
	if WhatWent(KindReading, named, before, Paging{}) != "Книга 2, гл. 12 „Белый порт“ целиком" || WhatWent(KindReading, named, after, Paging{}) != "Книга 1, гл. 12 „Белый порт“ целиком" {
		t.Fatal("the place is not the place of now")
	}
}

// Data «получатель»: the server, a program of this computer, the host of the provider.
func TestTheRecipientIsNamedInTheWordsOfTheAuthor(t *testing.T) {
	for _, example := range []struct{ profile, host, want string }{
		{"account", "canonkeeper.example.test", "сервер CanonKeeper"},
		{"own", "127.0.0.1:11434", "программа на этом компьютере (127.0.0.1:11434)"},
		{"own", "localhost:8080", "программа на этом компьютере (localhost:8080)"},
		{"own", "provider.example.com", "provider.example.com"},
	} {
		if got := RecipientWords(example.profile, example.host); got != example.want {
			t.Errorf("%s %s = %s", example.profile, example.host, got)
		}
	}
}
