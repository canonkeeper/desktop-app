// Package sagadraft is the PostgreSQL authority of the draft of a chapter as text (specs/2026-11-07-desktop-app.md,
// DA16.4; specs/2026-11-02-writer-workshop.md): the drafts of a project, their versions, the edit of a chapter of the
// book and «Добавить в книгу». Every read names the project and checks membership first; a write runs behind the
// session's write proof for an editor or above. The writing agent's authority over the same drafts is
// repository/postgres/writingagent, which works in the transactions of this one (WriteTx, ReadTx, LockedDraft).
package sagadraft

import (
	"context"
	"encoding/json"
	"errors"

	domain "canonkeeper/backend/internal/domain/sagadraft"
	"canonkeeper/backend/internal/projects"
	service "canonkeeper/backend/internal/services/sagadraft"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	librarystore "canonkeeper/backend/internal/store/library"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	_ service.ListStore   = (*Repository)(nil)
	_ service.GetStore    = (*Repository)(nil)
	_ service.CreateStore = (*Repository)(nil)
	_ service.UpdateStore = (*Repository)(nil)
	_ service.DeleteStore = (*Repository)(nil)
	_ service.AddStore    = (*Repository)(nil)
)

type Repository struct {
	db      *storedb.DB
	library *librarystore.Store
	seams   SeamCheck
}

// SeamCheck starts, in the transaction of a take, the check of the seams a piece taken from a version left in the
// working text (specs/2026-11-06-writing-agent.md, WA7.5). A build without the writing agent passes none: the seams of
// a take stay unchecked.
type SeamCheck func(ctx context.Context, tx pgx.Tx, write service.Write, draftID string, seams []int) error

func New(db *storedb.DB, library *librarystore.Store, seams SeamCheck) *Repository {
	return &Repository{db: db, library: library, seams: seams}
}

// ProjectAndActor reads the ids of a project and an actor; an id that is no UUID is a draft not found.
func ProjectAndActor(projectID, actorUserID string) (pgtype.UUID, pgtype.UUID, error) {
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

// RoleInTx is the role of the actor in the project; domain.ErrNotFound for a project he is not in.
func RoleInTx(ctx context.Context, queries *sqlc.Queries, project, actor pgtype.UUID) (projects.Role, error) {
	role, err := queries.GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{ActorUserID: actor, ProjectID: project})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return projects.Role(role), err
}

// ReadTx runs a read for a member of the project; any role reads.
func (repository *Repository) ReadTx(ctx context.Context, actor service.Actor, run func(*sqlc.Queries, pgtype.UUID) error) error {
	project, user, err := ProjectAndActor(actor.ProjectID, actor.UserID)
	if err != nil {
		return err
	}
	return repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		if _, err := RoleInTx(ctx, queries, project, user); err != nil {
			return err
		}
		return run(queries, project)
	})
}

// ListChapterDrafts lists the drafts of the project, newest first, without their text.
func (repository *Repository) ListChapterDrafts(ctx context.Context, actor service.Actor, cursor *domain.Cursor, limit int) (domain.Page, error) {
	var page domain.Page
	err := repository.ReadTx(ctx, actor, func(queries *sqlc.Queries, project pgtype.UUID) error {
		params := sqlc.ListChapterDraftsParams{ProjectID: project, LimitCount: int32(limit + 1)}
		if cursor != nil {
			id, err := storedb.ParseUUID(cursor.ID)
			if err != nil {
				return domain.ErrInvalid
			}
			params.HasCursor, params.CursorID, params.CursorAt = true, id, storedb.Timestamptz(cursor.CreatedAt)
		}
		rows, err := queries.ListChapterDrafts(ctx, params)
		if err != nil {
			return err
		}
		page.Drafts = make([]domain.Summary, 0, len(rows))
		for _, row := range rows {
			page.Total = row.TotalCount
			page.Drafts = append(page.Drafts, domain.Summary{
				ID: storedb.UUIDString(row.ID), Title: row.Title, Kind: domain.Kind(row.Kind),
				Stage: domain.StageOf(row.AddedChapterID.Valid), Words: int(row.Words),
				JobStatus: row.JobStatus.String, Revision: row.Revision, Edits: row.Edits,
				CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
			})
		}
		if len(page.Drafts) > limit {
			page.Drafts, page.HasMore = page.Drafts[:limit], true
		}
		return nil
	})
	return page, err
}

