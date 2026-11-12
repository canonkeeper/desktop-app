package notes

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	domain "canonkeeper/backend/internal/domain/notes"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	service "canonkeeper/backend/internal/services/notes"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Repository struct {
	db *storedb.DB
}

func New(db *storedb.DB) *Repository { return &Repository{db: db} }

type savedSource struct {
	Source  domain.Source                               `json:"source"`
	Chapter *governance.CanonicalChapterSourceLocatorV1 `json:"chapter,omitempty"`
	Chat    *service.ChatBinding                        `json:"chat,omitempty"`
}

// listedNote is a row of the catalog as the query gives it: the place of the book on the shelf and the order of the
// chapter make its place.
type listedNote struct {
	domain.Entry
	ProjectID    *string `json:"projectId"`
	BookID       *string `json:"bookId"`
	ChapterOrder *int    `json:"chapterOrder"`
}

// chapterPlace is the place of a chapter as the author reads it everywhere (specs/2026-11-10-ux1-names.md N1.1); the
// book is named by its place on the shelf (specs/2026-11-11-ux7-reorder.md RK1.6).
func chapterPlace(bookPlace, chapterOrder int, title string) string {
	return (&sagamemory.Chapter{BookOrder: bookPlace, Order: chapterOrder, Title: title}).Place()
}

func mapError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}
func noteValue(row sqlc.PersonalNote) (domain.Note, error) {
	note := domain.Note{ID: row.ID.String(), Title: row.Title, Body: row.Body, Revision: row.Revision, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
	if len(row.SourceJson) != 0 {
		var saved savedSource
		if err := json.Unmarshal(row.SourceJson, &saved); err != nil {
			return domain.Note{}, err
		}
		note.Source = &saved.Source
	}
	return note, nil
}
func (repository *Repository) Get(ctx context.Context, userID, noteID string) (domain.Note, error) {
	owner, err := storedb.ParseUUID(userID)
	if err != nil {
		return domain.Note{}, domain.ErrNotFound
	}
	id, err := storedb.ParseUUID(noteID)
	if err != nil {
		return domain.Note{}, domain.ErrNotFound
	}
	row, err := sqlc.New(repository.db.Pool).GetPersonalNote(ctx, sqlc.GetPersonalNoteParams{OwnerUserID: owner, ID: id})
	if err != nil {
		return domain.Note{}, mapError(err)
	}
	return noteValue(row)
}
func (repository *Repository) List(ctx context.Context, query service.ListQuery) (service.ListResult, error) {
	owner, err := storedb.ParseUUID(query.UserID)
	if err != nil {
		return service.ListResult{}, domain.ErrNotFound
	}
	cursorID := pgtype.UUID{}
	if query.Cursor.ID != "" {
		cursorID, err = storedb.ParseUUID(query.Cursor.ID)
		if err != nil {
			return service.ListResult{}, domain.ErrValidation
		}
	}
	projectID, bookID := pgtype.UUID{}, pgtype.UUID{}
	if query.Input.ProjectID != "" {
		projectID, err = storedb.ParseUUID(query.Input.ProjectID)
		if err != nil {
			return service.ListResult{}, domain.ErrValidation
		}
	}
	if query.Input.BookID != "" {
		bookID, err = storedb.ParseUUID(query.Input.BookID)
		if err != nil {
			return service.ListResult{}, domain.ErrValidation
		}
	}
	row, err := sqlc.New(repository.db.Pool).ListNotes(ctx, sqlc.ListNotesParams{
		ActorUserID: owner, LimitCount: int32(query.Limit), HasCursor: query.Cursor.ID != "",
		CursorID: cursorID, CursorUpdatedAt: storedb.Timestamptz(query.Cursor.UpdatedAt),
		SearchQuery: query.Input.Query, SourceKind: query.Input.Source, ProjectID: projectID, BookID: bookID,
	})
	if err != nil {
		return service.ListResult{}, err
	}
	var rows []listedNote
	if err := json.Unmarshal(row.Entries, &rows); err != nil {
		return service.ListResult{}, err
	}
	var projects []string
	for _, listed := range rows {
		if listed.ChapterTitle != "" && listed.ProjectID != nil && listed.BookID != nil && listed.ChapterOrder != nil {
			projects = append(projects, *listed.ProjectID)
		}
	}
	places, err := shelfPlaces(ctx, repository.db.Pool, projects)
	if err != nil {
		return service.ListResult{}, err
	}
	result := service.ListResult{TotalCount: row.TotalCount, Entries: make([]domain.Entry, len(rows))}
	for index, listed := range rows {
		result.Entries[index] = listed.Entry
		if listed.ChapterTitle != "" && listed.ProjectID != nil && listed.BookID != nil && listed.ChapterOrder != nil {
			result.Entries[index].Place = chapterPlace(places[*listed.ProjectID][*listed.BookID], *listed.ChapterOrder, listed.ChapterTitle)
		}
	}
	return result, nil
}

func (repository *Repository) Create(ctx context.Context, command service.CreateCommand) (domain.Note, error) {
	owner, err := storedb.ParseUUID(command.UserID)
	if err != nil {
		return domain.Note{}, domain.ErrNotFound
	}
	var result sqlc.PersonalNote
	err = repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := authstore.LockSessionWriteProof(ctx, tx, owner, command.Proof); err != nil {
			return err
		}
		queries := sqlc.New(tx)
		record, err := queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
			ScopeHash: command.ScopeHash, OperationID: "personal_note.create.v1", IdempotencyKeyHash: command.KeyHash,
			RequestHash: command.RequestHash, StatusCode: 201, ResponseJson: []byte("{}"), CreatedAt: storedb.Timestamptz(time.Now()),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			record, err = queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
				ScopeHash: command.ScopeHash, OperationID: "personal_note.create.v1", IdempotencyKeyHash: command.KeyHash,
			})
			if err != nil {
				return err
			}
			if record.RequestHash != command.RequestHash || record.StatusCode != 201 {
				return domain.ErrConflict
			}
			var replay struct {
				NoteID string `json:"noteId"`
			}
			if json.Unmarshal(record.ResponseJson, &replay) != nil {
				return domain.ErrConflict
			}
			id, err := storedb.ParseUUID(replay.NoteID)
			if err != nil {
				return domain.ErrConflict
			}
			result, err = queries.GetPersonalNote(ctx, sqlc.GetPersonalNoteParams{OwnerUserID: owner, ID: id})
			return err
		}
		if err != nil {
			return err
		}
		var sourceJSON []byte
		if command.Source != nil {
			captured, err := repository.captureSource(ctx, tx, owner, *command.Source)
			if err != nil {
				return err
			}
			sourceJSON, err = json.Marshal(captured)
			if err != nil {
				return err
			}
		}
		result, err = queries.CreatePersonalNote(ctx, sqlc.CreatePersonalNoteParams{OwnerUserID: owner, Title: command.Title, Body: command.Body, SourceJson: sourceJSON})
		if err != nil {
			return err
		}
		replay, err := json.Marshal(struct {
			NoteID string `json:"noteId"`
		}{result.ID.String()})
		if err != nil {
			return err
		}
		_, err = queries.UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{ID: record.ID, StatusCode: 201, ResponseJson: replay})
		return err
	})
	if err != nil {
		return domain.Note{}, mapError(err)
	}
	return noteValue(result)
}

