package sagamemory

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// The database side of the operator's chapter-cards command (specs/2026-10-29-chapter-cards-quality.md,
// UC1): coverage, the snapshot of the chapter texts the cards Job reads, the export of the current
// cards and the verified load of cards built elsewhere.

// CardsCoverage is the cards coverage of every project with chapter texts (the operator's
// chapter-cards status and the admin service status).
func (repository *Repository) CardsCoverage(ctx context.Context) ([]domain.CardsCoverage, error) {
	rows, err := sqlc.New(repository.db.Pool).ListChapterCardsCoverage(ctx, domain.CardVersion)
	if err != nil {
		return nil, err
	}
	coverage := make([]domain.CardsCoverage, len(rows))
	for i, row := range rows {
		coverage[i] = domain.CardsCoverage{
			ProjectID: storedb.UUIDString(row.ProjectID), Texts: row.Texts, CurrentCards: row.CurrentCards,
			OtherCards: row.OtherCards, Models: row.Models,
		}
	}
	return coverage, nil
}

// CardsSnapshotParagraph is one paragraph of a chapter text as the cards Job reads it.
type CardsSnapshotParagraph struct {
	ID    string `json:"id"`
	Order int32  `json:"order"`
	Text  string `json:"text"`
}

// CardsSnapshotChapter is one chapter of a text version with what the cards Job gives the model:
// the book and chapter it is, its place in the version and its paragraphs.
type CardsSnapshotChapter struct {
	ProjectID   string                   `json:"projectId"`
	View        string                   `json:"view"`
	ChapterID   string                   `json:"chapterId"`
	ContentHash string                   `json:"contentHash"`
	BookOrder   int                      `json:"bookOrder"`
	BookTitle   string                   `json:"bookTitle"`
	Order       int                      `json:"order"`
	Title       string                   `json:"title"`
	Paragraphs  []CardsSnapshotParagraph `json:"paragraphs"`
}

// CardsSnapshot visits every chapter of every text version the cards Job builds (of the given
// projects when there are any), version by version in reading order, in one read-only transaction.
func (repository *Repository) CardsSnapshot(ctx context.Context, projectIDs []string, visit func(CardsSnapshotChapter) error) error {
	wanted := map[string]bool{}
	for _, projectID := range projectIDs {
		parsed, err := storedb.ParseUUID(strings.TrimSpace(projectID))
		if err != nil {
			return ErrInvalidCards
		}
		wanted[storedb.UUIDString(parsed)] = true
	}
	versions, err := repository.IndexedVersions(ctx)
	if err != nil {
		return err
	}
	tx, err := repository.db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New(tx)
	for _, version := range versions {
		if len(wanted) > 0 && !wanted[version.ProjectID] {
			continue
		}
		project, err := storedb.ParseUUID(version.ProjectID)
		if err != nil {
			return err
		}
		selection, err := readSelection(ctx, queries, project, version.ProjectID, version.View)
		if err != nil {
			return err
		}
		for _, selected := range selection.Chapters {
			chapterID, err := storedb.ParseUUID(selected.ID)
			if err != nil {
				return err
			}
			rows, err := queries.ListSagaSearchSnapshotParagraphs(ctx, sqlc.ListSagaSearchSnapshotParagraphsParams{
				ChapterID: chapterID, Revision: selected.Revision, SourceView: version.View,
			})
			if err != nil {
				return err
			}
			chapter := CardsSnapshotChapter{
				ProjectID: version.ProjectID, View: version.View, ChapterID: selected.ID, ContentHash: selected.ContentHash,
				BookOrder: selected.BookOrder, BookTitle: selected.BookTitle, Order: selected.Order, Title: selected.Title,
				Paragraphs: make([]CardsSnapshotParagraph, len(rows)),
			}
			for i, row := range rows {
				chapter.Paragraphs[i] = CardsSnapshotParagraph{ID: storedb.UUIDString(row.ID), Order: row.Order, Text: row.PlainText}
			}
			if err := visit(chapter); err != nil {
				return err
			}
		}
	}
	return nil
}

// OperatorCard is one card of a chapter text as the operator exports and loads it.
type OperatorCard struct {
	ProjectID   string
	ChapterID   string
	ContentHash string
	Version     int
	Model       string
	Card        json.RawMessage
}

// CurrentCards visits the cards of the current chapter texts of live projects (of one project
// when projectID is not empty), whatever instructions wrote them.
func (repository *Repository) CurrentCards(ctx context.Context, projectID string, visit func(OperatorCard) error) error {
	var project pgtype.UUID
	if projectID != "" {
		parsed, err := storedb.ParseUUID(projectID)
		if err != nil {
			return ErrInvalidCards
		}
		project = parsed
	}
	rows, err := sqlc.New(repository.db.Pool).ListCurrentChapterCards(ctx, project)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := visit(OperatorCard{
			ProjectID: storedb.UUIDString(row.ProjectID), ChapterID: storedb.UUIDString(row.ChapterID),
			ContentHash: hex.EncodeToString(row.ContentHash), Version: int(row.Version), Model: row.Model, Card: row.CardJson,
		}); err != nil {
			return err
		}
	}
	return nil
}

