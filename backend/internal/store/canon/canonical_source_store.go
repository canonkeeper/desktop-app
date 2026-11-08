package canonstore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/indexing"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrCanonicalSourceNotCurrent = errors.New("store: canonical source is not current")
	ErrCanonicalSourceValidation = errors.New("store: invalid canonical source input")
)

type ResolvedChapterSource struct {
	SourceRef        string
	BookTitle        string
	ChapterTitle     string
	ChapterPosition  int
	SourceRevision   uint64
	SourceGeneration governance.Digest
	ContentHash      governance.Digest
	Content          string
	Paragraphs       []indexing.CanonicalChapterParagraph
	ExactRef         governance.ArtifactSourceRef
}

func (db *Store) ResolveCanonicalChapterSourceInTx(
	ctx context.Context,
	tx pgx.Tx,
	projectIDValue string,
	view governance.SourceView,
	bookIDValue string,
	chapterIDValue string,
) (ResolvedChapterSource, error) {
	if db == nil || tx == nil || !validSourceCatalogView(view) {
		return ResolvedChapterSource{}, ErrCanonicalSourceValidation
	}
	return resolveCanonicalChapterSource(
		ctx, sqlc.New(tx), projectIDValue, view, bookIDValue, chapterIDValue,
	)
}

func resolveCanonicalChapterSource(
	ctx context.Context,
	queries *sqlc.Queries,
	projectIDValue string,
	view governance.SourceView,
	bookIDValue string,
	chapterIDValue string,
) (ResolvedChapterSource, error) {
	if queries == nil || !validSourceCatalogView(view) {
		return ResolvedChapterSource{}, ErrCanonicalSourceValidation
	}
	projectID, err := storedb.ParseUUID(projectIDValue)
	if err != nil {
		return ResolvedChapterSource{}, ErrCanonicalSourceValidation
	}
	bookID, err := optionalCanonUUIDValue(bookIDValue)
	if err != nil {
		return ResolvedChapterSource{}, ErrCanonicalSourceValidation
	}
	chapterID, err := storedb.ParseUUID(chapterIDValue)
	if err != nil {
		return ResolvedChapterSource{}, ErrCanonicalSourceValidation
	}
	row, err := queries.ResolveCanonicalChapterSource(ctx, sqlc.ResolveCanonicalChapterSourceParams{
		ProjectID: projectID, SourceView: string(view), BookID: bookID, ChapterID: chapterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolvedChapterSource{}, ErrCanonicalSourceNotCurrent
	}
	if err != nil {
		return ResolvedChapterSource{}, err
	}
	if row.ChapterPosition < 1 {
		return ResolvedChapterSource{}, ErrCanonicalSourceNotCurrent
	}
	ref, err := newCurrentChapterRef(projectIDValue, view, chapterSourceRow{
		BookID: row.BookID, ChapterID: chapterID, SourceRevision: row.SourceRevision,
		LocatorSourceGeneration: row.LocatorSourceGeneration, ChapterContentHash: row.ChapterContentHash,
		ProjectGeneration: row.ProjectLifecycleGeneration, BookGeneration: row.BookLifecycleGeneration,
		ChapterGeneration: row.ChapterLifecycleGeneration,
	})
	if err != nil {
		return ResolvedChapterSource{}, err
	}
	return ResolvedChapterSource{
		SourceRef: chapterIDValue, BookTitle: row.BookTitle, ChapterTitle: row.ChapterTitle,
		ChapterPosition: int(row.ChapterPosition), SourceRevision: ref.SourceRevision,
		SourceGeneration: ref.SourceGeneration, ContentHash: ref.ContentHash,
		Content: row.Content, ExactRef: ref.ExactRef,
	}, nil
}

// CurrentChapterRef is the exact source identity of the current text of one chapter.
type CurrentChapterRef struct {
	BookID           string
	ChapterID        string
	SourceRevision   uint64
	SourceGeneration governance.Digest
	ContentHash      governance.Digest
	ExactRef         governance.ArtifactSourceRef
}

// ResolveCurrentChapterRefsInTx resolves the exact current source identities of many
// chapters with one query. Chapters without current indexed text are absent from the map.
func (db *Store) ResolveCurrentChapterRefsInTx(
	ctx context.Context,
	tx pgx.Tx,
	projectIDValue string,
	view governance.SourceView,
	chapterIDs []string,
) (map[string]CurrentChapterRef, error) {
	if db == nil || tx == nil || !validSourceCatalogView(view) {
		return nil, ErrCanonicalSourceValidation
	}
	projectID, err := storedb.ParseUUID(projectIDValue)
	if err != nil {
		return nil, ErrCanonicalSourceValidation
	}
	ids := make([]pgtype.UUID, len(chapterIDs))
	for index, value := range chapterIDs {
		if ids[index], err = storedb.ParseUUID(value); err != nil {
			return nil, ErrCanonicalSourceValidation
		}
	}
	refs := make(map[string]CurrentChapterRef, len(chapterIDs))
	if len(ids) == 0 {
		return refs, nil
	}
	rows, err := sqlc.New(tx).ListCanonicalChapterSourceRefs(ctx, sqlc.ListCanonicalChapterSourceRefsParams{
		ProjectID: projectID, SourceView: string(view), ChapterIds: ids,
	})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		ref, err := newCurrentChapterRef(projectIDValue, view, chapterSourceRow{
			BookID: row.BookID, ChapterID: row.ChapterID, SourceRevision: row.SourceRevision,
			LocatorSourceGeneration: row.LocatorSourceGeneration, ChapterContentHash: row.ChapterContentHash,
			ProjectGeneration: row.ProjectLifecycleGeneration, BookGeneration: row.BookLifecycleGeneration,
			ChapterGeneration: row.ChapterLifecycleGeneration,
		})
		if err != nil {
			return nil, err
		}
		refs[ref.ChapterID] = ref
	}
	return refs, nil
}

