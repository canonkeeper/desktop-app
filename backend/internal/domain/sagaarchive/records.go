// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"errors"
	"io"
	"sort"

	"canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
)

func (reading *reading) saga() error {
	saga := &reading.archive.Saga
	return reading.consume(EntrySaga, MaxSagaBytes, func(source io.Reader) error {
		if err := whole(source, MaxSagaBytes, saga); err != nil {
			return err
		}
		if !sagaLocal(*saga) {
			return refuse(CodeInvalid, "")
		}
		return nil
	})
}

func (reading *reading) books() error {
	reading.bookRefs = map[string]*Book{}
	reading.coverBooks = map[string]string{}
	keys, places := map[int]bool{}, map[int]bool{}
	return reading.consume(EntryBooks, MaxLineBytes*16, func(source io.Reader) error {
		err := eachLine(source, func(line []byte) error {
			var book Book
			if err := strict(line, &book); err != nil {
				return err
			}
			if !bookLocal(book) || reading.bookRefs[book.Ref] != nil || keys[book.KeyNumber] || places[book.Order] ||
				book.KeyNumber > reading.archive.Saga.BookKeyNumbers {
				return refuse(CodeInvalid, "")
			}
			if book.Cover != nil {
				if _, twice := reading.coverBooks[book.Cover.File]; twice {
					return refuse(CodeInvalid, "")
				}
				reading.coverBooks[book.Cover.File] = book.Ref
			}
			keys[book.KeyNumber], places[book.Order] = true, true
			reading.archive.Books = append(reading.archive.Books, book)
			reading.bookRefs[book.Ref] = &reading.archive.Books[len(reading.archive.Books)-1]
			return nil
		})
		// The pointers above point into a slice that grew: name the books anew.
		for index := range reading.archive.Books {
			reading.bookRefs[reading.archive.Books[index].Ref] = &reading.archive.Books[index]
		}
		return err
	})
}

// eachChapter reads chapters.jsonl, every chapter checked by itself and against the books.
func (archive *Archive) eachChapter(reading *reading, each func(Chapter) error) error {
	return reading.consume(EntryChapters, MaxUnpackedBytes, func(source io.Reader) error {
		return eachLine(source, func(line []byte) error {
			var chapter Chapter
			if err := strict(line, &chapter); err != nil {
				return err
			}
			if !chapterLocal(chapter) || reading.bookRefs[chapter.Book] == nil {
				return refuse(CodeInvalid, "")
			}
			return each(chapter)
		})
	})
}

func (reading *reading) chapters() error {
	reading.chapterRefs = map[string]int{}
	archive := reading.archive
	return archive.eachChapter(reading, func(chapter Chapter) error {
		if _, twice := reading.chapterRefs[chapter.Ref]; twice {
			return refuse(CodeInvalid, "")
		}
		digest, ok := ContentHash(chapter.Paragraphs)
		if !ok {
			return refuse(CodeInvalid, "")
		}
		head := ChapterHead{
			Ref: chapter.Ref, Book: chapter.Book, Order: chapter.Order, PublishedOrder: chapter.PublishedOrder, Title: chapter.Title,
			Paragraphs: len(chapter.Paragraphs), ContentHash: digest, paragraphs: make(map[string]struct{}, len(chapter.Paragraphs)),
		}
		for _, paragraph := range chapter.Paragraphs {
			head.paragraphs[paragraph.ID] = struct{}{}
		}
		reading.chapterRefs[chapter.Ref] = len(archive.Chapters)
		archive.Chapters = append(archive.Chapters, head)
		return nil
	})
}

func (reading *reading) cards() error {
	archive := reading.archive
	carded := map[string]bool{}
	err := reading.consume(EntryCards, MaxUnpackedBytes, func(source io.Reader) error {
		return eachLine(source, func(line []byte) error {
			var card Card
			if err := strict(line, &card); err != nil {
				return err
			}
			index, known := reading.chapterRefs[card.Chapter]
			if !cardLocal(card) || !known || carded[card.Chapter] {
				return refuse(CodeInvalid, "")
			}
			carded[card.Chapter] = true
			head := archive.Chapters[index]
			filed := FileCard{Card: card}
			if card.Version <= sagamemory.CardVersion && card.ContentHash == head.ContentHash {
				if parsed, err := sagamemory.ParseCard(string(card.Card), 1, head.Paragraphs); err == nil {
					filed.Accepted, filed.Parsed = true, parsed
				}
			}
			archive.Cards = append(archive.Cards, filed)
			return nil
		})
	})
	if err != nil {
		return err
	}
	return nil
}

