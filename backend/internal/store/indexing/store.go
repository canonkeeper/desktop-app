package indexingstore

import (
	"bytes"
	"context"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/indexing"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"
)

var (
	ErrTextIndexValidation = indexing.ErrTextIndexValidation
	ErrTextIndexConflict   = indexing.ErrTextIndexConflict
)

type CanonTextChapterDescriptor = indexing.CanonTextChapterDescriptor
type CanonTextChapterSnapshot = indexing.CanonTextChapterSnapshot
type ReplaceCanonTextChapterInput = indexing.ReplaceCanonTextChapterInput
type CanonTextChapterReplaceDisposition = indexing.CanonTextChapterReplaceDisposition

const (
	CanonTextChapterReplaced  = indexing.CanonTextChapterReplaced
	CanonTextChapterUnchanged = indexing.CanonTextChapterUnchanged
)

type storeFailpoint func(string) error

type Store struct {
	*storedb.DB
	failpoint storeFailpoint
}

type Option func(*Store)

func New(db *storedb.DB, options ...Option) *Store {
	store := &Store{DB: db}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	return store
}

func withFailpoint(failpoint storeFailpoint) Option {
	return func(store *Store) { store.failpoint = failpoint }
}

func (s *Store) hit(point string) error {
	if s.failpoint == nil {
		return nil
	}
	return s.failpoint(point)
}

func (s *Store) ListCanonTextChapterDescriptorsForClaim(
	ctx context.Context,
	claim jobs.WorkerClaim,
) ([]CanonTextChapterDescriptor, error) {
	projectID, jobID, err := textIndexClaimIDs(claim)
	if err != nil || s == nil || s.DB == nil {
		return nil, ErrTextIndexValidation
	}
	queries := sqlc.New(s.Pool)
	if _, err := validateClaimedCanonTextJob(ctx, queries, claim, "", 0); err != nil {
		return nil, err
	}
	rows, err := queries.ListClaimedCanonTextChapterDescriptors(ctx, sqlc.ListClaimedCanonTextChapterDescriptorsParams{
		ProjectID: projectID, JobID: jobID, ClaimIncarnation: claim.ClaimIncarnation,
	})
	if err != nil {
		return nil, mapTextIndexError(err)
	}
	descriptors := make([]CanonTextChapterDescriptor, 0, len(rows))
	for _, row := range rows {
		if row.ProjectLifecycleGeneration < 1 || row.CatalogRevision < 1 || row.SourceRevision < 1 || len(row.SourceGeneration) != 32 {
			return nil, ErrTextIndexConflict
		}
		var generation governance.Digest
		copy(generation[:], row.SourceGeneration)
		descriptor := CanonTextChapterDescriptor{
			ProjectID: storedb.UUIDString(row.ProjectID), ProjectLifecycleGeneration: row.ProjectLifecycleGeneration,
			SourceView: governance.SourceView(row.SourceView), CatalogRevision: uint64(row.CatalogRevision),
			BookID: storedb.UUIDString(row.BookID), ChapterID: storedb.UUIDString(row.ChapterID),
			SourceRevision: uint64(row.SourceRevision), SourceGeneration: generation,
		}
		if validateCanonTextDescriptor(descriptor) != nil {
			return nil, ErrTextIndexConflict
		}
		descriptors = append(descriptors, descriptor)
	}
	return descriptors, nil
}

func (s *Store) LoadCanonTextChapterSnapshotForClaim(
	ctx context.Context,
	claim jobs.WorkerClaim,
	descriptor CanonTextChapterDescriptor,
) (CanonTextChapterSnapshot, error) {
	projectID, jobID, err := textIndexClaimIDs(claim)
	if err != nil || validateCanonTextDescriptor(descriptor) != nil || descriptor.ProjectID != claim.ProjectID || s == nil || s.DB == nil {
		return CanonTextChapterSnapshot{}, ErrTextIndexValidation
	}
	bookID, _ := storedb.ParseUUID(descriptor.BookID)
	chapterID, _ := storedb.ParseUUID(descriptor.ChapterID)
	queries := sqlc.New(s.Pool)
	rows, err := queries.LoadClaimedCanonTextChapterSnapshot(ctx, sqlc.LoadClaimedCanonTextChapterSnapshotParams{
		SourceView: string(descriptor.SourceView), ProjectLifecycleGeneration: descriptor.ProjectLifecycleGeneration,
		CatalogRevision: int64(descriptor.CatalogRevision), BookID: bookID, ChapterID: chapterID,
		SourceRevision: int64(descriptor.SourceRevision), SourceGeneration: descriptor.SourceGeneration[:],
		ProjectID: projectID, JobID: jobID, ClaimIncarnation: claim.ClaimIncarnation,
	})
	if err != nil {
		return CanonTextChapterSnapshot{}, mapTextIndexError(err)
	}
	if len(rows) == 0 {
		return CanonTextChapterSnapshot{}, ErrTextIndexConflict
	}
	paragraphs := make([]indexing.CanonicalParagraphSource, 0, len(rows))
	contents := make([]governance.CanonicalParagraphContent, 0, len(rows))
	for _, row := range rows {
		if !row.ParagraphID.Valid {
			if row.ParagraphOrder.Valid || row.ParagraphContent.Valid || len(rows) != 1 {
				return CanonTextChapterSnapshot{}, ErrTextIndexConflict
			}
			continue
		}
		if !row.ParagraphOrder.Valid || row.ParagraphOrder.Int32 < 0 || !row.ParagraphContent.Valid {
			return CanonTextChapterSnapshot{}, ErrTextIndexConflict
		}
		paragraphID := storedb.UUIDString(row.ParagraphID)
		paragraphs = append(paragraphs, indexing.CanonicalParagraphSource{
			ProjectID: descriptor.ProjectID, BookID: descriptor.BookID, ChapterID: descriptor.ChapterID,
			ParagraphID: paragraphID, SourceView: descriptor.SourceView, SourceRevision: descriptor.SourceRevision,
			SourceGeneration: descriptor.SourceGeneration, Order: int(row.ParagraphOrder.Int32), Content: row.ParagraphContent.String,
		})
		contents = append(contents, governance.CanonicalParagraphContent{
			ParagraphID: paragraphID, Order: uint32(row.ParagraphOrder.Int32), Content: row.ParagraphContent.String,
		})
	}
	hash, err := governance.CanonicalChapterContentDigest(contents)
	if err != nil {
		return CanonTextChapterSnapshot{}, ErrTextIndexConflict
	}
	snapshot := CanonTextChapterSnapshot{
		Claim: claim, CanonTextChapterDescriptor: descriptor, Paragraphs: paragraphs, ChapterContentHash: hash,
	}
	snapshot.Unchanged = bytes.Equal(rows[0].IndexedChapterContentHash, hash[:])
	return snapshot, nil
}
