// Package sagamemory serves the saga memory of the chat tools and the saga reference: the text
// version of a project read per call from the saga search rows and the paragraphs in
// PostgreSQL (specs/2026-10-28-saga-search-in-postgres.md), and the chapter cards. Nothing is
// kept between calls.
package sagamemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	domain "canonkeeper/backend/internal/domain/sagamemory"
)

var ErrInvalid = errors.New("saga memory: invalid request")

// SelectedChapter is one chapter of the current text version with the revision whose
// paragraphs belong to it and the content hash of that revision.
type SelectedChapter struct {
	ID          string
	Revision    int64
	ContentHash string
	BookID      string
	BookOrder   int
	BookTitle   string
	Order       int
	Title       string
}

// Selection is the current chapter set of a text version and its fingerprint; the
// fingerprint changes whenever any chapter, revision, text, title or order changes.
type Selection struct {
	ProjectID   string
	View        string
	Fingerprint string
	Chapters    []SelectedChapter
}

// StoredWord is one word row of a stored chapter search text.
type StoredWord struct {
	TextID   string
	Word     string
	Stem     string
	Postings []byte
}

// ParagraphKey names one paragraph of a chapter revision by its stored order.
type ParagraphKey struct {
	ChapterID string
	Revision  int64
	Order     int32
}

// ParagraphText is one paragraph read for a call.
type ParagraphText struct {
	ChapterID string
	Order     int32
	Text      string
}

// SagaText is the port the saga reads the text version of a call through.
type SagaText interface {
	Selection(ctx context.Context, projectID, view string) (Selection, error)
	// Outline reads the selection and the stored search texts of its chapters by chapter text in
	// one snapshot.
	Outline(ctx context.Context, projectID, view string) (Selection, map[CardKey]domain.SearchText, error)
	// Vocabulary returns the looked-up words of the texts and every word of them with a prefix.
	Vocabulary(ctx context.Context, projectID string, textIDs []string, lookup domain.SearchLookup) ([]domain.VocabularyWord, error)
	// StemWords returns every word row of the texts with one of the stems.
	StemWords(ctx context.Context, projectID string, textIDs, stems []string) ([]StoredWord, error)
	// RareStemWords returns the word rows of the texts with one of the stems, without the stems whose rows
	// hold more than maxBytes of postings in these texts: those stay unread.
	RareStemWords(ctx context.Context, projectID string, textIDs, stems []string, maxBytes int64) ([]StoredWord, error)
	// WordsContaining returns every word row of the texts whose word contains part.
	WordsContaining(ctx context.Context, projectID string, textIDs []string, part string) ([]StoredWord, error)
	// Paragraphs reads paragraphs of chapter revisions of a text version.
	Paragraphs(ctx context.Context, projectID, view string, keys []ParagraphKey) ([]ParagraphText, error)
}

// CardSource is the port the saga reads chapter cards through: only the given parts of them, or
// only the facts about the given names (glossary keys; the source may return more, never fewer),
// the name registry of the project, nil without one, and the stamp of the cards and the registry.
type CardSource interface {
	Cards(ctx context.Context, projectID string, keys []CardKey, parts domain.CardParts) (map[CardKey]domain.Card, error)
	Facts(ctx context.Context, projectID string, keys []CardKey, names []string) (map[CardKey][]domain.Fact, error)
	Names(ctx context.Context, projectID string) (*domain.NameRegistry, error)
	CardsStamp(ctx context.Context, projectID string, keys []CardKey) (CardsStamp, error)
}

// CardsStamp is what changes whenever a reader of all cards would read something else: the number of
// the cards of the chapter texts, the time the latest was written and the digest of the name registry.
type CardsStamp struct {
	Cards   int64
	Written time.Time
	Names   string
	// Materials names what the author's records say now.
	Materials string
}

// Saga reads the text version, the search rows and the cards of a project for one call.
type Saga struct {
	text  SagaText
	cards CardSource
}

func NewSaga(text SagaText, cards CardSource) *Saga {
	if text == nil || cards == nil {
		panic("saga memory: text and card sources are required")
	}
	return &Saga{text: text, cards: cards}
}

