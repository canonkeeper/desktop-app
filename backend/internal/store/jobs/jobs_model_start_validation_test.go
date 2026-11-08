package jobstore

import (
	"testing"

	"canonkeeper/backend/internal/governance"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestModelPrivateInputRefRestoresExactChapter(t *testing.T) {
	const (
		projectID = "85000000-0000-4000-8000-000000000001"
		bookID    = "85000000-0000-4000-8000-000000000002"
		chapterID = "85000000-0000-4000-8000-000000000003"
	)
	vector := governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: 1,
		BookID: bookID, BookGeneration: 1,
		ChapterID: chapterID, ChapterGeneration: 1,
	}
	generation, err := governance.SourceGenerationDigest(vector)
	if err != nil {
		t.Fatal(err)
	}
	bookUUID, _ := storedb.ParseUUID(bookID)
	chapterUUID, _ := storedb.ParseUUID(chapterID)
	contentHash := governance.Digest{1}
	ref, err := modelPrivateInputRef(projectID, modelPrivateInputRefRow{
		refKind:           string(governance.ArtifactSourceRefChapter),
		sourceView:        pgtype.Text{String: string(governance.SourceViewDraft), Valid: true},
		projectGeneration: 1, bookID: bookUUID, bookGeneration: pgtype.Int8{Int64: 1, Valid: true},
		chapterID: chapterUUID, chapterGeneration: pgtype.Int8{Int64: 1, Valid: true},
		sourceRevision:                pgtype.Int8{Int64: 2, Valid: true},
		locatorSourceGenerationDigest: generation[:],
		textIdentityState:             pgtype.Text{String: string(governance.SourceIdentityTextDerivedCurrent), Valid: true},
		contentHash:                   contentHash[:],
	})
	if err != nil {
		t.Fatal(err)
	}
	fields, err := ref.Fields()
	if err != nil {
		t.Fatal(err)
	}
	if fields.RefKind != governance.ArtifactSourceRefChapter || fields.ChapterID != chapterID || fields.SourceRevision != 2 {
		t.Fatalf("restored private input ref = %#v", fields)
	}
}
