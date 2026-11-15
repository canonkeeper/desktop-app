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

// CardsQueue queues the cards Job of one text version; a Job already waiting is reused. It reports whether a Job waits
// for the version: a build whose helper is not connected queues one only for a saga with cards
// (specs/2026-11-13-desk-gateway.md GW4.5).
type CardsQueue interface {
	QueueCards(ctx context.Context, version TextVersion) (bool, error)
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
	namesSource
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
	policy   CatchUpPolicy
}

func NewQueueMissingCards(versions IndexedVersions, text selectionSource, cards cardsAndNames, queue CardsQueue) *QueueMissingCards {
	if versions == nil || text == nil || cards == nil || queue == nil {
		panic("saga memory: versions, text, cards and queue are required")
	}
	return &QueueMissingCards{versions: versions, text: text, cards: cards, queue: queue}
}

// ReadingMarks are the marks of the reading a catch-up leaves alone (specs/2026-11-15-desk-reading-rest.md RR1.2, RR1.8):
// the chapters marked «не прочиталась» and the names of a set marked, by the one rule of the policy of the reading.
type ReadingMarks interface {
	ChapterMarks
	NamesMarks
}

// CatchUpPolicy is the policy of the reading the catch-up asks: whether the model may read a saga now — the one rule of the
// builders (specs/2026-11-15-desk-reading-queue.md RQ2.1) — and the marks it leaves alone.
type CatchUpPolicy interface {
	ReadingAllowed(ctx context.Context, projectID string, sample bool) (bool, error)
	ReadingMarks
}

// WithPolicy makes the catch-up queue no Job for what the marks of the policy of the reading of the build keep, nor for what
// needs the model of a saga it may not read now; the build gives it once its modules are composed. Without it — the site —
// nothing is marked and every saga is read.
func (useCase *QueueMissingCards) WithPolicy(policy CatchUpPolicy) *QueueMissingCards {
	useCase.policy = policy
	return useCase
}

// Execute returns how many versions it queued cards for — a version the queue left without a Job is not counted; one
// failing version does not stop the others.
func (useCase *QueueMissingCards) Execute(ctx context.Context) (int, error) {
	versions, err := useCase.versions.IndexedVersions(ctx)
	if err != nil {
		return 0, err
	}
	queued := 0
	var errs []error
	for _, version := range versions {
		queue, err := useCase.needsJob(ctx, version, false)
		if err == nil && queue {
			var waits bool
			if waits, err = useCase.queue.QueueCards(ctx, version); waits {
				queued++
			}
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return queued, errors.Join(errs...)
}

// ExecuteFor queues the cards Job of the versions of one saga as Execute does (specs/2026-11-13-desk-consent.md CN3.2): the
// consent of its author given or resumed reads what its saga misses now, without a restart of the app — the register of
// names too, when the names of its cards are not those of the stored register (a reading stopped on its step: CN3.8).
func (useCase *QueueMissingCards) ExecuteFor(ctx context.Context, projectID string) (int, error) {
	versions, err := useCase.versions.IndexedVersions(ctx)
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, version := range versions {
		if version.ProjectID != projectID {
			continue
		}
		queue, err := useCase.needsJob(ctx, version, true)
		if err == nil && queue {
			var waits bool
			if waits, err = useCase.queue.QueueCards(ctx, version); waits {
				queued++
			}
		}
		if err != nil {
			return queued, err
		}
	}
	return queued, nil
}

// needsJob says whether a version needs its cards Job: a chapter lacks its card, the saga has no register or its map is
// not of its cards; with names, also when the names of its cards are not those of the stored register. What the marks of the
// reading keep — a chapter that did not read, names that did not come together — needs no Job
// (specs/2026-11-15-desk-reading-rest.md RR1.2, RR1.8). A saga the model may not read now gets a Job only for its map of the
// cards there are, behind — the one thing the Job does without the model; a saga without a card has no map
// (specs/2026-11-15-desk-reading-queue.md RQ2.1).
func (useCase *QueueMissingCards) needsJob(ctx context.Context, version TextVersion, names bool) (bool, error) {
	selection, missing, err := missingCards(ctx, useCase.text, useCase.cards, version.ProjectID, version.View)
	if err == nil && useCase.policy != nil {
		var allowed bool
		if allowed, err = useCase.policy.ReadingAllowed(ctx, version.ProjectID, false); err == nil && !allowed {
			if len(missing.Chapters) == len(selection.Chapters) {
				return false, nil
			}
			return useCase.mapBehind(ctx, version, selection)
		}
		if err == nil {
			missing, err = withoutUnread(ctx, useCase.policy, version.ProjectID, missing)
		}
	}
	if err != nil || len(missing.Chapters) > 0 {
		return err == nil, err
	}
	if len(selection.Chapters) == 0 {
		return false, nil
	}
	digest, err := useCase.cards.NamesDigest(ctx, version.ProjectID)
	if err != nil {
		return false, err
	}
	if digest == "" {
		if useCase.policy == nil {
			return true, nil
		}
		// No register yet: the Job builds it, unless the names of the cards are marked — then only its map may need it.
		if _, unread, err := NamesOf(ctx, useCase.cards, useCase.policy, version.ProjectID, selection); err != nil || unread == "" {
			return err == nil, err
		}
	} else if names {
		if wait, _, err := NamesOf(ctx, useCase.cards, useCase.policy, version.ProjectID, selection); err != nil || wait {
			return err == nil, err
		}
	}
	return useCase.mapBehind(ctx, version, selection)
}

// mapBehind says the prepared map of a version is not of its cards now.
func (useCase *QueueMissingCards) mapBehind(ctx context.Context, version TextVersion, selection Selection) (bool, error) {
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

// MissingCards returns the chapter selection of a text version and the chapters that have no card of their current text:
// what the app estimates the reading of (specs/2026-11-13-desk-consent.md CN1.3).
func MissingCards(ctx context.Context, text selectionSource, cards cardKeys, projectID, view string) (Selection, Selection, error) {
	return missingCards(ctx, text, cards, projectID, view)
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