func (repository *Repository) Update(ctx context.Context, actor service.Write, noteID string, input service.UpdateInput) (domain.Note, error) {
	var result sqlc.PersonalNote
	err := repository.mutate(ctx, actor, noteID, input.ExpectedRevision, func(queries *sqlc.Queries, locked sqlc.PersonalNote) error {
		body, title := pgtype.Text{}, pgtype.Text{}
		// A thought may be empty only beside the quote the note keeps (NT1.2).
		if input.Body != nil && domain.ValidateThought(*input.Body, len(locked.SourceJson) != 0) != nil {
			return domain.ErrValidation
		}
		if input.Body != nil {
			body = pgtype.Text{String: *input.Body, Valid: true}
		}
		if input.Title != nil {
			title = pgtype.Text{String: *input.Title, Valid: true}
		}
		row, err := queries.UpdatePersonalNote(ctx, sqlc.UpdatePersonalNoteParams{OwnerUserID: locked.OwnerUserID, ID: locked.ID, Body: body, Title: title, ExpectedRevision: input.ExpectedRevision})
		result = row
		return err
	})
	if err != nil {
		return domain.Note{}, err
	}
	return noteValue(result)
}
func (repository *Repository) Delete(ctx context.Context, actor service.Write, noteID string, revision int64) error {
	return repository.mutate(ctx, actor, noteID, revision, func(queries *sqlc.Queries, locked sqlc.PersonalNote) error {
		_, err := queries.DeletePersonalNote(ctx, sqlc.DeletePersonalNoteParams{OwnerUserID: locked.OwnerUserID, ID: locked.ID, ExpectedRevision: revision})
		return err
	})
}
func (repository *Repository) mutate(ctx context.Context, actor service.Write, noteID string, revision int64, mutation func(*sqlc.Queries, sqlc.PersonalNote) error) error {
	owner, err := storedb.ParseUUID(actor.UserID)
	if err != nil {
		return domain.ErrNotFound
	}
	id, err := storedb.ParseUUID(noteID)
	if err != nil {
		return domain.ErrNotFound
	}
	return mapError(repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := authstore.LockSessionWriteProof(ctx, tx, owner, actor.Proof); err != nil {
			return err
		}
		queries := sqlc.New(tx)
		row, err := queries.LockPersonalNote(ctx, sqlc.LockPersonalNoteParams{OwnerUserID: owner, ID: id})
		if err != nil {
			return err
		}
		if row.Revision != revision {
			return domain.ErrConflict
		}
		return mutation(queries, row)
	}))
}
