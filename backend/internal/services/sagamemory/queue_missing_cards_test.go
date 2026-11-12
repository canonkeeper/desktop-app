package sagamemory

import (
	"context"
	"reflect"
	"testing"
)

// Text indexed before cards existed gets its cards without a reindex: the backend queues the cards Job of every version that still lacks some.
func TestQueueMissingCardsQueuesOnlyVersionsLackingCards(t *testing.T) {
	text := cardsTextSource{chapters: []SelectedChapter{
		{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1. Старт"},
		{ID: "c2", ContentHash: "h2", BookOrder: 1, Order: 2, Title: "Глава 2. Путь"},
	}}
	versions := indexedVersions{{ProjectID: "carded", View: "draft"}, {ProjectID: "missing", View: "published"}}
	cards := versionCards{
		"carded":  {{ChapterID: "c1", ContentHash: "h1"}: true, {ChapterID: "c2", ContentHash: "h2"}: true},
		"missing": {{ChapterID: "c1", ContentHash: "h1"}: true},
	}
	queue := &cardsQueue{}

	named := map[string]string{"carded": "digest", "missing": "digest"}
	queued, err := NewQueueMissingCards(versions, text, namedVersions{versionCards: cards, digests: named, text: text}, queue).Execute(context.Background())
	if err != nil || queued != 1 || !reflect.DeepEqual(queue.queued, []TextVersion{{ProjectID: "missing", View: "published"}}) {
		t.Fatalf("Execute() = %d, %v, queued %v; want only the version lacking a card", queued, err, queue.queued)
	}
}

// specs/2026-11-01-index-foundation.md R5.6: a version with every card but no registry is queued, a
// version with its registry or without any chapter is not.
func TestQueueMissingCardsQueuesVersionsWithoutARegistry(t *testing.T) {
	text := cardsTextSource{chapters: []SelectedChapter{{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1. Старт"}}}
	versions := indexedVersions{{ProjectID: "named", View: "draft"}, {ProjectID: "unnamed", View: "draft"}}
	card := map[CardKey]bool{{ChapterID: "c1", ContentHash: "h1"}: true}
	cards := versionCards{"named": card, "unnamed": card}
	queue := &cardsQueue{}
	useCase := NewQueueMissingCards(versions, text, namedVersions{versionCards: cards, digests: map[string]string{"named": "digest"}, text: text}, queue)
	queued, err := useCase.Execute(context.Background())
	if err != nil || queued != 1 || !reflect.DeepEqual(queue.queued, []TextVersion{{ProjectID: "unnamed", View: "draft"}}) {
		t.Fatalf("Execute() = %d, %v, queued %v; want only the version without a registry", queued, err, queue.queued)
	}

	queue = &cardsQueue{}
	queued, err = NewQueueMissingCards(versions, cardsTextSource{}, namedVersions{versionCards: cards}, queue).Execute(context.Background())
	if err != nil || queued != 0 {
		t.Fatalf("no chapters: Execute() = %d, %v, queued %v; want nothing", queued, err, queue.queued)
	}
}

// specs/2026-11-04-saga-map.md R10.3: a version with all it needs but a map of its cards is queued, so a saga carded before the
// map gets it without a click.
func TestQueueMissingCardsQueuesVersionsWithoutACurrentMap(t *testing.T) {
	text := cardsTextSource{chapters: []SelectedChapter{{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1. Старт"}}}
	versions := indexedVersions{{ProjectID: "mapped", View: "draft"}, {ProjectID: "behind", View: "draft"}}
	card := map[CardKey]bool{{ChapterID: "c1", ContentHash: "h1"}: true}
	queue := &cardsQueue{}
	useCase := NewQueueMissingCards(versions, text, namedVersions{
		versionCards: versionCards{"mapped": card, "behind": card}, digests: map[string]string{"mapped": "digest", "behind": "digest"},
		text: text, behind: map[string]bool{"behind": true},
	}, queue)
	queued, err := useCase.Execute(context.Background())
	if err != nil || queued != 1 || !reflect.DeepEqual(queue.queued, []TextVersion{{ProjectID: "behind", View: "draft"}}) {
		t.Fatalf("Execute() = %d, %v, queued %v; want only the version behind", queued, err, queue.queued)
	}
}

type namedVersions struct {
	versionCards
	digests map[string]string
	text    cardsTextSource
	// behind lists the projects whose prepared map is not of their cards.
	behind map[string]bool
}

func (versions namedVersions) NamesDigest(_ context.Context, projectID string) (string, error) {
	return versions.digests[projectID], nil
}

func (versions namedVersions) CardsStamp(context.Context, string, []CardKey) (CardsStamp, error) {
	return CardsStamp{}, nil
}

func (versions namedVersions) StoredMapRevision(ctx context.Context, projectID, view string) (string, error) {
	if versions.behind[projectID] {
		return "", nil
	}
	selection, err := versions.text.Selection(ctx, projectID, view)
	if err != nil {
		return "", err
	}
	return mapRevision(outlineVersion(selection), CardsStamp{}), nil
}

type indexedVersions []TextVersion

func (versions indexedVersions) IndexedVersions(context.Context) ([]TextVersion, error) {
	return versions, nil
}

type versionCards map[string]map[CardKey]bool

func (cards versionCards) ExistingCards(_ context.Context, projectID string, keys []CardKey) (map[CardKey]bool, error) {
	found := map[CardKey]bool{}
	for _, key := range keys {
		if cards[projectID][key] {
			found[key] = true
		}
	}
	return found, nil
}

type cardsQueue struct {
	queued []TextVersion
}

func (queue *cardsQueue) QueueCards(_ context.Context, version TextVersion) (bool, error) {
	queue.queued = append(queue.queued, version)
	return true, nil
}
