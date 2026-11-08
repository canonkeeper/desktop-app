// Package authormaterials is the PostgreSQL authority of the materials of the author (specs/2026-11-04-saga-map.md, UC9).
// Every read and write names the project; the web writes run behind the session's write proof for an editor or above.
package authormaterials

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	domain "canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	service "canonkeeper/backend/internal/services/authormaterials"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	quotastore "canonkeeper/backend/internal/store/quota"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	_ service.ListStore    = (*Repository)(nil)
	_ service.UpdateStore  = (*Repository)(nil)
	_ service.DeleteStore  = (*Repository)(nil)
	_ service.StartStore   = (*Repository)(nil)
	_ service.ImportStore  = (*Repository)(nil)
	_ service.ConfirmStore = (*Repository)(nil)
	_ service.DiscardStore = (*Repository)(nil)
	_ service.RunStore     = (*Repository)(nil)
)

// maxReadRecords bounds what a reader of all records of a project takes at once.
const maxReadRecords = 5000

type Repository struct {
	db   *storedb.DB
	jobs *jobstore.Store
}

func New(db *storedb.DB, jobs *jobstore.Store) *Repository { return &Repository{db: db, jobs: jobs} }

func projectAndActor(projectID, actorUserID string) (pgtype.UUID, pgtype.UUID, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, domain.ErrNotFound
	}
	actor, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, domain.ErrNotFound
	}
	return project, actor, nil
}

func roleInTx(ctx context.Context, queries *sqlc.Queries, project, actor pgtype.UUID) (projects.Role, error) {
	role, err := queries.GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{ActorUserID: actor, ProjectID: project})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return projects.Role(role), err
}

func propertiesJSON(properties []domain.Property) ([]byte, error) {
	if properties == nil {
		properties = []domain.Property{}
	}
	return json.Marshal(properties)
}

func recordOf(row sqlc.GetAuthorMaterialRow) (domain.Record, error) {
	record := domain.Record{
		ID: storedb.UUIDString(row.ID), ProjectID: storedb.UUIDString(row.ProjectID), SourceName: row.SourceName,
		SourceKind: domain.SourceKind(row.SourceKind), Revision: row.Revision,
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
		Draft: domain.Draft{Name: row.Name, Kind: domain.Kind(row.Kind), Description: row.Description, Section: row.Section},
	}
	if err := json.Unmarshal(row.Properties, &record.Properties); err != nil {
		return domain.Record{}, err
	}
	return record, nil
}

// ListAuthorMaterials lists the records for a member of the project; any role reads.
func (repository *Repository) ListAuthorMaterials(ctx context.Context, actor service.Actor, filter domain.Filter) (domain.Page, error) {
	project, user, err := projectAndActor(actor.ProjectID, actor.UserID)
	if err != nil {
		return domain.Page{}, err
	}
	var page domain.Page
	err = repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		if _, err := roleInTx(ctx, queries, project, user); err != nil {
			return err
		}
		params := sqlc.ListAuthorMaterialsParams{
			ProjectID: project, Kinds: make([]string, len(filter.Kinds)), SearchQuery: filter.Query, AfterKey: filter.AfterKey, LimitCount: int32(filter.Limit + 1),
		}
		for index, kind := range filter.Kinds {
			params.Kinds[index] = string(kind)
		}
		rows, err := queries.ListAuthorMaterials(ctx, params)
		if err != nil {
			return err
		}
		keys := make([]string, 0, len(rows))
		page.Records = make([]domain.Record, 0, len(rows))
		for _, row := range rows {
			page.Total = row.TotalCount
			record, err := recordOf(sqlc.GetAuthorMaterialRow{
				ID: row.ID, ProjectID: row.ProjectID, Name: row.Name, NameKey: row.NameKey, Kind: row.Kind, Description: row.Description,
				Properties: row.Properties, Section: row.Section, SourceName: row.SourceName, SourceKind: row.SourceKind,
				Revision: row.Revision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			})
			if err != nil {
				return err
			}
			page.Records, keys = append(page.Records, record), append(keys, row.NameKey)
		}
		if len(page.Records) > filter.Limit {
			page.Records, page.NextKey = page.Records[:filter.Limit], keys[filter.Limit-1]
		}
		return nil
	})
	return page, err
}

