// SPDX-License-Identifier: AGPL-3.0-only

// Package sagaarchive reads a saga for its file in one snapshot and writes a file as a new saga in one transaction
// (specs/2026-11-13-desk-sagafile.md SF2, SF3): the rows of every owner, through the owners' own code.
package sagaarchive

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"canonkeeper/backend/internal/domain/authormemory"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
	"canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/projects"
	memoryrepo "canonkeeper/backend/internal/repository/postgres/authormemory"
	notesrepo "canonkeeper/backend/internal/repository/postgres/notes"
	service "canonkeeper/backend/internal/services/sagaarchive"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Repository is the saga file over PostgreSQL.
type Repository struct {
	db *storedb.DB
}

func New(db *storedb.DB) *Repository { return &Repository{db: db} }

// Snapshot reads the saga in one transaction that sees one moment of the base (SF2.2): a reader who is not a member of a
// live saga gets ErrNotFound, a member below the owner ErrForbidden.
func (repository *Repository) Snapshot(ctx context.Context, actorUserID, projectID string, read func(service.Snapshot) error) error {
	actor, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return service.ErrNotFound
	}
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.ErrNotFound
	}
	tx, err := repository.db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	role, err := sqlc.New(tx).GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{ActorUserID: actor, ProjectID: project})
	if errors.Is(err, pgx.ErrNoRows) {
		return service.ErrNotFound
	}
	if err != nil {
		return err
	}
	if projects.Role(role) != projects.RoleOwner {
		return service.ErrForbidden
	}
	if err := read(&snapshot{ctx: ctx, tx: tx, project: project, projectID: projectID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type snapshot struct {
	ctx       context.Context
	tx        pgx.Tx
	project   pgtype.UUID
	projectID string
}

func (snapshot *snapshot) Saga() (domain.Saga, error) {
	var saga domain.Saga
	err := snapshot.tx.QueryRow(snapshot.ctx, `
		SELECT name, description, book_key_numbers FROM projects WHERE id = $1 AND deleted_at IS NULL
	`, snapshot.project).Scan(&saga.Name, &saga.Description, &saga.BookKeyNumbers)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Saga{}, service.ErrNotFound
	}
	return saga, err
}

func (snapshot *snapshot) Books() ([]service.SnapshotBook, error) {
	rows, err := snapshot.tx.Query(snapshot.ctx, `
		SELECT book.id::text, book.key_number, book."order", book.title, book.subtitle, book.display_number, book.cover_color,
		       coalesce(cover.storage_key, ''), coalesce(cover.mime_type, ''), cover.content_sha256
		FROM books AS book
		LEFT JOIN book_cover_artifacts AS cover
		  ON cover.book_id = book.id AND cover.book_lifecycle_generation = book.lifecycle_generation AND cover.state = 'active'
		WHERE book.project_id = $1 AND book.deleted_at IS NULL
		ORDER BY book."order", book.id
	`, snapshot.project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var books []service.SnapshotBook
	for rows.Next() {
		var item service.SnapshotBook
		var number string
		var sum []byte
		if err := rows.Scan(&item.Book.Ref, &item.Book.KeyNumber, &item.Book.Order, &item.Book.Title, &item.Book.Subtitle,
			&number, &item.Book.CoverColor, &item.CoverKey, &item.CoverMIME, &sum); err != nil {
			return nil, err
		}
		item.Book.DisplayNumber = &number
		item.CoverSHA256 = hex.EncodeToString(sum)
		books = append(books, item)
	}
	return books, rows.Err()
}

// Chapters streams the paragraphs of the text of every chapter — the published one, or the draft of a chapter without
// one — so a saga of any size costs the memory of one chapter (SF2.5). The order of a chapter is its place in the book
// and the draft text; the published text keeps its own (SF1.3).
func (snapshot *snapshot) Chapters(each func(domain.Chapter) error) error {
	rows, err := snapshot.tx.Query(snapshot.ctx, `
		SELECT chapter.book_id::text, chapter.id::text, chapter."order",
		       CASE WHEN chapter.published_revision IS NULL THEN NULL ELSE published.chapter_order END,
		       chapter.title, paragraph.id::text, paragraph."order", paragraph.kind, paragraph.markdown
		FROM chapters AS chapter
		JOIN books AS book ON book.id = chapter.book_id AND book.project_id = chapter.project_id AND book.deleted_at IS NULL
		LEFT JOIN chapter_source_members AS published
		  ON published.project_id = chapter.project_id AND published.source_view = 'published' AND published.chapter_id = chapter.id
		JOIN chapter_paragraphs AS paragraph
		  ON paragraph.chapter_id = chapter.id
		 AND paragraph.revision = coalesce(chapter.published_revision, chapter.revision)
		 AND paragraph.variant = CASE WHEN chapter.published_revision IS NULL THEN 'draft' ELSE 'published' END
		WHERE chapter.project_id = $1 AND chapter.deleted_at IS NULL
		ORDER BY book."order", book.id, chapter."order", chapter.id, paragraph."order", paragraph.id
	`, snapshot.project)
	if err != nil {
		return err
	}
	defer rows.Close()
	var current *domain.Chapter
	for rows.Next() {
		var book, chapterID, title string
		var order int
		var published *int
		var paragraph domain.Paragraph
		if err := rows.Scan(&book, &chapterID, &order, &published, &title, &paragraph.ID, &paragraph.Order, &paragraph.Kind, &paragraph.Markdown); err != nil {
			return err
		}
		if current != nil && current.Ref != chapterID {
			if err := each(*current); err != nil {
				return err
			}
			current = nil
		}
		if current == nil {
			current = &domain.Chapter{Ref: chapterID, Book: book, Order: order, PublishedOrder: published, Title: title}
		}
		current.Paragraphs = append(current.Paragraphs, paragraph)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if current != nil {
		return each(*current)
	}
	return nil
}

func (snapshot *snapshot) Cards(each func(domain.Card) error) error {
	rows, err := snapshot.tx.Query(snapshot.ctx, `
		SELECT card.chapter_id::text, card.content_hash, card.version, card.model, card.built_from, card.card_json
		FROM chapter_cards AS card
		JOIN chapters AS chapter ON chapter.id = card.chapter_id AND chapter.project_id = card.project_id AND chapter.deleted_at IS NULL
		WHERE card.project_id = $1
		ORDER BY card.chapter_id, card.content_hash
	`, snapshot.project)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var card domain.Card
		var digest, built []byte
		var body []byte
		if err := rows.Scan(&card.Chapter, &digest, &card.Version, &card.Model, &built, &body); err != nil {
			return err
		}
		card.ContentHash, card.Card = hex.EncodeToString(digest), json.RawMessage(body)
		if len(built) > 0 {
			from := hex.EncodeToString(built)
			card.BuiltFrom = &from
		}
		if err := each(card); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (snapshot *snapshot) Names() (*domain.Names, error) {
	var names domain.Names
	var body []byte
	err := snapshot.tx.QueryRow(snapshot.ctx, `
		SELECT digest, model, names_json FROM saga_names WHERE project_id = $1
	`, snapshot.project).Scan(&names.Digest, &names.Model, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &names.Names); err != nil {
		return nil, err
	}
	return &names, nil
}

func (snapshot *snapshot) Layer() (*service.Layer, error) {
	var layer service.Layer
	var chapters, books, threads []byte
	err := snapshot.tx.QueryRow(snapshot.ctx, `
		SELECT model, chapters_json, books_json, threads_json
		FROM saga_threads WHERE project_id = $1 AND digest IS NOT NULL AND digest <> ''
	`, snapshot.project).Scan(&layer.Model, &chapters, &books, &threads)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, part := range []struct {
		body   []byte
		target any
	}{{chapters, &layer.Chapters}, {books, &layer.Books}, {threads, &layer.Threads}} {
		if len(part.body) > 0 {
			if err := json.Unmarshal(part.body, part.target); err != nil {
				return nil, err
			}
		}
	}
	if layer.Books == nil {
		layer.Books = []sagalayer.Book{}
	}
	return &layer, nil
}

func (snapshot *snapshot) MapHidden() ([]domain.MapHidden, error) {
	rows, err := sqlc.New(snapshot.tx).ListSagaMapHidden(snapshot.ctx, snapshot.project)
	if err != nil {
		return nil, err
	}
	items := make([]domain.MapHidden, len(rows))
	for index, row := range rows {
		items[len(rows)-1-index] = domain.MapHidden{
			NodeKey: row.NodeKey, OtherKey: row.OtherKey, NodeName: row.NodeName, OtherName: row.OtherName, CreatedAt: row.CreatedAt.Time.UTC(),
		}
	}
	return items, nil
}

func (snapshot *snapshot) Materials() ([]domain.Material, error) {
	rows, err := snapshot.tx.Query(snapshot.ctx, `
		SELECT name, kind, description, properties, section, source_name, source_kind, created_at, updated_at
		FROM author_materials WHERE project_id = $1
		ORDER BY created_at, id
	`, snapshot.project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var materials []domain.Material
	for rows.Next() {
		var material domain.Material
		var properties []byte
		if err := rows.Scan(&material.Name, &material.Kind, &material.Description, &properties, &material.Section,
			&material.SourceName, &material.SourceKind, &material.CreatedAt, &material.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(properties, &material.Properties); err != nil {
			return nil, err
		}
		if material.Properties == nil {
			material.Properties = []domain.Property{}
		}
		material.CreatedAt, material.UpdatedAt = material.CreatedAt.UTC(), material.UpdatedAt.UTC()
		materials = append(materials, material)
	}
	return materials, rows.Err()
}

func (snapshot *snapshot) Memory() ([]domain.Memory, error) {
	records, err := memoryrepo.SagaRecordsInTx(snapshot.ctx, snapshot.tx, snapshot.projectID)
	if err != nil {
		return nil, err
	}
	memory := make([]domain.Memory, len(records))
	for index, record := range records {
		memory[index] = domain.Memory{
			Kind: string(record.Kind), Body: record.Text, Name: record.Name, Chapter: record.ChapterID,
			Origin: string(record.Origin.Kind), CreatedAt: record.CreatedAt.UTC(), UpdatedAt: record.UpdatedAt.UTC(),
		}
		if record.Origin.Kind == "" {
			memory[index].Origin = string(authormemory.OriginManual)
		}
	}
	return memory, nil
}

func (snapshot *snapshot) Notes(ownerUserID string) ([]service.SnapshotNote, error) {
	notes, err := notesrepo.SagaNotesInTx(snapshot.ctx, snapshot.tx, ownerUserID, snapshot.projectID)
	if err != nil {
		return nil, err
	}
	out := make([]service.SnapshotNote, 0, len(notes))
	for _, note := range notes {
		item := service.SnapshotNote{
			Title: note.Title, Body: note.Body, CreatedAt: note.CreatedAt.UTC(), UpdatedAt: note.UpdatedAt.UTC(),
			Kind: note.Source.Kind, Quote: note.Source.Quote,
		}
		if locator := note.Source.Locator; locator != nil {
			item.ChapterID, item.Range = locator.ChapterID, locator.Range
			if locator.ParagraphID != nil {
				item.ParagraphID = *locator.ParagraphID
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func (snapshot *snapshot) Transcripts() ([]service.Transcript, error) {
	return readTranscripts(snapshot.ctx, snapshot.tx, snapshot.project)
}

// Chats reads the chats of the saga — not of a helper — with their turns: who asked each, the outcome of its Job and the
// title of the chapter attached to its question (SF1.8).
func (snapshot *snapshot) Chats(ownerUserID string) ([]service.ChatSource, error) {
	owner, err := storedb.ParseUUID(ownerUserID)
	if err != nil {
		return nil, service.ErrNotFound
	}
	rows, err := snapshot.tx.Query(snapshot.ctx, `
		SELECT chat.id::text, chat.title, chat.created_at, coalesce(chat.thread_id, ''),
		       turn.provider_turn_id, turn.created_at, coalesce(job.creator_user_id = $2, false),
		       CASE job.status WHEN 'failed' THEN $3 WHEN 'expired' THEN $3 WHEN 'canceled' THEN $4 ELSE '' END,
		       coalesce(attachment.title, '')
		FROM chat_sessions AS chat
		JOIN chat_turns AS turn ON turn.chat_id = chat.id AND turn.project_id = chat.project_id
		LEFT JOIN jobs AS job ON job.id = turn.job_id AND job.project_id = turn.project_id
		LEFT JOIN chat_turn_attachments AS attachment ON attachment.turn_id = turn.id AND attachment.chat_id = turn.chat_id
		WHERE chat.project_id = $1 AND chat.kind = 'research' AND chat.scope = 'project' AND chat.deleted_at IS NULL
		ORDER BY chat.created_at, chat.id, turn.created_at, turn.id
	`, snapshot.project, owner, domain.TurnFailed, domain.TurnInterrupted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chats []service.ChatSource
	for rows.Next() {
		var chat service.ChatSource
		var turn service.TurnSource
		var provider pgtype.Text
		if err := rows.Scan(&chat.ID, &chat.Title, &chat.CreatedAt, &chat.ThreadID, &provider, &turn.AskedAt, &turn.Mine,
			&turn.Settled, &turn.Attached); err != nil {
			return nil, err
		}
		if len(chats) == 0 || chats[len(chats)-1].ID != chat.ID {
			chat.CreatedAt = chat.CreatedAt.UTC()
			chats = append(chats, chat)
		}
		if provider.Valid && provider.String != "" {
			turn.ProviderTurnID, turn.AskedAt = provider.String, turn.AskedAt.UTC()
			current := &chats[len(chats)-1]
			current.Turns = append(current.Turns, turn)
		}
	}
	return chats, rows.Err()
}

// LeftOut counts what the saga has and the file does not carry: the chapters in work and the books and chapters in
// «Недавно удалённые» (SF1.9).
func (snapshot *snapshot) LeftOut() (domain.LeftOut, error) {
	var left domain.LeftOut
	err := snapshot.tx.QueryRow(snapshot.ctx, `
		SELECT (SELECT count(*) FROM chapter_drafts WHERE project_id = $1 AND added_chapter_id IS NULL),
		       (SELECT count(*) FROM books WHERE project_id = $1 AND deleted_at IS NOT NULL),
		       (SELECT count(*) FROM chapters AS chapter
		        JOIN books AS book ON book.id = chapter.book_id AND book.deleted_at IS NULL
		        WHERE chapter.project_id = $1 AND chapter.deleted_at IS NOT NULL)
	`, snapshot.project).Scan(&left.Drafts, &left.TrashedBooks, &left.TrashedChapters)
	return left, err
}

// readTranscripts reads the chats a saga file brought into a saga, oldest first.
func readTranscripts(ctx context.Context, tx pgx.Tx, project pgtype.UUID) ([]service.Transcript, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, title, created_at, last_turn_at, others_turns, turns
		FROM saga_transcripts WHERE project_id = $1
		ORDER BY created_at, id
	`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var transcripts []service.Transcript
	for rows.Next() {
		var transcript service.Transcript
		var turns []byte
		var created, last time.Time
		if err := rows.Scan(&transcript.ID, &transcript.Title, &created, &last, &transcript.OthersTurns, &turns); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(turns, &transcript.Turns); err != nil {
			return nil, err
		}
		transcript.CreatedAt, transcript.LastTurnAt = created.UTC(), last.UTC()
		transcripts = append(transcripts, transcript)
	}
	return transcripts, rows.Err()
}

// StrayCovers are the files of covers an open listed before its transaction and no cover row names: the transaction did
// not commit, the process fell before it took them away (SF3.9).
func (repository *Repository) StrayCovers(ctx context.Context, keys []string) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	rows, err := repository.db.Pool.Query(ctx, `
		SELECT key FROM unnest($1::text[]) AS key
		WHERE NOT EXISTS (SELECT 1 FROM book_cover_artifacts AS cover WHERE cover.storage_key = key)
	`, keys)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// readable checks that the reader may read a live saga.
func readable(ctx context.Context, tx pgx.Tx, actorUserID, projectID string) (pgtype.UUID, error) {
	actor, err := storedb.ParseUUID(actorUserID)
	if err != nil {
		return pgtype.UUID{}, service.ErrNotFound
	}
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return pgtype.UUID{}, service.ErrNotFound
	}
	role, err := sqlc.New(tx).GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{ActorUserID: actor, ProjectID: project})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !projects.CanReadProject(projects.Role(role)) {
		return pgtype.UUID{}, service.ErrNotFound
	}
	return project, err
}

// Transcripts lists the chats from files of a saga, newest first.
func (repository *Repository) Transcripts(ctx context.Context, actorUserID, projectID string) ([]service.Transcript, error) {
	var transcripts []service.Transcript
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		project, err := readable(ctx, tx, actorUserID, projectID)
		if err != nil {
			return err
		}
		transcripts, err = readTranscripts(ctx, tx, project)
		return err
	})
	slices.Reverse(transcripts)
	return transcripts, err
}

// Transcript reads one chat from a file of a saga.
func (repository *Repository) Transcript(ctx context.Context, actorUserID, projectID, transcriptID string) (service.Transcript, error) {
	var found service.Transcript
	err := repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		project, err := readable(ctx, tx, actorUserID, projectID)
		if err != nil {
			return err
		}
		transcripts, err := readTranscripts(ctx, tx, project)
		if err != nil {
			return err
		}
		for _, transcript := range transcripts {
			if transcript.ID == transcriptID {
				found = transcript
				return nil
			}
		}
		return service.ErrNotFound
	})
	return found, err
}
