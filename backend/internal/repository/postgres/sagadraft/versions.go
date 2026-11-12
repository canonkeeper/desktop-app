package sagadraft

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	domain "canonkeeper/backend/internal/domain/sagadraft"
	service "canonkeeper/backend/internal/services/sagadraft"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"
)

// The versions of the text of a draft (specs/2026-11-06-writing-agent.md, WA7.5, WA10): kept before every action that
// changes the working text and after every writing, read by any member, taken back into the text by the author.

// CreateVersionInTx keeps a version of the working text of a draft the caller locked, by the Job that made it when
// job is valid.
func CreateVersionInTx(ctx context.Context, queries *sqlc.Queries, project, id pgtype.UUID, version domain.Version, job pgtype.UUID) (int, error) {
	params := sqlc.CreateChapterDraftVersionParams{
		ProjectID: project, DraftID: id, Reason: string(version.Reason), Text: version.Text,
		Words: int32(version.Words), AuthorWords: int32(version.AuthorWords), JobID: job,
	}
	if version.Idea != nil {
		params.Idea = pgtype.Text{String: *version.Idea, Valid: true}
	}
	if version.PlanNumber > 0 {
		params.PlanNumber = pgtype.Int4{Int32: int32(version.PlanNumber), Valid: true}
	}
	if version.PackID != "" {
		var err error
		if params.PackID, err = storedb.ParseUUID(version.PackID); err != nil {
			return 0, domain.ErrInvalid
		}
	}
	if version.Place != "" {
		params.Place = pgtype.Text{String: version.Place, Valid: true}
	}
	if len(version.Support) > 0 {
		body, err := json.Marshal(version.Support)
		if err != nil {
			return 0, err
		}
		params.Support = body
	}
	number, err := queries.CreateChapterDraftVersion(ctx, params)
	return int(number), err
}

// SaveVersionInTx keeps a version of the working text of a draft the caller locked, outside a Job (before «Оставить
// этот», before taking a piece of a version).
func SaveVersionInTx(ctx context.Context, queries *sqlc.Queries, project, id pgtype.UUID, version domain.Version) (int, error) {
	return CreateVersionInTx(ctx, queries, project, id, version, pgtype.UUID{})
}

// ChapterDraftVersions lists the versions of the text of a draft, the latest first, for a reader of the project.
func (repository *Repository) ChapterDraftVersions(ctx context.Context, actor service.Actor, draftID string) ([]domain.Version, error) {
	var versions []domain.Version
	err := repository.ReadTx(ctx, actor, func(queries *sqlc.Queries, project pgtype.UUID) error {
		id, err := storedb.ParseUUID(draftID)
		if err != nil {
			return domain.ErrNotFound
		}
		if _, err := GetInTx(ctx, queries, project, id); err != nil {
			return err
		}
		rows, err := queries.ListChapterDraftVersions(ctx, sqlc.ListChapterDraftVersionsParams{ProjectID: project, DraftID: id})
		if err != nil {
			return err
		}
		versions = make([]domain.Version, len(rows))
		for i, row := range rows {
			versions[i] = domain.Version{Number: int(row.Number), Reason: domain.VersionReason(row.Reason), Words: int(row.Words),
				AuthorWords: int(row.AuthorWords), PlanNumber: int(row.PlanNumber.Int32), CreatedAt: row.CreatedAt.Time.UTC(),
				Place: row.Place.String, Scenes: int(row.Scenes)}
		}
		return nil
	})
	return versions, err
}

// ChapterDraftVersion reads one version of the text of a draft whole.
func (repository *Repository) ChapterDraftVersion(ctx context.Context, actor service.Actor, draftID string, number int) (domain.Version, error) {
	var version domain.Version
	err := repository.ReadTx(ctx, actor, func(queries *sqlc.Queries, project pgtype.UUID) error {
		id, err := storedb.ParseUUID(draftID)
		if err != nil {
			return domain.ErrNotFound
		}
		version, err = VersionInTx(ctx, queries, project, id, number)
		return err
	})
	return version, err
}

