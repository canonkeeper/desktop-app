package sagamemory

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	layerdomain "canonkeeper/backend/internal/domain/sagalayer"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	layerservice "canonkeeper/backend/internal/services/sagalayer"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	indexingstore "canonkeeper/backend/internal/store/indexing"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Stamps lists the chapters of a text version in reading order with what their cards were built from:
// the chapter text, the text a carried card was built from and the version of the instructions; a chapter
// without a card of its text has 0.
func (repository *Repository) Stamps(ctx context.Context, projectID, view string) ([]layerdomain.Stamp, error) {
	selection, err := repository.Selection(ctx, projectID, view)
	if err != nil {
		return nil, err
	}
	chapters := selection.Chapters
	keys := make([]service.CardKey, len(chapters))
	for i, chapter := range chapters {
		keys[i] = service.CardKey{ChapterID: chapter.ID, ContentHash: chapter.ContentHash}
	}
	project, chapterIDs, err := cardQueryArgs(projectID, keys)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(repository.db.Pool).ListChapterCardStamps(ctx, sqlc.ListChapterCardStampsParams{ProjectID: project, ChapterIds: chapterIDs})
	if err != nil {
		return nil, err
	}
	type built struct {
		version int
		from    string
	}
	cards := make(map[service.CardKey]built, len(rows))
	for _, row := range rows {
		cards[service.CardKey{ChapterID: storedb.UUIDString(row.ChapterID), ContentHash: hex.EncodeToString(row.ContentHash)}] = built{
			version: int(row.Version), from: hex.EncodeToString(row.BuiltFrom),
		}
	}
	stamps := make([]layerdomain.Stamp, len(chapters))
	for i, key := range keys {
		card := cards[key]
		stamps[i] = layerdomain.Stamp{Chapter: key.ChapterID, Hash: key.ContentHash, CardVersion: card.version}
		// A card carried over a change of signs names the text it was built from (specs/2026-10-27-update-book-from-file.md, R3.3).
		if card.version > 0 && card.from != key.ContentHash {
			stamps[i].Card = card.from
		}
	}
	return stamps, nil
}

func decodeLayerPart(body []byte, target any) error {
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("saga layer: stored build: %w", err)
	}
	return nil
}

// StoredLayer reads the row of a project with the checkpoint, nil without a row.
func (repository *Repository) StoredLayer(ctx context.Context, projectID string) (*layerdomain.Stored, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrInvalid
	}
	row, err := sqlc.New(repository.db.Pool).GetSagaThreadBuild(ctx, project)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	stored := &layerdomain.Stored{Digest: row.Digest.String}
	for _, part := range []struct {
		body   []byte
		target any
	}{
		{row.ChaptersJson, &stored.Layer.Chapters}, {row.BooksJson, &stored.Layer.Books},
		{row.ThreadsJson, &stored.Layer.Threads}, {row.CheckpointBooksJson, &stored.CheckpointBooks},
		{row.CheckpointJson, &stored.Steps},
	} {
		if err := decodeLayerPart(part.body, part.target); err != nil {
			return nil, err
		}
	}
	return stored, nil
}

// BuiltChapters reads what the finished build was built from, nil without one.
func (repository *Repository) BuiltChapters(ctx context.Context, projectID string) ([]layerdomain.Stamp, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrInvalid
	}
	body, err := sqlc.New(repository.db.Pool).GetSagaThreadChapters(ctx, project)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var chapters []layerdomain.Stamp
	return chapters, decodeLayerPart(body, &chapters)
}

// SagaThreads reads the finished build for the chat tool, nil without one: a build under way stays
// out of it. Without withBooks it leaves the books out, the larger part of it.
func (repository *Repository) SagaThreads(ctx context.Context, projectID string, withBooks bool) (*layerdomain.Layer, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrInvalid
	}
	queries := sqlc.New(repository.db.Pool)
	var chapters, threads, books []byte
	if withBooks {
		row, err := queries.GetSagaThreadsWithBooks(ctx, project)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		chapters, threads, books = row.ChaptersJson, row.ThreadsJson, row.BooksJson
	} else {
		row, err := queries.GetSagaThreads(ctx, project)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		chapters, threads = row.ChaptersJson, row.ThreadsJson
	}
	layer := &layerdomain.Layer{}
	for _, part := range []struct {
		body   []byte
		target any
	}{{chapters, &layer.Chapters}, {threads, &layer.Threads}, {books, &layer.Books}} {
		if err := decodeLayerPart(part.body, part.target); err != nil {
			return nil, err
		}
	}
	return layer, nil
}

