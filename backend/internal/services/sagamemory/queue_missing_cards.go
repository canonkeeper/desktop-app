package sagamemory

import (
	"context"
	"errors"
)

// TextVersion names one text version of a project.
type TextVersion struct {
	ProjectID string
	View      string
}

// IndexedVersions lists the text versions that have indexed chapters.
type IndexedVersions interface {
	IndexedVersions(ctx context.Context) ([]TextVersion, error)
}

// CardsQueue queues the cards Job of one text version; a Job already waiting is reused.
type CardsQueue interface {
	QueueCards(ctx context.Context, version TextVersion) error
}

type selectionSource interface {
	Selection(ctx context.Context, projectID, view string) (Selection, error)
}

type cardKeys interface {
	ExistingCards(ctx context.Context, projectID string, keys []CardKey) (map[CardKey]bool, error)
}

// cardsAndNames is what the start asks about a version: its cards, whether a registry exists and
// which cards its prepared map was built from.
type cardsAndNames interface {
	cardKeys
	NamesDigest(ctx context.Context, projectID string) (string, error)
	CardsStamp(ctx context.Context, projectID string, keys []CardKey) (CardsStamp, error)
	StoredMapRevision(ctx context.Context, projectID, view string) (string, error)
}

// QueueMissingCards queues the cards Job of every indexed text version whose chapters lack a
// card, whose project has no name registry or whose map is not of its current cards. The backend runs
// it at start, so text indexed before cards existed gets its cards without a reindex, carded text its
// registry without new cards (specs/2026-11-01-index-foundation.md, R5.6) and its map without a click
// (specs/2026-11-04-saga-map.md, R10.3).
type QueueMissingCards struct {
	versions IndexedVersions
	text     selectionSource
	cards    cardsAndNames
	queue    CardsQueue
}

func NewQueueMissingCards(versions IndexedVersions, text selectionSource, cards cardsAndNames, queue CardsQueue) *QueueMissingCards {
	if versions == nil || text == nil || cards == nil || queue == nil {
		panic("saga memory: versions, text, cards and queue are required")
	}
	return &QueueMissingCards{versions: versions, text: text, cards: cards, queue: queue}
}

// Execute returns how many versions it queued cards for; one failing version does not stop
// the others.
func (useCase *QueueMissingCards) Execute(ctx context.Context) (int, error) {
	versions, err := useCase.versions.IndexedVersions(ctx)
	if err != nil {
		return 0, err
	}
	queued := 0
	var errs []error
	for _, version := range versions {
		queue, err := useCase.needsJob(ctx, version)
		if err == nil && queue {
			if err = useCase.queue.QueueCards(ctx, version); err == nil {
				queued++
			}
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return queued, errors.Join(errs...)
}

func (useCase *QueueMissingCards) needsJob(ctx context.Context, version TextVersion) (bool, error) {
	selection, missing, err := missingCards(ctx, useCase.text, useCase.cards, version.ProjectID, version.View)
	if err != nil || len(missing.Chapters) > 0 {
		return err == nil, err
	}
	if len(selection.Chapters) == 0 {
		return false, nil
	}
	digest, err := useCase.cards.NamesDigest(ctx, version.ProjectID)
	if err != nil || digest == "" {
		return err == nil, err
	}
	stored, err := useCase.cards.StoredMapRevision(ctx, version.ProjectID, version.View)
	if err != nil {
		return false, err
	}
	outline := outlineVersion(selection)
	stamp, err := useCase.cards.CardsStamp(ctx, version.ProjectID, versionCardKeys(outline))
	if err != nil {
		return false, err
	}
	return stored != mapRevision(outline, stamp), nil
}

// missingCards returns the chapter selection of a text version and the chapters that have
// no card of their current text.
func missingCards(ctx context.Context, text selectionSource, cards cardKeys, projectID, view string) (Selection, Selection, error) {
	selection, err := text.Selection(ctx, projectID, view)
	if err != nil {
		return Selection{}, Selection{}, err
	}
	keys := make([]CardKey, len(selection.Chapters))
	for i, chapter := range selection.Chapters {
		keys[i] = CardKey{ChapterID: chapter.ID, ContentHash: chapter.ContentHash}
	}
	existing, err := cards.ExistingCards(ctx, projectID, keys)
	if err != nil {
		return Selection{}, Selection{}, err
	}
	missing := Selection{ProjectID: selection.ProjectID, View: selection.View, Fingerprint: selection.Fingerprint}
	for i, chapter := range selection.Chapters {
		if !existing[keys[i]] {
			missing.Chapters = append(missing.Chapters, chapter)
		}
	}
	return selection, missing, nil
}
