package canonstore

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/search"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	canonicalExactPhraseRawByteLimit = 2 * 1024 * 1024
	canonicalExactChapterScanLimit   = 10_001
)

func (db *Store) SearchExactPhrase(ctx context.Context, input search.ExactPhraseSearchInput) (search.ExactPhraseSearchOutput, error) {
	if db == nil || db.Pool == nil {
		return search.ExactPhraseSearchOutput{}, ErrCanonicalSourceValidation
	}
	projectID, actorID, err := validateExactPhraseInput(input)
	if err != nil {
		return search.ExactPhraseSearchOutput{}, err
	}
	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return search.ExactPhraseSearchOutput{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := sqlc.New(tx)
	role, err := queries.GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{
		ActorUserID: actorID, ProjectID: projectID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return search.ExactPhraseSearchOutput{}, search.ErrNotFound
	}
	if err != nil {
		return search.ExactPhraseSearchOutput{}, err
	}
	projectRole := projects.Role(role)
	if !projects.CanReadProject(projectRole) || input.SourceView == governance.SourceViewDraft && !projects.CanMutateProject(projectRole) {
		return search.ExactPhraseSearchOutput{}, search.ErrNotFound
	}
	selection, err := loadCanonicalTextSelectionInTx(ctx, tx, CanonicalTextSelectionInput{
		ProjectID: input.ProjectID, SourceView: input.SourceView, BookID: input.BookID, ChapterID: input.ChapterID,
	})
	if err != nil {
		if errors.Is(err, ErrCanonicalSourceValidation) {
			return search.ExactPhraseSearchOutput{}, search.ErrValidation
		}
		return search.ExactPhraseSearchOutput{}, err
	}
	manifest, ok := selection.currentBookManifests[input.BookID]
	if !ok || manifest == (governance.Digest{}) {
		return search.ExactPhraseSearchOutput{}, search.ErrBookTextNotCurrent
	}
	manifestHash := [32]byte(manifest)
	if input.ExpectedManifestHash != nil && *input.ExpectedManifestHash != manifestHash {
		return search.ExactPhraseSearchOutput{}, search.ErrCursorDrift
	}
	output, err := searchExactPhraseInTx(ctx, queries, input, manifestHash)
	if err != nil {
		return search.ExactPhraseSearchOutput{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return search.ExactPhraseSearchOutput{}, err
	}
	return output, nil
}

func validateExactPhraseInput(input search.ExactPhraseSearchInput) (pgtype.UUID, pgtype.UUID, error) {
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, search.ErrNotFound
	}
	actorID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, search.ErrNotFound
	}
	if !validSourceCatalogView(input.SourceView) || input.BookID == "" || input.Limit < 1 || input.Limit > 50 {
		return pgtype.UUID{}, pgtype.UUID{}, search.ErrValidation
	}
	if _, err := storedb.ParseUUID(input.BookID); err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, search.ErrValidation
	}
	if _, err := optionalCanonUUIDValue(input.ChapterID); err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, search.ErrValidation
	}
	query, ok := search.NormalizeExactPhrase(input.Query)
	if !ok || query == "" || query != input.Query || utf8.RuneCountInString(query) > 500 {
		return pgtype.UUID{}, pgtype.UUID{}, search.ErrValidation
	}
	if input.ExpectedManifestHash != nil && *input.ExpectedManifestHash == ([32]byte{}) {
		return pgtype.UUID{}, pgtype.UUID{}, search.ErrValidation
	}
	if input.Cursor != nil {
		if input.Cursor.ChapterOrder < 0 {
			return pgtype.UUID{}, pgtype.UUID{}, search.ErrValidation
		}
		if _, err := storedb.ParseUUID(input.Cursor.ChapterID); err != nil {
			return pgtype.UUID{}, pgtype.UUID{}, search.ErrValidation
		}
	}
	return projectID, actorID, nil
}

func searchExactPhraseInTx(ctx context.Context, queries *sqlc.Queries, input search.ExactPhraseSearchInput, manifestHash [32]byte) (search.ExactPhraseSearchOutput, error) {
	projectID, _ := storedb.ParseUUID(input.ProjectID)
	bookID, _ := storedb.ParseUUID(input.BookID)
	chapterID, _ := optionalCanonUUIDValue(input.ChapterID)
	params := sqlc.ListCanonicalExactChapterDocumentsParams{
		ProjectID: projectID, SourceView: string(input.SourceView), BookID: bookID,
		ChapterID: chapterID, Query: input.Query, CandidateLimit: canonicalExactChapterScanLimit,
	}
	if input.Cursor != nil {
		params.HasCursor = true
		params.CursorChapterOrder = input.Cursor.ChapterOrder
		params.CursorChapterID, _ = storedb.ParseUUID(input.Cursor.ChapterID)
	}
	rows, err := queries.ListCanonicalExactChapterDocuments(ctx, params)
	if err != nil {
		return search.ExactPhraseSearchOutput{}, err
	}
	if len(rows) == canonicalExactChapterScanLimit {
		return search.ExactPhraseSearchOutput{}, search.ErrExactPhraseBoundedness
	}
	output := search.ExactPhraseSearchOutput{Matches: []search.ExactPhraseMatch{}, ManifestHash: manifestHash}
	for _, row := range rows {
		output.RawBytesRead += len(row.Content)
		if output.RawBytesRead > canonicalExactPhraseRawByteLimit {
			return search.ExactPhraseSearchOutput{}, search.ErrExactPhraseBoundedness
		}
		normalized := normalizeCanonicalExactText(row.Content, 0)
		index := strings.Index(normalized.value, input.Query)
		if index < 0 {
			continue
		}
		end := index + len(input.Query)
		if end > len(normalized.spans) {
			return search.ExactPhraseSearchOutput{}, search.ErrCorrupt
		}
		chapter := storedb.UUIDString(row.ChapterID)
		output.Matches = append(output.Matches, search.ExactPhraseMatch{
			ChapterOrder: row.ChapterOrder, ChapterID: chapter,
			StartByte: normalized.spans[index].start, EndByte: normalized.spans[end-1].end,
			SourceRef: chapter,
		})
		if len(output.Matches) > input.Limit {
			output.Matches = output.Matches[:input.Limit]
			last := output.Matches[len(output.Matches)-1]
			output.NextCursor = &search.ExactPhraseCursor{ChapterOrder: last.ChapterOrder, ChapterID: last.ChapterID}
			output.Truncated = true
			return output, nil
		}
	}
	return output, nil
}
