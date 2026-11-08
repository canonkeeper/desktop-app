// Package authormemory is the PostgreSQL authority of the memory of the author
// (specs/2026-11-01-writer-assistant-index.md, UC4). Every read and write names the project; the web
// writes run behind the session's write proof for an editor or above, the chat tools write under the
// authority of their turn with CreateInTx.
package authormemory

import (
	"context"
	"encoding/json"
	"errors"

	domain "canonkeeper/backend/internal/domain/authormemory"
	"canonkeeper/backend/internal/projects"
	service "canonkeeper/backend/internal/services/authormemory"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	_ service.Access      = (*Repository)(nil)
	_ service.ListStore   = (*Repository)(nil)
	_ service.CreateStore = (*Repository)(nil)
	_ service.UpdateStore = (*Repository)(nil)
	_ service.DeleteStore = (*Repository)(nil)
)

type Repository struct{ db *storedb.DB }

func New(db *storedb.DB) *Repository { return &Repository{db: db} }

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

// CanWriteAuthorMemory says whether the actor is an editor or above in the project.
func (repository *Repository) CanWriteAuthorMemory(ctx context.Context, actorUserID, projectID string) (bool, error) {
	project, actor, err := projectAndActor(projectID, actorUserID)
	if err != nil {
		return false, err
	}
	role, err := roleInTx(ctx, sqlc.New(repository.db.Pool), project, actor)
	if err != nil {
		return false, err
	}
	return projects.CanMutateProject(role), nil
}

// ListAuthorMemory lists the records for a member of the project; any role reads.
func (repository *Repository) ListAuthorMemory(ctx context.Context, actor service.Actor, filter domain.Filter) (domain.Page, error) {
	project, user, err := projectAndActor(actor.ProjectID, actor.UserID)
	if err != nil {
		return domain.Page{}, err
	}
	var page domain.Page
	err = repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := roleInTx(ctx, sqlc.New(tx), project, user); err != nil {
			return err
		}
		page, err = ListInTx(ctx, tx, actor.ProjectID, filter)
		return err
	})
	return page, err
}

// ListInTx reads a page of the records of a project. The caller has checked who reads them.
func ListInTx(ctx context.Context, tx pgx.Tx, projectID string, filter domain.Filter) (domain.Page, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return domain.Page{}, domain.ErrNotFound
	}
	params := sqlc.ListAuthorMemoryParams{
		ProjectID: project, Kinds: make([]string, len(filter.Kinds)), SearchQuery: filter.Query, LimitCount: int32(filter.Limit + 1),
	}
	for index, kind := range filter.Kinds {
		params.Kinds[index] = string(kind)
	}
	if filter.JobID != "" {
		if params.JobID, err = storedb.ParseUUID(filter.JobID); err != nil {
			return domain.Page{}, domain.ErrInvalid
		}
	}
	if filter.Cursor != nil {
		id, err := storedb.ParseUUID(filter.Cursor.ID)
		if err != nil {
			return domain.Page{}, domain.ErrInvalid
		}
		params.HasCursor, params.CursorID, params.CursorAt = true, id, storedb.Timestamptz(filter.Cursor.CreatedAt)
	}
	rows, err := sqlc.New(tx).ListAuthorMemory(ctx, params)
	if err != nil {
		return domain.Page{}, err
	}
	page := domain.Page{Records: make([]domain.Record, 0, len(rows))}
	for _, row := range rows {
		page.Total = row.TotalCount
		page.Records = append(page.Records, recordOf(sqlc.GetAuthorMemoryRow{
			ID: row.ID, ProjectID: row.ProjectID, Kind: row.Kind, Body: row.Body, ChapterID: row.ChapterID, Name: row.Name,
			Revision: row.Revision, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, AuthorName: row.AuthorName,
		}))
	}
	if len(page.Records) > filter.Limit {
		page.Records, page.HasMore = page.Records[:filter.Limit], true
	}
	return page, nil
}

func recordOf(row sqlc.GetAuthorMemoryRow) domain.Record {
	record := domain.Record{
		ID: storedb.UUIDString(row.ID), ProjectID: storedb.UUIDString(row.ProjectID), Kind: domain.Kind(row.Kind), Text: row.Body,
		Name: row.Name.String, Revision: row.Revision, AuthorID: storedb.UUIDString(row.CreatedBy), AuthorName: row.AuthorName,
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
	if row.ChapterID.Valid {
		record.ChapterID = storedb.UUIDString(row.ChapterID)
	}
	return record
}

func optionalUUID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	id, err := storedb.ParseUUID(value)
	if err != nil {
		return pgtype.UUID{}, domain.ErrInvalid
	}
	return id, nil
}

func optionalText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

// CreateInTx keeps one record of the project written by the actor. The caller has checked his right.
func CreateInTx(ctx context.Context, tx pgx.Tx, projectID, actorUserID string, draft domain.Draft) (domain.Record, error) {
	project, actor, err := projectAndActor(projectID, actorUserID)
	if err != nil {
		return domain.Record{}, err
	}
	chapter, err := optionalUUID(draft.ChapterID)
	if err != nil {
		return domain.Record{}, err
	}
	var job pgtype.UUID
	if draft.JobID != "" {
		if job, err = storedb.ParseUUID(draft.JobID); err != nil {
			return domain.Record{}, domain.ErrInvalid
		}
	}
	id, err := sqlc.New(tx).CreateAuthorMemory(ctx, sqlc.CreateAuthorMemoryParams{
		ProjectID: project, Kind: string(draft.Kind), Body: draft.Text, ChapterID: chapter, Name: optionalText(draft.Name), JobID: job, Actor: actor,
	})
	if err != nil {
		return domain.Record{}, err
	}
	return getInTx(ctx, sqlc.New(tx), project, id)
}