// Outline reads the chapter selection of the current text version and the stored search texts
// of its chapters as one snapshot (UC2: «прочитать выборку глав и их тексты поиска»). A chapter
// without a stored text keeps an empty one; readers of text and search refuse such an outline.
func (saga *Saga) Outline(ctx context.Context, projectID, view string) (*domain.Outline, error) {
	if ctx == nil || projectID == "" || view == "" {
		return nil, ErrInvalid
	}
	selection, texts, err := saga.text.Outline(ctx, projectID, view)
	if err != nil {
		return nil, err
	}
	version := outlineVersion(selection)
	stored := make([]domain.SearchText, len(version.Chapters))
	for i, chapter := range version.Chapters {
		stored[i] = texts[CardKey{ChapterID: chapter.ID, ContentHash: chapter.ContentHash}]
	}
	return domain.NewOutline(version, stored), nil
}

// Search runs the queries of one search call over the stored rows (UC2): it looks up the
// vocabulary the queries consult, reads the word rows of their stems and ranks with the index
// code; a substring query reads only paragraphs having a word with its letters. It then loads
// the paragraphs of the hits shown(query, result) names, for their snippets.
func (saga *Saga) Search(
	ctx context.Context, outline *domain.Outline, queries []domain.Query, shown func(domain.Query, domain.Result) []domain.Hit,
) (*domain.Index, []domain.Result, error) {
	index, err := domain.NewSearchIndex(outline)
	if err != nil {
		return nil, nil, err
	}
	projectID, textIDs, chapters := outline.Version.ProjectID, outline.TextIDs(), outline.Chapters()
	var texts []string
	var wordQueries []domain.Query
	for _, query := range queries {
		if query.Mode != domain.ModeSubstring {
			texts = append(texts, query.Text)
			wordQueries = append(wordQueries, query)
		}
	}
	if len(wordQueries) > 0 {
		mode := wordQueries[0].Mode
		vocabulary, err := saga.text.Vocabulary(ctx, projectID, textIDs, index.Lookup(texts, mode))
		if err != nil {
			return nil, nil, err
		}
		index.AddVocabulary(vocabulary)
		rows, err := saga.text.StemWords(ctx, projectID, textIDs, index.QueryStems(texts, mode))
		if err != nil {
			return nil, nil, err
		}
		if err := index.AddPostings(wordRows(rows, chapters)); err != nil {
			return nil, nil, err
		}
	}
	results := make([]domain.Result, len(queries))
	shownParagraphs := map[int][]int{}
	for i, query := range queries {
		if query.Mode == domain.ModeSubstring {
			if results[i], err = saga.substring(ctx, outline, index, query); err != nil {
				return nil, nil, err
			}
		} else {
			results[i] = index.Run(query)
		}
		for _, hit := range shown(query, results[i]) {
			chapter, paragraph := index.ParagraphsOf(hit.Para)
			shownParagraphs[chapter] = append(shownParagraphs[chapter], paragraph)
		}
	}
	if err := saga.Paragraphs(ctx, outline, shownParagraphs); err != nil {
		return nil, nil, err
	}
	return index, results, nil
}

// Nearest ranks the paragraphs near one line of a chapter's plan for the writing of the chapter
// (domain.RankNearest) and loads the paragraphs of the best hits. The function words of the line are not
// looked up and the postings of its frequent stems stay in the database: no saga index is kept in memory.
func (saga *Saga) Nearest(ctx context.Context, outline *domain.Outline, query domain.NearestQuery) (*domain.Index, []domain.Hit, error) {
	index, err := domain.NewSearchIndex(outline)
	if err != nil {
		return nil, nil, err
	}
	pool := index.NearestPool(query)
	text := domain.NearestText(query.Text)
	if text == "" || len(pool.Chapters) == 0 {
		return index, nil, nil
	}
	projectID := outline.Version.ProjectID
	textIDs := make([]string, len(pool.Chapters))
	for i, chapter := range pool.Chapters {
		textIDs[i] = outline.Texts[chapter].ID
	}
	vocabulary, err := saga.text.Vocabulary(ctx, projectID, textIDs, index.Lookup([]string{text}, domain.ModeAny))
	if err != nil {
		return nil, nil, err
	}
	index.AddVocabulary(vocabulary)
	rows, err := saga.text.RareStemWords(ctx, projectID, textIDs, index.QueryStems([]string{text}, domain.ModeAny), pool.MaxPostingBytes())
	if err != nil {
		return nil, nil, err
	}
	hits, err := index.RankNearest(pool, query.Limit, wordRows(rows, outline.Chapters()))
	if err != nil {
		return nil, nil, err
	}
	wanted := map[int][]int{}
	for _, hit := range hits {
		chapter, paragraph := index.ParagraphsOf(hit.Para)
		wanted[chapter] = append(wanted[chapter], paragraph)
	}
	if err := saga.Paragraphs(ctx, outline, wanted); err != nil {
		return nil, nil, err
	}
	return index, hits, nil
}

