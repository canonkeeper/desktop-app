// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"context"
	"fmt"
	"io"
	"time"

	domain "canonkeeper/backend/internal/domain/sagaarchive"
	"canonkeeper/backend/internal/domain/sagalayer"
)

// ExportSaga saves a saga into its file (SF2): the snapshot of the base into a temporary file while the transaction is
// open, then the file to the reader, the chats as their threads are read, and the manifest. No model is called.
type ExportSaga struct {
	Snapshot  SnapshotStore
	Covers    CoverObjects
	Threads   Threads
	Temp      TempFiles
	Gate      *Gate
	Drawings  Drawings
	CreatedBy string
	Now       func() time.Time
}

// ExportInput names the saga and its owner.
type ExportInput struct {
	ActorUserID string
	ProjectID   string
}

// Prepared is a file whose part of the base is written: WriteTo sends it, Close removes what it holds.
type Prepared struct {
	// Name is the name of the file: the title of the saga, as the file of a book is named.
	Name string

	useCase *ExportSaga
	temp    TempFile
	relay   *relay
	writer  *domain.Writer
	chats   []ChatSource
	entries []domain.ChatEntry
	notRead []string
	leftOut domain.LeftOut
	saga    string
	release func()
}

// relay is the stream of the writer: the temporary file first, the reader after.
type relay struct{ out io.Writer }

func (relay *relay) Write(data []byte) (int, error) { return relay.out.Write(data) }

// Prepare reads the saga: the rights and the saga are checked and the base written into a temporary file before a byte
// goes to the reader (SF2.1, SF2.2).
func (useCase *ExportSaga) Prepare(ctx context.Context, input ExportInput) (*Prepared, error) {
	release, wait, ok := useCase.Gate.Enter(input.ActorUserID)
	if !ok {
		return nil, Busy{RetryAfter: wait}
	}
	temp, err := useCase.Temp.Create()
	if err != nil {
		release()
		return nil, err
	}
	prepared := &Prepared{useCase: useCase, temp: temp, relay: &relay{out: temp}, release: release}
	prepared.writer = domain.NewWriter(prepared.relay)
	err = useCase.Snapshot.Snapshot(ctx, input.ActorUserID, input.ProjectID, func(snapshot Snapshot) error {
		return prepared.writeBase(ctx, snapshot, input.ActorUserID)
	})
	if err == nil {
		err = prepared.writer.Flush()
	}
	if err == nil && useCase.Drawings != nil {
		prepared.leftOut.Drawings, err = useCase.Drawings(ctx, input.ProjectID)
	}
	if err != nil {
		prepared.Close()
		return nil, err
	}
	prepared.Name = prepared.saga
	return prepared, nil
}

// Close removes the temporary file and lets the gate go.
func (prepared *Prepared) Close() {
	if prepared.temp != nil {
		_ = prepared.temp.Remove()
		prepared.temp = nil
	}
	if prepared.release != nil {
		prepared.release()
		prepared.release = nil
	}
}

func (prepared *Prepared) writeBase(ctx context.Context, snapshot Snapshot, ownerUserID string) error {
	writer := prepared.writer
	saga, err := snapshot.Saga()
	if err != nil {
		return err
	}
	prepared.saga = saga.Name
	if err := writer.Saga(saga); err != nil {
		return err
	}
	books, err := snapshot.Books()
	if err != nil {
		return err
	}
	covers, err := prepared.writeBooks(ctx, books)
	if err != nil {
		return err
	}
	for _, cover := range covers {
		if err := writer.File(cover.file, cover.content); err != nil {
			return err
		}
	}
	// The notes come first: the paragraphs of a chapter are kept only for a chapter a note is made on, so the memory of a
	// save does not grow with the saga (SF2.5).
	notes, err := snapshot.Notes(ownerUserID)
	if err != nil {
		return err
	}
	noted := map[string]bool{}
	for _, note := range notes {
		noted[note.ChapterID] = true
	}
	written, err := prepared.writeChapters(snapshot, noted)
	if err != nil {
		return err
	}
	if err := prepared.writeCards(snapshot, written); err != nil {
		return err
	}
	names, err := snapshot.Names()
	if err != nil {
		return err
	}
	if names != nil {
		if err := writer.Names(*names); err != nil {
			return err
		}
	}
	layer, err := snapshot.Layer()
	if err != nil {
		return err
	}
	if layer != nil {
		if err := writer.Threads(fileLayer(*layer, written, books)); err != nil {
			return err
		}
	}
	if err := prepared.writeRecords(snapshot, notes, written); err != nil {
		return err
	}
	if prepared.leftOut, err = snapshot.LeftOut(); err != nil {
		return err
	}
	if prepared.chats, err = snapshot.Chats(ownerUserID); err != nil {
		return err
	}
	for _, chat := range prepared.chats {
		for _, turn := range chat.Turns {
			if !turn.Mine {
				prepared.leftOut.OthersTurns++
			}
		}
	}
	transcripts, err := snapshot.Transcripts()
	if err != nil {
		return err
	}
	for _, transcript := range transcripts {
		prepared.leftOut.OthersTurns += transcript.OthersTurns
		prepared.writeTranscript(domain.ChatEntry{
			Title: transcript.Title, CreatedAt: transcript.CreatedAt, LastTurnAt: transcript.LastTurnAt,
			OthersTurns: transcript.OthersTurns,
		}, transcript.Turns)
	}
	return writer.Flush()
}

