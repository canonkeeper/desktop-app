package canonstore

import (
	"math"

	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/search"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"
)

func lexicalSearchRow(input search.LexicalSearchInput, row sqlc.SearchCanonicalLexicalRow) (search.LexicalSearchRow, error) {
	if !row.ID.Valid || !row.ProjectID.Valid || !row.BookID.Valid || !row.ChapterID.Valid ||
		row.ProjectID != mustStoreUUID(input.ProjectID) || row.Rank <= 0 || math.IsNaN(row.Rank) || math.IsInf(row.Rank, 0) {
		return search.LexicalSearchRow{}, search.ErrCorrupt
	}
	if row.ID != row.ChapterID || row.ChapterPosition < 1 || row.SourceRevision < 1 || row.SourceView != string(input.SourceView) ||
		len(row.LocatorSourceGeneration) != len(governance.Digest{}) || len(row.ContentHash) != len(governance.Digest{}) {
		return search.LexicalSearchRow{}, search.ErrCorrupt
	}
	var generation, contentHash governance.Digest
	copy(generation[:], row.LocatorSourceGeneration)
	copy(contentHash[:], row.ContentHash)
	if generation == (governance.Digest{}) || contentHash == (governance.Digest{}) {
		return search.LexicalSearchRow{}, search.ErrCorrupt
	}
	// The number of the chapter of a found paragraph is the number the reader shows (specs/2026-11-15-reader-chapter-number.md RN1.2).
	number, _ := sagamemory.NumberedTitle(row.ChapterTitle, int(row.ChapterPosition))
	result := search.LexicalSearchRow{
		ID: storedb.UUIDString(row.ID), ProjectID: storedb.UUIDString(row.ProjectID),
		BookID: storedb.UUIDString(row.BookID), ChapterID: storedb.UUIDString(row.ChapterID),
		Content: row.Content, BookTitle: row.BookTitle, ChapterTitle: row.ChapterTitle, Rank: row.Rank,
		ChapterNumber: number,
		ChapterSource: &search.ChapterSourceIdentity{
			SourceView: input.SourceView, SourceRevision: uint64(row.SourceRevision),
			SourceGeneration: generation, ContentHash: contentHash,
		},
	}
	if row.SourceParagraphID != "" {
		if row.SourceParagraphPosition < 1 {
			return search.LexicalSearchRow{}, search.ErrCorrupt
		}
		result.ParagraphMatch = &search.ChapterParagraphMatch{
			ID: row.SourceParagraphID, Position: int(row.SourceParagraphPosition), Content: row.SourceParagraphContent,
		}
	}
	return result, nil
}
