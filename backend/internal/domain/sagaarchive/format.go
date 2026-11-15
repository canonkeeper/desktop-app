// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	library "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/domain/notes"
	"canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// The format of the saga file (specs/2026-11-13-desk-sagafile.md SF1).
const (
	Format    = "canonkeeper-saga"
	Version   = 1
	Extension = "cksaga"
)

// The records of the file (SF1 Data); each is written once and the manifest last.
const (
	EntrySaga      = "saga.json"
	EntryBooks     = "books.jsonl"
	EntryChapters  = "chapters.jsonl"
	EntryCards     = "cards.jsonl"
	EntryNames     = "names.json"
	EntryThreads   = "threads.json"
	EntryMapHidden = "map_hidden.jsonl"
	EntryMaterials = "author_materials.jsonl"
	EntryMemory    = "author_memory.jsonl"
	EntryNotes     = "notes.jsonl"
	EntryChats     = "chats.jsonl"
	EntryManifest  = "manifest.json"
)

// The limits of a file (SF3.1). The writer never writes more than they let through: a chapter line holds a book of the
// limits of an import, a transcript the turns of the limits below.
const (
	MaxFileBytes      int64 = 1 << 30
	MaxEntries              = 10_000
	MaxDirectoryBytes       = 8 << 20
	MaxUnpackedBytes  int64 = 4 << 30
	MaxRatio                = 50
	// MaxLineBytes is one line of a JSONL record: a chapter of a book of 10 million signs in its Markdown, escaped.
	MaxLineBytes = 128 << 20
	// MaxRecordBytes is one JSON record but the manifest: the registry of names, the layer of threads, a transcript.
	MaxRecordBytes   = 64 << 20
	MaxSagaBytes     = 1 << 20
	MaxManifestBytes = 16 << 20
	MaxCoverBytes    = library.MaxCoverBytes
	// MaxCoverPixels bounds the picture a cover unpacks into (SF3.9).
	MaxCoverPixels = 25_000_000
	// MaxKeyNumbers bounds the numbers of the books of a saga (SF3.1).
	MaxKeyNumbers = 10_000
	// MaxChapterRunes is the text of a chapter: the text of a book an import takes.
	MaxChapterRunes = 10_000_000
	// The limits of a transcript of a chat (SF3.8).
	MaxTranscriptTurns = 10_000
	MaxQuestionRunes   = 100_000
	MaxAnswerRunes     = 400_000
	MaxChatTitleRunes  = 180
	MaxAttachedRunes   = 200
	MaxCreatedByRunes  = 100
)

// The limits of the texts of a saga, a book and a chapter: those of the product.
const (
	MaxSagaNameRunes        = 160
	MaxSagaDescriptionRunes = 1000
	MaxBookTitleRunes       = 180
	MaxBookSubtitleRunes    = 240
	MaxChapterTitleRunes    = 180
	MaxModelRunes           = 200
	MaxMapKeyRunes          = 400
)

// Outcomes a refusal of a file names (SF3.2).
const (
	CodeNotArchive   = "saga_archive.not_archive"
	CodeDamaged      = "saga_archive.damaged"
	CodeNewerVersion = "saga_archive.newer_version"
	CodeTooLarge     = "saga_archive.too_large"
	CodeInvalid      = "saga_archive.invalid"
	CodeNoSpace      = "saga_archive.no_space"
)

// Refusal is a file the reader does not open: one code for one cause and the record it is in, never the text of the author.
type Refusal struct {
	Code  string
	Entry string
}

func (refusal *Refusal) Error() string {
	if refusal.Entry == "" {
		return "saga archive: " + refusal.Code
	}
	return "saga archive: " + refusal.Code + " in " + refusal.Entry
}

// ErrUnwritable is a record the writer refuses: the reader would not take it (SF1.10).
var ErrUnwritable = errors.New("saga archive: a record the reader would refuse")

func refuse(code, entry string) error { return &Refusal{Code: code, Entry: entry} }

// Saga is saga.json.
type Saga struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	BookKeyNumbers int    `json:"bookKeyNumbers"`
}

// Cover names the file of the cover of a book.
type Cover struct {
	File   string `json:"file"`
	MIME   string `json:"mime"`
	SHA256 string `json:"sha256"`
}

