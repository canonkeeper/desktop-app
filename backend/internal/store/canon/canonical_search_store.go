package canonstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/search"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const canonicalManifestSetHashDomain = "CKCanonicalSearchManifestSetV1\x00"

type CanonicalTextSelectionInput struct {
	ProjectID  string
	SourceView governance.SourceView
	BookID     string
	ChapterID  string
}

type CanonicalTextSelection struct {
	CurrentBookIDs       []string
	TextCoverage         jobs.TextCoverage
	currentBookManifests map[string]governance.Digest
}

type searchError struct {
	kind   error
	action search.Action
}

func (err *searchError) Error() string { return err.kind.Error() }
func (err *searchError) Unwrap() error { return err.kind }
func (err *searchError) SearchAction() search.Action {
	return err.action
}

func loadCanonicalTextSelectionInTx(ctx context.Context, tx pgx.Tx, input CanonicalTextSelectionInput) (CanonicalTextSelection, error) {
	if tx == nil || !validSourceCatalogView(input.SourceView) {
		return CanonicalTextSelection{}, ErrCanonicalSourceValidation
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return CanonicalTextSelection{}, ErrCanonicalSourceValidation
	}
	if input.BookID != "" {
		if _, err := storedb.ParseUUID(input.BookID); err != nil {
			return CanonicalTextSelection{}, ErrCanonicalSourceValidation
		}
	}
	if input.ChapterID != "" {
		if _, err := storedb.ParseUUID(input.ChapterID); err != nil {
			return CanonicalTextSelection{}, ErrCanonicalSourceValidation
		}
	}

	queries := sqlc.New(tx)
	rows, err := queries.ListCanonicalTextSelectionRows(ctx, sqlc.ListCanonicalTextSelectionRowsParams{
		ProjectID: projectID, SourceView: string(input.SourceView),
	})
	if err != nil {
		return CanonicalTextSelection{}, err
	}
	if len(rows) == 0 {
		return CanonicalTextSelection{}, canonicalSourceViewUnavailable(input.SourceView)
	}

	books := make([]canonicalTextBookCandidate, 0)
	targetBookID := input.BookID
	chapterFound := input.ChapterID == ""
	for _, row := range rows {
		if !row.BookID.Valid {
			continue
		}
		bookID := storedb.UUIDString(row.BookID)
		if len(books) == 0 || books[len(books)-1].id != bookID {
			books = append(books, canonicalTextBookCandidate{
				id:                    bookID,
				projectGeneration:     row.ProjectLifecycleGeneration,
				bookGeneration:        row.BookLifecycleGeneration.Int64,
				title:                 row.BookTitle.String,
				sourceCurrent:         row.BookLifecycleGeneration.Valid && row.BookTitle.Valid && row.LiveBookID.Valid && row.LiveBookID == row.BookID,
				indexStructureCurrent: true,
				chapters:              make([]governance.CanonicalBookChapter, 0),
			})
		}
		book := &books[len(books)-1]
		if !row.ChapterID.Valid {
			continue
		}
		book.chapterCount++
		chapterID := storedb.UUIDString(row.ChapterID)
		if chapterID == input.ChapterID {
			chapterFound = true
			if targetBookID != "" && targetBookID != bookID {
				return CanonicalTextSelection{}, ErrCanonicalSourceValidation
			}
			targetBookID = bookID
		}
		if !row.ChapterLifecycleGeneration.Valid || !row.SourceRevision.Valid ||
			!row.ChapterTitle.Valid || !row.ChapterOrder.Valid || row.ChapterOrder.Int32 < 0 ||
			!row.LiveChapterID.Valid || row.LiveChapterID != row.ChapterID {
			book.sourceCurrent = false
			continue
		}
		expectedGeneration, digestErr := governance.SourceGenerationDigest(governance.SourceGenerationVector{
			ProjectID: input.ProjectID, ProjectGeneration: governance.LifecycleGeneration(row.ProjectLifecycleGeneration),
			BookID: bookID, BookGeneration: governance.LifecycleGeneration(row.BookLifecycleGeneration.Int64),
			ChapterID: chapterID, ChapterGeneration: governance.LifecycleGeneration(row.ChapterLifecycleGeneration.Int64),
		})
		if digestErr != nil {
			book.sourceCurrent = false
			continue
		}
		if !row.StateSourceRevision.Valid || row.StateSourceRevision.Int64 != row.SourceRevision.Int64 ||
			!bytes.Equal(row.StateSourceGeneration, expectedGeneration[:]) ||
			len(row.StateChapterContentHash) != len(governance.Digest{}) {
			book.indexStructureCurrent = false
			continue
		}
		var chapterContentHash governance.Digest
		copy(chapterContentHash[:], row.StateChapterContentHash)
		book.chapters = append(book.chapters, governance.CanonicalBookChapter{
			ChapterID: chapterID, Order: uint32(row.ChapterOrder.Int32), Title: row.ChapterTitle.String,
			SourceRevision: uint64(row.SourceRevision.Int64), SourceGeneration: governance.Digest(expectedGeneration),
			ChapterContentHash: chapterContentHash,
		})
	}
	if !chapterFound {
		return CanonicalTextSelection{}, &searchError{kind: search.ErrBookTextNotCurrent, action: search.ActionFixSource}
	}

	selection := CanonicalTextSelection{
		CurrentBookIDs: make([]string, 0, len(books)), currentBookManifests: make(map[string]governance.Digest, len(books)),
	}
	for index := range books {
		book := &books[index]
		if input.SourceView == governance.SourceViewPublished && book.chapterCount == 0 {
			book.sourceCurrent = false
		}
		if book.sourceCurrent && book.indexStructureCurrent {
			book.current = len(book.chapters) == book.chapterCount
		}
		if book.current {
			manifest, err := governance.CanonicalBookSourceManifest(governance.CanonicalBookSource{
				ProjectID: input.ProjectID, ProjectLifecycleGeneration: governance.LifecycleGeneration(book.projectGeneration),
				SourceView: input.SourceView, BookID: book.id, Title: book.title,
				BookLifecycleGeneration: governance.LifecycleGeneration(book.bookGeneration), Chapters: book.chapters,
			})
			if err != nil {
				return CanonicalTextSelection{}, ErrCanonicalSourceNotCurrent
			}
			book.manifest = manifest
		}
		if book.current {
			selection.TextCoverage.CurrentBooks++
			selection.currentBookManifests[book.id] = book.manifest
			if targetBookID == "" || targetBookID == book.id {
				selection.CurrentBookIDs = append(selection.CurrentBookIDs, book.id)
			}
		}
	}
	selection.TextCoverage.TotalBooks = len(books)
	selection.TextCoverage.TotalNonCurrentBooks = len(books) - selection.TextCoverage.CurrentBooks
	selection.TextCoverage.NonCurrentBooks = selection.TextCoverage.TotalNonCurrentBooks

	if targetBookID != "" {
		for index := range books {
			if books[index].id != targetBookID {
				continue
			}
			if books[index].current {
				return selection, nil
			}
			action := search.ActionStartIndexing
			if !books[index].sourceCurrent {
				action = search.ActionFixSource
			}
			return CanonicalTextSelection{}, &searchError{kind: search.ErrBookTextNotCurrent, action: action}
		}
		return CanonicalTextSelection{}, &searchError{kind: search.ErrBookTextNotCurrent, action: search.ActionFixSource}
	}
	return selection, nil
}

