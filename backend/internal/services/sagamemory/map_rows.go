package sagamemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"canonkeeper/backend/internal/domain/authormaterials"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// mapRulesVersion names the rules of the prepared map in its revision: a new rule makes every map stale, and the catch-up at
// start builds them again (specs/2026-11-04-saga-map.md, UC10).
const mapRulesVersion = "saga-map/6"

// StoredMapPart is a part of an entry along the scale, gzip of JSON, for the chapters from First up to the next part.
type StoredMapPart struct {
	First int
	Body  []byte
}

// StoredMapRow is what a search of the map reads of one entry, its prepared card for the whole saga, gzip of JSON, and its parts
// along the scale.
type StoredMapRow struct {
	Key          string
	Name         string
	Type         string
	ChapterCount int
	SearchText   string
	Card         []byte
	Parts        []StoredMapPart
}

// StoredMapRows is one build of the map of a text version, with the chapter texts it was built from: a save finds out whether
// they are still current, so a deleted source leaves nothing behind.
type StoredMapRows struct {
	Keys     []CardKey
	View     string
	Revision string
	Document []byte
	Chapters []byte
	Nodes    []StoredMapRow
	Names    map[string]string
}

// MapStore keeps a build of the map while the cards Job runs under its claim and every chapter text of the build is current.
type MapStore interface {
	SaveMap(ctx context.Context, claim jobs.WorkerClaim, rows StoredMapRows) error
}

// MapRefresher prepares the map of a text version; the stage says where a failure happened, for the log.
type MapRefresher interface {
	Refresh(ctx context.Context, claim jobs.WorkerClaim, view string) (stage string, err error)
}

// MapBuilder prepares the map of a text version from its cards, the last step of a cards Job.
type MapBuilder struct {
	saga      *Saga
	store     MapStore
	materials MapMaterials
}

// MapMaterials reads the records of the author's materials of a project.
type MapMaterials interface {
	AuthorMaterials(ctx context.Context, projectID string) ([]authormaterials.Record, error)
}

func NewMapBuilder(saga *Saga, store MapStore, materials MapMaterials) *MapBuilder {
	if saga == nil || store == nil || materials == nil {
		panic("saga memory: saga, map store and materials are required")
	}
	return &MapBuilder{saga: saga, store: store, materials: materials}
}

// mapRevision names what the map of a version is built from: the rules, the chapter selection, the cards and the names.
func mapRevision(version *domain.Version, stamp CardsStamp) string {
	sum := sha256.Sum256([]byte(mapRulesVersion + "\x00" + cardsRevision(version, stamp)))
	return hex.EncodeToString(sum[:16])
}

// Refresh builds and keeps the map. A saga without cards has no map.
func (builder *MapBuilder) Refresh(ctx context.Context, claim jobs.WorkerClaim, view string) (stage string, err error) {
	outline, err := builder.saga.Outline(ctx, claim.ProjectID, view)
	if err != nil {
		return "map_read", err
	}
	// The revision is read before the cards: a card written meanwhile leaves the map behind, not ahead, of what it says it is.
	stamp, err := builder.saga.cards.CardsStamp(ctx, claim.ProjectID, versionCardKeys(outline.Version))
	if err != nil {
		return "map_read", err
	}
	cards, err := builder.saga.Cards(ctx, outline, 0, -1, domain.CardParts{Mentions: true, Events: true, Changes: true})
	if err != nil {
		return "map_read", err
	}
	if len(cards.Cards) == 0 {
		return "", nil
	}
	index, err := builder.saga.LinkIndex(ctx, outline, cards)
	if err != nil {
		return "map_read", err
	}
	records, err := builder.materials.AuthorMaterials(ctx, claim.ProjectID)
	if err != nil {
		return "map_read", err
	}
	rows, err := encodeMapRows(outline.Version, domain.BuildMapRows(outline.Version, cards, index, mapRevision(outline.Version, stamp), mapMaterials(records)), view)
	if err != nil {
		return "map_encode", err
	}
	if err := builder.store.SaveMap(ctx, claim, rows); err != nil {
		return "map_store", err
	}
	return "", nil
}

// maxMapKeyBytes keeps a key inside what an index of PostgreSQL takes: a card with a page of text for a name, and the API asks
// for no key that long, must not stop the map of a saga.
const maxMapKeyBytes = 1000

