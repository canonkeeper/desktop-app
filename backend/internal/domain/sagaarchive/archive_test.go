// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	library "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/domain/notes"
	"canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// The saga of the tests is made up: «Северный предел».
const (
	bookOne    = "11111111-1111-4111-8111-111111111111"
	bookTwo    = "22222222-2222-4222-8222-222222222222"
	chapterOne = "aaaaaaaa-0000-4000-8000-000000000001"
	chapterTwo = "aaaaaaaa-0000-4000-8000-000000000002"
	chapterSix = "aaaaaaaa-0000-4000-8000-000000000006"
)

var at = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)

func paragraph(id string, order int, markdown string) Paragraph {
	return Paragraph{ID: id, Order: order, Kind: "paragraph", Markdown: markdown}
}

func fixtureChapters() []Chapter {
	zero, one := 0, 1
	return []Chapter{
		{Ref: chapterOne, Book: bookOne, Order: 1, PublishedOrder: &zero, Title: "Глава 1. Перевал", Paragraphs: []Paragraph{
			paragraph("bbbbbbbb-0000-4000-8000-000000000001", 1, "Мара поднялась к перевалу на рассвете."),
			paragraph("bbbbbbbb-0000-4000-8000-000000000002", 2, "Ветер нёс *снег* и **соль**."),
		}},
		{Ref: chapterTwo, Book: bookOne, Order: 3, PublishedOrder: &one, Title: "Глава 2. Застава", Paragraphs: []Paragraph{
			paragraph("bbbbbbbb-0000-4000-8000-000000000003", 1, "На заставе Тарен ждал её третий день."),
		}},
		// A chapter only in the draft text.
		{Ref: chapterSix, Book: bookTwo, Order: 1, Title: "Северный маяк", Paragraphs: []Paragraph{
			paragraph("bbbbbbbb-0000-4000-8000-000000000006", 1, "Маяк горел без смотрителя."),
		}},
	}
}

func fixtureCard(t *testing.T, chapter Chapter, body string) Card {
	t.Helper()
	digest, ok := ContentHash(chapter.Paragraphs)
	if !ok {
		t.Fatal("the digest of a chapter of the saga")
	}
	return Card{Chapter: chapter.Ref, ContentHash: digest, Version: sagamemory.CardVersion, Model: "cards-model", Card: json.RawMessage(body)}
}

func coverPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewGray(image.Rect(0, 0, width, height))
	for i := range picture.Pix {
		picture.Pix[i] = color.Gray{Y: 200}.Y
	}
	var out bytes.Buffer
	if err := png.Encode(&out, picture); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type fixture struct {
	chapters []Chapter
	cards    []Card
	cover    []byte
}

func newFixture(t *testing.T) fixture {
	chapters := fixtureChapters()
	return fixture{
		chapters: chapters,
		cards: []Card{
			fixtureCard(t, chapters[0], `{"summary":"Мара идёт через перевал.","characters":[{"name":"Мара","aliases":["Марушка"]}]}`),
			fixtureCard(t, chapters[1], `{"summary":"Тарен ждёт Мару.","characters":[{"name":"Тарен"},{"name":"Мара"}]}`),
		},
		cover: coverPNG(t, 600, 900),
	}
}