// GetChapterDraft reads one draft for a member of its project.
func (repository *Repository) GetChapterDraft(ctx context.Context, actor service.Actor, draftID string) (domain.Draft, error) {
	var draft domain.Draft
	err := repository.ReadTx(ctx, actor, func(queries *sqlc.Queries, project pgtype.UUID) error {
		id, err := storedb.ParseUUID(draftID)
		if err != nil {
			return domain.ErrNotFound
		}
		draft, err = GetInTx(ctx, queries, project, id)
		return err
	})
	return draft, err
}

// GetInTx reads a draft of the project whole: its place, its edit, its last Job, its notes and its file.
func GetInTx(ctx context.Context, queries *sqlc.Queries, project, id pgtype.UUID) (domain.Draft, error) {
	row, err := queries.GetChapterDraft(ctx, sqlc.GetChapterDraftParams{ProjectID: project, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Draft{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Draft{}, err
	}
	draft := domain.Draft{
		ID: storedb.UUIDString(row.ID), ProjectID: storedb.UUIDString(row.ProjectID), Title: row.Title, Idea: row.Idea, Kind: domain.Kind(row.Kind),
		Text:      row.Body,
		BookTitle: row.BookTitle.String, AfterChapterTitle: row.AfterChapterTitle.String, AddedChapterTitle: row.AddedChapterTitle.String,
		PlaceGone: row.StoredBookID.Valid && !row.BookID.Valid || row.StoredAfterChapterID.Valid && !row.AfterChapterID.Valid,
		Revision:  row.Revision, AuthorName: row.AuthorName, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
	if row.BookID.Valid {
		draft.Place.BookID = storedb.UUIDString(row.BookID)
	}
	if row.AfterChapterID.Valid {
		draft.Place.AfterChapterID = storedb.UUIDString(row.AfterChapterID)
	}
	if row.StoredAddedChapterID.Valid {
		draft.AddedChapterID = storedb.UUIDString(row.StoredAddedChapterID)
	}
	if row.StoredEditsChapterID.Valid {
		draft.Edits = &domain.Edit{
			ChapterID: storedb.UUIDString(row.StoredEditsChapterID), Revision: row.EditsRevision.Int64, PreviousRevision: row.EditsPreviousRevision.Int64,
			Gone: !row.EditedChapterID.Valid,
		}
		if row.EditedChapterID.Valid {
			draft.Edits.ChapterTitle, draft.Edits.BookID = row.EditedChapterTitle.String, storedb.UUIDString(row.EditedBookID)
			draft.Edits.BookRevision, draft.Edits.BookWords = row.EditedRevision.Int64, int(row.EditedWords)
		}
	}
	if row.ChatID.Valid {
		draft.ChatID = storedb.UUIDString(row.ChatID)
	}
	draft.CaseChoice, draft.PlanIdeaHash = domain.Case(row.CaseChoice.String), row.PlanIdeaHash
	if row.PlanNumber.Valid {
		draft.PlanNumber = int(row.PlanNumber.Int32)
		draft.PlanApproved = row.PlanApproved.Valid && row.PlanApproved.Int32 == row.PlanNumber.Int32
	}
	if err := json.Unmarshal(row.PackChoices, &draft.PackChoices); err != nil {
		return domain.Draft{}, err
	}
	if row.JobID.Valid {
		draft.Job = &domain.Job{
			ID: storedb.UUIDString(row.JobID), Status: row.JobStatus.String, Mode: row.JobMode, Phase: row.JobPhase,
			Done: int(row.JobDone), Total: int(row.JobTotal), ErrorCode: row.JobErrorCode,
		}
	}
	words, err := queries.ChapterDraftTypicalWords(ctx, sqlc.ChapterDraftTypicalWordsParams{
		ProjectID: project, BookID: row.BookID, AfterChapterID: row.AfterChapterID, Chapters: domain.TypicalChapters,
	})
	if err != nil {
		return domain.Draft{}, err
	}
	typical := make([]int, len(words))
	for i, count := range words {
		typical[i] = int(count)
	}
	draft.TypicalWords = domain.TypicalWords(typical)
	notes, err := queries.ListChapterDraftNotes(ctx, sqlc.ListChapterDraftNotesParams{ProjectID: project, DraftID: id})
	if err != nil {
		return domain.Draft{}, err
	}
	draft.Notes = make([]domain.Note, len(notes))
	for i, note := range notes {
		draft.Notes[i] = NoteOf(note)
	}
	if draft.Original, err = originalSummaryInTx(ctx, queries, project, id); err != nil {
		return domain.Draft{}, err
	}
	return draft, nil
}

// WriteTx runs a mutation behind the session's write proof — of a verified address when verified asks for
// it — for an editor or above.
func (repository *Repository) WriteTx(
	ctx context.Context, write service.Write, verified bool, run func(pgx.Tx, *sqlc.Queries, pgtype.UUID, pgtype.UUID) error,
) error {
	project, actor, err := ProjectAndActor(write.ProjectID, write.UserID)
	if err != nil {
		return err
	}
	return repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		lock := authstore.LockSessionWriteProof
		if verified {
			lock = authstore.LockVerifiedSessionWriteProof
		}
		if _, err := lock(ctx, tx, actor, write.Proof); err != nil {
			return err
		}
		queries := sqlc.New(tx)
		role, err := RoleInTx(ctx, queries, project, actor)
		if err != nil {
			return err
		}
		if !projects.CanMutateProject(role) {
			return domain.ErrForbidden
		}
		return run(tx, queries, project, actor)
	})
}

type draftReplay struct {
	DraftID string `json:"draftId"`
}

// Recorded keeps what a mutation answers under its idempotency key: a replay of the same request reads the
// draft again, another request under the key is domain.ErrConflict.
func Recorded(
	ctx context.Context, queries *sqlc.Queries, project pgtype.UUID, replay service.Replay, status int32, start func() (string, error),
) (domain.Draft, error) {
	record, err := queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
		ScopeHash: replay.ScopeHash, OperationID: replay.OperationID, IdempotencyKeyHash: replay.KeyHash,
		RequestHash: replay.RequestHash, StatusCode: status, ResponseJson: []byte("{}"), CreatedAt: storedb.Timestamptz(replay.At),
	})
	var draftID string
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		kept, err := queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
			ScopeHash: replay.ScopeHash, OperationID: replay.OperationID, IdempotencyKeyHash: replay.KeyHash,
		})
		if err != nil {
			return domain.Draft{}, err
		}
		var answered draftReplay
		if kept.RequestHash != replay.RequestHash || json.Unmarshal(kept.ResponseJson, &answered) != nil {
			return domain.Draft{}, domain.ErrConflict
		}
		draftID = answered.DraftID
	case err != nil:
		return domain.Draft{}, err
	default:
		if draftID, err = start(); err != nil {
			return domain.Draft{}, err
		}
		response, err := json.Marshal(draftReplay{DraftID: draftID})
		if err != nil {
			return domain.Draft{}, err
		}
		if _, err := queries.UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{ID: record.ID, StatusCode: status, ResponseJson: response}); err != nil {
			return domain.Draft{}, err
		}
	}
	id, err := storedb.ParseUUID(draftID)
	if err != nil {
		return domain.Draft{}, domain.ErrConflict
	}
	return GetInTx(ctx, queries, project, id)
}

