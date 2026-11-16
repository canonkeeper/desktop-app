// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	coverrepo "canonkeeper/backend/internal/repository/postgres/library"
	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	service "canonkeeper/backend/internal/services/sagaarchive"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	librarystore "canonkeeper/backend/internal/store/library"
	projectstore "canonkeeper/backend/internal/store/projects"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// openOperation names the open in idempotency_records (SF3.12).
const openOperation = "saga_archive.open.v1"

// chapterBatch bounds the paragraphs one write of chapters carries.
const chapterBatch = 4000

// Opener writes a saga file as a new saga through the owners of its rows.
type Opener struct {
	db       *storedb.DB
	projects *projectstore.Store
	library  *librarystore.Store
	// failpoint stops an open before its commit, for the test of what a fall leaves (SF3.9).
	failpoint func() error
}

// NewOpener is the open over the stores of the build, with its quota fence.
func NewOpener(db *storedb.DB, projectsStore *projectstore.Store, libraryStore *librarystore.Store) *Opener {
	return &Opener{db: db, projects: projectsStore, library: libraryStore}
}

// WithFailpoint stops every open before its commit with the error of stop.
func (opener *Opener) WithFailpoint(stop func() error) *Opener {
	opener.failpoint = stop
	return opener
}

type replayKey struct{ scope, key, request string }

func keyOf(request service.OpenRequest) replayKey {
	hash := func(value string) string {
		sum := sha256.Sum256([]byte(value))
		return hex.EncodeToString(sum[:])
	}
	return replayKey{scope: hash("user:" + request.ActorUserID), key: hash(request.IdempotencyKey), request: request.FileSHA256}
}

// Replay is what a key already opened (SF3.12).
func (opener *Opener) Replay(ctx context.Context, request service.OpenRequest) (*service.Opened, error) {
	return replay(ctx, sqlc.New(opener.db.Pool), keyOf(request))
}

func replay(ctx context.Context, queries *sqlc.Queries, key replayKey) (*service.Opened, error) {
	record, err := queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
		ScopeHash: key.scope, OperationID: openOperation, IdempotencyKeyHash: key.key,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var opened service.Opened
	if record.RequestHash != key.request || json.Unmarshal(record.ResponseJson, &opened) != nil || opened.ProjectID == "" {
		return nil, service.ErrConflict
	}
	return &opened, nil
}

// Open writes the file in one transaction (SF3.3–SF3.9): the saga, its books, chapters and paragraphs with new ids, their
// texts, the cards, the registry and the layer, the records of the author, the notes, the chats, the key of the repeat.
// The write proof of the session is taken last, so the session is held no longer than an ordinary write (SF3.13). A request
// of the same key that committed first gives its answer, and wrote is false.
func (opener *Opener) Open(ctx context.Context, write service.OpenWrite) (service.Opened, bool, error) {
	key := keyOf(write.OpenRequest)
	var opened service.Opened
	wrote := false
	err := opener.db.WithTx(ctx, func(tx pgx.Tx) error {
		wrote = false
		queries := sqlc.New(tx)
		record, err := queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
			ScopeHash: key.scope, OperationID: openOperation, IdempotencyKeyHash: key.key, RequestHash: key.request,
			StatusCode: 201, ResponseJson: []byte("{}"), CreatedAt: storedb.Timestamptz(write.At),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			found, err := replay(ctx, queries, key)
			if err == nil && found == nil {
				err = service.ErrConflict
			}
			if found != nil {
				opened = *found
			}
			return err
		}
		if err != nil {
			return err
		}
		if opened, err = opener.write(ctx, tx, write); err != nil {
			return err
		}
		if opener.failpoint != nil {
			if err := opener.failpoint(); err != nil {
				return err
			}
		}
		actor, err := storedb.ParseUUID(write.ActorUserID)
		if err != nil {
			return projects.ErrForbidden
		}
		if _, err := authstore.LockVerifiedSessionWriteProof(ctx, tx, actor, write.Proof); err != nil {
			return err
		}
		response, err := json.Marshal(opened)
		if err != nil {
			return err
		}
		_, err = queries.UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{
			ID: record.ID, StatusCode: 201, ResponseJson: response,
		})
		wrote = err == nil
		return err
	})
	if err != nil {
		return service.Opened{}, false, err
	}
	return opened, wrote, nil
}

// written is a chapter the open wrote: its new id and book, the digest of its rows and, for a chapter a note is made on,
// the new ids of its paragraphs by their ids in the file.
type written struct {
	id, book    string
	bookGen     int64
	contentHash string
	published   bool
	paragraphs  map[string]string
}