// write writes the saga of the fixture; change changes a record before it goes.
func (fixture fixture) write(t *testing.T, change func(*Writer) error) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := NewWriter(&out)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(writer.Saga(Saga{Name: "Северный предел", Description: "Сага о дороге на север.", BookKeyNumbers: 3}))
	books, err := writer.Books()
	must(err)
	subtitle, number, colour := "Книга первая", "1", "#336699"
	cover := sha256.Sum256(fixture.cover)
	must(books.Add(Book{Ref: bookOne, KeyNumber: 1, Order: 2, Title: "Перевал", Subtitle: &subtitle, DisplayNumber: &number, CoverColor: &colour,
		Cover: &Cover{File: "covers/001.png", MIME: "image/png", SHA256: hex.EncodeToString(cover[:])}}))
	must(books.Add(Book{Ref: bookTwo, KeyNumber: 3, Order: 1, Title: "Маяк"}))
	must(books.Close())
	must(writer.File("covers/001.png", fixture.cover))
	chapters, err := writer.Chapters()
	must(err)
	for _, chapter := range fixture.chapters {
		must(chapters.Add(chapter))
	}
	must(chapters.Close())
	cards, err := writer.Cards()
	must(err)
	parsed := map[int]*sagamemory.Card{}
	for index, card := range fixture.cards {
		must(cards.Add(card))
		value, err := sagamemory.ParseCard(string(card.Card), 2)
		must(err)
		parsed[index] = &value
	}
	must(cards.Close())
	_, digest := sagamemory.BuildNameList(parsed)
	must(writer.Names(Names{Digest: digest, Model: "names-model", Names: sagamemory.NameRegistry{
		Persons: []sagamemory.NamePerson{{Name: "мара", Names: []string{"марушка"}}}, Shared: []string{}, Wrong: []sagamemory.WrongAlias{},
	}}))
	must(writer.Threads(Threads{
		Model:  "layer-model",
		Stamps: []Stamp{{Chapter: chapterOne, H: fixture.cards[0].ContentHash, CardVersion: 4}, {Chapter: chapterTwo, H: fixture.cards[1].ContentHash, CardVersion: 4}},
		Books: []sagalayer.Book{{ID: bookOne, Order: 1, Digest: fixture.cards[0].ContentHash, Arcs: []sagalayer.Arc{{
			Title: "Дорога", From: sagalayer.Ref{Chapter: chapterOne, P: 1}, To: sagalayer.Ref{Chapter: chapterTwo},
		}}}},
		Threads: []sagalayer.Thread{{Text: "Кто ждёт Мару на заставе", First: []sagalayer.Ref{{Chapter: chapterTwo, P: 1}}}},
	}))
	hidden, err := writer.MapHidden()
	must(err)
	must(hidden.Add(MapHidden{NodeKey: "тарен", NodeName: "Тарен", CreatedAt: at}))
	must(hidden.Close())
	materials, err := writer.Materials()
	must(err)
	must(materials.Add(Material{Name: "Северный маяк", Kind: "place", Description: "Маяк на краю льдов.", Properties: []Property{{Key: "Высота", Value: "сорок саженей"}},
		Section: "Места", SourceName: "мир.docx", SourceKind: "docx", CreatedAt: at, UpdatedAt: at}))
	must(materials.Close())
	memory, err := writer.Memory()
	must(err)
	must(memory.Add(Memory{Kind: "correction", Body: "Тарен — брат Мары.", Name: "Тарен", Chapter: chapterTwo, Origin: "chat", CreatedAt: at, UpdatedAt: at}))
	must(memory.Add(Memory{Kind: "plan", Body: "В третьей книге маяк погаснет.", Origin: "manual", CreatedAt: at, UpdatedAt: at}))
	must(memory.Close())
	notes, err := writer.Notes()
	must(err)
	paragraphID := "bbbbbbbb-0000-4000-8000-000000000003"
	must(notes.Add(Note{Title: "Застава", Body: "Сколько он ждал?", Source: &NoteSource{Kind: NoteSourceChapter, Chapter: chapterTwo,
		Paragraph: paragraphID, Range: &library.TextRange{StartOffset: 0, EndOffset: 10}, Quote: "На заставе"}, CreatedAt: at, UpdatedAt: at}))
	must(notes.Add(Note{Title: "Из чата", Body: "", Source: &NoteSource{Kind: NoteSourceChat, Quote: "Мара старше Тарена."}, CreatedAt: at, UpdatedAt: at}))
	must(notes.Close())
	if change != nil {
		must(change(writer))
	}
	must(writer.Transcript(ChatFile(1), ChatTranscript{Turns: []ChatTurn{{AskedAt: at, Question: "Кто ждёт Мару?",
		Answer: "Тарен, [гл. 2 ¶1](source:2/1).", Outcome: TurnAnswered, Attached: "Набросок главы"}}}))
	chats, err := writer.Chats()
	must(err)
	must(chats.Add(ChatEntry{File: ChatFile(1), Title: "Кто ждёт Мару", CreatedAt: at, LastTurnAt: at.Add(time.Minute), Turns: 1, OthersTurns: 2}))
	must(chats.Close())
	must(writer.Close(Manifest{CreatedBy: "CanonKeeper 0.1.0", CreatedAt: at, Saga: ManifestSaga{Name: "Северный предел"},
		ChatsNotRead: []string{"Чат от 12.11.2026"}, LeftOut: LeftOut{Drafts: 1, TrashedBooks: 1, OthersTurns: 2}}))
	return out.Bytes()
}