type coverFile struct {
	file    string
	content []byte
}

// writeBooks writes books.jsonl and reads the covers it names; a cover that cannot be read leaves its book without one.
func (prepared *Prepared) writeBooks(ctx context.Context, books []SnapshotBook) ([]coverFile, error) {
	lines, err := prepared.writer.Books()
	if err != nil {
		return nil, err
	}
	var covers []coverFile
	for _, item := range books {
		book := item.Book
		if item.CoverKey != "" {
			if content, ok := prepared.readCover(ctx, item); ok {
				if file, known := domain.CoverFile(len(covers)+1, item.CoverMIME); known {
					book.Cover = &domain.Cover{File: file, MIME: item.CoverMIME, SHA256: item.CoverSHA256}
					covers = append(covers, coverFile{file: file, content: content})
				}
			}
		}
		if err := lines.Add(book); err != nil {
			return nil, err
		}
	}
	return covers, lines.Close()
}

func (prepared *Prepared) readCover(ctx context.Context, book SnapshotBook) ([]byte, bool) {
	object, err := prepared.useCase.Covers.Open(ctx, book.CoverKey)
	if err != nil {
		return nil, false
	}
	defer func() { _ = object.Close() }()
	content, err := io.ReadAll(io.LimitReader(object, domain.MaxCoverBytes+1))
	if err != nil || int64(len(content)) > domain.MaxCoverBytes {
		return nil, false
	}
	return content, true
}

// writtenChapter is what the file says of a chapter it carries: the digest of its paragraphs and, for a chapter a note is
// made on, their ids.
type writtenChapter struct {
	digest     string
	book       string
	paragraphs map[string]struct{}
}

func (prepared *Prepared) writeChapters(snapshot Snapshot, noted map[string]bool) (map[string]writtenChapter, error) {
	lines, err := prepared.writer.Chapters()
	if err != nil {
		return nil, err
	}
	written := map[string]writtenChapter{}
	err = snapshot.Chapters(func(chapter domain.Chapter) error {
		digest, ok := domain.ContentHash(chapter.Paragraphs)
		if !ok {
			return fmt.Errorf("saga archive: chapter %s: %w", chapter.Ref, domain.ErrUnwritable)
		}
		var ids map[string]struct{}
		if noted[chapter.Ref] {
			ids = make(map[string]struct{}, len(chapter.Paragraphs))
			for _, paragraph := range chapter.Paragraphs {
				ids[paragraph.ID] = struct{}{}
			}
		}
		written[chapter.Ref] = writtenChapter{digest: digest, book: chapter.Book, paragraphs: ids}
		return lines.Add(chapter)
	})
	if err != nil {
		return nil, err
	}
	return written, lines.Close()
}

// writeCards writes the card of a chapter whose digest is the one of the text the file carries (SF1.4).
func (prepared *Prepared) writeCards(snapshot Snapshot, written map[string]writtenChapter) error {
	lines, err := prepared.writer.Cards()
	if err != nil {
		return err
	}
	if err := snapshot.Cards(func(card domain.Card) error {
		if chapter, carried := written[card.Chapter]; carried && chapter.digest == card.ContentHash {
			return lines.Add(card)
		}
		return nil
	}); err != nil {
		return err
	}
	return lines.Close()
}

