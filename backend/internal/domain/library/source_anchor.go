package library

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/governance"
)

type TextRange struct {
	StartOffset int     `json:"startOffset"`
	EndOffset   int     `json:"endOffset"`
	Quote       *string `json:"quote,omitempty"`
}

type ReaderLocator struct {
	ProjectID   string     `json:"projectId"`
	BookID      string     `json:"bookId"`
	ChapterID   string     `json:"chapterId"`
	TargetView  string     `json:"targetView"`
	Revision    int64      `json:"revision"`
	ParagraphID *string    `json:"paragraphId,omitempty"`
	Range       *TextRange `json:"range,omitempty"`
}

type SourceNavigationAnchor struct {
	Kind    string        `json:"kind"`
	Label   string        `json:"label"`
	Locator ReaderLocator `json:"locator"`
}

var navigationUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (locator ReaderLocator) ValidFor(source governance.CanonicalChapterSourceLocatorV1) bool {
	if source.Validate() != nil || locator.ProjectID != source.ProjectID || locator.BookID != source.BookID ||
		locator.ChapterID != source.ChapterID || locator.TargetView != string(source.SourceView) ||
		locator.Revision < 1 || uint64(locator.Revision) != source.SourceRevision {
		return false
	}
	if locator.ParagraphID != nil && !navigationUUID.MatchString(*locator.ParagraphID) {
		return false
	}
	if selected := locator.Range; selected != nil {
		return locator.ParagraphID != nil && selected.StartOffset >= 0 && selected.EndOffset >= selected.StartOffset &&
			(selected.Quote == nil || utf8.RuneCountInString(*selected.Quote) <= 500)
	}
	return true
}

func NewSourceNavigationAnchor(source governance.CanonicalChapterSourceLocatorV1, chapterNumber, paragraphID string, paragraphPosition int) (SourceNavigationAnchor, error) {
	locator := ReaderLocator{
		ProjectID: source.ProjectID, BookID: source.BookID, ChapterID: source.ChapterID,
		TargetView: string(source.SourceView), Revision: int64(source.SourceRevision),
	}
	anchor := SourceNavigationAnchor{Kind: "chapter", Label: "Глава " + chapterNumber}
	if paragraphID != "" {
		locator.ParagraphID = &paragraphID
		anchor.Kind = "paragraph"
		anchor.Label += fmt.Sprintf(" · абзац %d", paragraphPosition)
	}
	if !locator.ValidFor(source) || strings.TrimSpace(chapterNumber) == "" ||
		(paragraphID == "" && paragraphPosition != 0) || (paragraphID != "" && paragraphPosition < 1) {
		return SourceNavigationAnchor{}, ErrValidation
	}
	if source.SourceView == governance.SourceViewDraft {
		anchor.Label += " · черновик"
	} else {
		anchor.Label += " · опубликованная версия"
	}
	anchor.Locator = locator
	return anchor, nil
}
