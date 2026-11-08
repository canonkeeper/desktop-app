package library

import (
	"strings"

	"canonkeeper/backend/internal/governance"
)

type SourceCatalogRevision struct {
	ProjectID                  string
	ProjectLifecycleGeneration int64
	SourceView                 governance.SourceView
	CatalogRevision            uint64
}

func (revision SourceCatalogRevision) Validate() error {
	if revision.ProjectID == "" || revision.ProjectLifecycleGeneration < 1 ||
		revision.CatalogRevision < 1 || !validCatalogSourceView(revision.SourceView) {
		return ErrValidation
	}
	return nil
}

type BookSourceMember struct {
	ProjectID                  string
	ProjectLifecycleGeneration int64
	SourceView                 governance.SourceView
	BookID                     string
	BookLifecycleGeneration    int64
	Title                      string
}

func (member BookSourceMember) Validate() error {
	if member.ProjectID == "" || member.ProjectLifecycleGeneration < 1 ||
		member.BookID == "" || member.BookLifecycleGeneration < 1 || strings.TrimSpace(member.Title) == "" ||
		!validCatalogSourceView(member.SourceView) {
		return ErrValidation
	}
	return nil
}

type ChapterSourceMember struct {
	BookSourceMember
	ChapterID                  string
	ChapterLifecycleGeneration int64
	SourceRevision             uint64
	Title                      string
	Order                      int
}

type CanonicalParagraphSource struct {
	ParagraphID string
	Order       uint32
	Content     string
	ContentHash governance.Digest
}

type CanonicalChapterSource struct {
	ChapterID                  string
	ChapterLifecycleGeneration int64
	SourceRevision             uint64
	SourceGeneration           governance.Digest
	Title                      string
	Order                      uint32
	ChapterContentHash         governance.Digest
	Paragraphs                 []CanonicalParagraphSource
}

type CurrentBookSourceSnapshot struct {
	ProjectID                  string
	ProjectLifecycleGeneration int64
	SourceView                 governance.SourceView
	CatalogRevision            uint64
	BookID                     string
	BookLifecycleGeneration    int64
	ProjectBookOrder           uint32
	Title                      string
	Chapters                   []CanonicalChapterSource
}

func (member ChapterSourceMember) Validate() error {
	if member.BookSourceMember.Validate() != nil || member.ChapterID == "" ||
		member.ChapterLifecycleGeneration < 1 || member.SourceRevision < 1 ||
		strings.TrimSpace(member.Title) == "" || member.Order < 0 {
		return ErrValidation
	}
	return nil
}

func validCatalogSourceView(view governance.SourceView) bool {
	return view == governance.SourceViewDraft || view == governance.SourceViewPublished
}