func (reading *reading) names() error {
	if _, present := reading.archive.Manifest.Files[EntryNames]; !present {
		return nil
	}
	names := &Names{}
	err := reading.consume(EntryNames, MaxRecordBytes, func(source io.Reader) error {
		if err := whole(source, MaxRecordBytes, names); err != nil {
			return err
		}
		if !namesLocal(*names) {
			return refuse(CodeInvalid, "")
		}
		return nil
	})
	if err != nil {
		return err
	}
	reading.archive.Names = names
	reading.archive.NamesFresh = names.Digest == reading.archive.AcceptedNamesDigest()
	return nil
}

// AcceptedNamesDigest is the digest of the list of names of the accepted cards in the order of reading: the one the cards
// Job compares with the registry before it calls the model (SF3.6).
func (archive *Archive) AcceptedNamesDigest() string {
	order := archive.ReadingOrder()
	position := make(map[string]int, len(order))
	for index, chapter := range order {
		position[chapter] = index
	}
	cards := map[int]*sagamemory.Card{}
	for index := range archive.Cards {
		if card := &archive.Cards[index]; card.Accepted {
			cards[position[card.Chapter]] = &card.Parsed
		}
	}
	_, digest := sagamemory.BuildNameList(cards)
	return digest
}

// ReadingOrder lists the refs of the chapters as the saga reads them: by the place of the book, then of the chapter.
func (archive *Archive) ReadingOrder() []string {
	books := map[string]int{}
	for _, book := range archive.Books {
		books[book.Ref] = book.Order
	}
	chapters := append([]ChapterHead(nil), archive.Chapters...)
	sort.SliceStable(chapters, func(i, j int) bool {
		a, b := chapters[i], chapters[j]
		if books[a.Book] != books[b.Book] {
			return books[a.Book] < books[b.Book]
		}
		return a.Order < b.Order
	})
	refs := make([]string, len(chapters))
	for index, chapter := range chapters {
		refs[index] = chapter.Ref
	}
	return refs
}

func (reading *reading) threads() error {
	if _, present := reading.archive.Manifest.Files[EntryThreads]; !present {
		return nil
	}
	threads := &Threads{}
	err := reading.consume(EntryThreads, MaxRecordBytes, func(source io.Reader) error {
		if err := whole(source, MaxRecordBytes, threads); err != nil {
			return err
		}
		if !threadsLocal(*threads) || !reading.threadRefs(*threads) {
			return refuse(CodeInvalid, "")
		}
		return nil
	})
	if err != nil {
		return err
	}
	reading.archive.Threads = threads
	return nil
}

// threadRefs says every chapter and book the layer names is one of the file.
func (reading *reading) threadRefs(threads Threads) bool {
	chapter := func(ref string) bool {
		_, known := reading.chapterRefs[ref]
		return known
	}
	stamped := map[string]bool{}
	for _, stamp := range threads.Stamps {
		if !chapter(stamp.Chapter) || stamped[stamp.Chapter] {
			return false
		}
		stamped[stamp.Chapter] = true
	}
	refs := func(list []sagalayer.Ref) bool {
		for _, ref := range list {
			if !chapter(ref.Chapter) {
				return false
			}
		}
		return true
	}
	books := map[string]bool{}
	for _, book := range threads.Books {
		if reading.bookRefs[book.ID] == nil || books[book.ID] {
			return false
		}
		books[book.ID] = true
		for _, arc := range book.Arcs {
			if arc.From.Chapter != "" && !chapter(arc.From.Chapter) || arc.To.Chapter != "" && !chapter(arc.To.Chapter) {
				return false
			}
		}
		for _, thread := range book.Threads {
			if !refs(thread.First) || !refs(thread.Last) {
				return false
			}
		}
	}
	for _, thread := range threads.Threads {
		if !refs(thread.First) || !refs(thread.Last) {
			return false
		}
	}
	return true
}

func (reading *reading) mapHidden() error {
	seen := map[[2]string]bool{}
	return reading.consume(EntryMapHidden, MaxUnpackedBytes, func(source io.Reader) error {
		return eachLine(source, func(line []byte) error {
			var item MapHidden
			if err := strict(line, &item); err != nil {
				return err
			}
			pair := [2]string{item.NodeKey, item.OtherKey}
			if !mapHiddenLocal(item) || seen[pair] {
				return refuse(CodeInvalid, "")
			}
			seen[pair] = true
			reading.archive.MapHidden = append(reading.archive.MapHidden, item)
			return nil
		})
	})
}

func (reading *reading) materials() error {
	keys := map[string]bool{}
	return reading.consume(EntryMaterials, MaxUnpackedBytes, func(source io.Reader) error {
		return eachLine(source, func(line []byte) error {
			var material Material
			if err := strict(line, &material); err != nil {
				return err
			}
			key := sagamemory.GlossaryKey(material.Name)
			if !materialLocal(material) || keys[key] {
				return refuse(CodeInvalid, "")
			}
			keys[key] = true
			reading.archive.Materials = append(reading.archive.Materials, material)
			return nil
		})
	})
}