// requireCurrentTexts refuses a write when a chapter text of the build is gone: a source deleted
// during the calls leaves nothing of it behind (R2.8). The caller holds the project row against the
// deletion fence, which locks it for update and removes the row of the layer.
func requireCurrentTexts(ctx context.Context, queries *sqlc.Queries, projectID pgtype.UUID, chapters []layerdomain.Stamp) error {
	chapterIDs := make([]pgtype.UUID, len(chapters))
	hashes := make([][]byte, len(chapters))
	var err error
	for i, chapter := range chapters {
		if chapterIDs[i], err = storedb.ParseUUID(chapter.Chapter); err != nil {
			return service.ErrInvalid
		}
		if hashes[i], err = hex.DecodeString(chapter.Hash); err != nil || len(hashes[i]) != len(governance.Digest{}) {
			return service.ErrInvalid
		}
	}
	current, err := queries.CountSagaSearchCurrentTexts(ctx, sqlc.CountSagaSearchCurrentTextsParams{
		ProjectID: projectID, ChapterIds: chapterIDs, ContentHashes: hashes,
	})
	if err != nil {
		return err
	}
	if current != int64(len(chapters)) {
		return domain.ErrTextChanged
	}
	return nil
}

// SaveCheckpoint writes one step of a build while the Job runs under the claim and the chapter texts
// are current. Books go to a column of their own, so a verdict does not rewrite them.
func (repository *Repository) SaveCheckpoint(ctx context.Context, claim jobs.WorkerClaim, chapters []layerdomain.Stamp, checkpoint layerservice.Checkpoint, progress float64) error {
	projectID, jobID, err := claimIDs(claim)
	if err != nil {
		return err
	}
	var books []byte
	if checkpoint.Books != nil {
		if books, err = json.Marshal(checkpoint.Books); err != nil {
			return err
		}
	}
	steps, err := json.Marshal(checkpoint.Steps)
	if err != nil {
		return err
	}
	return repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		if _, err := queries.LockSagaSearchProject(ctx, projectID); err != nil {
			return claimError(err)
		}
		claimed, err := queries.RecordSagaLayerProgress(ctx, sqlc.RecordSagaLayerProgressParams{
			Progress: progress, ProjectID: projectID, JobID: jobID, ClaimIncarnation: claim.ClaimIncarnation,
		})
		if err != nil {
			return err
		}
		if claimed != 1 {
			return ErrClaimLost
		}
		if err := requireCurrentTexts(ctx, queries, projectID, chapters); err != nil {
			return err
		}
		return queries.SaveSagaThreadsCheckpoint(ctx, sqlc.SaveSagaThreadsCheckpointParams{
			ProjectID: projectID, Model: checkpoint.Model, CheckpointBooksJson: books, CheckpointJson: steps,
		})
	})
}

// FinishLayer commits the terminal state of the layer Job and, in the same transaction, a new build.
func (repository *Repository) FinishLayer(ctx context.Context, claim jobs.WorkerClaim, outcome layerservice.LayerOutcome) error {
	if outcome.Subject.ProjectID != claim.ProjectID {
		return layerservice.ErrInvalid
	}
	var resultJSON, errorJSON []byte
	var err error
	if outcome.Result != nil {
		if resultJSON, err = jobs.EncodeResult(jobs.KindSagaLayer, *outcome.Result); err != nil {
			return layerservice.ErrInvalid
		}
	}
	if outcome.Error != nil {
		if errorJSON, err = jobs.EncodeError(*outcome.Error); err != nil {
			return layerservice.ErrInvalid
		}
	}
	end := claimedJobEnd{
		claim: claim, kind: jobs.KindSagaLayer, view: outcome.Subject.SourceView, status: outcome.Status,
		resultJSON: resultJSON, errorJSON: errorJSON,
	}
	if outcome.Layer != nil {
		chapters, err := json.Marshal(outcome.Layer.Chapters)
		if err != nil {
			return err
		}
		books, err := json.Marshal(outcome.Layer.Books)
		if err != nil {
			return err
		}
		threads, err := json.Marshal(outcome.Layer.Threads)
		if err != nil {
			return err
		}
		end.inTx = func(tx pgx.Tx, projectID pgtype.UUID) error {
			queries := sqlc.New(tx)
			if err := requireCurrentTexts(ctx, queries, projectID, outcome.Layer.Chapters); err != nil {
				return err
			}
			return queries.FinishSagaThreads(ctx, sqlc.FinishSagaThreadsParams{
				ProjectID: projectID, Digest: outcome.Digest, ChaptersJson: chapters, BooksJson: books,
				ThreadsJson: threads, Model: outcome.Model,
			})
		}
	}
	return repository.endClaimedJob(ctx, end)
}

// QueueLayer queues the layer Job of a project through the text index owner, as the cards Job.
func (repository *Repository) QueueLayer(ctx context.Context, projectID, view string) error {
	return indexingstore.New(repository.db).QueueSagaLayer(ctx, projectID, governance.SourceView(view))
}

// LayerVersions lists the text versions the start looks at: those the cards Job builds.
func (repository *Repository) LayerVersions(ctx context.Context) ([]layerservice.TextVersion, error) {
	versions, err := repository.IndexedVersions(ctx)
	if err != nil {
		return nil, err
	}
	layers := make([]layerservice.TextVersion, len(versions))
	for i, version := range versions {
		layers[i] = layerservice.TextVersion{ProjectID: version.ProjectID, View: version.View}
	}
	return layers, nil
}