// AuthorMaterials reads the records of a project for a caller that has checked who reads them: the map and the tools.
func (repository *Repository) AuthorMaterials(ctx context.Context, projectID string) ([]domain.Record, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, domain.ErrNotFound
	}
	rows, err := sqlc.New(repository.db.Pool).AllAuthorMaterials(ctx, sqlc.AllAuthorMaterialsParams{ProjectID: project, LimitCount: maxReadRecords})
	if err != nil {
		return nil, err
	}
	records := make([]domain.Record, 0, len(rows))
	for _, row := range rows {
		record, err := recordOf(sqlc.GetAuthorMaterialRow(row))
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// writeTx runs a web mutation behind the session's write proof for an editor or above.
func (repository *Repository) writeTx(ctx context.Context, write service.Write, run func(pgx.Tx, *sqlc.Queries, pgtype.UUID, pgtype.UUID) error) error {
	project, actor, err := projectAndActor(write.ProjectID, write.UserID)
	if err != nil {
		return err
	}
	return repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := authstore.LockSessionWriteProof(ctx, tx, actor, write.Proof); err != nil {
			return err
		}
		queries := sqlc.New(tx)
		role, err := roleInTx(ctx, queries, project, actor)
		if err != nil {
			return err
		}
		if !projects.CanMutateProject(role) {
			return domain.ErrForbidden
		}
		return run(tx, queries, project, actor)
	})
}

