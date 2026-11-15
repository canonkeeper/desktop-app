// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/sagamemory"

	"golang.org/x/text/unicode/norm"
)

// Archive is a saga file the reader took: every record checked (SF3.1) and kept but the paragraphs of the chapters and the
// transcripts of the chats, which EachChapter and EachChat read again from the file; Accepted says which cards fit their
// chapter (SF3.5).
type Archive struct {
	Manifest  Manifest
	Saga      Saga
	Books     []Book
	Chapters  []ChapterHead
	Cards     []FileCard
	Names     *Names
	Threads   *Threads
	MapHidden []MapHidden
	Materials []Material
	Memory    []Memory
	Notes     []Note
	Chats     []ChatEntry
	// Sections are the records of the workshops this build does not read (SF3.10).
	Sections []string
	// NamesFresh says the registry of the file is the one of its accepted cards: no call of the model rebuilds it (SF3.6).
	NamesFresh bool

	files   map[string]*zip.File
	reading *reading
}

// callback is an error of the caller of a second reading: it goes back as it is.
type callback struct{ err error }

func (failure callback) Error() string { return failure.err.Error() }

// again is a second reading of the file the reader took: the records are read anew under the same limits.
func (archive *Archive) again() *reading {
	again := *archive.reading
	again.read = 0
	return &again
}

// EachChapter reads the chapters with their paragraphs again, in the order of the file.
func (archive *Archive) EachChapter(each func(Chapter) error) error {
	return archive.eachChapter(archive.again(), func(chapter Chapter) error {
		if err := each(chapter); err != nil {
			return callback{err}
		}
		return nil
	})
}

// EachChat reads the transcripts of the chats again, in the order of chats.jsonl.
func (archive *Archive) EachChat(each func(ChatEntry, ChatTranscript) error) error {
	return archive.eachChat(archive.again(), func(entry ChatEntry, transcript ChatTranscript) error {
		if err := each(entry, transcript); err != nil {
			return callback{err}
		}
		return nil
	})
}

// Cover reads the file of a cover the books name.
func (archive *Archive) Cover(file string) ([]byte, error) {
	if _, named := archive.reading.coverBooks[file]; !named {
		return nil, refuse(CodeInvalid, file)
	}
	var content []byte
	err := archive.again().consume(file, MaxCoverBytes, func(source io.Reader) error {
		var err error
		content, err = io.ReadAll(source)
		return err
	})
	return content, err
}

// ChapterHead is a chapter of the file without its paragraphs: ContentHash is the digest of its paragraphs as the file has
// them, Runes the signs of its Markdown.
type ChapterHead struct {
	Ref            string
	Book           string
	Order          int
	PublishedOrder *int
	Title          string
	Paragraphs     int
	ContentHash    string
	paragraphs     map[string]struct{}
}

// HasParagraph says the chapter of the file has the paragraph.
func (head ChapterHead) HasParagraph(id string) bool {
	_, ok := head.paragraphs[id]
	return ok
}

// FileCard is a card of the file with what the reader made of it: Accepted when its digest is the one of its chapter and
// its version is not newer than the product knows; Parsed is the card as the product keeps it.
type FileCard struct {
	Card
	Accepted bool
	Parsed   sagamemory.Card
}