func read(t *testing.T, file []byte) (*Archive, error) {
	t.Helper()
	return Read(bytes.NewReader(file), int64(len(file)))
}

func code(err error) string {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	if err != nil {
		return "error: " + err.Error()
	}
	return ""
}

// specs/2026-11-13-desk-sagafile.md SF1.1, SF1.2, SF1.10: the writer writes records only of the table, each once, the manifest
// last with the SHA-256 of every other record, and what it wrote passes every check of the reader.
func TestTheWriterWritesWhatTheReaderTakes(t *testing.T) {
	fixture := newFixture(t)
	file := fixture.write(t, nil)
	zipped, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range zipped.File {
		names = append(names, entry.Name)
	}
	want := []string{EntrySaga, EntryBooks, "covers/001.png", EntryChapters, EntryCards, EntryNames, EntryThreads, EntryMapHidden,
		EntryMaterials, EntryMemory, EntryNotes, "chats/001.json", EntryChats, EntryManifest}
	if !slices.Equal(names, want) {
		t.Fatalf("the records = %v\nwant %v", names, want)
	}
	archive, err := read(t, file)
	if err != nil {
		t.Fatalf("the reader refused the file of the writer: %v", err)
	}
	for _, entry := range zipped.File[:len(zipped.File)-1] {
		source, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.New()
		if _, err := io.Copy(sum, source); err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(sum.Sum(nil)); archive.Manifest.Files[entry.Name] != got {
			t.Fatalf("the SHA-256 of %s = %s, the manifest says %s", entry.Name, got, archive.Manifest.Files[entry.Name])
		}
	}
	if archive.Manifest.Counts != (Counts{Books: 2, Chapters: 3, Cards: 2, Materials: 1, Memory: 2, Notes: 2, Chats: 1}) {
		t.Fatalf("the counts = %+v", archive.Manifest.Counts)
	}
	if len(archive.Cards) != 2 || !archive.Cards[0].Accepted || !archive.Cards[1].Accepted || !archive.NamesFresh {
		t.Fatalf("the cards = %+v, fresh names %v; want both accepted and the registry fresh", archive.Cards, archive.NamesFresh)
	}
	var read []Chapter
	if err := archive.EachChapter(func(chapter Chapter) error {
		read = append(read, chapter)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(read)
	wantJSON, _ := json.Marshal(fixture.chapters)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("the chapters read again = %s\nwant %s", gotJSON, wantJSON)
	}
	if !archive.Chapters[1].HasParagraph("bbbbbbbb-0000-4000-8000-000000000003") || archive.Chapters[1].HasParagraph("bbbbbbbb-0000-4000-8000-000000000001") {
		t.Fatal("the paragraphs of a chapter of the file are not its own")
	}
	if order := archive.ReadingOrder(); !slices.Equal(order, []string{chapterSix, chapterOne, chapterTwo}) {
		t.Fatalf("the order of reading = %v; want the book on place 1 first", order)
	}
	cover, err := archive.Cover("covers/001.png")
	if err != nil || !bytes.Equal(cover, fixture.cover) {
		t.Fatalf("the cover read again = %d bytes, %v", len(cover), err)
	}
	if picture, ok := CoverPicture(cover, "image/png"); !ok || picture.Width != 600 {
		t.Fatalf("the cover of the file = %+v, %v; want it taken", picture, ok)
	}
	turns := 0
	if err := archive.EachChat(func(entry ChatEntry, transcript ChatTranscript) error {
		turns += len(transcript.Turns)
		return nil
	}); err != nil || turns != 1 {
		t.Fatalf("the chats read again = %d turns, %v", turns, err)
	}
	failure := errors.New("the base is gone")
	if err := archive.EachChapter(func(Chapter) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("an error of the caller of a second reading = %v; want it back as it is", err)
	}
}

// SF1.1: a file cut anywhere is damaged — never another saga, never a file of the wrong kind.
func TestAFileCutAnywhereIsDamaged(t *testing.T) {
	file := newFixture(t).write(t, nil)
	for cut := 1; cut < len(file); cut += max(1, len(file)/400) {
		if _, err := read(t, file[:cut]); code(err) != CodeDamaged {
			t.Fatalf("a file cut at %d of %d = %v; want it damaged", cut, len(file), err)
		}
	}
}

// SF1.4, SF3.5: a card whose digest is not the one of its chapter in the file, or of a version newer than the product knows,
// is not taken, and the file still opens; the registry of names of other cards is not fresh.
func TestACardOfAnotherTextIsNotTaken(t *testing.T) {
	fixture := newFixture(t)
	fixture.cards[0].ContentHash = fixture.cards[1].ContentHash
	fixture.cards[1].Version = sagamemory.CardVersion + 1
	archive, err := read(t, fixture.write(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if archive.Cards[0].Accepted || archive.Cards[1].Accepted || archive.NamesFresh {
		t.Fatalf("the cards = %+v, fresh %v; want neither taken and the registry not fresh", archive.Cards, archive.NamesFresh)
	}
}

// SF3.9: a cover of a picture too large to unpack or of a type other than its book names is not taken.
func TestACoverOutOfTheRulesIsNotTaken(t *testing.T) {
	if _, ok := CoverPicture(coverPNG(t, 600, 900), "image/jpeg"); ok {
		t.Fatal("a PNG named a JPEG is taken")
	}
	if _, ok := CoverPicture(coverPNG(t, 400, 900), "image/png"); ok {
		t.Fatal("a cover narrower than an upload takes is taken")
	}
	// The header of a PNG of 50 000 × 50 000: its size says nothing of what it unpacks into.
	huge := coverPNG(t, 600, 900)
	huge = append([]byte(nil), huge...)
	putBigEndian(huge[16:20], 50_000)
	putBigEndian(huge[20:24], 50_000)
	if _, ok := CoverPicture(huge, "image/png"); ok {
		t.Fatal("a cover of 50 000 × 50 000 is taken")
	}
}

func putBigEndian(target []byte, value uint32) {
	target[0], target[1], target[2], target[3] = byte(value>>24), byte(value>>16), byte(value>>8), byte(value)
}

// SF1.10: the writer refuses a record the reader would refuse.
func TestTheWriterRefusesWhatTheReaderRefuses(t *testing.T) {
	writer := NewWriter(&bytes.Buffer{})
	chapters, err := writer.Chapters()
	if err != nil {
		t.Fatal(err)
	}
	bad := fixtureChapters()[0]
	bad.Paragraphs[0].Markdown = "Знак\x01управления"
	if err := chapters.Add(bad); !errors.Is(err, ErrUnwritable) {
		t.Fatalf("a paragraph with a sign of control = %v; want it refused", err)
	}
	if err := writer.Close(Manifest{CreatedBy: "x", CreatedAt: at, Saga: ManifestSaga{Name: "x"}}); err == nil {
		t.Fatal("a writer that failed closed the file")
	}
}

// SF1.10: a paragraph with a break of a line inside — a line of verse, a break of a line in Word — is a paragraph of the
// book as the import keeps it: the writer writes it, the reader takes it as it is and the card of its chapter is taken; a
// carriage return and other signs of control stay refused.
func TestAParagraphWithABreakOfALineGoesIntoTheFile(t *testing.T) {
	fixture := newFixture(t)
	fixture.chapters[0].Paragraphs[0].Markdown = "Мара поднялась к перевалу\nна *рассвете*."
	fixture.cards[0] = fixtureCard(t, fixture.chapters[0], `{"summary":"Мара идёт через перевал."}`)
	archive, err := read(t, fixture.write(t, nil))
	if err != nil {
		t.Fatalf("the reader refused a paragraph with a break of a line: %v", err)
	}
	var first Chapter
	if err := archive.EachChapter(func(chapter Chapter) error {
		if chapter.Ref == chapterOne {
			first = chapter
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := first.Paragraphs[0].Markdown; got != "Мара поднялась к перевалу\nна *рассвете*." {
		t.Fatalf("the paragraph read again = %q", got)
	}
	if !archive.Cards[0].Accepted {
		t.Fatal("the card of a chapter with a break of a line is not taken")
	}
	for _, sign := range []string{"\r", "\r\n", "\v", "\f", "\u0085"} {
		writer := NewWriter(&bytes.Buffer{})
		chapters, err := writer.Chapters()
		if err != nil {
			t.Fatal(err)
		}
		bad := fixtureChapters()[0]
		bad.Paragraphs[0].Markdown = "Строка" + sign + "строка"
		if err := chapters.Add(bad); !errors.Is(err, ErrUnwritable) {
			t.Fatalf("a paragraph with %q = %v; want it refused", sign, err)
		}
	}
}

// SF1.10, SF3.1: the names of a saga, a book and a chapter pass the checks of their domains — words within the limit, no
// zero — so a tab or another sign the product keeps in a name does not stop the save, and the file opens.
func TestTheNamesOfTheProductGoAsTheyAre(t *testing.T) {
	var out bytes.Buffer
	writer := NewWriter(&out)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	name, subtitle, number := "Северный\tпредел", "Книга\u0001первая", strings.Repeat("I", 60)
	must(writer.Saga(Saga{Name: name, Description: "Сага\tо дороге� на север.", BookKeyNumbers: 1}))
	books, err := writer.Books()
	must(err)
	must(books.Add(Book{Ref: bookOne, KeyNumber: 1, Order: 1, Title: "Перевал\tпервый", Subtitle: &subtitle, DisplayNumber: &number}))
	must(books.Close())
	chapters, err := writer.Chapters()
	must(err)
	must(chapters.Add(Chapter{Ref: chapterOne, Book: bookOne, Order: 1, Title: "Глава\t1. Перевал�", Paragraphs: []Paragraph{
		paragraph("bbbbbbbb-0000-4000-8000-000000000001", 1, "Мара поднялась к перевалу."),
	}}))
	must(chapters.Close())
	must(writer.Close(Manifest{CreatedBy: "CanonKeeper 0.1.0", CreatedAt: at, Saga: ManifestSaga{Name: name}}))
	archive, err := read(t, out.Bytes())
	if err != nil {
		t.Fatalf("the reader refused names the product keeps: %v", err)
	}
	if archive.Saga.Name != name || archive.Books[0].Title != "Перевал\tпервый" || archive.Chapters[0].Title != "Глава\t1. Перевал�" {
		t.Fatalf("the names of the file = %q, %q, %q", archive.Saga.Name, archive.Books[0].Title, archive.Chapters[0].Title)
	}
	for _, bad := range []Saga{{Name: "Северный\x00предел"}, {Name: " \t "}, {Name: strings.Repeat("с", MaxSagaNameRunes+1)}} {
		if err := NewWriter(&bytes.Buffer{}).Saga(bad); !errors.Is(err, ErrUnwritable) {
			t.Fatalf("a name %q = %v; want it refused", bad.Name, err)
		}
	}
}

// SF1.7, SF3.7: the quote of a note that loses its source stands above the thought, and the note keeps within the limit of
// a note whatever its thought: the quote is cut first, and a thought with no room beside it goes alone; a thought of
// nothing but spaces makes no note without words.
func TestAQuoteAboveAThoughtKeepsTheNoteWithinItsLimit(t *testing.T) {
	quote := strings.Repeat("Мара старше Тарена. ", 20)
	for _, thought := range []string{"", "Проверить.", strings.Repeat("м", notes.MaxTextLength-3), strings.Repeat("м", notes.MaxTextLength-2),
		strings.Repeat("м", notes.MaxTextLength-1), strings.Repeat("м", notes.MaxTextLength), strings.Repeat(" ", notes.MaxTextLength-1)} {
		body := QuotedBody(quote, thought)
		if notes.ValidateText(body) != nil || strings.TrimSpace(body) == "" {
			t.Fatalf("a quote above a thought of %d signs = %d signs; want a note within %d with words", utf8.RuneCountInString(thought),
				utf8.RuneCountInString(body), notes.MaxTextLength)
		}
		if strings.TrimSpace(thought) != "" && !strings.HasSuffix(body, thought) {
			t.Fatalf("the thought of %d signs is not kept whole", utf8.RuneCountInString(thought))
		}
	}
	if body := QuotedBody("Тарен — брат Мары.", "Проверить."); body != "> Тарен — брат Мары.\n\nПроверить." {
		t.Fatalf("a short quote above a thought = %q", body)
	}
}

func sortedKeys(files map[string]string) []string {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