type opening struct {
	ctx       context.Context
	tx        pgx.Tx
	library   *librarystore.Store
	archive   *domain.Archive
	write     service.OpenWrite
	projectID string
	books     map[string]librarystore.ArchiveBookRow
	chapters  map[string]written
	noted     map[string]bool
}

func (opener *Opener) write(ctx context.Context, tx pgx.Tx, write service.OpenWrite) (service.Opened, error) {
	archive := write.Archive
	name, renamed, err := sagaName(ctx, tx, write.ActorUserID, archive, write.Origin)
	if err != nil {
		return service.Opened{}, err
	}
	var projectID, membershipID string
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text, gen_random_uuid()::text`).Scan(&projectID, &membershipID); err != nil {
		return service.Opened{}, err
	}
	if _, err := opener.projects.CreateProjectInTx(ctx, tx, projects.CreateProjectStoreInput{
		ProjectID: projectID, OwnerUserID: write.ActorUserID, OwnerMembershipID: membershipID,
		Title: name, Description: archive.Saga.Description, CreatedAt: write.At,
	}); err != nil {
		return service.Opened{}, err
	}
	open := &opening{
		ctx: ctx, tx: tx, library: opener.library, archive: archive, write: write, projectID: projectID,
		books: map[string]librarystore.ArchiveBookRow{}, chapters: map[string]written{}, noted: map[string]bool{},
	}
	for _, note := range archive.Notes {
		if note.Source != nil && note.Source.Kind == domain.NoteSourceChapter {
			open.noted[note.Source.Chapter] = true
		}
	}
	steps := []func() error{open.writeBooks, open.writeChapters}
	for _, step := range steps {
		if err := step(); err != nil {
			return service.Opened{}, err
		}
	}
	report, err := open.writeMemory()
	if err != nil {
		return service.Opened{}, err
	}
	if err := open.writeRecords(&report); err != nil {
		return service.Opened{}, err
	}
	published := false
	for _, chapter := range open.chapters {
		published = published || chapter.published
	}
	if err := opener.library.FinishArchiveTextInTx(ctx, tx, projectID, 1, published); err != nil {
		return service.Opened{}, err
	}
	report.Books, report.Chapters = len(archive.Books), len(archive.Chapters)
	report.ChatsNotRead, report.CoversSkipped = append([]string{}, archive.Manifest.ChatsNotRead...), write.Skipped
	report.LeftOut, report.Sections = archive.Manifest.LeftOut, append([]string{}, archive.Sections...)
	return service.Opened{ProjectID: projectID, Name: name, Renamed: renamed, Report: report}, nil
}

// sagaName is the name of the new saga: the name of the file, or «<name> — из файла от <date>» when the author has a live
// saga of that name — «<name> — с сайта от <date>» of a saga transferred from the site (specs/2026-11-16-desk-site-saga.md
// TR5.2) —, cut with «…» to the limit of a name (SF3.3).
func sagaName(ctx context.Context, tx pgx.Tx, actorUserID string, archive *domain.Archive, origin service.Origin) (string, bool, error) {
	name := archive.Saga.Name
	var taken bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM projects WHERE owner_user_id = $1::uuid AND name = $2 AND deleted_at IS NULL)
	`, actorUserID, name).Scan(&taken); err != nil || !taken {
		return name, false, err
	}
	from := " — из файла от "
	if origin == service.FromSite {
		from = " — с сайта от "
	}
	suffix := from + archive.Manifest.CreatedAt.UTC().Format("02.01.2006")
	room := domain.MaxSagaNameRunes - utf8.RuneCountInString(suffix)
	if runes := []rune(name); len(runes) > room {
		name = string(runes[:room-1]) + "…"
	}
	return name + suffix, true, nil
}

func (open *opening) writeBooks() error {
	published := map[string]bool{}
	for _, chapter := range open.archive.Chapters {
		published[chapter.Book] = published[chapter.Book] || chapter.PublishedOrder != nil
	}
	for _, book := range open.archive.Books {
		number := strconv.Itoa(book.Order)
		if book.DisplayNumber != nil {
			number = *book.DisplayNumber
		}
		row, err := open.library.CreateArchiveBookInTx(open.ctx, open.tx, librarystore.ArchiveBook{
			ProjectID: open.projectID, ProjectGeneration: 1, KeyNumber: book.KeyNumber, Order: book.Order, Title: book.Title,
			Subtitle: book.Subtitle, CoverColor: book.CoverColor, DisplayNumber: number, Published: published[book.Ref], At: open.write.At,
		})
		if err != nil {
			return err
		}
		open.books[book.Ref] = row
		if cover, taken := open.write.Covers[book.Ref]; taken {
			if err := coverrepo.InsertArchiveCoverInTx(open.ctx, open.tx, open.projectID, row.Generation, domainlibrary.ActivateCoverInput{
				Actor: domainlibrary.Actor{UserID: open.write.ActorUserID}, ArtifactID: cover.ArtifactID, BookID: row.ID.String(),
				StorageKey: cover.StorageKey, MIMEType: cover.MIME, ByteSize: cover.Size, Width: cover.Width, Height: cover.Height,
				ContentSHA256: cover.SHA256, At: open.write.At,
			}); err != nil {
				return err
			}
		}
	}
	return librarystore.RaiseBookKeyNumbersInTx(open.ctx, open.tx, open.projectID, open.archive.Saga.BookKeyNumbers)
}