// VersionInTx reads one version of the text of a draft whole.
func VersionInTx(ctx context.Context, queries *sqlc.Queries, project, id pgtype.UUID, number int) (domain.Version, error) {
	row, err := queries.GetChapterDraftVersion(ctx, sqlc.GetChapterDraftVersionParams{ProjectID: project, DraftID: id, Number: int32(number)})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Version{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Version{}, err
	}
	version := domain.Version{
		Number: int(row.Number), Reason: domain.VersionReason(row.Reason), Text: row.Text, Words: int(row.Words), AuthorWords: int(row.AuthorWords),
		PlanNumber: int(row.PlanNumber.Int32), CreatedAt: row.CreatedAt.Time.UTC(), Place: row.Place.String,
	}
	if row.Idea.Valid {
		version.Idea = &row.Idea.String
	}
	if row.PackID.Valid {
		version.PackID = storedb.UUIDString(row.PackID)
	}
	if row.JobID.Valid {
		version.JobID = storedb.UUIDString(row.JobID)
	}
	if len(row.Support) > 0 {
		if err := json.Unmarshal(row.Support, &version.Support); err != nil {
			return domain.Version{}, err
		}
	}
	return version, nil
}

// TakeChapterDraftVersion puts a version of the text, or a piece of it, into the working text at the revision the page
// saw, for an editor or above (WA7.5): the text before goes into a version first, and a piece starts the Job that
// checks its seams. A spent quota of rewrites leaves the piece in the text unchecked: the take is the author's edit,
// the check only a help; the Job of the draft says whether the seams are being checked.
func (repository *Repository) TakeChapterDraftVersion(ctx context.Context, write service.Write, draftID string, number int, expected int64, take domain.Take) (domain.Draft, error) {
	var taken domain.Draft
	err := repository.WriteTx(ctx, write, false, func(tx pgx.Tx, queries *sqlc.Queries, project, actor pgtype.UUID) error {
		if err := LockProjectInTx(ctx, tx, project, false); err != nil {
			return err
		}
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
		version, err := VersionInTx(ctx, queries, project, id, number)
		if err != nil {
			return err
		}
		text, seams, err := domain.TakeInto(draft.Text, version.Text, take)
		if err != nil {
			return err
		}
		if !domain.ValidBody(text, domain.MaxChapterRunes) {
			return domain.ErrInvalid
		}
		reason := domain.VersionBeforeTake
		if take.Whole {
			reason = domain.VersionBeforeRestore
		}
		before := domain.NewVersion(reason, draft.Text)
		// A piece put in place of a place names that place (specs/2026-11-11-ux9-writing-b.md WB3.1).
		if !take.Whole {
			before.Place = domain.ChangedPlace(draft.Text, text)
		}
		// A version that keeps the idea gives it back whole too, and the draft before the return keeps its idea the same
		// way: «Вернуть как было до разговора» and its «Отменить» (specs/2026-11-10-ux5-voice.md VO1.3).
		idea := draft.Idea
		if take.Whole && version.Idea != nil {
			before, idea = before.WithIdea(draft.Idea), *version.Idea
		}
		if _, err := SaveVersionInTx(ctx, queries, project, id, before); err != nil {
			return err
		}
		book, chapter, err := OptionalPlace(draft.Place)
		if err != nil {
			return err
		}
		if err := queries.UpdateChapterDraft(ctx, sqlc.UpdateChapterDraftParams{
			ProjectID: project, ID: id, Title: draft.Title, BookID: book, AfterChapterID: chapter, Idea: idea, Kind: string(draft.Kind),
			Body: text, Actor: actor,
		}); err != nil {
			return err
		}
		if len(seams) > 0 && repository.seams != nil {
			if err := repository.seams(ctx, tx, write, draftID, seams); err != nil {
				return err
			}
		}
		taken, err = GetInTx(ctx, queries, project, id)
		return err
	})
	return taken, err
}