var ErrInvalidCards = errors.New("saga memory: invalid operator cards")

// CardsLoad is what a load did for one project.
type CardsLoad struct {
	Inserted, Replaced int
	// Refused names why nothing of the project was loaded; empty when it was.
	Refused string
}

// LoadCards writes the cards of one project in one transaction: the project is live, no cards
// Job of it runs, every card is of the current instructions, checks as a card and belongs to a
// current chapter text; otherwise the project is refused and nothing of it is written. A card
// replaces the card of the same text, whatever model wrote it.
func (repository *Repository) LoadCards(ctx context.Context, projectID string, cards []OperatorCard) (CardsLoad, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return CardsLoad{}, ErrInvalidCards
	}
	var outcome CardsLoad
	err = repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		return loadCardsInTx(ctx, tx, project, projectID, cards, &outcome)
	})
	if errors.Is(err, errCardsRefused) {
		return outcome, nil
	}
	return outcome, err
}

// loadCardsInTx is the transaction of LoadCards; a refusal sets the outcome and returns
// errCardsRefused, which rolls the transaction back.
func loadCardsInTx(ctx context.Context, tx pgx.Tx, project pgtype.UUID, projectID string, cards []OperatorCard, outcome *CardsLoad) error {
	refuse := func(format string, args ...any) error {
		*outcome = CardsLoad{Refused: fmt.Sprintf(format, args...)}
		return errCardsRefused
	}
	queries := sqlc.New(tx)
	if _, err := queries.LockSagaSearchProject(ctx, project); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return refuse("the project is deleted")
		}
		return err
	}
	// The project's cards Jobs stay locked until the load commits: the claim skips locked Job
	// rows, so a queued Job starts only after the load and finds the cards written.
	statuses, err := queries.LockChapterCardsJobs(ctx, project)
	if err != nil {
		return err
	}
	for _, status := range statuses {
		if status != "queued" {
			return refuse("a cards Job of the project is running: load after it ends")
		}
	}
	for _, card := range cards {
		reason, err := loadCard(ctx, queries, project, projectID, card, outcome)
		if err != nil {
			return err
		}
		if reason != "" {
			return refuse("%s", reason)
		}
	}
	return nil
}

// loadCard checks and writes one card of a load; it returns why the project is refused, if it is.
func loadCard(ctx context.Context, queries *sqlc.Queries, project pgtype.UUID, projectID string, card OperatorCard, outcome *CardsLoad) (string, error) {
	if card.ProjectID != projectID {
		return fmt.Sprintf("the card of chapter %s belongs to project %s", card.ChapterID, card.ProjectID), nil
	}
	if card.Version != domain.CardVersion {
		return fmt.Sprintf("the card of chapter %s is of instructions v%d; this backend builds v%d", card.ChapterID, card.Version, domain.CardVersion), nil
	}
	card.Model = strings.TrimSpace(card.Model)
	if card.Model == "" || utf8.RuneCountInString(card.Model) > 200 {
		return fmt.Sprintf("the card of chapter %s names no model", card.ChapterID), nil
	}
	// A card checks as the cards Job's parser leaves it: an object with a summary, bounded.
	parsed, err := domain.ParseCard(string(card.Card), 1<<30)
	if err != nil || len(card.Card) == 0 || card.Card[0] != '{' {
		return fmt.Sprintf("the card of chapter %s does not check as a card", card.ChapterID), nil
	}
	body, err := json.Marshal(parsed)
	if err != nil {
		return "", err
	}
	chapterID, err := storedb.ParseUUID(card.ChapterID)
	if err != nil {
		return fmt.Sprintf("chapter %q is not an id", card.ChapterID), nil
	}
	hash, err := hex.DecodeString(card.ContentHash)
	if err != nil || len(hash) != 32 {
		return fmt.Sprintf("the content hash of chapter %s is not a hash", card.ChapterID), nil
	}
	current, err := queries.CountSagaSearchCurrentTexts(ctx, sqlc.CountSagaSearchCurrentTextsParams{
		ProjectID: project, ChapterIds: []pgtype.UUID{chapterID}, ContentHashes: [][]byte{hash},
	})
	if err != nil {
		return "", err
	}
	if current != 1 {
		return fmt.Sprintf("the text of chapter %s changed since the snapshot", card.ChapterID), nil
	}
	inserted, err := queries.ReplaceChapterCard(ctx, sqlc.ReplaceChapterCardParams{
		ProjectID: project, ChapterID: chapterID, ContentHash: hash, CardJson: body, Model: card.Model, Version: int32(card.Version),
	})
	if err != nil {
		return "", err
	}
	if inserted {
		outcome.Inserted++
	} else {
		outcome.Replaced++
	}
	return "", nil
}

// errCardsRefused rolls back a refused load.
var errCardsRefused = errors.New("saga memory: cards load refused")
