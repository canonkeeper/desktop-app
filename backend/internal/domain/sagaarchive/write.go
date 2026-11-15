// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
)

// Writer writes a saga file record after record as the saga is read (SF1.1): each record once, compressed by Deflate, and
// the manifest with the SHA-256 of every other record last, so a file cut short has no manifest. It refuses a record the
// reader would not take (SF1.10).
type Writer struct {
	zip    *zip.Writer
	files  map[string]string
	counts map[string]int
	open   *Lines
	err    error
}

// NewWriter starts a file over the stream.
func NewWriter(out io.Writer) *Writer {
	return &Writer{zip: zip.NewWriter(out), files: map[string]string{}, counts: map[string]int{}}
}

// Lines is a JSONL record under way.
type Lines struct {
	writer *Writer
	name   string
	out    io.Writer
	sum    hash.Hash
	check  func(any) bool
}

func (writer *Writer) begin(name string) (io.Writer, hash.Hash, error) {
	if writer.err != nil {
		return nil, nil, writer.err
	}
	if writer.open != nil {
		return nil, nil, fmt.Errorf("saga archive: %s begins while %s is open", name, writer.open.name)
	}
	if _, twice := writer.files[name]; twice || name == EntryManifest || !knownName(name) {
		return nil, nil, fmt.Errorf("saga archive: record %s: %w", name, ErrUnwritable)
	}
	out, err := writer.zip.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		writer.err = err
		return nil, nil, err
	}
	sum := sha256.New()
	writer.files[name] = ""
	return io.MultiWriter(out, sum), sum, nil
}

func (writer *Writer) end(name string, sum hash.Hash) {
	writer.files[name] = hex.EncodeToString(sum.Sum(nil))
}

func (writer *Writer) fail(err error) error {
	if writer.err == nil {
		writer.err = err
	}
	return err
}

// record writes one JSON record.
func (writer *Writer) record(name string, value any, check bool) error {
	if !check {
		return fmt.Errorf("saga archive: record %s: %w", name, ErrUnwritable)
	}
	body, err := json.Marshal(value)
	if err != nil {
		return writer.fail(err)
	}
	if len(body)+1 > MaxRecordBytes {
		return fmt.Errorf("saga archive: record %s: %w", name, ErrUnwritable)
	}
	out, sum, err := writer.begin(name)
	if err != nil {
		return err
	}
	if _, err := out.Write(append(body, '\n')); err != nil {
		return writer.fail(err)
	}
	writer.end(name, sum)
	return nil
}

// Flush puts what is written into the stream: the file so far, before the stream changes hands.
func (writer *Writer) Flush() error {
	if writer.err != nil {
		return writer.err
	}
	return writer.fail(writer.zip.Flush())
}

// Saga writes saga.json.
func (writer *Writer) Saga(saga Saga) error { return writer.record(EntrySaga, saga, sagaLocal(saga)) }

// Names writes names.json.
func (writer *Writer) Names(names Names) error {
	return writer.record(EntryNames, names, namesLocal(names))
}

// Threads writes threads.json.
func (writer *Writer) Threads(threads Threads) error {
	return writer.record(EntryThreads, threads, threadsLocal(threads))
}

// Transcript writes the transcript of a chat under its file; a transcript the reader would not take is refused and the
// file goes on without it (SF1.8).
func (writer *Writer) Transcript(file string, transcript ChatTranscript) error {
	if !chatName.MatchString(file) {
		return fmt.Errorf("saga archive: record %s: %w", file, ErrUnwritable)
	}
	return writer.record(file, transcript, transcriptLocal(transcript))
}

// File writes a record as it is: the file of a cover.
func (writer *Writer) File(name string, content []byte) error {
	if !coverName.MatchString(name) || int64(len(content)) > MaxCoverBytes {
		return writer.fail(fmt.Errorf("saga archive: record %s: %w", name, ErrUnwritable))
	}
	out, sum, err := writer.begin(name)
	if err != nil {
		return err
	}
	if _, err := out.Write(content); err != nil {
		return writer.fail(err)
	}
	writer.end(name, sum)
	return nil
}

// Books begins books.jsonl; Chapters, Cards and the others the records of their kind.
func (writer *Writer) Books() (*Lines, error) { return writer.records(EntryBooks, isBook) }

func (writer *Writer) Chapters() (*Lines, error) { return writer.records(EntryChapters, isChapter) }

func (writer *Writer) Cards() (*Lines, error) { return writer.records(EntryCards, isCard) }

