package sagamemory

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"time"
)

// MapHiddenItem is one decision of the author: a node, or the link of two nodes when OtherKey is set.
type MapHiddenItem struct {
	ID        string
	NodeKey   string
	OtherKey  string
	NodeName  string
	OtherName string
	CreatedAt time.Time
}

// NewMapHidden gathers the decisions of a project.
func NewMapHidden(items []MapHiddenItem) MapHidden {
	hidden := MapHidden{Nodes: map[string]string{}, Links: map[[2]string]string{}}
	for _, item := range items {
		if item.OtherKey == "" {
			hidden.Nodes[item.NodeKey] = item.ID
		} else {
			hidden.Links[MapHiddenPair(item.NodeKey, item.OtherKey)] = item.ID
		}
	}
	return hidden
}

// MapHidden is what the author took off the map of a project (specs/2026-11-04-saga-map.md, R11.3), applied when the prepared
// rows are read: the decision by the key of a node and by the pair of keys of a link, in MapHiddenPair order.
type MapHidden struct {
	Nodes map[string]string
	Links map[[2]string]string
}

// MapHiddenPair orders the keys of a link.
func MapHiddenPair(a, b string) [2]string {
	if b < a {
		a, b = b, a
	}
	return [2]string{a, b}
}

// Empty says the author took nothing off the map.
func (hidden MapHidden) Empty() bool { return len(hidden.Nodes) == 0 && len(hidden.Links) == 0 }

// HidesLink says a card of the node does not print the link to other.
func (hidden MapHidden) HidesLink(node, other string) bool {
	_, gone := hidden.Nodes[other]
	if gone {
		return true
	}
	_, gone = hidden.Links[MapHiddenPair(node, other)]
	return gone
}

// Tag tells one set of decisions from another in the revision of the map: a browser holding the map keeps it until they change.
func (hidden MapHidden) Tag() string {
	if hidden.Empty() {
		return ""
	}
	ids := append(slices.Collect(maps.Values(hidden.Nodes)), slices.Collect(maps.Values(hidden.Links))...)
	slices.Sort(ids)
	digest := sha256.New()
	for _, id := range ids {
		digest.Write([]byte(id))
		digest.Write([]byte{0})
	}
	return "~" + hex.EncodeToString(digest.Sum(nil))[:16]
}

// Hide takes what the author took off the map out of the document of the graph.
func (document *MapDocument) Hide(hidden MapHidden) {
	if hidden.Empty() {
		return
	}
	index := make([]int, len(document.Nodes))
	nodes := make([]MapDocumentNode, 0, len(document.Nodes))
	for i, node := range document.Nodes {
		if _, gone := hidden.Nodes[node.Key]; gone {
			index[i] = -1
			continue
		}
		index[i] = len(nodes)
		nodes = append(nodes, node)
	}
	links := make([]MapDocumentLink, 0, len(document.Links))
	for _, link := range document.Links {
		_, gone := hidden.Links[MapHiddenPair(document.Nodes[link.Source].Key, document.Nodes[link.Target].Key)]
		if gone || index[link.Source] < 0 || index[link.Target] < 0 {
			continue
		}
		link.Source, link.Target = index[link.Source], index[link.Target]
		links = append(links, link)
	}
	document.NodesTotal -= len(document.Nodes) - len(nodes)
	if document.Hero != nil {
		if _, gone := hidden.Nodes[*document.Hero]; gone {
			document.Hero = nil
		}
	}
	document.Nodes, document.Links = nodes, links
}
