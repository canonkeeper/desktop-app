package sagareference

import (
	"context"
	"strings"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// MapHiddenStore keeps what the author took off the map (specs/2026-11-04-saga-map.md, R11.3). A write runs under the write proof
// of the session, for an editor or above: the store checks both in the transaction of the write.
type MapHiddenStore interface {
	HideMapItem(ctx context.Context, write MapWrite, item sagamemory.MapHiddenItem) (sagamemory.MapHiddenItem, error)
	RestoreMapItem(ctx context.Context, write MapWrite, id string) error
}

// MapWrite is a decision of the author about the map.
type MapWrite struct {
	Scope
	Proof auth.SessionWriteProof
}

// ListMapHidden is listSagaMapHidden: the list «Убранное», newest first.
type ListMapHidden struct {
	access Access
	maps   PreparedMaps
}

func NewListMapHidden(access Access, maps PreparedMaps) *ListMapHidden {
	if access == nil || maps == nil {
		panic("saga reference: map hidden dependencies are required")
	}
	return &ListMapHidden{access: access, maps: maps}
}

func (useCase *ListMapHidden) Execute(ctx context.Context, input Scope) ([]sagamemory.MapHiddenItem, error) {
	scope, err := readable(ctx, useCase.access, input)
	if err != nil {
		return nil, err
	}
	return useCase.maps.MapHiddenItems(ctx, scope.ProjectID)
}

// HideInput names a node by its key and, for a link, the other end.
type HideInput struct {
	NodeKey  string
	OtherKey string
}

// HideOnMap is hideSagaMapItem: «Убрать с карты» of a node and «Это не связь» of a link. The names come from the prepared rows, so
// a decision is about what the saga knows, and hiding it twice is one decision.
type HideOnMap struct {
	access Access
	maps   PreparedMaps
	store  MapHiddenStore
}

func NewHideOnMap(access Access, maps PreparedMaps, store MapHiddenStore) *HideOnMap {
	if access == nil || maps == nil || store == nil {
		panic("saga reference: map hide dependencies are required")
	}
	return &HideOnMap{access: access, maps: maps, store: store}
}

func (useCase *HideOnMap) Execute(ctx context.Context, write MapWrite, input HideInput) (sagamemory.MapHiddenItem, error) {
	if !write.Proof.Valid() || strings.TrimSpace(input.NodeKey) == "" || !validText(input.NodeKey, maxKeyRunes) || !validText(input.OtherKey, maxKeyRunes) {
		return sagamemory.MapHiddenItem{}, ErrInvalid
	}
	scope, err := readable(ctx, useCase.access, write.Scope)
	if err != nil {
		return sagamemory.MapHiddenItem{}, err
	}
	node, err := useCase.preparedNode(ctx, scope, input.NodeKey)
	if err != nil {
		return sagamemory.MapHiddenItem{}, err
	}
	item := sagamemory.MapHiddenItem{NodeKey: node.Key, NodeName: node.Name}
	if strings.TrimSpace(input.OtherKey) != "" {
		other, err := useCase.preparedNode(ctx, scope, input.OtherKey)
		if err != nil {
			return sagamemory.MapHiddenItem{}, err
		}
		if other.Key == node.Key {
			return sagamemory.MapHiddenItem{}, ErrInvalid
		}
		item.OtherKey, item.OtherName = other.Key, other.Name
		if pair := sagamemory.MapHiddenPair(node.Key, other.Key); pair[0] != node.Key {
			item.NodeKey, item.NodeName, item.OtherKey, item.OtherName = other.Key, other.Name, node.Key, node.Name
		}
	}
	write.Scope = scope
	return useCase.store.HideMapItem(ctx, write, item)
}

func (useCase *HideOnMap) preparedNode(ctx context.Context, scope Scope, key string) (sagamemory.MapCardDocument, error) {
	stored, found, err := useCase.maps.PreparedCard(ctx, scope.ProjectID, scope.View, sagamemory.GlossaryKey(key))
	if err != nil {
		return sagamemory.MapCardDocument{}, err
	}
	if !found {
		return sagamemory.MapCardDocument{}, ErrNotFound
	}
	var card sagamemory.MapCardDocument
	err = sagamemory.DecodeStored(stored, &card)
	return card, err
}

// RestoreOnMap is restoreSagaMapItem: «Отменить» and «Вернуть на карту».
type RestoreOnMap struct {
	store MapHiddenStore
}

func NewRestoreOnMap(store MapHiddenStore) *RestoreOnMap {
	if store == nil {
		panic("saga reference: map restore dependencies are required")
	}
	return &RestoreOnMap{store: store}
}

func (useCase *RestoreOnMap) Execute(ctx context.Context, write MapWrite, id string) error {
	if !write.Proof.Valid() || strings.TrimSpace(id) == "" {
		return ErrInvalid
	}
	return useCase.store.RestoreMapItem(ctx, write, strings.TrimSpace(id))
}
