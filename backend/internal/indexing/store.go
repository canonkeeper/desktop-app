package indexing

import (
	"context"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

type CanonTextChapterDescriptor struct {
	ProjectID                  string
	ProjectLifecycleGeneration int64
	SourceView                 governance.SourceView
	CatalogRevision            uint64
	BookID                     string
	ChapterID                  string
	SourceRevision             uint64
	SourceGeneration           governance.Digest
}

type CanonicalParagraphSource struct {
	ProjectID        string
	BookID           string
	ChapterID        string
	ParagraphID      string
	SourceView       governance.SourceView
	SourceRevision   uint64
	SourceGeneration governance.Digest
	Order            int
	Content          string
}

type CanonTextChapterSnapshot struct {
	Claim jobs.WorkerClaim
	CanonTextChapterDescriptor
	Paragraphs         []CanonicalParagraphSource
	ChapterContentHash governance.Digest
	Unchanged          bool
}

type ReplaceCanonTextChapterInput struct {
	Snapshot  CanonTextChapterSnapshot
	IndexedAt time.Time
}

type CanonTextChapterReplaceDisposition string

const (
	CanonTextChapterReplaced  CanonTextChapterReplaceDisposition = "replaced"
	CanonTextChapterUnchanged CanonTextChapterReplaceDisposition = "unchanged"
)

type Store interface {
	ListCanonTextChapterDescriptorsForClaim(context.Context, jobs.WorkerClaim) ([]CanonTextChapterDescriptor, error)
	LoadCanonTextChapterSnapshotForClaim(context.Context, jobs.WorkerClaim, CanonTextChapterDescriptor) (CanonTextChapterSnapshot, error)
	ReplaceCanonTextChapterForClaim(context.Context, ReplaceCanonTextChapterInput) (CanonTextChapterReplaceDisposition, error)
	CompleteCanonTextIndexForClaim(context.Context, jobs.WorkerClaim, jobs.CanonTextIndexSubject) error
	CancelCanonTextIndexForClaim(context.Context, jobs.WorkerClaim, jobs.CanonTextIndexSubject) error
	FailCanonTextIndexForClaim(context.Context, jobs.WorkerClaim, jobs.CanonTextIndexSubject, jobs.Error) error
	StartIndexing(context.Context, StartIndexingStoreInput) (jobs.Job, error)
}