type chapterSourceRow struct {
	BookID, ChapterID                                    pgtype.UUID
	SourceRevision                                       int64
	LocatorSourceGeneration, ChapterContentHash          []byte
	ProjectGeneration, BookGeneration, ChapterGeneration int64
}

func newCurrentChapterRef(projectIDValue string, view governance.SourceView, row chapterSourceRow) (CurrentChapterRef, error) {
	if row.SourceRevision < 1 || row.ProjectGeneration < 1 || row.BookGeneration < 1 || row.ChapterGeneration < 1 ||
		len(row.LocatorSourceGeneration) != len(governance.Digest{}) || len(row.ChapterContentHash) != len(governance.Digest{}) {
		return CurrentChapterRef{}, ErrCanonicalSourceNotCurrent
	}
	ref := CurrentChapterRef{
		BookID: storedb.UUIDString(row.BookID), ChapterID: storedb.UUIDString(row.ChapterID), SourceRevision: uint64(row.SourceRevision),
	}
	copy(ref.SourceGeneration[:], row.LocatorSourceGeneration)
	copy(ref.ContentHash[:], row.ChapterContentHash)
	vector := governance.SourceGenerationVector{
		ProjectID: projectIDValue, ProjectGeneration: governance.LifecycleGeneration(row.ProjectGeneration),
		BookID: ref.BookID, BookGeneration: governance.LifecycleGeneration(row.BookGeneration),
		ChapterID: ref.ChapterID, ChapterGeneration: governance.LifecycleGeneration(row.ChapterGeneration),
	}
	wantGeneration, err := governance.SourceGenerationDigest(vector)
	if err != nil || wantGeneration != ref.SourceGeneration || ref.ContentHash == (governance.Digest{}) {
		return CurrentChapterRef{}, ErrCanonicalSourceNotCurrent
	}
	sourceView, err := governance.PresentSourceView(view)
	if err != nil {
		return CurrentChapterRef{}, ErrCanonicalSourceValidation
	}
	ref.ExactRef, err = governance.NewExactChapterArtifactRef(governance.ChapterSourceRefInput{
		Source: vector, SourceRevision: ref.SourceRevision, SourceView: sourceView,
		SourceGenerationDigest: ref.SourceGeneration, ContentHash: ref.ContentHash, Chat: governance.AbsentChatArm(),
	})
	if err != nil {
		return CurrentChapterRef{}, ErrCanonicalSourceNotCurrent
	}
	return ref, nil
}

