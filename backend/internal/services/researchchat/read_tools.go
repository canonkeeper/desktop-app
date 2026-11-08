package researchchat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagadraft"
	"canonkeeper/backend/internal/domain/sagamemory"
)

type ToolContext struct {
	Caller agent.ToolCaller
	TurnID string
}

// ChapterRevision names the exact chapter text a tool result was derived from.
type ChapterRevision struct {
	ChapterID string
	Revision  int64
}

// ReadRepository is the port the chat tools use for the turn's read authority and the
// retention of every chapter whose text or derived data reached the model.
type ReadRepository interface {
	ReadAuthority(context.Context, ToolContext) (researchdomain.ReadAuthority, error)
	RetainChapters(context.Context, ToolContext, researchdomain.ReadAuthority, []ChapterRevision) error
}

// SagaText reads the current text version of a project for one tool call from the saga search
// rows (specs/2026-10-28-saga-search-in-postgres.md, UC2, UC3): the outline, a search, paragraphs
// and chapters on demand, and the chapter cards of a range.
type SagaText interface {
	Outline(ctx context.Context, projectID, view string) (*sagamemory.Outline, error)
	Search(ctx context.Context, outline *sagamemory.Outline, queries []sagamemory.Query,
		shown func(sagamemory.Query, sagamemory.Result) []sagamemory.Hit) (*sagamemory.Index, []sagamemory.Result, error)
	Paragraphs(ctx context.Context, outline *sagamemory.Outline, wanted map[int][]int) error
	Chapters(ctx context.Context, outline *sagamemory.Outline, chapters []int) error
	Cards(ctx context.Context, outline *sagamemory.Outline, from, to int, parts sagamemory.CardParts) (*sagamemory.CardSet, error)
	Facts(ctx context.Context, outline *sagamemory.Outline, to int, names []string) ([]sagamemory.ChapterFact, error)
}

// ScanModel is the direct model client the scan tool reads the saga with; it reports what each
// call cost.
type ScanModel interface {
	Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error)
}

// UsageRecorder adds model spend to the day's aggregates (quota spec UC7); it never fails the
// scan, a failed write is logged by the recorder.
type UsageRecorder interface {
	RecordModelUsage(ctx context.Context, record quota.UsageRecord)
}

// ScanSettings bound the scan tool: parallel model calls, the text size of one model call
// and the time one tool call may spend. Model names the scan model in the usage records.
type ScanSettings struct {
	Parallel   int
	ChunkRunes int
	Budget     time.Duration
	Model      string
}

// ArgumentError is a correctable tool argument error explained to the model.
type ArgumentError struct {
	Path    string
	Message string
}

func (err *ArgumentError) Error() string { return err.Path + ": " + err.Message }

func argumentError(path, format string, args ...any) error {
	return &ArgumentError{Path: path, Message: fmt.Sprintf(format, args...)}
}

type SearchInput struct {
	Queries []string
	Match   string
	Order   string
	From    string
	To      string
	Books   []int
	Limit   int
	Offset  int
}

type ReadInput struct {
	Chapter string
	From    int
	To      int
	Around  int
	Radius  *int
}

// ChaptersDetail selects what the chapter list shows from the chapter cards.
type ChaptersDetail string

const (
	ChaptersTitles  ChaptersDetail = "titles"
	ChaptersSummary ChaptersDetail = "summary"
	ChaptersCard    ChaptersDetail = "card"
)

type ChaptersInput struct {
	From   string
	To     string
	Books  []int
	Title  string
	Detail ChaptersDetail
}

const (
	maxSearchQueries   = 8
	maxQueryRunes      = 300
	defaultSearchLimit = 30
	maxSearchLimit     = 150
	defaultReadRadius  = 4
	maxReadRadius      = 40
)

// ReadTools are the research agent's saga memory tools (R3.1): paragraph search, chapter
// reads, the chapter list with card summaries, the glossary, state changes and the scan.
// Every result is returned only after the chapters it was derived from are retained by the
// turn, so deleting such a chapter deletes the chat answer (R3.3).
type ReadTools struct {
	repository ReadRepository
	saga       SagaText
	scanModel  ScanModel
	usage      UsageRecorder
	scan       ScanSettings
	layer      ThreadLayer
	materials  MaterialsReader
	// attachments reads the chapters attached to the questions of the chat (specs/2026-11-07-chat-attach-chapter.md CA3).
	attachments AttachmentReader
}