func (writer *Writer) MapHidden() (*Lines, error) { return writer.records(EntryMapHidden, isMapHidden) }

func (writer *Writer) Materials() (*Lines, error) { return writer.records(EntryMaterials, isMaterial) }

func (writer *Writer) Memory() (*Lines, error) { return writer.records(EntryMemory, isMemory) }

func (writer *Writer) Notes() (*Lines, error) { return writer.records(EntryNotes, isNote) }

func (writer *Writer) Chats() (*Lines, error) { return writer.records(EntryChats, isChatEntry) }

func (writer *Writer) records(name string, check func(any) bool) (*Lines, error) {
	out, sum, err := writer.begin(name)
	if err != nil {
		return nil, err
	}
	writer.open = &Lines{writer: writer, name: name, out: out, sum: sum, check: check}
	return writer.open, nil
}

// Add writes one line.
func (lines *Lines) Add(value any) error {
	writer := lines.writer
	if writer.err != nil {
		return writer.err
	}
	if !lines.check(value) {
		return writer.fail(fmt.Errorf("saga archive: a line of %s: %w", lines.name, ErrUnwritable))
	}
	body, err := json.Marshal(value)
	if err != nil {
		return writer.fail(err)
	}
	if len(body)+1 > MaxLineBytes {
		return writer.fail(fmt.Errorf("saga archive: a line of %s: %w", lines.name, ErrUnwritable))
	}
	if _, err := lines.out.Write(append(body, '\n')); err != nil {
		return writer.fail(err)
	}
	writer.counts[lines.name]++
	return nil
}

// Close ends the record.
func (lines *Lines) Close() error {
	writer := lines.writer
	if writer.open != lines {
		return errors.New("saga archive: the record is not open")
	}
	writer.open = nil
	writer.end(lines.name, lines.sum)
	return writer.err
}

// Close writes the manifest — the counts, what is left out and the SHA-256 of every record written — and ends the file.
// The empty records of the table are written here, so a file always has them.
func (writer *Writer) Close(manifest Manifest) error {
	if writer.err != nil {
		return writer.err
	}
	if writer.open != nil {
		return writer.fail(fmt.Errorf("saga archive: %s is open", writer.open.name))
	}
	for name, required := range fixedEntries {
		if _, written := writer.files[name]; required && !written {
			lines, err := writer.records(name, func(any) bool { return false })
			if err != nil {
				return err
			}
			if err := lines.Close(); err != nil {
				return err
			}
		}
	}
	manifest.Format, manifest.Version = Format, Version
	manifest.Files = writer.files
	manifest.Counts = Counts{
		Books: writer.counts[EntryBooks], Chapters: writer.counts[EntryChapters], Cards: writer.counts[EntryCards],
		Materials: writer.counts[EntryMaterials], Memory: writer.counts[EntryMemory], Notes: writer.counts[EntryNotes],
		Chats: writer.counts[EntryChats],
	}
	if manifest.ChatsNotRead == nil {
		manifest.ChatsNotRead = []string{}
	}
	if manifest.Sections == nil {
		manifest.Sections = []string{}
	}
	if !manifestLocal(manifest) {
		return writer.fail(fmt.Errorf("saga archive: the manifest: %w", ErrUnwritable))
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return writer.fail(err)
	}
	out, err := writer.zip.CreateHeader(&zip.FileHeader{Name: EntryManifest, Method: zip.Deflate})
	if err != nil {
		return writer.fail(err)
	}
	if _, err := out.Write(append(body, '\n')); err != nil {
		return writer.fail(err)
	}
	return writer.fail(writer.zip.Close())
}

func isBook(value any) bool {
	book, ok := value.(Book)
	return ok && bookLocal(book)
}

func isChapter(value any) bool {
	chapter, ok := value.(Chapter)
	return ok && chapterLocal(chapter)
}

func isCard(value any) bool {
	card, ok := value.(Card)
	return ok && cardLocal(card)
}

func isMapHidden(value any) bool {
	item, ok := value.(MapHidden)
	return ok && mapHiddenLocal(item)
}

func isMaterial(value any) bool {
	material, ok := value.(Material)
	return ok && materialLocal(material)
}

func isMemory(value any) bool {
	record, ok := value.(Memory)
	return ok && memoryLocal(record)
}

func isNote(value any) bool {
	note, ok := value.(Note)
	return ok && noteLocal(note)
}

func isChatEntry(value any) bool {
	entry, ok := value.(ChatEntry)
	return ok && chatEntryLocal(entry)
}
