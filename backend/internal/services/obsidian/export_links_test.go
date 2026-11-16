// SPDX-License-Identifier: AGPL-3.0-only

package obsidian_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/obsidian"
)

// The ids of a saga as the database gives them: canonical, so the places of its references are links canonkeeper://.
const (
	linkedSagaID = "0b7c1a52-5d1e-4f43-9f6b-2a8c1e0b9d44"
	linkedFirst  = "7c3f0a52-5d1e-4f43-9f6b-2a8c1e0b9d45"
	linkedSecond = "7c3f0a52-5d1e-4f43-9f6b-2a8c1e0b9d46"
)

// linkedSaga is the saga of sagaReads with canonical ids: Мара first met at the third paragraph of the first chapter, an event
// of that chapter at its fifth, Сава without a place.
type linkedSaga struct{ sagaReads }

func (saga linkedSaga) Node(ctx context.Context, actor, project, key string) (sagamemory.MapCardDocument, error) {
	card, err := saga.sagaReads.Node(ctx, actor, project, key)
	if err == nil && key == "мара" {
		card.First = &sagamemory.CardMention{Chapter: sagamemory.MapChapterRef{ChapterID: linkedFirst, Label: "Книга 1, гл. 1 „Застава“"}, Paragraph: 3}
	}
	return card, err
}

func (linkedSaga) Chapters(context.Context, string) (*sagamemory.Version, *sagamemory.CardSet, error) {
	version := sagamemory.NewVersion(linkedSagaID, "published", "fp", []sagamemory.Chapter{
		{ID: linkedFirst, BookID: "book-1", BookOrder: 1, BookKeyNumber: 1, BookTitle: "Туманный берег", Order: 1, Title: "Глава 1. Застава"},
		{ID: linkedSecond, BookID: "book-1", BookOrder: 1, BookKeyNumber: 1, BookTitle: "Туманный берег", Order: 2, Title: "Глава 2. Переправа"},
	})
	cards := map[int]*sagamemory.Card{
		0: {Summary: "Мара у ворот.", Events: []sagamemory.Event{{Event: "Мара держит ворота", Who: []string{"Мара"}, P: 5}}, Characters: []sagamemory.Mention{{Name: "Мара"}}},
		1: {Summary: "Сава у переправы.", Characters: []sagamemory.Mention{{Name: "Сава"}}},
	}
	return version, sagamemory.NewCardSet(cards, nil), nil
}

// The links of a place a reference written before them did not have: «абзац N» was its words, the reference of a chapter had
// no line that opens it.
var (
	paragraphLinkOf = regexp.MustCompile(`\[(абзац \d+)\]\(canonkeeper://[^)\s]*\)`)
	openLineOf      = regexp.MustCompile(`\[Открыть главу в CanonKeeper\]\(canonkeeper://[^)\s]*\)\n\n`)
)

// specs/2026-11-16-desk-links-writer.md LW1.4: the references an export wrote before the links — the same content, its places as
// words — are updated once by the next export, which writes the links; the one the author changed meanwhile is not updated
// without his word (OE2.5); the export after that writes nothing.
func TestReferencesExportedBeforeTheLinksAreUpdatedOnce(t *testing.T) {
	run := newExportCase(t)
	run.export.Saga = linkedSaga{sagaReads{nodes: []string{"Мара", "Сава"}}}
	export := func(key string) service.Exported {
		t.Helper()
		exported, err := run.export.Execute(context.Background(), service.ExportInput{ActorUserID: "author", ProjectID: linkedSagaID, Key: key})
		if err != nil {
			t.Fatalf("the export %s = %v", key, err)
		}
		return exported
	}
	export("key-1")

	// The folder as the export before the links left it: every reference in the words it had, the manifest naming what it wrote.
	manifest := run.manifest()
	before := map[string]string{}
	for at, file := range manifest.Files {
		full := filepath.Join(run.folder(), filepath.FromSlash(file.Path))
		data, err := os.ReadFile(full)
		if err != nil {
			t.Fatal(err)
		}
		old := paragraphLinkOf.ReplaceAllString(openLineOf.ReplaceAllString(string(data), ""), "$1")
		if old != string(data) {
			before[file.Path] = old
		}
		if err := os.WriteFile(full, []byte(old), 0o644); err != nil {
			t.Fatal(err)
		}
		manifest.Files[at].Print, manifest.Files[at].Sum = obsidian.ExportPrint([]byte(old)), obsidian.ExportSum([]byte(old))
	}
	if err := os.WriteFile(filepath.Join(run.folder(), ".canonkeeper", "manifest.json"), manifest.Encode(), 0o644); err != nil {
		t.Fatal(err)
	}
	mara, first, second := "Герои/Мара.md", "Главы/Туманный берег/1 Застава.md", "Главы/Туманный берег/2 Переправа.md"
	if len(before) != 3 || !strings.Contains(before[mara], "Застава“]], абзац 3\n") || !strings.Contains(before[first], "Мара]] · абзац 5\n") {
		t.Fatalf("the references the links change: %q", before)
	}
	// The author changed the reference of the second chapter.
	edited := before[second] + "правка автора\n"
	if err := os.WriteFile(filepath.Join(run.folder(), filepath.FromSlash(second)), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	updated := export("key-2")
	slices.Sort(updated.Updated.Names)
	if !slices.Equal(updated.Updated.Names, []string{mara, first}) || !slices.Equal(updated.Kept.Names, []string{second}) ||
		updated.Unchanged.Count != 2 || updated.Created.Count != 0 || updated.Moved.Count != 0 || !updated.Complete {
		t.Fatalf("the export after the links = %+v", updated)
	}
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(run.folder(), filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for name, want := range map[string]string{
		mara:  "Застава“]], [абзац 3](canonkeeper://open?saga=" + linkedSagaID + "&chapter=" + linkedFirst + "&p=3)\n",
		first: "\n[Открыть главу в CanonKeeper](canonkeeper://open?saga=" + linkedSagaID + "&chapter=" + linkedFirst + ")\n",
	} {
		if text := read(name); !strings.Contains(text, want) {
			t.Fatalf("%s has no %q:\n%s", name, want, text)
		}
	}
	if text := read(second); text != edited {
		t.Fatalf("the reference the author changed is not his:\n%s", text)
	}

	again := export("key-3")
	if again.Updated.Count+again.Created.Count+again.Moved.Count != 0 || again.Unchanged.Count != 4 || !slices.Equal(again.Kept.Names, []string{second}) {
		t.Fatalf("the export after that = %+v", again)
	}
}