func lockAtRevision(ctx context.Context, queries *sqlc.Queries, project pgtype.UUID, recordID string, expected int64) (pgtype.UUID, error) {
	id, err := storedb.ParseUUID(recordID)
	if err != nil {
		return pgtype.UUID{}, domain.ErrNotFound
	}
	revision, err := queries.LockAuthorMaterialRevision(ctx, sqlc.LockAuthorMaterialRevisionParams{ProjectID: project, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, domain.ErrNotFound
	}
	if err != nil {
		return pgtype.UUID{}, err
	}
	if revision != expected {
		return pgtype.UUID{}, domain.ErrConflict
	}
	return id, nil
}

func isNameTaken(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}

// UpdateAuthorMaterial replaces what a record says at the expected revision; another record of the name is a conflict.
func (repository *Repository) UpdateAuthorMaterial(ctx context.Context, write service.Write, recordID string, expected int64, draft domain.Draft, key string) (domain.Record, error) {
	properties, err := propertiesJSON(draft.Properties)
	if err != nil {
		return domain.Record{}, domain.ErrInvalid
	}
	var record domain.Record
	err = repository.writeTx(ctx, write, func(_ pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		id, err := lockAtRevision(ctx, queries, project, recordID, expected)
		if err != nil {
			return err
		}
		if _, err := queries.UpdateAuthorMaterial(ctx, sqlc.UpdateAuthorMaterialParams{
			ProjectID: project, ID: id, Name: draft.Name, NameKey: key, Kind: string(draft.Kind), Description: draft.Description,
			Properties: properties, Section: draft.Section, Actor: actor, ExpectedRevision: expected,
		}); err != nil {
			if isNameTaken(err) {
				return domain.ErrConflict
			}
			return err
		}
		row, err := queries.GetAuthorMaterial(ctx, sqlc.GetAuthorMaterialParams{ProjectID: project, ID: id})
		if err != nil {
			return err
		}
		record, err = recordOf(row)
		return err
	})
	return record, err
}

// DeleteAuthorMaterial removes a record at the expected revision.
func (repository *Repository) DeleteAuthorMaterial(ctx context.Context, write service.Write, recordID string, expected int64) error {
	return repository.writeTx(ctx, write, func(_ pgx.Tx, queries *sqlc.Queries, project, _ pgtype.UUID) error {
		id, err := lockAtRevision(ctx, queries, project, recordID, expected)
		if err != nil {
			return err
		}
		_, err = queries.DeleteAuthorMaterial(ctx, sqlc.DeleteAuthorMaterialParams{ProjectID: project, ID: id, ExpectedRevision: expected})
		if err != nil {
			return err
		}
		return queries.DeleteSagaMapNodesOfProject(ctx, project)
	})
}

func running(status string) bool {
	switch jobs.Status(status) {
	case jobs.StatusSucceeded, jobs.StatusFailed, jobs.StatusCanceled:
		return false
	default:
		return true
	}
}

type startReplay struct {
	ImportID string `json:"importId"`
}

// StartAuthorMaterialsImport keeps what a file holds and queues its Job in one transaction with the quota of the day. It
// replaces an import that waits for an answer and refuses while another one is running.
func (repository *Repository) StartAuthorMaterialsImport(ctx context.Context, command service.StartCommand) (domain.Import, error) {
	var started domain.Import
	err := repository.writeTx(ctx, command.Write, func(tx pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		// One start of a project at a time: the row of its import cannot lock an import that is not there yet.
		if _, err := tx.Exec(ctx, `SELECT id FROM projects WHERE id = $1 FOR UPDATE`, project); err != nil {
			return err
		}
		idempotency, err := queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
			ScopeHash: command.ScopeHash, OperationID: command.OperationID, IdempotencyKeyHash: command.KeyHash,
			RequestHash: command.RequestHash, StatusCode: 202, ResponseJson: []byte("{}"), CreatedAt: storedb.Timestamptz(command.At),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			kept, err := queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
				ScopeHash: command.ScopeHash, OperationID: command.OperationID, IdempotencyKeyHash: command.KeyHash,
			})
			if err != nil {
				return err
			}
			var replay startReplay
			if kept.RequestHash != command.RequestHash || json.Unmarshal(kept.ResponseJson, &replay) != nil {
				return domain.ErrConflict
			}
			found, err := currentInTx(ctx, queries, project)
			if err != nil {
				return err
			}
			if found == nil || found.ID != replay.ImportID {
				return domain.ErrConflict
			}
			started = *found
			return nil
		}
		if err != nil {
			return err
		}
		held, err := queries.LockAuthorMaterialImport(ctx, project)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case running(held.JobStatus):
			return domain.ErrImportBusy
		default:
			if _, err := queries.DeleteAuthorMaterialImport(ctx, sqlc.DeleteAuthorMaterialImportParams{ProjectID: project, ID: held.ID}); err != nil {
				return err
			}
		}
		if err := quotastore.ReserveImportsInTx(ctx, tx, command.UserID, 1, command.At); err != nil {
			return err
		}
		var jobID, importID string
		if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text, gen_random_uuid()::text`).Scan(&jobID, &importID); err != nil {
			return err
		}
		created, err := repository.jobs.CreateAuthorMaterialsImportJobInTx(ctx, tx, jobstore.AuthorMaterialsImportJobInput{
			JobID: jobID, CreatorUserID: command.UserID, CreatedAt: command.At,
			Subject: jobs.AuthorMaterialsImportSubject{Type: jobs.KindAuthorMaterialsImport, ProjectID: command.ProjectID, ImportID: importID},
		})
		if err != nil {
			return err
		}
		job, err := storedb.ParseUUID(created.ID)
		if err != nil {
			return err
		}
		id, err := storedb.ParseUUID(importID)
		if err != nil {
			return err
		}
		if err := queries.CreateAuthorMaterialImport(ctx, sqlc.CreateAuthorMaterialImportParams{
			ID: id, ProjectID: project, JobID: job, SourceName: command.SourceName, SourceKind: string(command.SourceKind),
			Parsed: command.Parsed, Actor: actor,
		}); err != nil {
			return err
		}
		response, err := json.Marshal(startReplay{ImportID: importID})
		if err != nil {
			return err
		}
		if _, err := queries.UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{ID: idempotency.ID, StatusCode: 202, ResponseJson: response}); err != nil {
			return err
		}
		found, err := currentInTx(ctx, queries, project)
		if err != nil {
			return err
		}
		if found == nil {
			return domain.ErrConflict
		}
		started = *found
		return nil
	})
	return started, err
}

func currentInTx(ctx context.Context, queries *sqlc.Queries, project pgtype.UUID) (*domain.Import, error) {
	row, err := queries.GetAuthorMaterialImport(ctx, project)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	found := &domain.Import{
		ID: storedb.UUIDString(row.ID), JobID: storedb.UUIDString(row.JobID), JobStatus: row.JobStatus, SourceName: row.SourceName,
		SourceKind: domain.SourceKind(row.SourceKind), CreatedAt: row.CreatedAt.Time.UTC(),
	}
	if row.Candidates != nil {
		found.Candidates = []domain.Draft{}
		if err := json.Unmarshal(row.Candidates, &found.Candidates); err != nil {
			return nil, err
		}
	}
	return found, nil
}

// AuthorMaterialsImport reads the import of a project for one of its members with the name keys its records hold.
func (repository *Repository) AuthorMaterialsImport(ctx context.Context, actor service.Actor) (*domain.Import, map[string]bool, error) {
	project, user, err := projectAndActor(actor.ProjectID, actor.UserID)
	if err != nil {
		return nil, nil, err
	}
	var found *domain.Import
	keys := map[string]bool{}
	err = repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		if _, err := roleInTx(ctx, queries, project, user); err != nil {
			return err
		}
		if found, err = currentInTx(ctx, queries, project); err != nil || found == nil {
			return err
		}
		held, err := queries.AuthorMaterialKeys(ctx, project)
		for _, key := range held {
			keys[key] = true
		}
		return err
	})
	return found, keys, err
}

type confirmReplay struct {
	Saved int `json:"saved"`
}

// ConfirmAuthorMaterialsImport keeps the chosen candidates and removes the import in one transaction.
func (repository *Repository) ConfirmAuthorMaterialsImport(ctx context.Context, command service.ConfirmCommand) (int, error) {
	id, err := storedb.ParseUUID(command.ImportID)
	if err != nil {
		return 0, domain.ErrNotFound
	}
	saved := 0
	err = repository.writeTx(ctx, command.Write, func(_ pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		idempotency, err := queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
			ScopeHash: command.ScopeHash, OperationID: command.OperationID, IdempotencyKeyHash: command.KeyHash,
			RequestHash: command.RequestHash, StatusCode: 200, ResponseJson: []byte("{}"), CreatedAt: storedb.Timestamptz(time.Now()),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			kept, err := queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
				ScopeHash: command.ScopeHash, OperationID: command.OperationID, IdempotencyKeyHash: command.KeyHash,
			})
			if err != nil {
				return err
			}
			var replay confirmReplay
			if kept.RequestHash != command.RequestHash || json.Unmarshal(kept.ResponseJson, &replay) != nil {
				return domain.ErrConflict
			}
			saved = replay.Saved
			return nil
		}
		if err != nil {
			return err
		}
		held, err := queries.LockAuthorMaterialImport(ctx, project)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && held.ID != id {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if jobs.Status(held.JobStatus) != jobs.StatusSucceeded || held.Candidates == nil {
			return domain.ErrImportBusy
		}
		var candidates []domain.Draft
		if err := json.Unmarshal(held.Candidates, &candidates); err != nil {
			return err
		}
		drafts, err := domain.Chosen(candidates, command.Choices)
		if err != nil {
			return err
		}
		for _, draft := range drafts {
			key := sagamemory.GlossaryKey(draft.Name)
			if key == "" {
				return domain.ErrInvalid
			}
			properties, err := propertiesJSON(draft.Properties)
			if err != nil {
				return domain.ErrInvalid
			}
			if err := queries.UpsertAuthorMaterial(ctx, sqlc.UpsertAuthorMaterialParams{
				ProjectID: project, Name: draft.Name, NameKey: key, Kind: string(draft.Kind), Description: draft.Description, Properties: properties,
				Section: draft.Section, SourceName: held.SourceName, SourceKind: held.SourceKind, Actor: actor,
			}); err != nil {
				return err
			}
		}
		if _, err := queries.DeleteAuthorMaterialImport(ctx, sqlc.DeleteAuthorMaterialImportParams{ProjectID: project, ID: id}); err != nil {
			return err
		}
		saved = len(drafts)
		response, err := json.Marshal(confirmReplay{Saved: saved})
		if err != nil {
			return err
		}
		_, err = queries.UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{ID: idempotency.ID, StatusCode: 200, ResponseJson: response})
		return err
	})
	return saved, err
}

// DiscardAuthorMaterialsImport removes an import whose Job has ended, with what it holds.
func (repository *Repository) DiscardAuthorMaterialsImport(ctx context.Context, write service.Write, importID string) error {
	id, err := storedb.ParseUUID(importID)
	if err != nil {
		return domain.ErrNotFound
	}
	return repository.writeTx(ctx, write, func(_ pgx.Tx, queries *sqlc.Queries, project, _ pgtype.UUID) error {
		held, err := queries.LockAuthorMaterialImport(ctx, project)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && held.ID != id {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if running(held.JobStatus) {
			return domain.ErrImportBusy
		}
		_, err = queries.DeleteAuthorMaterialImport(ctx, sqlc.DeleteAuthorMaterialImportParams{ProjectID: project, ID: id})
		return err
	})
}

// ImportParsed reads what the file of a Job holds under its claim; jobs.ErrConflict when the import is gone.
func (repository *Repository) ImportParsed(ctx context.Context, claim jobs.WorkerClaim, importID string) (domain.Parsed, error) {
	project, err := storedb.ParseUUID(claim.ProjectID)
	if err != nil {
		return domain.Parsed{}, service.ErrInvalidJob
	}
	job, err := storedb.ParseUUID(claim.JobID)
	if err != nil {
		return domain.Parsed{}, service.ErrInvalidJob
	}
	id, err := storedb.ParseUUID(importID)
	if err != nil {
		return domain.Parsed{}, service.ErrInvalidJob
	}
	body, err := sqlc.New(repository.db.Pool).GetAuthorMaterialImportParsed(ctx, sqlc.GetAuthorMaterialImportParsedParams{ProjectID: project, ID: id, JobID: job})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Parsed{}, jobs.ErrConflict
	}
	if err != nil {
		return domain.Parsed{}, err
	}
	var parsed domain.Parsed
	if err := json.Unmarshal(body, &parsed); err != nil {
		return domain.Parsed{}, err
	}
	return parsed, nil
}

// FinishImport ends the Job under its claim: a success keeps the candidates, a failure keeps only the import to say so,
// a stop removes the import. The text of the file goes in every case.
func (repository *Repository) FinishImport(ctx context.Context, claim jobs.WorkerClaim, outcome service.ImportOutcome) error {
	from := jobs.StatusRunning
	var resultJSON, errorJSON, candidates []byte
	var err error
	switch outcome.Status {
	case jobs.StatusSucceeded:
		drafts := outcome.Candidates
		if drafts == nil {
			drafts = []domain.Draft{}
		}
		if candidates, err = json.Marshal(drafts); err != nil {
			return service.ErrInvalidJob
		}
		if resultJSON, err = jobs.EncodeResult(jobs.KindAuthorMaterialsImport, jobs.AuthorMaterialsImportResult{
			Type: jobs.KindAuthorMaterialsImport, Candidates: len(drafts),
		}); err != nil {
			return service.ErrInvalidJob
		}
	case jobs.StatusFailed:
	case jobs.StatusCanceled:
		from = jobs.StatusCanceling
	default:
		return service.ErrInvalidJob
	}
	if outcome.Error != nil {
		if errorJSON, err = jobs.EncodeError(*outcome.Error); err != nil {
			return service.ErrInvalidJob
		}
	}
	project, err := storedb.ParseUUID(claim.ProjectID)
	if err != nil {
		return service.ErrInvalidJob
	}
	job, err := storedb.ParseUUID(claim.JobID)
	if err != nil {
		return service.ErrInvalidJob
	}
	return repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		return jobstore.EndClaimedJobInTx(ctx, tx, jobstore.ClaimedJobEnd{
			Kind: jobs.KindAuthorMaterialsImport, ProjectID: project, JobID: job, From: from, To: outcome.Status,
			ClaimIncarnation: claim.ClaimIncarnation, ResultJSON: resultJSON, ErrorJSON: errorJSON,
		}, func() error {
			queries := sqlc.New(tx)
			if outcome.Status == jobs.StatusCanceled {
				return queries.DeleteAuthorMaterialImportOfJob(ctx, sqlc.DeleteAuthorMaterialImportOfJobParams{ProjectID: project, JobID: job})
			}
			_, err := queries.EndAuthorMaterialImport(ctx, sqlc.EndAuthorMaterialImportParams{ProjectID: project, JobID: job, Candidates: candidates})
			return err
		})
	})
}