// Book is a line of books.jsonl.
type Book struct {
	Ref           string  `json:"ref"`
	KeyNumber     int     `json:"keyNumber"`
	Order         int     `json:"order"`
	Title         string  `json:"title"`
	Subtitle      *string `json:"subtitle,omitempty"`
	DisplayNumber *string `json:"displayNumber,omitempty"`
	CoverColor    *string `json:"coverColor,omitempty"`
	Cover         *Cover  `json:"cover,omitempty"`
}

// Paragraph is a paragraph of a chapter: its id in the saga it came from, its order, kind and Markdown. Its plain text is
// the projection of the product (SF1.3).
type Paragraph struct {
	ID       string `json:"id"`
	Order    int    `json:"order"`
	Kind     string `json:"kind"`
	Markdown string `json:"markdown"`
}

// Chapter is a line of chapters.jsonl: Order is its place in the book and in the draft text, PublishedOrder its place in
// the published text, absent for a chapter without one; the key of a chapter is built of them (SF1.3).
type Chapter struct {
	Ref            string      `json:"ref"`
	Book           string      `json:"book"`
	Order          int         `json:"order"`
	PublishedOrder *int        `json:"publishedOrder,omitempty"`
	Title          string      `json:"title"`
	Paragraphs     []Paragraph `json:"paragraphs"`
}

// Card is a line of cards.jsonl (SF1.4).
type Card struct {
	Chapter     string          `json:"chapter"`
	ContentHash string          `json:"contentHash"`
	Version     int             `json:"version"`
	Model       string          `json:"model"`
	BuiltFrom   *string         `json:"builtFrom,omitempty"`
	Card        json.RawMessage `json:"card"`
}

// Names is names.json: the registry of names as the saga keeps it (SF1.5).
type Names struct {
	Digest string                  `json:"digest"`
	Model  string                  `json:"model"`
	Names  sagamemory.NameRegistry `json:"names"`
}

// Stamp is what the card of a chapter of a build of the layer was built from (sagalayer.Stamp, with the ref of the chapter).
type Stamp struct {
	Chapter     string  `json:"chapter"`
	H           string  `json:"h"`
	K           *string `json:"k,omitempty"`
	CardVersion int     `json:"cardVersion"`
}

// Threads is threads.json: the finished build of the layer, without the checkpoint of a build under way (SF1.5). The ids of
// books and chapters in it are the refs of the file.
type Threads struct {
	Model   string             `json:"model"`
	Stamps  []Stamp            `json:"stamps"`
	Books   []sagalayer.Book   `json:"books"`
	Threads []sagalayer.Thread `json:"threads"`
}

// MapHidden is a line of map_hidden.jsonl: what the author took off the map.
type MapHidden struct {
	NodeKey   string    `json:"nodeKey"`
	OtherKey  string    `json:"otherKey"`
	NodeName  string    `json:"nodeName"`
	OtherName string    `json:"otherName"`
	CreatedAt time.Time `json:"createdAt"`
}

