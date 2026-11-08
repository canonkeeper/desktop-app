package sagamemorytest

import (
	"context"
	"slices"
	"strings"

	"canonkeeper/backend/internal/domain/authormaterials"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
)

// Materials is a list of the author's records, as the map builder reads them.
type Materials []authormaterials.Record

func (materials Materials) AuthorMaterials(context.Context, string) ([]authormaterials.Record, error) {
	return materials, nil
}

// Maps keeps prepared maps in memory and answers the reads by key like the PostgreSQL queries.
type Maps struct {
	rows map[string]service.StoredMapRows
	// Queued counts the builds asked for.
	Queued int
	// Reads counts the reads of the prepared rows.
	Reads int
	// Hidden is what the author took off the map.
	Hidden []domain.MapHiddenItem
}

func (maps *Maps) MapHiddenItems(context.Context, string) ([]domain.MapHiddenItem, error) {
	return maps.Hidden, nil
}

func NewMaps() *Maps { return &Maps{rows: map[string]service.StoredMapRows{}} }

func (maps *Maps) SaveMap(_ context.Context, claim jobs.WorkerClaim, rows service.StoredMapRows) error {
	maps.rows[claim.ProjectID+"\x00"+rows.View] = rows
	return nil
}

// Forget removes the prepared map of a version, as a source deletion does.
func (maps *Maps) Forget(projectID, view string) { delete(maps.rows, projectID+"\x00"+view) }

func (maps *Maps) QueueMap(context.Context, string, string) error {
	maps.Queued++
	return nil
}

func (maps *Maps) PreparedMap(_ context.Context, projectID, view string, known []string) (string, []byte, bool, error) {
	maps.Reads++
	rows, ok := maps.rows[projectID+"\x00"+view]
	if !ok {
		return "", nil, false, nil
	}
	if slices.Contains(known, rows.Revision) {
		return rows.Revision, nil, true, nil
	}
	return rows.Revision, rows.Document, true, nil
}

func (maps *Maps) node(projectID, view, name string) (service.StoredMapRows, service.StoredMapRow, bool) {
	maps.Reads++
	rows, ok := maps.rows[projectID+"\x00"+view]
	if !ok {
		return rows, service.StoredMapRow{}, false
	}
	key, ok := rows.Names[name]
	if !ok {
		return rows, service.StoredMapRow{}, false
	}
	index := slices.IndexFunc(rows.Nodes, func(node service.StoredMapRow) bool { return node.Key == key })
	if index < 0 {
		return rows, service.StoredMapRow{}, false
	}
	return rows, rows.Nodes[index], true
}

func (maps *Maps) PreparedCard(_ context.Context, projectID, view, name string) ([]byte, bool, error) {
	_, node, ok := maps.node(projectID, view, name)
	return node.Card, ok, nil
}

func (maps *Maps) PreparedChapters(_ context.Context, projectID, view string) ([]byte, bool, error) {
	maps.Reads++
	rows, ok := maps.rows[projectID+"\x00"+view]
	return rows.Chapters, ok, nil
}

func (maps *Maps) PreparedNodePart(_ context.Context, projectID, view, name string, chapter int) ([]byte, bool, error) {
	_, node, ok := maps.node(projectID, view, name)
	if !ok {
		return nil, false, nil
	}
	var body []byte
	for _, part := range node.Parts {
		if part.First <= chapter {
			body = part.Body
		}
	}
	return body, true, nil
}

func (maps *Maps) SearchMap(_ context.Context, projectID, view, query string, limit int) ([]domain.MapSearchEntry, error) {
	maps.Reads++
	var found []service.StoredMapRow
	for _, node := range maps.rows[projectID+"\x00"+view].Nodes {
		if strings.Contains(node.SearchText, query) {
			found = append(found, node)
		}
	}
	slices.SortStableFunc(found, func(a, b service.StoredMapRow) int {
		if a.ChapterCount != b.ChapterCount {
			return b.ChapterCount - a.ChapterCount
		}
		return strings.Compare(a.Name, b.Name)
	})
	entries := []domain.MapSearchEntry{}
	for _, node := range found[:min(len(found), limit)] {
		entries = append(entries, domain.MapSearchEntry{Key: node.Key, Name: node.Name, Type: node.Type, Material: domain.IsMaterialType(node.Type)})
	}
	return entries, nil
}