func loadCurrentBookSourceSnapshot(ctx context.Context, queries *sqlc.Queries, projectIDValue string, view governance.SourceView, bookIDValue string) (library.CurrentBookSourceSnapshot, error) {
	if !validSourceCatalogView(view) {
		return library.CurrentBookSourceSnapshot{}, ErrCanonicalSourceValidation
	}
	projectID, err := storedb.ParseUUID(projectIDValue)
	if err != nil {
		return library.CurrentBookSourceSnapshot{}, ErrCanonicalSourceValidation
	}
	bookID, err := storedb.ParseUUID(bookIDValue)
	if err != nil {
		return library.CurrentBookSourceSnapshot{}, ErrCanonicalSourceValidation
	}
	rows, err := queries.LoadCurrentBookSourceSnapshotRows(ctx, sqlc.LoadCurrentBookSourceSnapshotRowsParams{
		ProjectID: projectID, SourceView: string(view), BookID: bookID,
	})
	if err != nil {
		return library.CurrentBookSourceSnapshot{}, err
	}
	if len(rows) == 0 {
		return library.CurrentBookSourceSnapshot{}, ErrCanonicalSourceNotCurrent
	}
	first := rows[0]
	if first.ProjectLifecycleGeneration < 1 || first.BookLifecycleGeneration < 1 ||
		first.CatalogRevision < 1 || first.ProjectBookOrder < 0 {
		return library.CurrentBookSourceSnapshot{}, ErrCanonicalSourceNotCurrent
	}
	snapshot := library.CurrentBookSourceSnapshot{
		ProjectID: storedb.UUIDString(first.ProjectID), ProjectLifecycleGeneration: first.ProjectLifecycleGeneration,
		SourceView: governance.SourceView(first.SourceView), CatalogRevision: uint64(first.CatalogRevision),
		BookID: storedb.UUIDString(first.BookID), BookLifecycleGeneration: first.BookLifecycleGeneration,
		ProjectBookOrder: uint32(first.ProjectBookOrder), Title: first.BookTitle,
		Chapters: make([]library.CanonicalChapterSource, 0),
	}
	for _, row := range rows {
		if !sameSnapshotBook(first, row) {
			return library.CurrentBookSourceSnapshot{}, ErrCanonicalSourceNotCurrent
		}
		if !row.ChapterID.Valid {
			if row.ParagraphID.Valid {
				return library.CurrentBookSourceSnapshot{}, ErrCanonicalSourceNotCurrent
			}
			continue
		}
		chapterID := storedb.UUIDString(row.ChapterID)
		if !row.ChapterLifecycleGeneration.Valid || row.ChapterLifecycleGeneration.Int64 < 1 ||
			!row.SourceRevision.Valid || row.SourceRevision.Int64 < 1 ||
			!row.ChapterTitle.Valid || !row.ChapterOrder.Valid || row.ChapterOrder.Int32 < 0 {
			return library.CurrentBookSourceSnapshot{}, ErrCanonicalSourceNotCurrent
		}
		if len(snapshot.Chapters) == 0 || snapshot.Chapters[len(snapshot.Chapters)-1].ChapterID != chapterID {
			vector := governance.SourceGenerationVector{
				ProjectID: snapshot.ProjectID, ProjectGeneration: governance.LifecycleGeneration(snapshot.ProjectLifecycleGeneration),
				BookID: snapshot.BookID, BookGeneration: governance.LifecycleGeneration(snapshot.BookLifecycleGeneration),
				ChapterID: chapterID, ChapterGeneration: governance.LifecycleGeneration(row.ChapterLifecycleGeneration.Int64),
			}
			generation, generationErr := governance.SourceGenerationDigest(vector)
			if generationErr != nil {
				return library.CurrentBookSourceSnapshot{}, ErrCanonicalSourceNotCurrent
			}
			snapshot.Chapters = append(snapshot.Chapters, library.CanonicalChapterSource{
				ChapterID: chapterID, ChapterLifecycleGeneration: row.ChapterLifecycleGeneration.Int64,
				SourceRevision: uint64(row.SourceRevision.Int64), SourceGeneration: governance.Digest(generation),
				Title: row.ChapterTitle.String, Order: uint32(row.ChapterOrder.Int32),
				Paragraphs: make([]library.CanonicalParagraphSource, 0),
			})
		}
		chapter := &snapshot.Chapters[len(snapshot.Chapters)-1]
		if chapter.SourceRevision != uint64(row.SourceRevision.Int64) || chapter.Order != uint32(row.ChapterOrder.Int32) ||
			chapter.Title != row.ChapterTitle.String || chapter.ChapterLifecycleGeneration != row.ChapterLifecycleGeneration.Int64 {
			return library.CurrentBookSourceSnapshot{}, ErrCanonicalSourceNotCurrent
		}
		if row.ParagraphID.Valid {
			if !row.ParagraphOrder.Valid || row.ParagraphOrder.Int32 < 0 || !row.ParagraphContent.Valid {
				return library.CurrentBookSourceSnapshot{}, ErrCanonicalSourceNotCurrent
			}
			contentHash, hashErr := governance.CanonicalParagraphContentDigest(row.ParagraphContent.String)
			if hashErr != nil {
				return library.CurrentBookSourceSnapshot{}, ErrCanonicalSourceNotCurrent
			}
			chapter.Paragraphs = append(chapter.Paragraphs, library.CanonicalParagraphSource{
				ParagraphID: storedb.UUIDString(row.ParagraphID), Order: uint32(row.ParagraphOrder.Int32),
				Content: row.ParagraphContent.String, ContentHash: contentHash,
			})
		}
	}
	for index := range snapshot.Chapters {
		paragraphs := make([]governance.CanonicalParagraphContent, 0, len(snapshot.Chapters[index].Paragraphs))
		for _, paragraph := range snapshot.Chapters[index].Paragraphs {
			paragraphs = append(paragraphs, governance.CanonicalParagraphContent{
				ParagraphID: paragraph.ParagraphID, Order: paragraph.Order, Content: paragraph.Content,
			})
		}
		hash, hashErr := governance.CanonicalChapterContentDigest(paragraphs)
		if hashErr != nil {
			return library.CurrentBookSourceSnapshot{}, ErrCanonicalSourceNotCurrent
		}
		snapshot.Chapters[index].ChapterContentHash = hash
	}
	return snapshot, nil
}

