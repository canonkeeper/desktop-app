// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/obsidian"
	"canonkeeper/backend/internal/services/sagareference"
)

// exportView is the text the map of a saga is built of: the published one.
const exportView = "published"

// exportCards are the parts of the cards of the chapters a reference of a chapter says (specs/2026-11-15-desk-obsidian-export.md
// Data «справка главы»), with the mentions the glossary is built of.
var exportCards = sagamemory.CardParts{Summary: true, Mentions: true, Changes: true, Events: true}

// obsidianSagaMap reads what an export writes of a saga by the use cases of the pages of the core (OE1.6, OE2.3): the map
// as the page of the map reads it — the Job of the cards queued by it when the map is behind them —, the card of a node as
// the page shows it, and the chapters of the published text with their cards. The core is not changed.
type obsidianSagaMap struct {
	core  app.Core
	maps  *sagareference.GetMap
	nodes *sagareference.GetMapNode
}

func newObsidianSagaMap(core app.Core) obsidianSagaMap {
	return obsidianSagaMap{
		core:  core,
		maps:  sagareference.NewGetMap(core.SagaStore, core.Saga, core.SagaStore, core.SagaStore),
		nodes: sagareference.NewGetMapNode(core.SagaStore, core.SagaStore),
	}
}

func (reads obsidianSagaMap) Map(ctx context.Context, actorUserID, projectID string) (sagamemory.MapDocument, error) {
	result, err := reads.maps.Execute(ctx, sagareference.MapInput{Scope: sagareference.Scope{ActorUserID: actorUserID, ProjectID: projectID, View: exportView}})
	if err != nil {
		return sagamemory.MapDocument{}, referenceError(err)
	}
	var document sagamemory.MapDocument
	err = sagamemory.DecodeStored(result.Document, &document)
	return document, err
}

func (reads obsidianSagaMap) Node(ctx context.Context, actorUserID, projectID, key string) (sagamemory.MapCardDocument, error) {
	result, err := reads.nodes.Execute(ctx, sagareference.MapNodeInput{
		Scope: sagareference.Scope{ActorUserID: actorUserID, ProjectID: projectID, View: exportView}, Key: key,
	})
	if errors.Is(err, sagareference.ErrNotFound) {
		return sagamemory.MapCardDocument{}, service.ErrNodeGone
	}
	if err != nil {
		return sagamemory.MapCardDocument{}, referenceError(err)
	}
	var card sagamemory.MapCardDocument
	err = sagamemory.DecodeStored(result.Document, &card)
	return card, err
}

func (reads obsidianSagaMap) Chapters(ctx context.Context, projectID string) (*sagamemory.Version, *sagamemory.CardSet, error) {
	outline, err := reads.core.Saga.Outline(ctx, projectID, exportView)
	if err != nil {
		return nil, nil, err
	}
	cards, err := reads.core.Saga.Cards(ctx, outline, 0, -1, exportCards)
	if err != nil {
		return nil, nil, err
	}
	return outline.Version, cards, nil
}

// referenceError is a refusal of the reads of the pages as the bridge says it.
func referenceError(err error) error {
	switch {
	case errors.Is(err, sagareference.ErrNotFound):
		return service.ErrNotFound
	case errors.Is(err, sagareference.ErrDenied):
		return service.ErrForbidden
	}
	return err
}
