package canonstore

import (
	"testing"

	"canonkeeper/backend/internal/governance"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestArtifactSourceTransferRestoresExactChapterRef(t *testing.T) {
	const (
		projectID = "81000000-0000-4000-8000-000000000001"
		bookID    = "81000000-0000-4000-8000-000000000002"
		chapterID = "81000000-0000-4000-8000-000000000003"
	)
	vector := governance.SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: 1,
		BookID: bookID, BookGeneration: 2,
		ChapterID: chapterID, ChapterGeneration: 3,
	}
	generation, err := governance.SourceGenerationDigest(vector)
	if err != nil {
		t.Fatal(err)
	}
	contentHash := governance.Digest{4}
	projectUUID, _ := storedb.ParseUUID(projectID)
	bookUUID, _ := storedb.ParseUUID(bookID)
	chapterUUID, _ := storedb.ParseUUID(chapterID)
	ref, err := artifactSourceTransferStoredRef(sqlc.ArtifactSourceRef{
		ProjectID: projectUUID, RefKind: string(governance.ArtifactSourceRefChapter),
		SourceView:        pgtype.Text{String: string(governance.SourceViewDraft), Valid: true},
		ProjectGeneration: 1, BookID: bookUUID, BookGeneration: pgtype.Int8{Int64: 2, Valid: true},
		ChapterID: chapterUUID, ChapterGeneration: pgtype.Int8{Int64: 3, Valid: true},
		SourceRevision:          pgtype.Int8{Int64: 5, Valid: true},
		LocatorSourceGeneration: generation[:], ContentHash: contentHash[:],
		TextIdentityState: pgtype.Text{String: string(governance.SourceIdentityTextDerivedCurrent), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	fields, err := ref.Fields()
	if err != nil {
		t.Fatal(err)
	}
	if fields.RefKind != governance.ArtifactSourceRefChapter || fields.ProjectID != projectID ||
		fields.BookID != bookID || fields.ChapterID != chapterID || fields.SourceRevision != 5 ||
		fields.LocatorSourceGenerationDigest != generation || fields.ContentHash != contentHash {
		t.Fatalf("restored chapter ref = %#v", fields)
	}
}

func TestArtifactSourceTransferRejectsLegacyLocatorRow(t *testing.T) {
	const (
		projectID = "82000000-0000-4000-8000-000000000001"
		bookID    = "82000000-0000-4000-8000-000000000002"
		chapterID = "82000000-0000-4000-8000-000000000003"
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
	projectUUID, _ := storedb.ParseUUID(projectID)
	bookUUID, _ := storedb.ParseUUID(bookID)
	chapterUUID, _ := storedb.ParseUUID(chapterID)
	if _, err := artifactSourceTransferStoredRef(sqlc.ArtifactSourceRef{
		ProjectID: projectUUID, RefKind: "locator",
		SourceView:        pgtype.Text{String: string(governance.SourceViewDraft), Valid: true},
		ProjectGeneration: 1, BookID: bookUUID, BookGeneration: pgtype.Int8{Int64: 1, Valid: true},
		ChapterID: chapterUUID, ChapterGeneration: pgtype.Int8{Int64: 1, Valid: true},
		SourceRevision:          pgtype.Int8{Int64: 1, Valid: true},
		LocatorSourceGeneration: generation[:],
		TextIdentityState:       pgtype.Text{String: "lifecycle_only", Valid: true},
	}); err == nil {
		t.Fatal("legacy locator row was accepted")
	}
}
