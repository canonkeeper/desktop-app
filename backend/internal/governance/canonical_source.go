package governance

import (
	"errors"
	"math"
	"sort"
	"strings"
)

const (
	canonicalParagraphContentDomain = "CKParagraphContentV1\x00"
	canonicalChapterContentDomain   = "CKChapterContentV1\x00"
	canonicalBookManifestDomain     = "CKBookSourceManifestV1\x00"
	canonicalMetadataValueDomain    = "CKCanonicalMetadataValueV1\x00"
)

type CanonicalMetadataKind string

const (
	CanonicalMetadataBookTitle    CanonicalMetadataKind = "book_title"
	CanonicalMetadataChapterTitle CanonicalMetadataKind = "chapter_title"
)

type CanonicalChapterSourceLocatorV1 struct {
	ProjectID        string     `json:"projectId"`
	BookID           string     `json:"bookId"`
	ChapterID        string     `json:"chapterId"`
	SourceRef        string     `json:"sourceRef"`
	SourceView       SourceView `json:"sourceView"`
	SourceRevision   uint64     `json:"sourceRevision"`
	SourceGeneration Digest     `json:"sourceGeneration"`
	ContentHash      Digest     `json:"contentHash"`
}

func (locator CanonicalChapterSourceLocatorV1) Validate() error {
	for _, id := range []string{locator.ProjectID, locator.BookID, locator.ChapterID} {
		if _, err := parseCanonicalUUID(id); err != nil {
			return errors.New("governance: invalid canonical chapter source locator")
		}
	}
	if locator.SourceRef != locator.ChapterID || !validCanonicalSourceView(locator.SourceView) || locator.SourceRevision == 0 ||
		locator.SourceRevision > math.MaxInt64 || locator.SourceGeneration == (Digest{}) ||
		locator.ContentHash == (Digest{}) {
		return errors.New("governance: invalid canonical chapter source locator")
	}
	return nil
}

type CanonicalParagraphContent struct {
	ParagraphID string
	Order       uint32
	Content     string
}

type CanonicalChapterSourceV1 struct {
	ProjectID        string
	BookID           string
	ChapterID        string
	SourceView       SourceView
	SourceRevision   uint64
	SourceGeneration Digest
	ContentHash      Digest
}

func (source CanonicalChapterSourceV1) Validate() error {
	for _, value := range []string{source.ProjectID, source.BookID, source.ChapterID} {
		if _, err := parseCanonicalUUID(value); err != nil {
			return errors.New("governance: invalid canonical chapter source")
		}
	}
	if source.SourceView != SourceViewDraft && source.SourceView != SourceViewPublished ||
		source.SourceRevision == 0 || source.SourceGeneration == (Digest{}) || source.ContentHash == (Digest{}) {
		return errors.New("governance: invalid canonical chapter source")
	}
	return nil
}

type CanonicalBookChapter struct {
	ChapterID          string
	Order              uint32
	Title              string
	SourceRevision     uint64
	SourceGeneration   Digest
	ChapterContentHash Digest
}

type CanonicalBookSource struct {
	ProjectID                  string
	ProjectLifecycleGeneration LifecycleGeneration
	SourceView                 SourceView
	BookID                     string
	Title                      string
	BookLifecycleGeneration    LifecycleGeneration
	Chapters                   []CanonicalBookChapter

	// Снимок источника хранит эти поля, но manifest намеренно их не включает.
	ProjectBookOrder uint32
	CatalogRevision  uint64
}

func CanonicalParagraphContentDigest(content string) (Digest, error) {
	projection, err := appendEncodedString(nil, content)
	digest, err := hashProjection(canonicalParagraphContentDomain, projection, err)
	return Digest(digest), err
}