// NewReadTools builds the chat tools; usage may be nil where nothing records spend.
func NewReadTools(repository ReadRepository, saga SagaText, scanModel ScanModel, usage UsageRecorder, scan ScanSettings, layer ThreadLayer) *ReadTools {
	if repository == nil || saga == nil || scanModel == nil || layer == nil {
		panic("research chat: read repository, saga text, scan model and thread layer are required")
	}
	if scan.Parallel <= 0 {
		scan.Parallel = defaultScanParallel
	}
	if scan.ChunkRunes <= 0 {
		scan.ChunkRunes = defaultScanChunkRunes
	}
	if scan.Budget <= 0 {
		scan.Budget = defaultScanBudget
	}
	return &ReadTools{repository: repository, saga: saga, scanModel: scanModel, usage: usage, scan: scan, layer: layer}
}

// Search runs UC1 of specs/2026-10-09-saga-memory.md over the saga search rows (UC2 of
// specs/2026-10-28-saga-search-in-postgres.md): resolve the turn authority, read the outline of
// the turn's text version, run the queries and retain every chapter in the answer.
func (tools *ReadTools) Search(ctx context.Context, call ToolContext, input SearchInput) (string, error) {
	if len(input.Queries) == 0 || len(input.Queries) > maxSearchQueries {
		return "", argumentError("$.queries", "нужно от 1 до %d запросов", maxSearchQueries)
	}
	for _, query := range input.Queries {
		if strings.TrimSpace(query) == "" || !utf8.ValidString(query) || utf8.RuneCountInString(query) > maxQueryRunes {
			return "", argumentError("$.queries", "запрос должен быть непустой строкой до %d символов", maxQueryRunes)
		}
	}
	mode := sagamemory.Mode(input.Match)
	switch mode {
	case "":
		mode = sagamemory.ModeAny
	case sagamemory.ModeAny, sagamemory.ModeAll, sagamemory.ModePhrase, sagamemory.ModeSubstring:
	default:
		return "", argumentError("$.match", "допустимы any, all, phrase, substring")
	}
	if input.Order != "" && input.Order != "relevance" && input.Order != "story" {
		return "", argumentError("$.order", "допустимы relevance и story")
	}
	if input.Limit < 0 || input.Limit > maxSearchLimit || input.Offset < 0 {
		return "", argumentError("$.limit", "limit от 1 до %d, offset не меньше 0", maxSearchLimit)
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultSearchLimit
	}
	return tools.run(ctx, call, func(saga *sagaScope) (string, error) {
		from, to, err := saga.chapterRange(input.From, input.To)
		if err != nil {
			return "", err
		}
		var out strings.Builder
		if saga.end < len(saga.version().Chapters) {
			if saga.end == 0 {
				return "До места черновика глав нет: искать негде.", nil
			}
			if to < 0 || to >= saga.end {
				to = saga.end - 1
			}
			out.WriteString("Ищу только в главах до места черновика: дальше события ещё не случились.\n")
		}
		books, err := saga.books(input.Books)
		if err != nil {
			return "", err
		}
		queries := make([]sagamemory.Query, len(input.Queries))
		for i, text := range input.Queries {
			queries[i] = sagamemory.Query{Text: text, Mode: mode, From: from, To: to, BookIDs: books, Story: input.Order == "story"}
		}
		shown := func(query sagamemory.Query, result sagamemory.Result) []sagamemory.Hit {
			return shownHits(result, query.Story, limit, input.Offset)
		}
		index, results, err := tools.saga.Search(ctx, saga.outline, queries, shown)
		if err != nil {
			return "", err
		}
		for i, query := range queries {
			writeSearchResult(&out, saga, index, query.Text, mode, query.Story, results[i], limit, input.Offset)
		}
		records, err := tools.projectMaterials(ctx, saga.authority.ProjectID)
		if err != nil || len(records) == 0 {
			return out.String(), err
		}
		cards, err := saga.cards(0, -1, sagamemory.CardParts{Mentions: true})
		if err != nil {
			return "", err
		}
		for _, text := range input.Queries {
			writeMaterialList(&out, materialMatches(records, text), cards.Glossary)
		}
		return out.String(), nil
	})
}