func (reading *reading) memory() error {
	return reading.consume(EntryMemory, MaxUnpackedBytes, func(source io.Reader) error {
		return eachLine(source, func(line []byte) error {
			var record Memory
			if err := strict(line, &record); err != nil {
				return err
			}
			if _, known := reading.chapterRefs[record.Chapter]; !memoryLocal(record) || record.Chapter != "" && !known {
				return refuse(CodeInvalid, "")
			}
			reading.archive.Memory = append(reading.archive.Memory, record)
			return nil
		})
	})
}

func (reading *reading) notes() error {
	return reading.consume(EntryNotes, MaxUnpackedBytes, func(source io.Reader) error {
		return eachLine(source, func(line []byte) error {
			var note Note
			if err := strict(line, &note); err != nil {
				return err
			}
			if !noteLocal(note) {
				return refuse(CodeInvalid, "")
			}
			if source := note.Source; source != nil && source.Kind == NoteSourceChapter {
				if _, known := reading.chapterRefs[source.Chapter]; !known {
					return refuse(CodeInvalid, "")
				}
			}
			reading.archive.Notes = append(reading.archive.Notes, note)
			return nil
		})
	})
}

func (reading *reading) chats() error {
	reading.chatFiles = map[string]bool{}
	err := reading.consume(EntryChats, MaxUnpackedBytes, func(source io.Reader) error {
		return eachLine(source, func(line []byte) error {
			var entry ChatEntry
			if err := strict(line, &entry); err != nil {
				return err
			}
			if _, filed := reading.archive.Manifest.Files[entry.File]; !chatEntryLocal(entry) || !filed || reading.chatFiles[entry.File] {
				return refuse(CodeInvalid, "")
			}
			reading.chatFiles[entry.File] = true
			reading.archive.Chats = append(reading.archive.Chats, entry)
			return nil
		})
	})
	if err != nil {
		return err
	}
	for name := range reading.archive.Manifest.Files {
		if chatName.MatchString(name) && !reading.chatFiles[name] {
			return refuse(CodeInvalid, name)
		}
	}
	return reading.archive.eachChat(reading, func(ChatEntry, ChatTranscript) error { return nil })
}

// eachChat reads the transcript of every chat, checked against its line.
func (archive *Archive) eachChat(reading *reading, each func(ChatEntry, ChatTranscript) error) error {
	for _, entry := range archive.Chats {
		var transcript ChatTranscript
		err := reading.consume(entry.File, MaxRecordBytes, func(source io.Reader) error {
			if err := whole(source, MaxRecordBytes, &transcript); err != nil {
				return err
			}
			if !transcriptLocal(transcript) || len(transcript.Turns) != entry.Turns {
				return refuse(CodeInvalid, "")
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err := each(entry, transcript); err != nil {
			return err
		}
	}
	return nil
}

// covers checks the file of every cover against its book: the record is there once, its SHA-256 is the book's and its size
// within the limit of an upload of a cover (SF3.1). Whether the picture is taken is SF3.9.
func (reading *reading) covers() error {
	for name := range reading.archive.Manifest.Files {
		if !coverName.MatchString(name) {
			continue
		}
		ref, named := reading.coverBooks[name]
		if !named || reading.archive.Manifest.Files[name] != reading.bookRefs[ref].Cover.SHA256 {
			return refuse(CodeInvalid, name)
		}
		if err := reading.consume(name, MaxCoverBytes, func(source io.Reader) error { return nil }); err != nil {
			var refusal *Refusal
			if errors.As(err, &refusal) && refusal.Code == CodeTooLarge {
				return refuse(CodeInvalid, name)
			}
			return err
		}
	}
	for file := range reading.coverBooks {
		if _, filed := reading.archive.Manifest.Files[file]; !filed {
			return refuse(CodeInvalid, EntryBooks)
		}
	}
	return nil
}

// workshops keeps the records of the workshops, checked by their digest, for the report (SF3.10).
func (reading *reading) workshops() error {
	for name := range reading.archive.Manifest.Files {
		if !workshopName.MatchString(name) {
			continue
		}
		if err := reading.consume(name, MaxUnpackedBytes, func(io.Reader) error { return nil }); err != nil {
			return err
		}
		reading.archive.Sections = append(reading.archive.Sections, name)
	}
	sort.Strings(reading.archive.Sections)
	return nil
}

// counts says the manifest counts what the file holds.
func (reading *reading) counts() error {
	archive := reading.archive
	if archive.Manifest.Counts != (Counts{
		Books: len(archive.Books), Chapters: len(archive.Chapters), Cards: len(archive.Cards), Materials: len(archive.Materials),
		Memory: len(archive.Memory), Notes: len(archive.Notes), Chats: len(archive.Chats),
	}) {
		return refuse(CodeInvalid, EntryManifest)
	}
	return nil
}