// OptionalUUID reads an id that may be empty; one that is no UUID names no place in the saga.
func OptionalUUID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	id, err := storedb.ParseUUID(value)
	if err != nil {
		return pgtype.UUID{}, domain.ErrNoDestination
	}
	return id, nil
}

// PlaceInTx checks a place stands in the saga: a live book of the project and a live chapter of that book.
func PlaceInTx(ctx context.Context, tx pgx.Tx, project pgtype.UUID, place domain.Place) (pgtype.UUID, pgtype.UUID, error) {
	book, err := OptionalUUID(place.BookID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	chapter, err := OptionalUUID(place.AfterChapterID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	if !book.Valid {
		return book, chapter, nil
	}
	var found bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM books WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL)
		  AND ($3::uuid IS NULL OR EXISTS (
		    SELECT 1 FROM chapters WHERE id = $3 AND book_id = $1 AND project_id = $2 AND deleted_at IS NULL
		  ))
	`, book, project, chapter).Scan(&found); err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	if !found {
		return pgtype.UUID{}, pgtype.UUID{}, domain.ErrNoDestination
	}
	return book, chapter, nil
}

// CreateChapterDraft keeps a draft under its idempotency key.
func (repository *Repository) CreateChapterDraft(ctx context.Context, command service.CreateCommand) (domain.Draft, error) {
	var draft domain.Draft
	err := repository.WriteTx(ctx, command.Write, false, func(tx pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		var err error
		draft, err = Recorded(ctx, queries, project, command.Replay, 201, func() (string, error) {
			book, chapter, err := PlaceInTx(ctx, tx, project, command.Place)
			if err != nil {
				return "", err
			}
			id, err := queries.CreateChapterDraft(ctx, sqlc.CreateChapterDraftParams{
				ProjectID: project, Title: command.Title, BookID: book, AfterChapterID: chapter,
				Idea: command.Idea, Kind: string(command.Kind), Body: command.Text, Actor: actor,
			})
			return storedb.UUIDString(id), err
		})
		return err
	})
	return draft, err
}

// LockedDraft locks a draft of the project and reads what a write decides by. A Job is known only as active
// or not: its text is not read again.
func LockedDraft(ctx context.Context, queries *sqlc.Queries, project pgtype.UUID, draftID string) (pgtype.UUID, domain.Draft, error) {
	id, err := storedb.ParseUUID(draftID)
	if err != nil {
		return pgtype.UUID{}, domain.Draft{}, domain.ErrNotFound
	}
	row, err := queries.LockChapterDraft(ctx, sqlc.LockChapterDraftParams{ProjectID: project, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, domain.Draft{}, domain.ErrNotFound
	}
	if err != nil {
		return pgtype.UUID{}, domain.Draft{}, err
	}
	draft := domain.Draft{
		ID: draftID, Title: row.Title, Idea: row.Idea, Kind: domain.Kind(row.Kind), Text: row.Body, Revision: row.Revision,
	}
	if row.BookID.Valid {
		draft.Place.BookID = storedb.UUIDString(row.BookID)
	}
	if row.AfterChapterID.Valid {
		draft.Place.AfterChapterID = storedb.UUIDString(row.AfterChapterID)
	}
	if row.AddedChapterID.Valid {
		draft.AddedChapterID = storedb.UUIDString(row.AddedChapterID)
	}
	if row.PlanNumber.Valid {
		draft.PlanNumber = int(row.PlanNumber.Int32)
	}
	if row.EditsChapterID.Valid {
		draft.Edits = &domain.Edit{ChapterID: storedb.UUIDString(row.EditsChapterID), Revision: row.EditsRevision.Int64, PreviousRevision: row.EditsPreviousRevision.Int64}
	}
	if row.ChatID.Valid {
		draft.ChatID = storedb.UUIDString(row.ChatID)
	}
	if row.JobID.Valid {
		active, err := queries.ChapterDraftJobActive(ctx, sqlc.ChapterDraftJobActiveParams{JobID: row.JobID, ProjectID: project})
		if err != nil {
			return pgtype.UUID{}, domain.Draft{}, err
		}
		if active {
			draft.Job = &domain.Job{Status: "running"}
		}
	}
	return id, draft, nil
}

// UpdateChapterDraft applies an edit at the expected revision.
func (repository *Repository) UpdateChapterDraft(ctx context.Context, write service.Write, draftID string, expected int64, change domain.Change) (domain.Draft, error) {
	var updated domain.Draft
	err := repository.WriteTx(ctx, write, false, func(tx pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		id, draft, err := LockedDraft(ctx, queries, project, draftID)
		if err != nil {
			return err
		}
		switch {
		case draft.AddedChapterID != "":
			return domain.ErrAdded
		case draft.Job.Active():
			return domain.ErrBusy
		case draft.Revision != expected:
			return domain.ErrConflict
		}
		next := change.Apply(draft)
		book, chapter, err := OptionalPlace(next.Place)
		if err != nil {
			return err
		}
		// The author takes a piece out: the text before goes into a version (specs/2026-11-06-writing-agent.md, WA7.7).
		if change.KeepVersion {
			if _, err := SaveVersionInTx(ctx, queries, project, id, domain.NewVersion(domain.VersionBeforeDelete, draft.Text)); err != nil {
				return err
			}
		}
		if change.Place != nil {
			if book, chapter, err = PlaceInTx(ctx, tx, project, next.Place); err != nil {
				return err
			}
		}
		if err := queries.UpdateChapterDraft(ctx, sqlc.UpdateChapterDraftParams{
			ProjectID: project, ID: id, Title: next.Title, BookID: book, AfterChapterID: chapter, Idea: next.Idea, Kind: string(next.Kind),
			Body: next.Text, Actor: actor,
		}); err != nil {
			return err
		}
		// The case the author chose, or his words again (WA1).
		if change.CaseChoice != nil {
			choices := draft.PackChoices
			if choices == nil {
				choices = []domain.PackChoice{}
			}
			body, err := json.Marshal(choices)
			if err != nil {
				return err
			}
			if err := queries.SetChapterDraftDecisions(ctx, sqlc.SetChapterDraftDecisionsParams{
				ProjectID: project, ID: id, CaseChoice: pgtype.Text{String: string(*change.CaseChoice), Valid: *change.CaseChoice != ""}, PackChoices: body,
			}); err != nil {
				return err
			}
		}
		updated, err = GetInTx(ctx, queries, project, id)
		return err
	})
	return updated, err
}

// OptionalPlace reads the ids of a place without checking it stands in the saga.
func OptionalPlace(place domain.Place) (pgtype.UUID, pgtype.UUID, error) {
	book, err := OptionalUUID(place.BookID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	chapter, err := OptionalUUID(place.AfterChapterID)
	return book, chapter, err
}

// DeleteChapterDraft removes a draft at the expected revision and, in its transaction, takes the author's text out of
// the rows of all its runs (specs/2026-11-01-writer-assistant-index.md R7.3). The row stays with its status, times and
// creator, who the spend is written to. A run that goes on makes it domain.ErrBusy; an ended one writes nothing more.
func (repository *Repository) DeleteChapterDraft(ctx context.Context, write service.Write, draftID string, expected int64) error {
	return repository.WriteTx(ctx, write, false, func(tx pgx.Tx, queries *sqlc.Queries, project, _ pgtype.UUID) error {
		if err := LockProjectInTx(ctx, tx, project, false); err != nil {
			return err
		}
		id, draft, err := LockedDraft(ctx, queries, project, draftID)
		if err != nil {
			return err
		}
		switch {
		case draft.Job.Active():
			return domain.ErrBusy
		case draft.Revision != expected:
			return domain.ErrConflict
		}
		if err := queries.EraseChapterDraftJobText(ctx, sqlc.EraseChapterDraftJobTextParams{ProjectID: project, DraftID: id}); err != nil {
			return err
		}
		return queries.DeleteChapterDraft(ctx, sqlc.DeleteChapterDraftParams{ProjectID: project, ID: id})
	})
}

// NoteOf is a note as its row holds it.
func NoteOf(row sqlc.ListChapterDraftNotesRow) domain.Note {
	note := domain.Note{
		ID: storedb.UUIDString(row.ID), Number: int(row.Number), Kind: domain.NoteKind(row.Kind), Target: domain.NoteTarget(row.Target),
		Origin: row.Origin, Section: row.Section.String, Quote: row.Quote.String, Body: row.Body, Replacement: row.Replacement.String,
		Options: row.Options, Refs: row.Refs, Answer: row.Answer.String, Status: domain.NoteStatus(row.Status),
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
	if row.VariantGroup.Valid {
		note.VariantGroup = storedb.UUIDString(row.VariantGroup)
	}
	note.TurnID = row.TurnID
	note.AnchorBefore, note.AnchorAfter = row.AnchorBefore.String, row.AnchorAfter.String
	note.Blocks, note.Known, note.Effects, note.Recommend, note.Fallback = row.Blocks.String, row.Known.String, row.Effects, row.Recommend.String, row.Fallback.String
	note.Level, note.Category, note.BookQuote, note.Scene = row.Level.String, row.Category.String, row.BookQuote.String, int(row.Scene.Int32)
	if row.PackID.Valid {
		note.PackID = storedb.UUIDString(row.PackID)
	}
	return note
}

// originalSummaryInTx is what the page shows of the chapter loaded as a file, nil without one.
func originalSummaryInTx(ctx context.Context, queries *sqlc.Queries, project, id pgtype.UUID) (*domain.Original, error) {
	row, err := queries.ChapterDraftOriginalSummary(ctx, sqlc.ChapterDraftOriginalSummaryParams{ProjectID: project, DraftID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &domain.Original{FileName: row.FileName, Format: row.Format, Words: int(row.Words), LoadedAt: row.CreatedAt.Time.UTC()}, nil
}

func pgUUID(value string) (pgtype.UUID, error) {
	return storedb.ParseUUID(value)
}
