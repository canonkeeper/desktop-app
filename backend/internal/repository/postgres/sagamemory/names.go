package sagamemory

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Names reads the name registry of a project, nil without one.
func (repository *Repository) Names(ctx context.Context, projectID string) (*domain.NameRegistry, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrInvalid
	}
	body, err := sqlc.New(repository.db.Pool).GetSagaNames(ctx, project)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var registry domain.NameRegistry
	if err := json.Unmarshal(body, &registry); err != nil {
		return nil, fmt.Errorf("saga memory: name registry: %w", err)
	}
	return &registry, nil
}

// NamesDigest reads the digest of the project's registry, empty without one.
func (repository *Repository) NamesDigest(ctx context.Context, projectID string) (string, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return "", service.ErrInvalid
	}
	digest, err := sqlc.New(repository.db.Pool).GetSagaNamesDigest(ctx, project)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return digest, err
}

// SaveNames writes the registry while the Job runs under the claim. The model call behind it takes
// minutes, so the transaction holds the project row against a deletion fence, which locks it for update
// and removes the row, and writes only if every chapter text of the list is still current: a source
// deleted meanwhile leaves no names of it (specs/2026-11-01-index-foundation.md, R5.3).
func (repository *Repository) SaveNames(ctx context.Context, claim jobs.WorkerClaim, names service.StoredNames) error {
	projectID, jobID, err := claimIDs(claim)
	if err != nil {
		return err
	}
	chapterIDs := make([]pgtype.UUID, len(names.Keys))
	hashes := make([][]byte, len(names.Keys))
	for i, key := range names.Keys {
		if chapterIDs[i], err = storedb.ParseUUID(key.ChapterID); err != nil {
			return service.ErrInvalid
		}
		if hashes[i], err = hex.DecodeString(key.ContentHash); err != nil || len(hashes[i]) != len(governance.Digest{}) {
			return service.ErrInvalid
		}
	}
	body, err := json.Marshal(names.Registry)
	if err != nil {
		return err
	}
	return repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		if _, err := queries.LockSagaSearchProject(ctx, projectID); err != nil {
			return claimError(err)
		}
		claimed, err := queries.RecordChapterCardsProgress(ctx, sqlc.RecordChapterCardsProgressParams{
			Progress: 1, ProjectID: projectID, JobID: jobID, ClaimIncarnation: claim.ClaimIncarnation,
		})
		if err != nil {
			return err
		}
		if claimed != 1 {
			return ErrClaimLost
		}
		current, err := queries.CountSagaSearchCurrentTexts(ctx, sqlc.CountSagaSearchCurrentTextsParams{
			ProjectID: projectID, ChapterIds: chapterIDs, ContentHashes: hashes,
		})
		if err != nil {
			return err
		}
		if current != int64(len(names.Keys)) {
			return domain.ErrTextChanged
		}
		return queries.UpsertSagaNames(ctx, sqlc.UpsertSagaNamesParams{
			ProjectID: projectID, Digest: names.Digest, NamesJson: body, Model: names.Model,
		})
	})
}

// QueueLoadedCards queues the cards Job of each indexed text version of a project after a load of
// its cards: the Job finds every card in place and refreshes the name registry
// (specs/2026-11-01-index-foundation.md, R5.6).
func (repository *Repository) QueueLoadedCards(ctx context.Context, projectID string) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.ErrInvalid
	}
	rows, err := repository.db.Pool.Query(ctx, `
		SELECT DISTINCT source_view FROM chapter_text_index_state WHERE project_id = $1 ORDER BY source_view
	`, project)
	if err != nil {
		return err
	}
	views, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, view := range views {
		if err := repository.QueueCards(ctx, service.TextVersion{ProjectID: projectID, View: view}); err != nil {
			return err
		}
	}
	return nil
}