// substringBatch bounds the chapters a needle without letters reads at once.
const substringBatch = 64

// substring reads the paragraphs that have a word containing the longest run of letters of the
// needle and matches them; a needle without letters reads the chapters of its range whole, in
// batches. Paragraph texts are dropped once matched.
func (saga *Saga) substring(ctx context.Context, outline *domain.Outline, index *domain.Index, query domain.Query) (domain.Result, error) {
	var batches []map[int][]int
	if part := domain.SubstringWordPart(query.Text); part != "" {
		rows, err := saga.text.WordsContaining(ctx, outline.Version.ProjectID, outline.TextIDs(), part)
		if err != nil {
			return domain.Result{}, err
		}
		candidates, err := index.SubstringCandidates(query, wordRows(rows, outline.Chapters()))
		if err != nil {
			return domain.Result{}, err
		}
		batches = append(batches, candidates)
	} else {
		chapters := index.SubstringChapters(query)
		for start := 0; start < len(chapters); start += substringBatch {
			batch := map[int][]int{}
			for _, chapter := range chapters[start:min(start+substringBatch, len(chapters))] {
				for paragraph := range outline.Version.Chapters[chapter].ParagraphCount {
					batch[chapter] = append(batch[chapter], paragraph)
				}
			}
			batches = append(batches, batch)
		}
	}
	var hits []domain.Hit
	for _, batch := range batches {
		if err := saga.Paragraphs(ctx, outline, batch); err != nil {
			return domain.Result{}, err
		}
		for chapter := range batch {
			hits = append(hits, index.SubstringHits(query, chapter)...)
			outline.Version.Chapters[chapter].Paragraphs = nil
		}
	}
	return index.SubstringResult(query, hits), nil
}

func wordRows(rows []StoredWord, chapters map[string]int) []domain.WordPostings {
	out := make([]domain.WordPostings, 0, len(rows))
	for _, row := range rows {
		chapter, ok := chapters[row.TextID]
		if !ok {
			chapter = -1
		}
		out = append(out, domain.WordPostings{Chapter: chapter, Word: row.Word, Stem: row.Stem, Postings: row.Postings})
	}
	return out
}

// Paragraphs loads paragraphs (0-based, by reading-order chapter) into the outline version by
// their stored order; the other paragraphs of a chapter stay empty. A paragraph that is gone, as
// when its chapter was deleted after the outline was read, fails the call with ErrTextChanged.
func (saga *Saga) Paragraphs(ctx context.Context, outline *domain.Outline, wanted map[int][]int) error {
	if !outline.Ready() {
		return domain.ErrSearchNotReady
	}
	var keys []ParagraphKey
	byOrder := map[string]map[int32]int{}
	for chapter, paragraphs := range wanted {
		item := outline.Version.Chapters[chapter]
		orders := outline.Texts[chapter].Orders
		at := map[int32]int{}
		for _, paragraph := range paragraphs {
			if paragraph < 0 || paragraph >= len(orders) {
				return ErrInvalid
			}
			keys = append(keys, ParagraphKey{ChapterID: item.ID, Revision: item.Revision, Order: orders[paragraph]})
			at[orders[paragraph]] = paragraph
		}
		byOrder[item.ID] = at
	}
	if len(keys) == 0 {
		return nil
	}
	read, err := saga.text.Paragraphs(ctx, outline.Version.ProjectID, outline.Version.View, keys)
	if err != nil {
		return err
	}
	chapters := map[string]int{}
	for chapter := range wanted {
		chapters[outline.Version.Chapters[chapter].ID] = chapter
	}
	type stored struct {
		chapterID string
		order     int32
	}
	loaded := make(map[stored]bool, len(keys))
	for _, paragraph := range read {
		chapter, ok := chapters[paragraph.ChapterID]
		at, found := byOrder[paragraph.ChapterID][paragraph.Order]
		if !ok || !found {
			continue
		}
		outline.Version.LoadParagraph(chapter, at, paragraph.Text)
		loaded[stored{paragraph.ChapterID, paragraph.Order}] = true
	}
	for _, key := range keys {
		if !loaded[stored{key.ChapterID, key.Order}] {
			return domain.ErrTextChanged
		}
	}
	return nil
}

