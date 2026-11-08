package governance_test

import (
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
)

func TestSourceViewMatchesLibraryParagraphVariant(t *testing.T) {
	if string(governance.SourceViewDraft) != string(library.ParagraphVariantDraft) ||
		string(governance.SourceViewPublished) != string(library.ParagraphVariantPublished) {
		t.Fatal("governance.SourceView no longer maps exactly to library.ParagraphVariant")
	}
}