// Property is one «key — value» of a material.
type Property struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Material is a line of author_materials.jsonl.
type Material struct {
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	Description string     `json:"description"`
	Properties  []Property `json:"properties"`
	Section     string     `json:"section"`
	SourceName  string     `json:"sourceName"`
	SourceKind  string     `json:"sourceKind"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// Memory is a line of author_memory.jsonl: Origin is where it came from — manual, chat, draft, drawing (SF4.2).
type Memory struct {
	Kind      string    `json:"kind"`
	Body      string    `json:"body"`
	Name      string    `json:"name,omitempty"`
	Chapter   string    `json:"chapter,omitempty"`
	Origin    string    `json:"origin"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// The kinds of the source of a note.
const (
	NoteSourceChapter = "chapter"
	NoteSourceChat    = "chat"
)

// NoteSource is where a note was made: a place of a chapter with its quote, or a quote of a chat.
type NoteSource struct {
	Kind      string             `json:"kind"`
	Chapter   string             `json:"chapter,omitempty"`
	Paragraph string             `json:"paragraph,omitempty"`
	Range     *library.TextRange `json:"range,omitempty"`
	Quote     string             `json:"quote"`
}

// Note is a line of notes.jsonl.
type Note struct {
	Title     string      `json:"title"`
	Body      string      `json:"body"`
	Source    *NoteSource `json:"source,omitempty"`
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
}

// ChatEntry is a line of chats.jsonl: the file of its transcript and how many turns of other members it left out.
type ChatEntry struct {
	File        string    `json:"file"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"createdAt"`
	LastTurnAt  time.Time `json:"lastTurnAt"`
	Turns       int       `json:"turns"`
	OthersTurns int       `json:"othersTurns"`
}

// ChatTurn is a turn of the author of the file: the question, the answer of a completed turn, the outcome and the title of
// the chapter attached to the question.
type ChatTurn struct {
	AskedAt  time.Time `json:"askedAt"`
	Question string    `json:"question"`
	Answer   string    `json:"answer,omitempty"`
	Outcome  string    `json:"outcome"`
	Attached string    `json:"attached,omitempty"`
}

// ChatTranscript is chats/NNN.json.
type ChatTranscript struct {
	Turns []ChatTurn `json:"turns"`
}

// Counts are the records of a file the manifest counts.
type Counts struct {
	Books     int `json:"books"`
	Chapters  int `json:"chapters"`
	Cards     int `json:"cards"`
	Materials int `json:"materials"`
	Memory    int `json:"memory"`
	Notes     int `json:"notes"`
	Chats     int `json:"chats"`
}

// LeftOut is what the saga has and the file does not carry (SF1.9).
type LeftOut struct {
	Drafts          int `json:"drafts"`
	Drawings        int `json:"drawings"`
	TrashedBooks    int `json:"trashedBooks"`
	TrashedChapters int `json:"trashedChapters"`
	OthersTurns     int `json:"othersTurns"`
}

// ManifestSaga names the saga of a file.
type ManifestSaga struct {
	Name string `json:"name"`
}

// Manifest is manifest.json, the last record: who made the file and when, what it holds and the SHA-256 of every other
// record (SF1.1).
type Manifest struct {
	Format       string            `json:"format"`
	Version      int               `json:"version"`
	CreatedBy    string            `json:"createdBy"`
	CreatedAt    time.Time         `json:"createdAt"`
	Saga         ManifestSaga      `json:"saga"`
	Counts       Counts            `json:"counts"`
	ChatsNotRead []string          `json:"chatsNotRead"`
	LeftOut      LeftOut           `json:"leftOut"`
	Sections     []string          `json:"sections"`
	Files        map[string]string `json:"files"`
}

var (
	coverName    = regexp.MustCompile(`^covers/[0-9]{3,6}\.(jpg|png|webp)$`)
	chatName     = regexp.MustCompile(`^chats/[0-9]{3,6}\.json$`)
	workshopName = regexp.MustCompile(`^workshops/[a-z0-9-]{1,40}(/[A-Za-z0-9._-]{1,80}){1,4}$`)
	uuidPattern  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hashPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// fixedEntries are the records of the table; the optional ones may be absent.
var fixedEntries = map[string]bool{
	EntrySaga: true, EntryBooks: true, EntryChapters: true, EntryCards: true, EntryNames: false, EntryThreads: false,
	EntryMapHidden: true, EntryMaterials: true, EntryMemory: true, EntryNotes: true, EntryChats: true,
}

// coverExtensions are the files of the covers by their type.
var coverExtensions = map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"}

// CoverFile names the file of the cover of the nth book with a cover of the type.
func CoverFile(n int, mime string) (string, bool) {
	extension, known := coverExtensions[mime]
	if !known {
		return "", false
	}
	return fmt.Sprintf("covers/%03d.%s", n, extension), true
}

// ChatFile names the transcript of the nth chat.
func ChatFile(n int) string { return fmt.Sprintf("chats/%03d.json", n) }

// QuotedBody is the thought of a note with its quote above it as a quote of Markdown: the quote of a chat, which no saga
// carries as the source of a note, or of a chapter the file does not carry (SF1.7, SF3.7). The quote is cut with «…» so the
// note keeps within its limit; a thought that leaves no room for a quote goes alone, and a thought of nothing but spaces
// is no thought.
func QuotedBody(quote, body string) string {
	lines := strings.Split(quote, "\n")
	for index, text := range lines {
		lines[index] = "> " + text
	}
	quoted := strings.Join(lines, "\n")
	if strings.TrimSpace(body) == "" {
		return cut(quoted, notes.MaxTextLength)
	}
	allowed := notes.MaxTextLength - utf8.RuneCountInString(body) - len("\n\n")
	if allowed < 1 {
		return body
	}
	return cut(quoted, allowed) + "\n\n" + body
}

// cut keeps a text within limit signs, the last of them «…» when it is cut.
func cut(text string, limit int) string {
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}
