package search

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/governance"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var (
	ErrSourceViewUnavailable  = errors.New("search: source view unavailable")
	ErrBookTextNotCurrent     = errors.New("search: book text not current")
	ErrCursorDrift            = errors.New("search: cursor drift")
	ErrCorrupt                = errors.New("search: result corrupt")
	ErrExactPhraseBoundedness = errors.New("search: exact phrase boundedness exceeded")
)

type Action string

const (
	ActionCreateOrImportSource Action = "create_or_import_source"
	ActionPublishSource        Action = "publish_source"
	ActionFixSource            Action = "fix_source"
	ActionStartIndexing        Action = "start_indexing"
)

type Store interface {
	SearchLexical(context.Context, LexicalSearchInput) (LexicalSearchOutput, error)
	SearchExactPhrase(context.Context, ExactPhraseSearchInput) (ExactPhraseSearchOutput, error)
}

type LexicalCursor struct {
	RankBits  uint64
	BookID    string
	ChapterID string
	ResultID  string
}

type LexicalSearchInput struct {
	ActorUserID          string
	ProjectID            string
	SourceView           governance.SourceView
	Query                string
	BookID               string
	ChapterID            string
	UpdatedSince         *time.Time
	Limit                int
	Cursor               *LexicalCursor
	ExpectedManifestHash *[32]byte
}

type LexicalSearchRow struct {
	ID             string
	ProjectID      string
	BookID         string
	ChapterID      string
	Content        string
	BookTitle      string
	ChapterTitle   string
	ChapterNumber  string
	ParagraphMatch *ChapterParagraphMatch
	Rank           float64
	ChapterSource  *ChapterSourceIdentity
}

type ChapterParagraphMatch struct {
	ID       string
	Position int
	Content  string
}

type ChapterSourceIdentity struct {
	SourceView       governance.SourceView
	SourceRevision   uint64
	SourceGeneration governance.Digest
	ContentHash      governance.Digest
}

type LexicalSearchOutput struct {
	Rows         []LexicalSearchRow
	TextCoverage TextCoverage
	ManifestHash [32]byte
}

type ExactPhraseCursor struct {
	ChapterOrder int32
	ChapterID    string
}

type ExactPhraseSearchInput struct {
	ActorUserID          string
	ProjectID            string
	SourceView           governance.SourceView
	BookID               string
	ChapterID            string
	Query                string
	Limit                int
	Cursor               *ExactPhraseCursor
	ExpectedManifestHash *[32]byte
}

type ExactPhraseMatch struct {
	ChapterOrder int32
	ChapterID    string
	StartByte    uint32
	EndByte      uint32
	SourceRef    string
}

type ExactPhraseSearchOutput struct {
	Matches      []ExactPhraseMatch
	ManifestHash [32]byte
	NextCursor   *ExactPhraseCursor
	Truncated    bool
	RawBytesRead int
}

func NormalizeExactPhrase(value string) (string, bool) {
	if !utf8.ValidString(value) {
		return "", false
	}
	folded := cases.Fold().String(norm.NFC.String(value))
	return strings.Join(strings.Fields(folded), " "), true
}