// encodeMapRows writes each row as gzip of JSON outside the transaction of the save: each card for the whole saga as the API
// gives it, and the parts of the node to cut at a chapter.
func encodeMapRows(version *domain.Version, built domain.MapRows, view string) (StoredMapRows, error) {
	rows := StoredMapRows{Keys: versionCardKeys(version), View: view, Revision: built.Document.Revision, Names: map[string]string{}, Nodes: make([]StoredMapRow, 0, len(built.Nodes))}
	for name, node := range built.Names {
		if len(name) <= maxMapKeyBytes && len(node) <= maxMapKeyBytes {
			rows.Names[name] = node
		}
	}
	var err error
	if rows.Document, err = domain.EncodeStored(built.Document); err != nil {
		return StoredMapRows{}, err
	}
	if rows.Chapters, err = domain.EncodeStored(built.Document.Chapters); err != nil {
		return StoredMapRows{}, err
	}
	for i := range built.Nodes {
		node := &built.Nodes[i]
		if len(node.Key) > maxMapKeyBytes {
			continue
		}
		row := StoredMapRow{Key: node.Key, Name: node.Name, Type: node.Type, ChapterCount: len(node.Chapters), SearchText: node.SearchText}
		if row.Card, err = domain.EncodeStored(node.Card(built.Document.Chapters, -1, domain.MapHidden{})); err != nil {
			return StoredMapRows{}, err
		}
		for _, part := range node.Parts(len(built.Document.Chapters)) {
			body, err := domain.EncodeStored(part.Node)
			if err != nil {
				return StoredMapRows{}, err
			}
			row.Parts = append(row.Parts, StoredMapPart{First: part.First, Body: body})
		}
		rows.Nodes = append(rows.Nodes, row)
	}
	return rows, nil
}

// CurrentMap says what the map of a text version should be built from now, reading the chapter selection and the stamp of the
// cards: the cards themselves stay where they are.
func (saga *Saga) CurrentMap(ctx context.Context, projectID, view string) (domain.MapNow, error) {
	version, err := saga.ChapterVersion(ctx, projectID, view)
	if err != nil {
		return domain.MapNow{}, err
	}
	stamp, err := saga.cards.CardsStamp(ctx, projectID, versionCardKeys(version))
	if err != nil {
		return domain.MapNow{}, err
	}
	return domain.MapNow{
		Revision: mapRevision(version, stamp),
		Coverage: domain.MapCoverage{Chapters: len(version.Chapters), WithCards: int(stamp.Cards)},
	}, nil
}

func versionCardKeys(version *domain.Version) []CardKey {
	keys := make([]CardKey, len(version.Chapters))
	for i, chapter := range version.Chapters {
		keys[i] = CardKey{ChapterID: chapter.ID, ContentHash: chapter.ContentHash}
	}
	return keys
}

// mapMaterials is what the map needs of the records.
func mapMaterials(records []authormaterials.Record) []domain.MapMaterial {
	materials := make([]domain.MapMaterial, len(records))
	for i, record := range records {
		properties := make([]domain.CardMaterialProperty, len(record.Properties))
		for at, property := range record.Properties {
			properties[at] = domain.CardMaterialProperty{Key: property.Key, Value: property.Value}
		}
		materials[i] = domain.MapMaterial{Name: record.Name, Category: materialCategory(record.Kind), Text: materialText(record), Card: domain.CardMaterial{
			ID: record.ID, Name: record.Name, Kind: string(record.Kind), Description: record.Description, Properties: properties,
			Section: record.Section, SourceName: record.SourceName, SourceKind: string(record.SourceKind), Revision: record.Revision,
		}}
	}
	return materials
}

// materialCategory is the category of the map of a kind of record; skills and others are not drawn.
func materialCategory(kind authormaterials.Kind) domain.MapCategory {
	switch kind {
	case authormaterials.KindPlace:
		return domain.MapPlace
	case authormaterials.KindCharacter:
		return domain.MapCharacter
	case authormaterials.KindItem:
		return domain.MapItem
	case authormaterials.KindFaction:
		return domain.MapFaction
	default:
		return ""
	}
}

// materialText is everything a record says, for finding the names it speaks of.
func materialText(record authormaterials.Record) string {
	parts := []string{record.Description}
	for _, property := range record.Properties {
		parts = append(parts, property.Value)
	}
	return strings.Join(parts, "\n")
}