type canonicalTextBookCandidate struct {
	id                    string
	projectGeneration     int64
	bookGeneration        int64
	title                 string
	chapterCount          int
	sourceCurrent         bool
	indexStructureCurrent bool
	current               bool
	chapters              []governance.CanonicalBookChapter
	manifest              governance.Digest
}

func (db *Store) SearchLexical(ctx context.Context, input search.LexicalSearchInput) (search.LexicalSearchOutput, error) {
	if db == nil || db.Pool == nil {
		return search.LexicalSearchOutput{}, ErrCanonicalSourceValidation
	}
	projectID, actorID, err := validateLexicalInput(input)
	if err != nil {
		return search.LexicalSearchOutput{}, err
	}
	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return search.LexicalSearchOutput{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := sqlc.New(tx)
	role, err := queries.GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{
		ActorUserID: actorID, ProjectID: projectID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return search.LexicalSearchOutput{}, search.ErrNotFound
	} else if err != nil {
		return search.LexicalSearchOutput{}, err
	}
	projectRole := projects.Role(role)
	if !projects.CanReadProject(projectRole) || (input.SourceView == governance.SourceViewDraft && !projects.CanMutateProject(projectRole)) {
		return search.LexicalSearchOutput{}, search.ErrNotFound
	}

	output, err := db.SearchLexicalInTx(ctx, tx, input)
	if err != nil {
		return search.LexicalSearchOutput{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return search.LexicalSearchOutput{}, err
	}
	return output, nil
}

func (db *Store) SearchLexicalInTx(ctx context.Context, tx pgx.Tx, input search.LexicalSearchInput) (search.LexicalSearchOutput, error) {
	if tx == nil {
		return search.LexicalSearchOutput{}, search.ErrValidation
	}
	if _, err := validateLexicalScopeInput(input); err != nil {
		return search.LexicalSearchOutput{}, err
	}
	selection, err := loadCanonicalTextSelectionInTx(ctx, tx, CanonicalTextSelectionInput{
		ProjectID: input.ProjectID, SourceView: input.SourceView, BookID: input.BookID, ChapterID: input.ChapterID,
	})
	if errors.Is(err, ErrCanonicalSourceValidation) {
		return search.LexicalSearchOutput{}, search.ErrValidation
	}
	if err != nil {
		return search.LexicalSearchOutput{}, err
	}
	return searchLexicalInTx(ctx, tx, input, selection)
}

func searchLexicalInTx(ctx context.Context, tx pgx.Tx, input search.LexicalSearchInput, selection CanonicalTextSelection) (search.LexicalSearchOutput, error) {
	manifestHash := canonicalManifestSetHash(canonicalManifestEntries(selection))
	if input.ExpectedManifestHash != nil && *input.ExpectedManifestHash != manifestHash {
		return search.LexicalSearchOutput{}, search.ErrCursorDrift
	}
	output := search.LexicalSearchOutput{TextCoverage: search.TextCoverage(selection.TextCoverage), ManifestHash: manifestHash}
	projectID, _ := storedb.ParseUUID(input.ProjectID)
	bookID, _ := optionalCanonUUIDValue(input.BookID)
	chapterID, _ := optionalCanonUUIDValue(input.ChapterID)
	currentBookIDs := make([]pgtype.UUID, len(selection.CurrentBookIDs))
	for index, value := range selection.CurrentBookIDs {
		currentBookIDs[index], _ = storedb.ParseUUID(value)
	}
	params := sqlc.SearchCanonicalLexicalParams{
		LimitCount: int32(input.Limit), Query: input.Query,
		ProjectID: projectID, SourceView: string(input.SourceView), CurrentBookIds: currentBookIDs,
		BookID: bookID, ChapterID: chapterID, UpdatedSince: optionalCanonTimestamptzValue(input.UpdatedSince),
		CursorBookID: projectID, CursorChapterID: projectID, CursorResultID: projectID,
	}
	if input.Cursor != nil {
		params.HasCursor = true
		params.CursorRank = math.Float64frombits(input.Cursor.RankBits)
		params.CursorBookID, _ = storedb.ParseUUID(input.Cursor.BookID)
		params.CursorChapterID, _ = storedb.ParseUUID(input.Cursor.ChapterID)
		params.CursorResultID, _ = storedb.ParseUUID(input.Cursor.ResultID)
	}
	rows, err := sqlc.New(tx).SearchCanonicalLexical(ctx, params)
	if err != nil {
		return search.LexicalSearchOutput{}, err
	}
	output.Rows = make([]search.LexicalSearchRow, len(rows))
	for index, row := range rows {
		output.Rows[index], err = lexicalSearchRow(input, row)
		if err != nil {
			return search.LexicalSearchOutput{}, err
		}
	}
	return output, nil
}

func validateLexicalInput(input search.LexicalSearchInput) (pgtype.UUID, pgtype.UUID, error) {
	projectID, err := validateLexicalScopeInput(input)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	actorID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, search.ErrNotFound
	}
	return projectID, actorID, nil
}

func validateLexicalScopeInput(input search.LexicalSearchInput) (pgtype.UUID, error) {
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return pgtype.UUID{}, search.ErrNotFound
	}
	if !validSourceCatalogView(input.SourceView) || strings.TrimSpace(input.Query) == "" ||
		!utf8.ValidString(input.Query) || utf8.RuneCountInString(input.Query) > 500 || input.Limit < 1 || input.Limit > 51 {
		return pgtype.UUID{}, search.ErrValidation
	}
	if _, err := optionalCanonUUIDValue(input.BookID); err != nil {
		return pgtype.UUID{}, search.ErrValidation
	}
	if _, err := optionalCanonUUIDValue(input.ChapterID); err != nil {
		return pgtype.UUID{}, search.ErrValidation
	}
	if input.UpdatedSince != nil && input.UpdatedSince.IsZero() {
		return pgtype.UUID{}, search.ErrValidation
	}
	if input.ExpectedManifestHash != nil && *input.ExpectedManifestHash == ([32]byte{}) {
		return pgtype.UUID{}, search.ErrValidation
	}
	if input.Cursor != nil {
		rank := math.Float64frombits(input.Cursor.RankBits)
		if rank <= 0 || math.IsNaN(rank) || math.IsInf(rank, 0) {
			return pgtype.UUID{}, search.ErrValidation
		}
		for _, value := range []string{input.Cursor.BookID, input.Cursor.ChapterID, input.Cursor.ResultID} {
			if _, err := storedb.ParseUUID(value); err != nil {
				return pgtype.UUID{}, search.ErrValidation
			}
		}
	}
	return projectID, nil
}