// Chapters loads whole chapters into the outline version.
func (saga *Saga) Chapters(ctx context.Context, outline *domain.Outline, chapters []int) error {
	wanted := make(map[int][]int, len(chapters))
	for _, chapter := range chapters {
		paragraphs := make([]int, outline.Version.Chapters[chapter].ParagraphCount)
		for i := range paragraphs {
			paragraphs[i] = i
		}
		wanted[chapter] = paragraphs
	}
	return saga.Paragraphs(ctx, outline, wanted)
}

// Cards reads the parts of the cards of the chapters from..to (reading-order indexes,
// inclusive; to < 0 for the last chapter) of the version and builds the glossary from them, with the
// name registry when the mentions are read.
func (saga *Saga) Cards(ctx context.Context, outline *domain.Outline, from, to int, parts domain.CardParts) (*domain.CardSet, error) {
	chapters := outline.Version.Chapters
	if to < 0 || to >= len(chapters) {
		to = len(chapters) - 1
	}
	from = max(from, 0)
	keys := make([]CardKey, 0, max(0, to-from+1))
	for i := from; i <= to; i++ {
		keys = append(keys, CardKey{ChapterID: chapters[i].ID, ContentHash: chapters[i].ContentHash})
	}
	found, err := saga.cards.Cards(ctx, outline.Version.ProjectID, keys, parts)
	if err != nil {
		return nil, err
	}
	cards := make(map[int]*domain.Card, len(found))
	for i := from; i <= to; i++ {
		if card, ok := found[CardKey{ChapterID: chapters[i].ID, ContentHash: chapters[i].ContentHash}]; ok {
			cards[i] = &card
		}
	}
	var names *domain.NameRegistry
	if parts.Mentions {
		if names, err = saga.cards.Names(ctx, outline.Version.ProjectID); err != nil {
			return nil, err
		}
	}
	return domain.NewCardSet(cards, names), nil
}

// cardsRevision names what the cards of the text version say now (specs/2026-11-04-saga-map.md, R1.5):
// it changes with the chapter selection, with any card of its chapter texts and with the name registry,
// so a reader of all cards may answer «not modified» without reading them.
func cardsRevision(version *domain.Version, stamp CardsStamp) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d\x00%d\x00%s\x00%s",
		version.View, version.Fingerprint, stamp.Cards, stamp.Written.UnixMicro(), stamp.Names, stamp.Materials))
	return hex.EncodeToString(sum[:])
}

// Facts reads the facts the cards of chapters 0..to (reading-order indexes; to < 0 for all) state
// about the given names, in story order (specs/2026-10-29-chapter-cards-quality.md, UC4); the facts
// of a chapter off the main line come as past.
func (saga *Saga) Facts(ctx context.Context, outline *domain.Outline, to int, names []string) ([]domain.ChapterFact, error) {
	chapters := outline.Version.Chapters
	if to < 0 || to >= len(chapters) {
		to = len(chapters) - 1
	}
	keys := make([]CardKey, 0, to+1)
	for i := 0; i <= to; i++ {
		keys = append(keys, CardKey{ChapterID: chapters[i].ID, ContentHash: chapters[i].ContentHash})
	}
	found, err := saga.cards.Facts(ctx, outline.Version.ProjectID, keys, names)
	if err != nil {
		return nil, err
	}
	stating := make([]CardKey, 0, len(found))
	for key := range found {
		stating = append(stating, key)
	}
	// The facts of a chapter off the main line (another storyline, a recollection) are history.
	times, err := saga.cards.Cards(ctx, outline.Version.ProjectID, stating, domain.CardParts{ChapterTime: true})
	if err != nil {
		return nil, err
	}
	var facts []domain.ChapterFact
	for i, key := range keys {
		offLine := times[key].When.OffLine()
		for _, fact := range found[key] {
			fact.Past = fact.Past || offLine
			facts = append(facts, domain.ChapterFact{Chapter: i, Fact: fact})
		}
	}
	return facts, nil
}

// LinkIndex returns the index the change links of a card set stem with: the vocabulary entries
// of the words of the cards (UC3).
func (saga *Saga) LinkIndex(ctx context.Context, outline *domain.Outline, cards *domain.CardSet) (*domain.Index, error) {
	index, err := domain.NewSearchIndex(outline)
	if err != nil {
		return nil, err
	}
	words := cards.LinkWords()
	if len(words) == 0 {
		return index, nil
	}
	vocabulary, err := saga.text.Vocabulary(ctx, outline.Version.ProjectID, outline.TextIDs(), domain.SearchLookup{Words: words})
	if err != nil {
		return nil, err
	}
	index.AddVocabulary(vocabulary)
	return index, nil
}