func bookSourceSnapshotManifest(snapshot library.CurrentBookSourceSnapshot) (governance.Digest, error) {
	source := governance.CanonicalBookSource{
		ProjectID:                  snapshot.ProjectID,
		ProjectLifecycleGeneration: governance.LifecycleGeneration(snapshot.ProjectLifecycleGeneration),
		SourceView:                 snapshot.SourceView, BookID: snapshot.BookID, Title: snapshot.Title,
		BookLifecycleGeneration: governance.LifecycleGeneration(snapshot.BookLifecycleGeneration),
		ProjectBookOrder:        snapshot.ProjectBookOrder, CatalogRevision: snapshot.CatalogRevision,
		Chapters: make([]governance.CanonicalBookChapter, 0, len(snapshot.Chapters)),
	}
	for _, chapter := range snapshot.Chapters {
		source.Chapters = append(source.Chapters, governance.CanonicalBookChapter{
			ChapterID: chapter.ChapterID, Order: chapter.Order, Title: chapter.Title,
			SourceRevision: chapter.SourceRevision, SourceGeneration: chapter.SourceGeneration,
			ChapterContentHash: chapter.ChapterContentHash,
		})
	}
	return governance.CanonicalBookSourceManifest(source)
}

func sameSnapshotBook(first, row sqlc.LoadCurrentBookSourceSnapshotRowsRow) bool {
	return first.ProjectID == row.ProjectID && first.SourceView == row.SourceView &&
		first.ProjectLifecycleGeneration == row.ProjectLifecycleGeneration && first.CatalogRevision == row.CatalogRevision &&
		first.BookID == row.BookID && first.BookLifecycleGeneration == row.BookLifecycleGeneration &&
		first.ProjectBookOrder == row.ProjectBookOrder && first.BookTitle == row.BookTitle
}
