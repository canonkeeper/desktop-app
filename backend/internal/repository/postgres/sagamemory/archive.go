package sagamemory

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"

	layerdomain "canonkeeper/backend/internal/domain/sagalayer"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// The memory of a saga a saga file brought is written on the transaction that created the saga, without a claim of a Job
// (specs/2026-11-13-desk-sagafile.md SF3.5, SF3.6): the cards under the digests of the chapters it wrote, the registry of
// names and the finished layer of threads as the file has them.

// ErrNotNewSaga is a write of a saga file into a saga that has a registry, a layer or a Job: only the transaction that
// created the saga writes them without a claim.
var ErrNotNewSaga = errors.New("saga memory: the memory of a file goes only into the saga its transaction created")

// ArchiveCard is a card of a saga file for a chapter the open wrote.
type ArchiveCard struct {
	ChapterID   string
	ContentHash string
	Card        domain.Card
	Version     int
	Model       string
	// BuiltFrom is the text the card was built from when it is another one, empty when it is the text of the chapter.
	BuiltFrom string
}

// ArchiveLayer is the finished build of the layer with the ids of the new saga.
type ArchiveLayer struct {
	Digest   string
	Model    string
	Chapters []layerdomain.Stamp
	Books    []layerdomain.Book
	Threads  []layerdomain.Thread
}

// WriteArchiveMemoryInTx writes the memory of a saga file into the saga the transaction created: it refuses a saga that
// has a registry, a layer or a Job.
func WriteArchiveMemoryInTx(ctx context.Context, tx pgx.Tx, projectID string, cards []ArchiveCard, names *domain.NameRegistry,
	namesDigest, namesModel string, layer *ArchiveLayer,
) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return ErrNotNewSaga
	}
	var taken bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM saga_names WHERE project_id = $1)
		    OR EXISTS (SELECT 1 FROM saga_threads WHERE project_id = $1)
		    OR EXISTS (SELECT 1 FROM jobs WHERE project_id = $1)
		    OR EXISTS (SELECT 1 FROM chapter_cards WHERE project_id = $1)
	`, project).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return ErrNotNewSaga
	}
	for _, card := range cards {
		chapter, err := storedb.ParseUUID(card.ChapterID)
		if err != nil {
			return ErrNotNewSaga
		}
		hash, err := hex.DecodeString(card.ContentHash)
		if err != nil {
			return err
		}
		var built []byte
		if card.BuiltFrom != "" {
			if built, err = hex.DecodeString(card.BuiltFrom); err != nil {
				return err
			}
		}
		body, err := json.Marshal(card.Card)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO chapter_cards (project_id, chapter_id, content_hash, card_json, model, version, built_from)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, project, chapter, hash, body, card.Model, card.Version, built); err != nil {
			return err
		}
	}
	if names != nil {
		body, err := json.Marshal(names)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO saga_names (project_id, digest, names_json, model) VALUES ($1, $2, $3, $4)
		`, project, namesDigest, body, namesModel); err != nil {
			return err
		}
	}
	if layer != nil {
		if layer.Chapters == nil {
			layer.Chapters = []layerdomain.Stamp{}
		}
		if layer.Books == nil {
			layer.Books = []layerdomain.Book{}
		}
		if layer.Threads == nil {
			layer.Threads = []layerdomain.Thread{}
		}
		parts := make([][]byte, 3)
		for index, part := range []any{layer.Chapters, layer.Books, layer.Threads} {
			if parts[index], err = json.Marshal(part); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO saga_threads (project_id, digest, chapters_json, books_json, threads_json, model)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, project, layer.Digest, parts[0], parts[1], parts[2], layer.Model); err != nil {
			return err
		}
	}
	return nil
}

// WriteArchiveMapHiddenInTx writes the decisions of the author about the map of a saga file.
func WriteArchiveMapHiddenInTx(ctx context.Context, tx pgx.Tx, projectID string, items []domain.MapHiddenItem) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return ErrNotNewSaga
	}
	for _, item := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO saga_map_hidden (project_id, node_key, other_key, node_name, other_name, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, project, item.NodeKey, item.OtherKey, item.NodeName, item.OtherName, storedb.Timestamptz(item.CreatedAt)); err != nil {
			return err
		}
	}
	return nil
}