// Read returns a numbered paragraph range of one chapter.
func (tools *ReadTools) Read(ctx context.Context, call ToolContext, input ReadInput) (string, error) {
	if strings.TrimSpace(input.Chapter) == "" {
		return "", argumentError("$.chapter", "укажите главу: сквозной номер («481») или ключ из search/chapters")
	}
	if input.From < 0 || input.To < 0 || input.Around < 0 || input.Radius != nil && (*input.Radius < 0 || *input.Radius > maxReadRadius) {
		return "", argumentError("$", "номера абзацев начинаются с 1, radius от 0 до %d", maxReadRadius)
	}
	return tools.run(ctx, call, func(saga *sagaScope) (string, error) {
		chapter, err := saga.chapter("$.chapter", input.Chapter)
		if err != nil {
			return "", err
		}
		total := chapter.ParagraphCount
		first, last := 1, total
		if input.Around > 0 {
			radius := defaultReadRadius
			if input.Radius != nil {
				radius = *input.Radius
			}
			first, last = input.Around-radius, input.Around+radius
		} else {
			if input.From > 0 {
				first = input.From
			}
			if input.To > 0 {
				last = input.To
			}
		}
		first, last = max(first, 1), min(last, total)
		if first > last {
			return "", argumentError("$", "в главе %s всего %d абзацев", chapter.Key, total)
		}
		wanted := make([]int, 0, last-first+1)
		for paragraph := first - 1; paragraph < last; paragraph++ {
			wanted = append(wanted, paragraph)
		}
		if err := tools.saga.Paragraphs(ctx, saga.outline, map[int][]int{chapter.Index: wanted}); err != nil {
			return "", err
		}
		saga.use(chapter)
		return renderRead(chapter, first, last), nil
	})
}

// Chapters lists the chapters of the saga in reading order; detail=summary adds the card
// summary and detail=card also its time markers and state changes.
func (tools *ReadTools) Chapters(ctx context.Context, call ToolContext, input ChaptersInput) (string, error) {
	if !utf8.ValidString(input.Title) || utf8.RuneCountInString(input.Title) > maxQueryRunes {
		return "", argumentError("$.title", "название до %d символов", maxQueryRunes)
	}
	switch input.Detail {
	case "":
		input.Detail = ChaptersTitles
	case ChaptersTitles, ChaptersSummary, ChaptersCard:
	default:
		return "", argumentError("$.detail", "допустимы titles, summary, card")
	}
	return tools.run(ctx, call, func(saga *sagaScope) (string, error) {
		from, to, err := saga.chapterRange(input.From, input.To)
		if err != nil {
			return "", err
		}
		books, err := saga.books(input.Books)
		if err != nil {
			return "", err
		}
		var cards *sagamemory.CardSet
		if input.Detail != ChaptersTitles {
			parts := sagamemory.CardParts{Summary: true}
			if input.Detail == ChaptersCard {
				parts.Time, parts.Changes, parts.Knows, parts.Hooks = true, true, true, true
			}
			if cards, err = saga.cards(from, to, parts); err != nil {
				return "", err
			}
		}
		return renderChapters(saga, from, to, books, sagamemory.Normalize(strings.TrimSpace(input.Title)), input.Detail, cards), nil
	})
}

func (tools *ReadTools) run(ctx context.Context, call ToolContext, render func(*sagaScope) (string, error)) (string, error) {
	if err := ValidToolContext(ctx, call); err != nil {
		return "", err
	}
	authority, err := tools.repository.ReadAuthority(ctx, call)
	if err != nil {
		return "", err
	}
	outline, err := tools.saga.Outline(ctx, authority.ProjectID, authority.SourceView)
	if err != nil {
		return "", err
	}
	if !outline.Ready() {
		return "", sagamemory.ErrSearchNotReady
	}
	saga := &sagaScope{
		outline: outline, authority: authority, used: map[int]bool{}, end: momentEnd(authority, outline.Version.Chapters),
		loadCards: func(from, to int, parts sagamemory.CardParts) (*sagamemory.CardSet, error) {
			return tools.saga.Cards(ctx, outline, from, to, parts)
		},
		loadFacts: func(to int, names []string) ([]sagamemory.ChapterFact, error) {
			return tools.saga.Facts(ctx, outline, to, names)
		},
	}
	output, err := render(saga)
	if err != nil {
		return "", err
	}
	if err := tools.repository.RetainChapters(ctx, call, authority, saga.retained()); err != nil {
		return "", err
	}
	return output, nil
}

// sagaScope is the text version of a call seen through the turn's scope: a book-scoped chat
// sees only its book. It records every chapter whose text or derived data enters the output.
type sagaScope struct {
	outline   *sagamemory.Outline
	authority researchdomain.ReadAuthority
	// end bounds what the helper of a chapter in writing reads: the chapters before the place of its draft
	// (specs/2026-11-06-writing-agent.md, WA3.3); for the chat it is the whole saga.
	end       int
	used      map[int]bool
	loadCards func(from, to int, parts sagamemory.CardParts) (*sagamemory.CardSet, error)
	loadFacts func(to int, names []string) ([]sagamemory.ChapterFact, error)
}

func (saga *sagaScope) version() *sagamemory.Version { return saga.outline.Version }

