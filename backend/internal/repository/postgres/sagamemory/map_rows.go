package sagamemory

import (
	"context"
	"encoding/hex"
	"errors"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// SaveMap replaces the prepared map of a text version while the Job runs under the claim. The build behind it reads the whole
// saga, so the transaction holds the project row against a deletion fence, which locks it for update and removes the rows, and
// writes only if every chapter text of the build is still current: a source deleted meanwhile leaves nothing of it
// (specs/2026-11-04-saga-map.md, R10.4).
func (repository *Repository) SaveMap(ctx context.Context, claim jobs.WorkerClaim, rows service.StoredMapRows) error {
	projectID, jobID, err := claimIDs(claim)
	if err != nil {
		return err
	}
	chapterIDs := make([]pgtype.UUID, len(rows.Keys))
	hashes := make([][]byte, len(rows.Keys))
	for i, key := range rows.Keys {
		if chapterIDs[i], err = storedb.ParseUUID(key.ChapterID); err != nil {
			return service.ErrInvalid
		}
		if hashes[i], err = hex.DecodeString(key.ContentHash); err != nil || len(hashes[i]) != len(governance.Digest{}) {
			return service.ErrInvalid
		}
	}
	return repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		if _, err := queries.LockSagaSearchProject(ctx, projectID); err != nil {
			return claimError(err)
		}
		held, err := queries.ChapterCardsClaimHeld(ctx, sqlc.ChapterCardsClaimHeldParams{ProjectID: projectID, JobID: jobID, ClaimIncarnation: claim.ClaimIncarnation})
		if err != nil {
			return err
		}
		if !held {
			return ErrClaimLost
		}
		current, err := queries.CountSagaSearchCurrentTexts(ctx, sqlc.CountSagaSearchCurrentTextsParams{
			ProjectID: projectID, ChapterIds: chapterIDs, ContentHashes: hashes,
		})
		if err != nil {
			return err
		}
		if current != int64(len(rows.Keys)) {
			return domain.ErrTextChanged
		}
		if err := queries.DeleteSagaMapGraph(ctx, sqlc.DeleteSagaMapGraphParams{ProjectID: projectID, SourceView: rows.View}); err != nil {
			return err
		}
		if err := queries.InsertSagaMapGraph(ctx, sqlc.InsertSagaMapGraphParams{
			ProjectID: projectID, SourceView: rows.View, Revision: rows.Revision, Document: rows.Document, Chapters: rows.Chapters,
		}); err != nil {
			return err
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"saga_map_nodes"}, []string{"project_id", "source_view", "node_key", "name", "type", "chapter_count", "search_text", "card"},
			pgx.CopyFromSlice(len(rows.Nodes), func(i int) ([]any, error) {
				node := &rows.Nodes[i]
				return []any{projectID, rows.View, node.Key, node.Name, node.Type, int32(node.ChapterCount), node.SearchText, node.Card}, nil
			})); err != nil {
			return err
		}
		var parts [][]any
		for _, node := range rows.Nodes {
			for _, part := range node.Parts {
				parts = append(parts, []any{projectID, rows.View, node.Key, int32(part.First), part.Body})
			}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"saga_map_node_parts"}, []string{"project_id", "source_view", "node_key", "first_chapter", "body"}, pgx.CopyFromRows(parts)); err != nil {
			return err
		}
		names := make([][]any, 0, len(rows.Names))
		for name, node := range rows.Names {
			names = append(names, []any{projectID, rows.View, name, node})
		}
		_, err = tx.CopyFrom(ctx, pgx.Identifier{"saga_map_names"}, []string{"project_id", "source_view", "name_key", "node_key"}, pgx.CopyFromRows(names))
		return err
	})
}

// QueueMap queues the cards Job of a version unless one is waiting, at work or between its portions: its last step builds the map.
func (repository *Repository) QueueMap(ctx context.Context, projectID, view string) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.ErrInvalid
	}
	active, err := sqlc.New(repository.db.Pool).ChapterCardsJobActive(ctx, sqlc.ChapterCardsJobActiveParams{ProjectID: project, SourceView: view})
	if err != nil || active {
		return err
	}
	_, err = repository.QueueCards(ctx, service.TextVersion{ProjectID: projectID, View: view})
	return err
}