// writeChapters writes the chapters as the file reads them again, the chapters of one book that follow each other in one
// write of up to chapterBatch paragraphs.
func (open *opening) writeChapters() error {
	var batch []domain.Chapter
	paragraphs := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		book := open.books[batch[0].Book]
		chapters := make([]librarystore.ArchiveChapter, len(batch))
		for index, chapter := range batch {
			inputs := make([]library.ParagraphInput, len(chapter.Paragraphs))
			for position, paragraph := range chapter.Paragraphs {
				inputs[position] = library.ParagraphInput{Kind: library.ParagraphKind(paragraph.Kind), Order: paragraph.Order, Markdown: paragraph.Markdown}
			}
			chapters[index] = librarystore.ArchiveChapter{Title: chapter.Title, Order: chapter.Order, PublishedOrder: chapter.PublishedOrder, Paragraphs: inputs}
		}
		rows, err := open.library.WriteArchiveChaptersInTx(open.ctx, open.tx, book, chapters)
		if err != nil {
			return err
		}
		for index, chapter := range batch {
			row := written{id: rows[index].ID, book: book.ID.String(), bookGen: book.Generation, contentHash: rows[index].ContentHash,
				published: chapter.PublishedOrder != nil}
			if open.noted[chapter.Ref] {
				row.paragraphs = make(map[string]string, len(chapter.Paragraphs))
				for position, paragraph := range chapter.Paragraphs {
					row.paragraphs[paragraph.ID] = rows[index].Paragraphs[position]
				}
			}
			open.chapters[chapter.Ref] = row
		}
		batch, paragraphs = nil, 0
		return nil
	}
	err := open.archive.EachChapter(func(chapter domain.Chapter) error {
		if len(batch) > 0 && (batch[0].Book != chapter.Book || paragraphs+len(chapter.Paragraphs) > chapterBatch) {
			if err := flush(); err != nil {
				return err
			}
		}
		batch = append(batch, chapter)
		paragraphs += len(chapter.Paragraphs)
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

// writeMemory writes the cards that fit their chapters under the digests of the chapters written, the registry of names
// and the layer moved to the new ids (SF3.5, SF3.6).
func (open *opening) writeMemory() (service.Report, error) {
	archive := open.archive
	var report service.Report
	cards := make([]sagamemoryrepo.ArchiveCard, 0, len(archive.Cards))
	accepted := map[string]domain.FileCard{}
	for _, card := range archive.Cards {
		if !card.Accepted {
			continue
		}
		chapter := open.chapters[card.Chapter]
		built := ""
		if card.BuiltFrom != nil {
			built = *card.BuiltFrom
		}
		cards = append(cards, sagamemoryrepo.ArchiveCard{
			ChapterID: chapter.id, ContentHash: chapter.contentHash, Card: card.Parsed, Version: card.Version, Model: card.Model, BuiltFrom: built,
		})
		accepted[card.Chapter] = card
	}
	report.Cards = len(cards)
	places := open.places()
	for _, ref := range archive.ReadingOrder() {
		if _, carded := accepted[ref]; carded {
			continue
		}
		report.ChaptersWithoutCards.Count++
		if len(report.ChaptersWithoutCards.Places) < 20 {
			report.ChaptersWithoutCards.Places = append(report.ChaptersWithoutCards.Places, places[ref])
		}
	}
	if report.ChaptersWithoutCards.Places == nil {
		report.ChaptersWithoutCards.Places = []string{}
	}
	var registry *sagamemory.NameRegistry
	var digest, model string
	if archive.Names != nil {
		registry, digest, model = &archive.Names.Names, archive.Names.Digest, archive.Names.Model
		report.NamesFresh = archive.NamesFresh
	}
	var layer *sagamemoryrepo.ArchiveLayer
	if archive.Threads != nil {
		layer = open.layer(accepted)
		report.Threads = true
	}
	return report, sagamemoryrepo.WriteArchiveMemoryInTx(open.ctx, open.tx, open.projectID, cards, registry, digest, model, layer)
}