// Read takes a saga file: the container, the manifest and the SHA-256 of every record, and every record by its checks,
// before anything is written anywhere (SF3.1). A refusal is a *Refusal with one code for one cause.
func Read(file io.ReaderAt, size int64) (*Archive, error) {
	reader, err := openContainer(file, size)
	if err != nil {
		return nil, err
	}
	reading := &reading{
		archive:  &Archive{files: map[string]*zip.File{}},
		unpacked: min(MaxUnpackedBytes, max(size*MaxRatio, 1<<20)),
	}
	reading.archive.reading = reading
	if err := reading.entries(reader); err != nil {
		return nil, err
	}
	if err := reading.manifest(); err != nil {
		return nil, err
	}
	for _, step := range []func() error{
		reading.saga, reading.books, reading.chapters, reading.cards, reading.names, reading.threads, reading.mapHidden,
		reading.materials, reading.memory, reading.notes, reading.chats, reading.covers, reading.workshops, reading.counts,
	} {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return reading.archive, nil
}

// openContainer checks the zip before the standard library reads its directory: the number of records and the size of the
// directory from the end record (SF3.1).
func openContainer(file io.ReaderAt, size int64) (*zip.Reader, error) {
	if size > MaxFileBytes {
		return nil, refuse(CodeTooLarge, "")
	}
	head := make([]byte, min(size, 4))
	if _, err := file.ReadAt(head, 0); err != nil && !errors.Is(err, io.EOF) {
		return nil, refuse(CodeNotArchive, "")
	}
	// The beginning of a zip cut short is a damaged file; anything else is not one.
	if string(head) != "PK\x03\x04" {
		if len(head) > 0 && strings.HasPrefix("PK\x03\x04", string(head)) {
			return nil, refuse(CodeDamaged, "")
		}
		return nil, refuse(CodeNotArchive, "")
	}
	entries, directory, err := endOfDirectory(file, size)
	if err != nil {
		return nil, err
	}
	if entries > MaxEntries || directory > MaxDirectoryBytes {
		return nil, refuse(CodeTooLarge, "")
	}
	reader, err := zip.NewReader(file, size)
	if errors.Is(err, zip.ErrInsecurePath) {
		return nil, refuse(CodeInvalid, "")
	}
	if err != nil || uint64(len(reader.File)) != entries {
		return nil, refuse(CodeDamaged, "")
	}
	return reader, nil
}

const (
	endSignature       = 0x06054b50
	end64LocatorSign   = 0x07064b50
	end64Signature     = 0x06064b50
	endRecordBytes     = 22
	end64LocatorBytes  = 20
	end64RecordBytes   = 56
	maxEndCommentBytes = 0xffff
)

// endOfDirectory reads the number of records and the size of the directory from the end of the archive, ZIP64 included.
func endOfDirectory(file io.ReaderAt, size int64) (entries, directory uint64, err error) {
	tail := min(size, endRecordBytes+maxEndCommentBytes)
	buffer := make([]byte, tail)
	if _, err := file.ReadAt(buffer, size-tail); err != nil && !errors.Is(err, io.EOF) {
		return 0, 0, refuse(CodeDamaged, "")
	}
	at := -1
	for i := len(buffer) - endRecordBytes; i >= 0; i-- {
		if binary.LittleEndian.Uint32(buffer[i:]) == endSignature &&
			i+endRecordBytes+int(binary.LittleEndian.Uint16(buffer[i+20:])) == len(buffer) {
			at = i
			break
		}
	}
	if at < 0 {
		return 0, 0, refuse(CodeDamaged, "")
	}
	record := buffer[at:]
	entries = uint64(binary.LittleEndian.Uint16(record[10:]))
	directory = uint64(binary.LittleEndian.Uint32(record[12:]))
	offset := uint64(binary.LittleEndian.Uint32(record[16:]))
	if entries != 0xffff && directory != 0xffffffff && offset != 0xffffffff {
		return entries, directory, nil
	}
	locatorAt := size - tail + int64(at) - end64LocatorBytes
	if locatorAt < 0 {
		return 0, 0, refuse(CodeDamaged, "")
	}
	locator := make([]byte, end64LocatorBytes)
	if _, err := file.ReadAt(locator, locatorAt); err != nil || binary.LittleEndian.Uint32(locator) != end64LocatorSign {
		return 0, 0, refuse(CodeDamaged, "")
	}
	recordAt := binary.LittleEndian.Uint64(locator[8:])
	if recordAt > uint64(size)-end64RecordBytes {
		return 0, 0, refuse(CodeDamaged, "")
	}
	record64 := make([]byte, end64RecordBytes)
	if _, err := file.ReadAt(record64, int64(recordAt)); err != nil || binary.LittleEndian.Uint32(record64) != end64Signature {
		return 0, 0, refuse(CodeDamaged, "")
	}
	return binary.LittleEndian.Uint64(record64[32:]), binary.LittleEndian.Uint64(record64[40:]), nil
}

type reading struct {
	archive     *Archive
	unpacked    int64
	read        int64
	bookRefs    map[string]*Book
	chapterRefs map[string]int
	coverBooks  map[string]string
	chatFiles   map[string]bool
}

// entries checks every record of the directory: no cipher, no directory, no link, Store or Deflate, a name of the table,
// each once without regard to case (SF3.1).
func (reading *reading) entries(reader *zip.Reader) error {
	seen := map[string]bool{}
	var declared uint64
	for _, file := range reader.File {
		name := file.Name
		if file.Flags&0x1 != 0 || file.Method != zip.Store && file.Method != zip.Deflate ||
			strings.HasSuffix(name, "/") || file.Mode()&(fs.ModeDir|fs.ModeSymlink|fs.ModeType) != 0 {
			return refuse(CodeInvalid, printable(name))
		}
		if !utf8.ValidString(name) || name != EntryManifest && !knownName(name) {
			return refuse(CodeInvalid, printable(name))
		}
		key := strings.ToLower(norm.NFC.String(name))
		if seen[key] {
			return refuse(CodeInvalid, printable(name))
		}
		seen[key] = true
		if declared += file.UncompressedSize64; declared > uint64(reading.unpacked) {
			return refuse(CodeTooLarge, "")
		}
		reading.archive.files[name] = file
	}
	return nil
}

// printable is a name of a record a refusal may say: one of the table, or nothing.
func printable(name string) string {
	if name == EntryManifest || knownName(name) {
		return name
	}
	return ""
}

// counter counts what a record unpacks into: over the limit of the record or of the whole file it stops.
type counter struct {
	reading *reading
	source  io.Reader
	left    int64
}

func (counter *counter) Read(buffer []byte) (int, error) {
	n, err := counter.source.Read(buffer)
	counter.left -= int64(n)
	counter.reading.read += int64(n)
	if counter.left < 0 || counter.reading.read > counter.reading.unpacked {
		return n, refuse(CodeTooLarge, "")
	}
	return n, err
}

// consume reads a record once through its parser while its SHA-256 is taken: a digest other than the manifest's is a
// damaged file whatever the parser said; an error of the parser is the refusal of that record.
func (reading *reading) consume(name string, limit int64, parse func(io.Reader) error) error {
	file := reading.archive.files[name]
	if file == nil {
		return refuse(CodeDamaged, name)
	}
	source, err := file.Open()
	if err != nil {
		return refuse(CodeDamaged, name)
	}
	defer func() { _ = source.Close() }()
	sum := sha256.New()
	counted := &counter{reading: reading, source: source, left: limit}
	stream := io.TeeReader(counted, sum)
	parsed := parse(stream)
	var failure callback
	if errors.As(parsed, &failure) {
		return failure.err
	}
	if _, err := io.Copy(io.Discard, stream); err != nil {
		var refusal *Refusal
		if errors.As(err, &refusal) {
			return &Refusal{Code: refusal.Code, Entry: name}
		}
		return refuse(CodeDamaged, name)
	}
	if hex.EncodeToString(sum.Sum(nil)) != reading.archive.Manifest.Files[name] {
		return refuse(CodeDamaged, name)
	}
	if parsed == nil {
		return nil
	}
	var refusal *Refusal
	if errors.As(parsed, &refusal) {
		if refusal.Entry == "" {
			return &Refusal{Code: refusal.Code, Entry: name}
		}
		return refusal
	}
	return refuse(CodeInvalid, name)
}

// strict decodes one JSON value into its type: no field it does not know, nothing after it.
func strict(body []byte, target any) error {
	if !utf8.Valid(body) {
		return errors.New("saga archive: not UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("saga archive: more after the value")
	}
	return nil
}

// whole reads a JSON record whole within its limit.
func whole(source io.Reader, limit int64, target any) error {
	body, err := io.ReadAll(io.LimitReader(source, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return refuse(CodeTooLarge, "")
	}
	return strict(body, target)
}

// lineLimit is MaxLineBytes; a test lowers it.
var lineLimit = MaxLineBytes

// eachLine reads a JSONL record line by line, each a value of its type.
func eachLine(source io.Reader, line func([]byte) error) error {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 0, min(64<<10, lineLimit)), lineLimit)
	for scanner.Scan() {
		if err := line(scanner.Bytes()); err != nil {
			return err
		}
	}
	if err := scanner.Err(); errors.Is(err, bufio.ErrTooLong) {
		return refuse(CodeTooLarge, "")
	} else if err != nil {
		return err
	}
	return nil
}

func (reading *reading) manifest() error {
	file := reading.archive.files[EntryManifest]
	if file == nil {
		return refuse(CodeDamaged, EntryManifest)
	}
	source, err := file.Open()
	if err != nil {
		return refuse(CodeDamaged, EntryManifest)
	}
	defer func() { _ = source.Close() }()
	body, err := io.ReadAll(io.LimitReader(&counter{reading: reading, source: source, left: MaxManifestBytes}, MaxManifestBytes+1))
	var refusal *Refusal
	if errors.As(err, &refusal) || len(body) > MaxManifestBytes {
		return refuse(CodeTooLarge, EntryManifest)
	}
	if err != nil {
		return refuse(CodeDamaged, EntryManifest)
	}
	var probe struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if !utf8.Valid(body) || json.Unmarshal(body, &probe) != nil {
		return refuse(CodeDamaged, EntryManifest)
	}
	if probe.Format != Format {
		return refuse(CodeNotArchive, "")
	}
	if probe.Version > Version {
		return refuse(CodeNewerVersion, "")
	}
	manifest := &reading.archive.Manifest
	if strict(body, manifest) != nil || manifest.Version < 1 || !manifestLocal(*manifest) {
		return refuse(CodeInvalid, EntryManifest)
	}
	// The records are the files of the manifest, one to one.
	if len(manifest.Files) != len(reading.archive.files)-1 {
		return refuse(CodeDamaged, EntryManifest)
	}
	for name := range reading.archive.files {
		if _, named := manifest.Files[name]; name != EntryManifest && !named {
			return refuse(CodeDamaged, EntryManifest)
		}
	}
	for name, required := range fixedEntries {
		if _, present := manifest.Files[name]; required && !present {
			return refuse(CodeInvalid, name)
		}
	}
	return nil
}