func (saga *sagaScope) inScope(chapter *sagamemory.Chapter) bool {
	return (saga.authority.Scope != researchdomain.ScopeBook || chapter.BookID == saga.authority.BookID) && chapter.Index < saga.end
}

// momentEnd is where the reads of a turn end: the place of the draft of the helper of a chapter in writing, or the
// end of the saga.
func momentEnd(authority researchdomain.ReadAuthority, chapters []sagamemory.Chapter) int {
	if !authority.Moment {
		return len(chapters)
	}
	return sagadraft.Place{BookID: authority.MomentBookID, AfterChapterID: authority.MomentAfterChapterID}.End(chapters)
}

// OutcomeAfterPlace refuses a chapter after the place of the draft to its helper: its events have not happened yet.
const OutcomeAfterPlace = "after_place"

func (saga *sagaScope) use(chapter *sagamemory.Chapter) { saga.used[chapter.Index] = true }

func (saga *sagaScope) retained() []ChapterRevision {
	chapters := make([]ChapterRevision, 0, len(saga.used))
	for index, chapter := range saga.version().Chapters {
		if saga.used[index] {
			chapters = append(chapters, ChapterRevision{ChapterID: chapter.ID, Revision: chapter.Revision})
		}
	}
	return chapters
}

func (saga *sagaScope) chapter(path, key string) (*sagamemory.Chapter, error) {
	chapter, ok := saga.version().Find(key)
	if ok && chapter.Index >= saga.end {
		return nil, &ToolOutcome{
			Type: OutcomeAfterPlace, Message: fmt.Sprintf("глава %q стоит после места черновика: её события ещё не случились; читай главы до места", key),
			Next: []string{"correct_arguments_and_retry"},
		}
	}
	if !ok || !saga.inScope(chapter) {
		return nil, argumentError(path, "глава %q не найдена; используйте сквозной номер главы («481») или ключ из результатов search/chapters", key)
	}
	return chapter, nil
}

// chapterRange resolves optional chapter keys to reading-order indexes; To is -1 when open.
func (saga *sagaScope) chapterRange(from, to string) (int, int, error) {
	start, end := 0, -1
	if strings.TrimSpace(from) != "" {
		chapter, err := saga.chapter("$.from", from)
		if err != nil {
			return 0, 0, err
		}
		start = chapter.Index
	}
	if strings.TrimSpace(to) != "" {
		chapter, err := saga.chapter("$.to", to)
		if err != nil {
			return 0, 0, err
		}
		end = chapter.Index
		if start > end {
			return 0, 0, argumentError("$.from", "диапазон глав пуст: from после to")
		}
	}
	return start, end, nil
}

// books maps volume numbers to book IDs inside the scope; no volumes means the whole scope.
func (saga *sagaScope) books(numbers []int) ([]string, error) {
	if saga.authority.Scope == researchdomain.ScopeBook {
		return []string{saga.authority.BookID}, nil
	}
	ids := make([]string, 0, len(numbers))
	for _, number := range numbers {
		found := ""
		for _, chapter := range saga.version().Chapters {
			if chapter.BookOrder == number {
				found = chapter.BookID
				break
			}
		}
		if found == "" {
			return nil, argumentError("$.books", "тома %d нет в саге", number)
		}
		ids = append(ids, found)
	}
	return ids, nil
}

// cards reads the parts of the cards of the chapters from..to (reading-order indexes; to < 0
// for the last).
func (saga *sagaScope) cards(from, to int, parts sagamemory.CardParts) (*sagamemory.CardSet, error) {
	return saga.loadCards(from, to, parts)
}

// inRange reports whether a chapter is inside the turn scope, the reading-order range
// (to is -1 when open) and the selected books.
func (saga *sagaScope) inRange(chapter *sagamemory.Chapter, from, to int, books map[string]bool) bool {
	return saga.inScope(chapter) && chapter.Index >= from && (to < 0 || chapter.Index <= to) &&
		(len(books) == 0 || books[chapter.BookID])
}

// ValidToolContext refuses a call that is not of the root thread of a turn.
func ValidToolContext(ctx context.Context, call ToolContext) error {
	if ctx == nil || call.Caller.ThreadID == "" || call.Caller.RootThreadID == "" || call.TurnID == "" ||
		call.Caller.ThreadID != call.Caller.RootThreadID || call.Caller.AgentRole != "" {
		return researchdomain.ErrForbidden
	}
	return nil
}

// IsArgumentError reports whether err is a correctable argument error and returns it.
func IsArgumentError(err error) (*ArgumentError, bool) {
	var argument *ArgumentError
	ok := errors.As(err, &argument)
	return argument, ok
}
