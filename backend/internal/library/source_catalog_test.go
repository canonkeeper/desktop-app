package library

import (
	"testing"

	"canonkeeper/backend/internal/governance"
)

func TestSourceCatalogTypesAcceptOnlyCompleteDraftOrPublishedIdentity(t *testing.T) {
	for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		member := ChapterSourceMember{
			BookSourceMember: BookSourceMember{
				ProjectID: "project", ProjectLifecycleGeneration: 1,
				SourceView: view, BookID: "book", BookLifecycleGeneration: 1, Title: "Book",
			},
			ChapterID: "chapter", ChapterLifecycleGeneration: 1,
			SourceRevision: 1, Title: "Chapter", Order: 0,
		}
		if err := member.Validate(); err != nil {
			t.Fatalf("Validate(%q) error = %v", view, err)
		}
	}

	invalid := ChapterSourceMember{
		BookSourceMember: BookSourceMember{
			ProjectID: "project", ProjectLifecycleGeneration: 1,
			SourceView: governance.SourceView("preview"), BookID: "book", BookLifecycleGeneration: 1, Title: "Book",
		},
		ChapterID: "chapter", ChapterLifecycleGeneration: 1,
		SourceRevision: 1, Title: "Chapter", Order: 0,
	}
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate(preview) error = nil")
	}

	invalid.SourceView = governance.SourceViewDraft
	invalid.SourceRevision = 0
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate(zero source revision) error = nil")
	}
	invalid.SourceRevision = 1
	invalid.Title = "   "
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate(blank chapter title) error = nil")
	}
}

func TestSourceCatalogRevisionRejectsAbsentMarkerShape(t *testing.T) {
	valid := SourceCatalogRevision{
		ProjectID: "project", ProjectLifecycleGeneration: 1,
		SourceView: governance.SourceViewDraft, CatalogRevision: 1,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	valid.CatalogRevision = 0
	if err := valid.Validate(); err == nil {
		t.Fatal("Validate(zero catalog revision) error = nil")
	}
}
