// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"encoding/json"

	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/authormemory"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/domain/notes"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
	"canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/domain/shelf"
	materialsrepo "canonkeeper/backend/internal/repository/postgres/authormaterials"
	memoryrepo "canonkeeper/backend/internal/repository/postgres/authormemory"
	notesrepo "canonkeeper/backend/internal/repository/postgres/notes"
	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	service "canonkeeper/backend/internal/services/sagaarchive"
)

// places names every chapter of the file by its place in the new saga, as the author reads it: the book by its place on
// the shelf, the chapter by its place among the chapters of its book in the file (sagamemory.PlacesInBooks;
// specs/2026-11-14-chapter-place-number.md CP1.1).
func (open *opening) places() map[string]string {
	shelved := make([]shelf.Book, len(open.archive.Books))
	for index, book := range open.archive.Books {
		shelved[index] = shelf.Book{ID: book.Ref, Order: book.Order}
	}
	rank := shelf.Places(shelved)
	chapters := make([]sagamemory.Chapter, len(open.archive.Chapters))
	for index, chapter := range open.archive.Chapters {
		chapters[index] = sagamemory.Chapter{ID: chapter.Ref, BookID: chapter.Book, BookOrder: rank[chapter.Book], Order: chapter.Order, Title: chapter.Title}
	}
	sagamemory.PlacesInBooks(chapters)
	places := make(map[string]string, len(chapters))
	for index := range chapters {
		places[chapters[index].ID] = chapters[index].Place()
	}
	return places
}

// layer moves the layer of the file to the new saga (SF3.6): a stamp gets the id of its new chapter and, when the layer was
// built from the card the file brings, the digest of the new chapter; another stays behind as it was. The digests of the
// books and of the build are counted over the moved stamps, the places in the threads name the new chapters, and a place
// of a chapter the file does not carry goes.
func (open *opening) layer(accepted map[string]domain.FileCard) *sagamemoryrepo.ArchiveLayer {
	threads := open.archive.Threads
	chapterOf := func(ref string) (string, bool) {
		chapter, ok := open.chapters[ref]
		return chapter.id, ok
	}
	bookOf := map[string]string{}
	for _, chapter := range open.archive.Chapters {
		bookOf[chapter.Ref] = chapter.Book
	}
	stamps := make([]sagalayer.Stamp, 0, len(threads.Stamps))
	byBook := map[string][]sagalayer.Stamp{}
	for _, stamp := range threads.Stamps {
		id, ok := chapterOf(stamp.Chapter)
		if !ok {
			continue
		}
		moved := sagalayer.Stamp{Chapter: id, Hash: stamp.H, CardVersion: stamp.CardVersion}
		if stamp.K != nil {
			moved.Card = *stamp.K
		}
		built := moved.Hash
		if moved.Card != "" {
			built = moved.Card
		}
		if card, carded := accepted[stamp.Chapter]; carded {
			cardBuilt := card.ContentHash
			if card.BuiltFrom != nil {
				cardBuilt = *card.BuiltFrom
			}
			if built == cardBuilt {
				moved.Hash, moved.Card = open.chapters[stamp.Chapter].contentHash, ""
				if card.BuiltFrom != nil {
					moved.Card = *card.BuiltFrom
				}
			}
		}
		stamps = append(stamps, moved)
		byBook[bookOf[stamp.Chapter]] = append(byBook[bookOf[stamp.Chapter]], moved)
	}
	refs := func(list []sagalayer.Ref) []sagalayer.Ref {
		kept := make([]sagalayer.Ref, 0, len(list))
		for _, ref := range list {
			if id, ok := chapterOf(ref.Chapter); ok {
				kept = append(kept, sagalayer.Ref{Chapter: id, P: ref.P})
			}
		}
		return kept
	}
	single := func(ref sagalayer.Ref) sagalayer.Ref {
		if id, ok := chapterOf(ref.Chapter); ok {
			return sagalayer.Ref{Chapter: id, P: ref.P}
		}
		return sagalayer.Ref{}
	}
	moveThreads := func(list []sagalayer.Thread) []sagalayer.Thread {
		moved := make([]sagalayer.Thread, len(list))
		for index, thread := range list {
			thread.First, thread.Last = refs(thread.First), refs(thread.Last)
			moved[index] = thread
		}
		return moved
	}
	books := make([]sagalayer.Book, 0, len(threads.Books))
	for _, book := range threads.Books {
		row, ok := open.books[book.ID]
		if !ok {
			continue
		}
		moved := sagalayer.Book{ID: row.ID.String(), Order: book.Order, Digest: sagalayer.Digest(byBook[book.ID]), Threads: moveThreads(book.Threads)}
		for _, arc := range book.Arcs {
			arc.From, arc.To = single(arc.From), single(arc.To)
			moved.Arcs = append(moved.Arcs, arc)
		}
		books = append(books, moved)
	}
	return &sagamemoryrepo.ArchiveLayer{
		Digest: sagalayer.Digest(stamps), Model: threads.Model, Chapters: stamps, Books: books, Threads: moveThreads(threads.Threads),
	}
}

