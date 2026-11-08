package search

import (
	"encoding/json"
	"testing"

	"canonkeeper/backend/internal/governance"
)

func TestChapterSearchAnchorUsesOnlyPersistedParagraphMatch(t *testing.T) {
	for _, view := range []governance.SourceView{governance.SourceViewPublished, governance.SourceViewDraft} {
		t.Run(string(view), func(t *testing.T) {
			row := canonicalChapterRowForTest(t, canonicalSearchChapterID, 1, 0)
			row.ChapterNumber = "12"
			row.ChapterSource.SourceView = view
			row.ParagraphMatch = &ChapterParagraphMatch{ID: canonicalSearchResultID, Position: 18, Content: "дракон в абзаце"}
			request := SearchRequest{ProjectID: row.ProjectID, SourceView: view, Query: "дракон"}
			result, err := searchResultFromRow(request, row)
			if err != nil {
				t.Fatal(err)
			}
			locator := result.Locator
			anchor := locator.SourceAnchor
			if anchor.Kind != "paragraph" || anchor.Locator.ParagraphID == nil || *anchor.Locator.ParagraphID != row.ParagraphMatch.ID ||
				anchor.Locator.TargetView != string(view) || anchor.Locator.Revision != int64(row.ChapterSource.SourceRevision) ||
				anchor.Locator.BookID != row.BookID || anchor.Locator.ChapterID != row.ChapterID || result.Excerpt != row.ParagraphMatch.Content {
				t.Fatalf("source anchor = %#v, excerpt = %q", anchor, result.Excerpt)
			}
			if locator.SourceRef != row.ChapterID {
				t.Fatal("navigation changed canonical chapter identity")
			}
			label := "Глава 12 · абзац 18 · опубликованная версия"
			if view == governance.SourceViewDraft {
				label = "Глава 12 · абзац 18 · черновик"
			}
			if anchor.Label != label {
				t.Fatalf("label = %q, want %q", anchor.Label, label)
			}

			row.ParagraphMatch = nil
			result, err = searchResultFromRow(request, row)
			if err != nil {
				t.Fatal(err)
			}
			anchor = result.Locator.SourceAnchor
			if anchor.Kind != "chapter" || anchor.Locator.ParagraphID != nil {
				t.Fatalf("whole-chapter result invented a paragraph: %#v", anchor)
			}
			raw, err := json.Marshal(anchor.Locator)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			if _, present := wire["paragraphId"]; present {
				t.Fatal("chapter anchor contains paragraphId")
			}
		})
	}
}
