// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"errors"
	"testing"

	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"

	"github.com/jackc/pgx/v5"
)

// specs/2026-11-13-desk-sagafile.md SF1.5: a layer with a finished build goes into the file without the checkpoint of a build
// under way; a layer without a finished build does not go.
func TestTheFileCarriesAFinishedLayerOnly(t *testing.T) {
	world := newWorld(t, "ck_saga_file_layer")
	opened := world.mustOpen(northernLimit(t, fixtureOptions{}), "layer-key")
	if _, err := world.db.Pool.Exec(world.ctx, `
		UPDATE saga_threads SET checkpoint_json = '{"digest":"under way"}', checkpoint_books_json = '[{"id":"x","order":1,"digest":"y"}]'
		WHERE project_id = $1
	`, opened.ProjectID); err != nil {
		t.Fatal(err)
	}
	archive := read(t, world.save(opened.ProjectID))
	if archive.Threads == nil || len(archive.Threads.Stamps) != 6 || len(archive.Threads.Books) != 3 {
		t.Fatalf("the layer of the file = %+v", archive.Threads)
	}
	if _, err := world.db.Pool.Exec(world.ctx, `UPDATE saga_threads SET digest = NULL WHERE project_id = $1`, opened.ProjectID); err != nil {
		t.Fatal(err)
	}
	if archive := read(t, world.save(opened.ProjectID)); archive.Threads != nil {
		t.Fatalf("a layer without a finished build went into the file: %+v", archive.Threads)
	}
}

// SF3.6: the memory of a file is written without a claim only into the saga the transaction created: a saga that has a
// registry, a layer or a Job is refused.
func TestTheMemoryOfAFileGoesOnlyIntoANewSaga(t *testing.T) {
	world := newWorld(t, "ck_saga_file_new_only")
	opened := world.mustOpen(northernLimit(t, fixtureOptions{}), "new-only-key")
	err := world.db.WithTx(world.ctx, func(tx pgx.Tx) error {
		return sagamemoryrepo.WriteArchiveMemoryInTx(world.ctx, tx, opened.ProjectID, nil, nil, "", "", nil)
	})
	if !errors.Is(err, sagamemoryrepo.ErrNotNewSaga) {
		t.Fatalf("the memory of a file into a saga with its memory = %v; want a refusal", err)
	}
}