// StoredMapRevision returns the revision of the prepared map, empty without one.
func (repository *Repository) StoredMapRevision(ctx context.Context, projectID, view string) (string, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return "", service.ErrInvalid
	}
	revision, err := sqlc.New(repository.db.Pool).GetSagaMapGraphRevision(ctx, sqlc.GetSagaMapGraphRevisionParams{ProjectID: project, SourceView: view})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return revision, err
}

// PreparedMap reads the prepared graph by its key; document is nil when the browser holds the revision.
func (repository *Repository) PreparedMap(ctx context.Context, projectID, view string, known []string) (revision string, document []byte, found bool, err error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return "", nil, false, service.ErrInvalid
	}
	if known == nil {
		known = []string{}
	}
	row, err := sqlc.New(repository.db.Pool).GetSagaMapGraph(ctx, sqlc.GetSagaMapGraphParams{Known: known, ProjectID: project, SourceView: view})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	body, _ := row.Document.([]byte)
	return row.Revision, body, true, nil
}

// PreparedCard reads the prepared card of a name.
func (repository *Repository) PreparedCard(ctx context.Context, projectID, view, name string) ([]byte, bool, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, false, service.ErrInvalid
	}
	card, err := sqlc.New(repository.db.Pool).GetSagaMapCard(ctx, sqlc.GetSagaMapCardParams{ProjectID: project, SourceView: view, NameKey: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return card, err == nil, err
}

// PreparedChapters reads the chapters of the prepared map, to find a chapter of the scale.
func (repository *Repository) PreparedChapters(ctx context.Context, projectID, view string) ([]byte, bool, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, false, service.ErrInvalid
	}
	chapters, err := sqlc.New(repository.db.Pool).GetSagaMapChapters(ctx, sqlc.GetSagaMapChaptersParams{ProjectID: project, SourceView: view})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return chapters, err == nil, err
}

// PreparedNodePart reads the part of the node of a name that holds a chapter.
func (repository *Repository) PreparedNodePart(ctx context.Context, projectID, view, name string, chapter int) ([]byte, bool, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, false, service.ErrInvalid
	}
	body, err := sqlc.New(repository.db.Pool).GetSagaMapNodePart(ctx, sqlc.GetSagaMapNodePartParams{Chapter: int32(chapter), ProjectID: project, SourceView: view, NameKey: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return body, err == nil, err
}

// SearchMap finds the entries of the prepared map whose text holds a normalized query, most mentioned first.
func (repository *Repository) SearchMap(ctx context.Context, projectID, view, query string, limit int) ([]domain.MapSearchEntry, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrInvalid
	}
	rows, err := sqlc.New(repository.db.Pool).SearchSagaMapNodes(ctx, sqlc.SearchSagaMapNodesParams{ProjectID: project, SourceView: view, Query: query, RowLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	entries := make([]domain.MapSearchEntry, len(rows))
	for i, row := range rows {
		entries[i] = domain.MapSearchEntry{Key: row.NodeKey, Name: row.Name, Type: row.Type, Material: domain.IsMaterialType(row.Type)}
	}
	return entries, nil
}

// bookView is the one text version of a saga since the single version of 27.10.
const bookView = "published"

// BookNames says what the prepared map of the book calls the given name keys. A record of the author the book does not know has a
// node too: it is no name of the book. A saga whose map is not there yet knows none.
func (repository *Repository) BookNames(ctx context.Context, projectID string, keys []string) (map[string]string, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrInvalid
	}
	rows, err := sqlc.New(repository.db.Pool).ListSagaMapNamesOfNodes(ctx, sqlc.ListSagaMapNamesOfNodesParams{ProjectID: project, SourceView: bookView, NameKeys: keys})
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(rows))
	for _, row := range rows {
		if !domain.IsMaterialType(row.Type) {
			names[row.NameKey] = row.Name
		}
	}
	return names, nil
}