// fileLayer is the layer with the refs of the file: the stamps, books and places of chapters the file does not carry go.
func fileLayer(layer Layer, written map[string]writtenChapter, books []SnapshotBook) domain.Threads {
	carried := func(chapter string) bool {
		_, ok := written[chapter]
		return ok
	}
	shelf := map[string]bool{}
	for _, book := range books {
		shelf[book.Book.Ref] = true
	}
	refs := func(list []sagalayer.Ref) []sagalayer.Ref {
		kept := make([]sagalayer.Ref, 0, len(list))
		for _, ref := range list {
			if carried(ref.Chapter) {
				kept = append(kept, ref)
			}
		}
		return kept
	}
	single := func(ref sagalayer.Ref) sagalayer.Ref {
		if carried(ref.Chapter) {
			return ref
		}
		return sagalayer.Ref{}
	}
	threads := func(list []sagalayer.Thread) []sagalayer.Thread {
		kept := make([]sagalayer.Thread, len(list))
		for index, thread := range list {
			thread.First, thread.Last = refs(thread.First), refs(thread.Last)
			kept[index] = thread
		}
		return kept
	}
	out := domain.Threads{Model: layer.Model, Stamps: []domain.Stamp{}, Books: []sagalayer.Book{}, Threads: threads(layer.Threads)}
	for _, stamp := range layer.Chapters {
		if !carried(stamp.Chapter) {
			continue
		}
		filed := domain.Stamp{Chapter: stamp.Chapter, H: stamp.Hash, CardVersion: stamp.CardVersion}
		if stamp.Card != "" {
			card := stamp.Card
			filed.K = &card
		}
		out.Stamps = append(out.Stamps, filed)
	}
	for _, book := range layer.Books {
		if !shelf[book.ID] {
			continue
		}
		for index := range book.Arcs {
			book.Arcs[index].From, book.Arcs[index].To = single(book.Arcs[index].From), single(book.Arcs[index].To)
		}
		book.Threads = threads(book.Threads)
		out.Books = append(out.Books, book)
	}
	return out
}

// writeRecords writes the records of the saga whole and the notes of the owner (SF1.7): a chapter of a record the file
// does not carry goes, and a note made on it keeps its quote in its body.
func (prepared *Prepared) writeRecords(snapshot Snapshot, notes []SnapshotNote, written map[string]writtenChapter) error {
	writer := prepared.writer
	hidden, err := snapshot.MapHidden()
	if err != nil {
		return err
	}
	if err := addAll(writer.MapHidden, hidden); err != nil {
		return err
	}
	materials, err := snapshot.Materials()
	if err != nil {
		return err
	}
	if err := addAll(writer.Materials, materials); err != nil {
		return err
	}
	memory, err := snapshot.Memory()
	if err != nil {
		return err
	}
	for index := range memory {
		if _, carried := written[memory[index].Chapter]; !carried {
			memory[index].Chapter = ""
		}
	}
	if err := addAll(writer.Memory, memory); err != nil {
		return err
	}
	filed := make([]domain.Note, 0, len(notes))
	for _, note := range notes {
		filed = append(filed, fileNote(note, written))
	}
	return addAll(writer.Notes, filed)
}

func fileNote(note SnapshotNote, written map[string]writtenChapter) domain.Note {
	filed := domain.Note{Title: note.Title, Body: note.Body, CreatedAt: note.CreatedAt, UpdatedAt: note.UpdatedAt}
	switch chapter, carried := written[note.ChapterID]; {
	case note.Kind == domain.NoteSourceChat:
		filed.Source = &domain.NoteSource{Kind: domain.NoteSourceChat, Quote: note.Quote}
	case note.Kind == domain.NoteSourceChapter && carried:
		source := &domain.NoteSource{Kind: domain.NoteSourceChapter, Chapter: note.ChapterID, Quote: note.Quote}
		if _, own := chapter.paragraphs[note.ParagraphID]; own {
			source.Paragraph, source.Range = note.ParagraphID, note.Range
		}
		filed.Source = source
	default:
		filed.Body = domain.QuotedBody(note.Quote, note.Body)
	}
	return filed
}

func addAll[T any](begin func() (*domain.Lines, error), values []T) error {
	lines, err := begin()
	if err != nil {
		return err
	}
	for _, value := range values {
		if err := lines.Add(value); err != nil {
			return err
		}
	}
	return lines.Close()
}