// writeRecords writes the decisions about the map, the materials, the memory with its chapter in the new saga and where it
// came from, the notes of the author bound to the new chapters, and the chats as records to read (SF3.7, SF3.8).
func (open *opening) writeRecords(report *service.Report) error {
	ctx, tx, archive, actor := open.ctx, open.tx, open.archive, open.write.ActorUserID
	hidden := make([]sagamemory.MapHiddenItem, len(archive.MapHidden))
	for index, item := range archive.MapHidden {
		hidden[index] = sagamemory.MapHiddenItem{NodeKey: item.NodeKey, OtherKey: item.OtherKey, NodeName: item.NodeName, OtherName: item.OtherName, CreatedAt: item.CreatedAt}
	}
	if err := sagamemoryrepo.WriteArchiveMapHiddenInTx(ctx, tx, open.projectID, hidden); err != nil {
		return err
	}
	materials := make([]materialsrepo.ArchiveMaterial, len(archive.Materials))
	for index, material := range archive.Materials {
		materials[index] = materialsrepo.ArchiveMaterial{
			Draft: domain.MaterialDraft(material), Key: sagamemory.GlossaryKey(material.Name), SourceName: material.SourceName,
			SourceKind: material.SourceKind, CreatedAt: material.CreatedAt, UpdatedAt: material.UpdatedAt,
		}
		if materials[index].Draft.Properties == nil {
			materials[index].Draft.Properties = []authormaterials.Property{}
		}
	}
	if err := materialsrepo.CreateArchiveInTx(ctx, tx, open.projectID, actor, materials); err != nil {
		return err
	}
	report.Materials = len(materials)
	for _, record := range archive.Memory {
		carried, _ := authormemory.ParseCarried(record.Origin)
		if err := memoryrepo.CreateCarriedInTx(ctx, tx, open.projectID, actor, authormemory.Record{
			Kind: authormemory.Kind(record.Kind), Text: record.Body, Name: record.Name, ChapterID: open.chapters[record.Chapter].id,
			Carried: carried, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		}); err != nil {
			return err
		}
	}
	report.Memory = len(archive.Memory)
	for _, note := range archive.Notes {
		if err := notesrepo.CreateArchiveNoteInTx(ctx, tx, actor, open.note(note)); err != nil {
			return err
		}
	}
	report.Notes = len(archive.Notes)
	err := archive.EachChat(func(entry domain.ChatEntry, transcript domain.ChatTranscript) error {
		turns, err := json.Marshal(transcript.Turns)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO saga_transcripts (project_id, title, created_at, last_turn_at, others_turns, turns)
			VALUES ($1::uuid, $2, $3, $4, $5, $6)
		`, open.projectID, entry.Title, entry.CreatedAt, entry.LastTurnAt, entry.OthersTurns, turns)
		return err
	})
	report.Chats = len(archive.Chats)
	return err
}

// note is a note of the file for the new saga: a note of a chapter with a published text is bound to it, at its paragraph
// when the chapter of the file has it; any other keeps its quote in its body (SF3.7).
func (open *opening) note(note domain.Note) notes.Note {
	out := notes.Note{Title: note.Title, Body: note.Body, CreatedAt: note.CreatedAt, UpdatedAt: note.UpdatedAt}
	source := note.Source
	if source == nil {
		return out
	}
	chapter, known := open.chapters[source.Chapter]
	if source.Kind != domain.NoteSourceChapter || !known || !chapter.published {
		out.Body = domain.QuotedBody(source.Quote, note.Body)
		return out
	}
	locator := &domainlibrary.ReaderLocator{ProjectID: open.projectID, BookID: chapter.book, ChapterID: chapter.id, TargetView: "published", Revision: 1}
	if paragraph, found := chapter.paragraphs[source.Paragraph]; found && source.Paragraph != "" {
		locator.ParagraphID, locator.Range = &paragraph, source.Range
	}
	out.Source = &notes.Source{Kind: "chapter", Locator: locator, Quote: source.Quote}
	return out
}