func canonicalManifestEntries(selection CanonicalTextSelection) []canonicalManifestEntry {
	entries := make([]canonicalManifestEntry, 0, len(selection.CurrentBookIDs))
	for _, bookID := range selection.CurrentBookIDs {
		entries = append(entries, canonicalManifestEntry{bookID: bookID, manifest: selection.currentBookManifests[bookID]})
	}
	return entries
}

type canonicalManifestEntry struct {
	bookID   string
	manifest governance.Digest
}

func canonicalManifestSetHash(entries []canonicalManifestEntry) [32]byte {
	entries = append([]canonicalManifestEntry(nil), entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].bookID < entries[j].bookID })
	projection := append([]byte(nil), canonicalManifestSetHashDomain...)
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(entries)))
	projection = append(projection, count[:]...)
	for _, entry := range entries {
		bookID, err := storedb.ParseUUID(entry.bookID)
		if err != nil || entry.manifest == (governance.Digest{}) {
			return [32]byte{}
		}
		projection = append(projection, bookID.Bytes[:]...)
		projection = append(projection, entry.manifest[:]...)
	}
	return sha256.Sum256(projection)
}

func mustStoreUUID(value string) pgtype.UUID {
	parsed, _ := storedb.ParseUUID(value)
	return parsed
}

func optionalCanonUUIDValue(value string) (pgtype.UUID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return pgtype.UUID{}, nil
	}
	return storedb.ParseUUID(value)
}

func optionalCanonTimestamptzValue(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return storedb.Timestamptz(*value)
}

func canonicalSourceViewUnavailable(view governance.SourceView) error {
	action := search.ActionCreateOrImportSource
	if view == governance.SourceViewPublished {
		action = search.ActionPublishSource
	}
	return &searchError{kind: search.ErrSourceViewUnavailable, action: action}
}