func getInTx(ctx context.Context, queries *sqlc.Queries, project, id pgtype.UUID) (domain.Record, error) {
	row, err := queries.GetAuthorMemory(ctx, sqlc.GetAuthorMemoryParams{ProjectID: project, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Record{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Record{}, err
	}
	return recordOf(row), nil
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

type createReplay struct {
	RecordID string `json:"recordId"`
}

// CreateAuthorMemory keeps a record under its idempotency key.
func (repository *Repository) CreateAuthorMemory(ctx context.Context, command service.CreateCommand) (domain.Record, error) {
	var record domain.Record
	err := repository.writeTx(ctx, command.Write, func(tx pgx.Tx, queries *sqlc.Queries, project, _ pgtype.UUID) error {
		idempotency, err := queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
			ScopeHash: command.ScopeHash, OperationID: command.OperationID, IdempotencyKeyHash: command.KeyHash,
			RequestHash: command.RequestHash, StatusCode: 201, ResponseJson: []byte("{}"), CreatedAt: storedb.Timestamptz(command.At),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			kept, err := queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
				ScopeHash: command.ScopeHash, OperationID: command.OperationID, IdempotencyKeyHash: command.KeyHash,
			})
			if err != nil {
				return err
			}
			if kept.RequestHash != command.RequestHash {
				return domain.ErrConflict
			}
			var replay createReplay
			if json.Unmarshal(kept.ResponseJson, &replay) != nil {
				return domain.ErrConflict
			}
			id, err := storedb.ParseUUID(replay.RecordID)
			if err != nil {
				return domain.ErrConflict
			}
			// The record may have been removed since: the first answer cannot be given again.
			record, err = getInTx(ctx, queries, project, id)
			return err
		}
		if err != nil {
			return err
		}
		if record, err = CreateInTx(ctx, tx, command.ProjectID, command.UserID, command.Draft); err != nil {
			return err
		}
		response, err := json.Marshal(createReplay{RecordID: record.ID})
		if err != nil {
			return err
		}
		_, err = queries.UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{ID: idempotency.ID, StatusCode: 201, ResponseJson: response})
		return err
	})
	return record, err
}

// lockAtRevision locks a record of the project and checks its revision.
func lockAtRevision(ctx context.Context, queries *sqlc.Queries, project pgtype.UUID, recordID string, expected int64) (pgtype.UUID, error) {
	id, err := storedb.ParseUUID(recordID)
	if err != nil {
		return pgtype.UUID{}, domain.ErrNotFound
	}
	revision, err := queries.LockAuthorMemoryRevision(ctx, sqlc.LockAuthorMemoryRevisionParams{ProjectID: project, ID: id})
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

// UpdateAuthorMemory replaces what a record says at the expected revision.
func (repository *Repository) UpdateAuthorMemory(ctx context.Context, write service.Write, recordID string, expected int64, draft domain.Draft) (domain.Record, error) {
	chapter, err := optionalUUID(draft.ChapterID)
	if err != nil {
		return domain.Record{}, err
	}
	var record domain.Record
	err = repository.writeTx(ctx, write, func(_ pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		id, err := lockAtRevision(ctx, queries, project, recordID, expected)
		if err != nil {
			return err
		}
		if _, err := queries.UpdateAuthorMemory(ctx, sqlc.UpdateAuthorMemoryParams{
			ProjectID: project, ID: id, Kind: string(draft.Kind), Body: draft.Text, ChapterID: chapter, Name: optionalText(draft.Name),
			Actor: actor, ExpectedRevision: expected,
		}); err != nil {
			return err
		}
		record, err = getInTx(ctx, queries, project, id)
		return err
	})
	return record, err
}

// DeleteAuthorMemory removes a record at the expected revision.
func (repository *Repository) DeleteAuthorMemory(ctx context.Context, write service.Write, recordID string, expected int64) error {
	return repository.writeTx(ctx, write, func(_ pgx.Tx, queries *sqlc.Queries, project, _ pgtype.UUID) error {
		id, err := lockAtRevision(ctx, queries, project, recordID, expected)
		if err != nil {
			return err
		}
		_, err = queries.DeleteAuthorMemory(ctx, sqlc.DeleteAuthorMemoryParams{ProjectID: project, ID: id, ExpectedRevision: expected})
		return err
	})
}

// projectRecordsPage is the page ProjectRecords reads a project's memory in.
const projectRecordsPage = 200

// ProjectRecords reads every record of a project, newest first, for the pack of a chapter on its moment
// (specs/2026-11-06-writing-agent.md, WA2.6): a system read behind a use case that checked the actor.
func (repository *Repository) ProjectRecords(ctx context.Context, projectID string) ([]domain.Record, error) {
	var records []domain.Record
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		filter := domain.Filter{Limit: projectRecordsPage}
		for {
			page, err := ListInTx(ctx, tx, projectID, filter)
			if err != nil {
				return err
			}
			records = append(records, page.Records...)
			if !page.HasMore || len(page.Records) == 0 {
				return nil
			}
			last := page.Records[len(page.Records)-1]
			filter.Cursor = &domain.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
		}
	})
	return records, err
}