func CanonicalChapterContentDigest(paragraphs []CanonicalParagraphContent) (Digest, error) {
	ordered := append([]CanonicalParagraphContent(nil), paragraphs...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Order != ordered[j].Order {
			return ordered[i].Order < ordered[j].Order
		}
		return ordered[i].ParagraphID < ordered[j].ParagraphID
	})
	projection := appendU32(nil, uint32(len(ordered)))
	seen := make(map[string]struct{}, len(ordered))
	for _, paragraph := range ordered {
		paragraphID, err := parseCanonicalUUID(paragraph.ParagraphID)
		if err != nil {
			return Digest{}, errors.New("governance: invalid canonical paragraph")
		}
		if _, exists := seen[paragraph.ParagraphID]; exists {
			return Digest{}, errors.New("governance: duplicate canonical paragraph")
		}
		seen[paragraph.ParagraphID] = struct{}{}
		contentHash, err := CanonicalParagraphContentDigest(paragraph.Content)
		if err != nil {
			return Digest{}, err
		}
		projection = append(projection, paragraphID[:]...)
		projection = appendU32(projection, paragraph.Order)
		projection = append(projection, contentHash[:]...)
	}
	digest, err := hashProjection(canonicalChapterContentDomain, projection, nil)
	return Digest(digest), err
}

func CanonicalBookSourceManifest(source CanonicalBookSource) (Digest, error) {
	projectID, err := parseCanonicalUUID(source.ProjectID)
	if err != nil || source.ProjectLifecycleGeneration == 0 || !validCanonicalSourceView(source.SourceView) {
		return Digest{}, errors.New("governance: invalid canonical book project")
	}
	bookID, err := parseCanonicalUUID(source.BookID)
	if err != nil || source.BookLifecycleGeneration == 0 {
		return Digest{}, errors.New("governance: invalid canonical book")
	}
	bookTitleHash, err := CanonicalMetadataValueDigest(CanonicalMetadataBookTitle, source.Title)
	if err != nil {
		return Digest{}, err
	}
	ordered := append([]CanonicalBookChapter(nil), source.Chapters...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Order != ordered[j].Order {
			return ordered[i].Order < ordered[j].Order
		}
		return ordered[i].ChapterID < ordered[j].ChapterID
	})

	projection := append([]byte(nil), projectID[:]...)
	projection = appendU64(projection, uint64(source.ProjectLifecycleGeneration))
	projection, err = appendEncodedString(projection, string(source.SourceView))
	if err != nil {
		return Digest{}, err
	}
	projection = append(projection, bookID[:]...)
	projection = append(projection, bookTitleHash[:]...)
	projection = appendU64(projection, uint64(source.BookLifecycleGeneration))
	projection = appendU32(projection, uint32(len(ordered)))
	seen := make(map[string]struct{}, len(ordered))
	for _, chapter := range ordered {
		chapterID, err := parseCanonicalUUID(chapter.ChapterID)
		if err != nil || chapter.SourceRevision == 0 || chapter.SourceRevision > math.MaxInt64 || chapter.SourceGeneration == (Digest{}) ||
			chapter.ChapterContentHash == (Digest{}) {
			return Digest{}, errors.New("governance: invalid canonical book chapter")
		}
		if _, exists := seen[chapter.ChapterID]; exists {
			return Digest{}, errors.New("governance: duplicate canonical book chapter")
		}
		seen[chapter.ChapterID] = struct{}{}
		titleHash, err := CanonicalMetadataValueDigest(CanonicalMetadataChapterTitle, chapter.Title)
		if err != nil {
			return Digest{}, err
		}
		projection = append(projection, chapterID[:]...)
		projection = appendU32(projection, chapter.Order)
		projection = append(projection, titleHash[:]...)
		projection = appendU64(projection, chapter.SourceRevision)
		projection = append(projection, chapter.SourceGeneration[:]...)
		projection = append(projection, chapter.ChapterContentHash[:]...)
	}
	digest, err := hashProjection(canonicalBookManifestDomain, projection, nil)
	return Digest(digest), err
}

func CanonicalMetadataValueDigest(kind CanonicalMetadataKind, value string) (Digest, error) {
	if kind != CanonicalMetadataBookTitle && kind != CanonicalMetadataChapterTitle {
		return Digest{}, errors.New("governance: unknown canonical metadata kind")
	}
	if strings.TrimSpace(value) == "" {
		return Digest{}, errors.New("governance: empty canonical metadata title")
	}
	projection, err := appendEncodedString(nil, string(kind))
	if err != nil {
		return Digest{}, err
	}
	projection, err = appendEncodedString(projection, value)
	digest, err := hashProjection(canonicalMetadataValueDomain, projection, err)
	return Digest(digest), err
}

func validCanonicalSourceView(view SourceView) bool {
	return view == SourceViewDraft || view == SourceViewPublished
}
