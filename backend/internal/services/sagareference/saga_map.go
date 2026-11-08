package sagareference

import (
	"context"
	"slices"
	"strings"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// buildingTag marks the revision of a map whose cards have moved on: the page asks again until the new one is there, and a
// browser holding the map from before the change learns of it.
const buildingTag = "+building"

// PreparedMaps reads the prepared rows of the map by key (specs/2026-11-04-saga-map.md, UC10). The documents are gzip of JSON.
type PreparedMaps interface {
	PreparedMap(ctx context.Context, projectID, view string, known []string) (revision string, document []byte, found bool, err error)
	PreparedCard(ctx context.Context, projectID, view, name string) (card []byte, found bool, err error)
	PreparedChapters(ctx context.Context, projectID, view string) (chapters []byte, found bool, err error)
	PreparedNodePart(ctx context.Context, projectID, view, name string, chapter int) (body []byte, found bool, err error)
	SearchMap(ctx context.Context, projectID, view, query string, limit int) ([]sagamemory.MapSearchEntry, error)
	MapHiddenItems(ctx context.Context, projectID string) ([]sagamemory.MapHiddenItem, error)
}

// MapCards says what the cards of a text version are now, without reading them.
type MapCards interface {
	CurrentMap(ctx context.Context, projectID, view string) (sagamemory.MapNow, error)
}

// MapBuilds asks for a new build of the map: the cards Job of the version, unless one is at work.
type MapBuilds interface {
	QueueMap(ctx context.Context, projectID, view string) error
}

// MapInput names the text version and the revisions of the map the browser holds.
type MapInput struct {
	Scope
	Known []string
}

// MapResult is the prepared document of the map; NotModified when the browser holds it, and no Revision when there is no map
// to keep yet.
type MapResult struct {
	Revision    string
	NotModified bool
	Document    []byte
}

// GetMap serves the graph of the saga (UC1, UC10): the stored document, as it is, without what the author took off it (R11.3). The
// revision of the cards says whether it is current; a map behind its cards goes out marked as being rebuilt, a saga with cards and
// no map asks for it and says so. No card of a chapter is read.
type GetMap struct {
	access Access
	cards  MapCards
	maps   PreparedMaps
	builds MapBuilds
}

func NewGetMap(access Access, cards MapCards, maps PreparedMaps, builds MapBuilds) *GetMap {
	if access == nil || cards == nil || maps == nil || builds == nil {
		panic("saga reference: map dependencies are required")
	}
	return &GetMap{access: access, cards: cards, maps: maps, builds: builds}
}

func (useCase *GetMap) Execute(ctx context.Context, input MapInput) (MapResult, error) {
	scope, err := readable(ctx, useCase.access, input.Scope)
	if err != nil {
		return MapResult{}, err
	}
	now, err := useCase.cards.CurrentMap(ctx, scope.ProjectID, scope.View)
	if err != nil {
		return MapResult{}, err
	}
	if now.Coverage.WithCards == 0 {
		return emptyMap(now.Coverage, false)
	}
	items, err := useCase.maps.MapHiddenItems(ctx, scope.ProjectID)
	if err != nil {
		return MapResult{}, err
	}
	hidden := sagamemory.NewMapHidden(items)
	// The stored document is left out only for a browser holding the current map with the current decisions.
	var known []string
	if slices.Contains(input.Known, now.Revision+hidden.Tag()) {
		known = []string{now.Revision}
	}
	stored, document, found, err := useCase.maps.PreparedMap(ctx, scope.ProjectID, scope.View, known)
	if err != nil {
		return MapResult{}, err
	}
	if found && stored == now.Revision {
		if known == nil && !hidden.Empty() {
			if document, err = withoutHidden(document, hidden); err != nil {
				return MapResult{}, err
			}
		}
		return MapResult{Revision: stored + hidden.Tag(), NotModified: known != nil, Document: document}, nil
	}
	if err := useCase.builds.QueueMap(ctx, scope.ProjectID, scope.View); err != nil {
		return MapResult{}, err
	}
	if !found {
		return emptyMap(now.Coverage, true)
	}
	tag := stored + hidden.Tag() + buildingTag
	if slices.Contains(input.Known, tag) {
		return MapResult{Revision: tag, NotModified: true}, nil
	}
	var behind sagamemory.MapDocument
	if err := sagamemory.DecodeStored(document, &behind); err != nil {
		return MapResult{}, err
	}
	behind.Building = true
	behind.Hide(hidden)
	if document, err = sagamemory.EncodeStored(behind); err != nil {
		return MapResult{}, err
	}
	return MapResult{Revision: tag, Document: document}, nil
}

// withoutHidden takes the decisions of the author out of a stored document.
func withoutHidden(stored []byte, hidden sagamemory.MapHidden) ([]byte, error) {
	var document sagamemory.MapDocument
	if err := sagamemory.DecodeStored(stored, &document); err != nil {
		return nil, err
	}
	document.Hide(hidden)
	return sagamemory.EncodeStored(document)
}

// emptyMap is the answer without a prepared map: the coverage of the saga and, when asked for, the word that it is coming.
func emptyMap(coverage sagamemory.MapCoverage, building bool) (MapResult, error) {
	document, err := sagamemory.EncodeStored(sagamemory.MapDocument{
		Coverage: coverage, Chapters: []sagamemory.MapChapterRef{}, Nodes: []sagamemory.MapDocumentNode{}, Links: []sagamemory.MapDocumentLink{}, Building: building,
	})
	return MapResult{Document: document}, err
}

// MapNodeInput names an entry by its key, name or alias and the chapter key the map shows; empty for
// the whole saga.
type MapNodeInput struct {
	Scope
	Key string
	To  string
}

// MapNodeResult is the card of a node, gzip of JSON.
type MapNodeResult struct {
	Document []byte
}

// GetMapNode serves the card of a node (UC2, UC10): the card of the whole saga is stored as it is served, one read by key; at a
// chapter of the scale, or when the author took something off the map (R11.3), it is cut from the part of the node holding that
// chapter, found by the chapters of the map.
type GetMapNode struct {
	access Access
	maps   PreparedMaps
}

func NewGetMapNode(access Access, maps PreparedMaps) *GetMapNode {
	if access == nil || maps == nil {
		panic("saga reference: map node dependencies are required")
	}
	return &GetMapNode{access: access, maps: maps}
}

func (useCase *GetMapNode) Execute(ctx context.Context, input MapNodeInput) (MapNodeResult, error) {
	if strings.TrimSpace(input.Key) == "" || !validText(input.Key, maxKeyRunes) || !validText(input.To, maxKeyRunes) {
		return MapNodeResult{}, ErrInvalid
	}
	scope, err := readable(ctx, useCase.access, input.Scope)
	if err != nil {
		return MapNodeResult{}, err
	}
	name := sagamemory.GlossaryKey(input.Key)
	to := strings.TrimSpace(input.To)
	items, err := useCase.maps.MapHiddenItems(ctx, scope.ProjectID)
	if err != nil {
		return MapNodeResult{}, err
	}
	hidden := sagamemory.NewMapHidden(items)
	if to == "" && hidden.Empty() {
		card, found, err := useCase.maps.PreparedCard(ctx, scope.ProjectID, scope.View, name)
		if err != nil {
			return MapNodeResult{}, err
		}
		if !found {
			return MapNodeResult{}, ErrNotFound
		}
		return MapNodeResult{Document: card}, nil
	}
	stored, found, err := useCase.maps.PreparedChapters(ctx, scope.ProjectID, scope.View)
	if err != nil {
		return MapNodeResult{}, err
	}
	if !found {
		return MapNodeResult{}, ErrNotFound
	}
	var chapters []sagamemory.MapChapterRef
	if err := sagamemory.DecodeStored(stored, &chapters); err != nil {
		return MapNodeResult{}, err
	}
	// The whole saga is the part of the last chapter, not cut.
	index, cut := len(chapters)-1, -1
	if to != "" {
		index = slices.IndexFunc(chapters, func(chapter sagamemory.MapChapterRef) bool { return chapter.Key == to })
		if index < 0 {
			return MapNodeResult{}, ErrInvalid
		}
		cut = index
	}
	body, found, err := useCase.maps.PreparedNodePart(ctx, scope.ProjectID, scope.View, name, max(index, 0))
	if err != nil {
		return MapNodeResult{}, err
	}
	if !found {
		return MapNodeResult{}, ErrNotFound
	}
	var node sagamemory.StoredNode
	if err := sagamemory.DecodeStored(body, &node); err != nil {
		return MapNodeResult{}, err
	}
	document, err := sagamemory.EncodeStored(node.Card(chapters, cut, hidden))
	return MapNodeResult{Document: document}, err
}

const (
	defaultMapSearchLimit = 8
	maxMapSearchLimit     = 50
)

// MapSearchInput is a search of the map: a query over names, aliases, roles and notes; an empty one gives the most mentioned entries.
type MapSearchInput struct {
	Scope
	Query string
	Limit int
}

// SearchMap finds entries of the saga, on the map or off it (R3.3, R10.6): one read of the prepared rows, in the order the glossary
// gives. A saga whose map is not there yet finds nothing, and its page has no search to ask with.
type SearchMap struct {
	access Access
	maps   PreparedMaps
}

func NewSearchMap(access Access, maps PreparedMaps) *SearchMap {
	if access == nil || maps == nil {
		panic("saga reference: map search dependencies are required")
	}
	return &SearchMap{access: access, maps: maps}
}

func (useCase *SearchMap) Execute(ctx context.Context, input MapSearchInput) ([]sagamemory.MapSearchEntry, error) {
	if !validText(input.Query, maxQueryRunes) || input.Limit < 0 || input.Limit > maxMapSearchLimit {
		return nil, ErrInvalid
	}
	if input.Limit == 0 {
		input.Limit = defaultMapSearchLimit
	}
	scope, err := readable(ctx, useCase.access, input.Scope)
	if err != nil {
		return nil, err
	}
	query := sagamemory.GlossaryKey(input.Query)
	if query == "" && strings.TrimSpace(input.Query) != "" {
		return []sagamemory.MapSearchEntry{}, nil
	}
	return useCase.maps.SearchMap(ctx, scope.ProjectID, scope.View, query, input.Limit)
}
