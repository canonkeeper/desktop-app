// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"testing"
	"time"

	library "canonkeeper/backend/internal/domain/library"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
	"canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// The saga of the tests is made up: «Северный предел». Its file has what a saga of the site has after a life: the shelf
// in another order than the numbers of the books, a number given to a book that is gone (the counter above every live
// number), two chapters «Глава 5» in two books, a gap in the order of the chapters of a book, a chapter only in the draft
// text, cards of versions 3 and 4 and one carried over a change of signs, the registry of names, a layer behind by one
// chapter, materials, memory of every origin, notes, a chat and a cover.

var fixtureAt = time.Date(2026, 11, 12, 18, 30, 0, 0, time.UTC)

func ref(kind string, n int) string { return fmt.Sprintf("%s-0000-4000-8000-%012d", kind, n) }

func bookRef(n int) string    { return ref("b0000000", n) }
func chapterRef(n int) string { return ref("c0000000", n) }
func paragraphID(chapter, n int) string {
	return fmt.Sprintf("d%07d-0000-4000-8000-%012d", chapter, n)
}

type fixtureChapter struct {
	chapter domain.Chapter
	card    string
	version int
	built   string
}

func fixtureChapters() []fixtureChapter {
	zero, one, two := 0, 1, 2
	paragraphs := func(chapter int, texts ...string) []domain.Paragraph {
		out := make([]domain.Paragraph, len(texts))
		for index, text := range texts {
			out[index] = domain.Paragraph{ID: paragraphID(chapter, index+1), Order: index + 1, Kind: "paragraph", Markdown: text}
		}
		return out
	}
	mara := `{"summary":"Мара идёт к перевалу.","characters":[{"name":"Мара","aliases":["Марушка"]},{"name":"Тарен"}]}`
	return []fixtureChapter{
		{chapter: domain.Chapter{Ref: chapterRef(1), Book: bookRef(1), Order: 1, PublishedOrder: &zero, Title: "Глава 5. Перевал",
			Paragraphs: paragraphs(1, "Мара поднялась к перевалу на рассвете.", "Ветер нёс *снег* и **соль**.")}, card: mara, version: 4},
		{chapter: domain.Chapter{Ref: chapterRef(2), Book: bookRef(1), Order: 3, PublishedOrder: &one, Title: "Застава",
			Paragraphs: paragraphs(2, "На заставе Тарен ждал её третий день.", "Огонь почти погас.")}, card: mara, version: 3},
		{chapter: domain.Chapter{Ref: chapterRef(3), Book: bookRef(1), Order: 4, PublishedOrder: &two, Title: "Метель",
			Paragraphs: paragraphs(3, "Метель шла двое суток.")}, card: mara, version: 4, built: hexOf("Метель шла двое суток, и ещё одни.")},
		{chapter: domain.Chapter{Ref: chapterRef(4), Book: bookRef(2), Order: 1, PublishedOrder: &zero, Title: "Глава 5. Маяк",
			Paragraphs: paragraphs(4, "Маяк горел без смотрителя.")}, card: `{"summary":"Маяк горит сам.","characters":[{"name":"Тарен"}]}`, version: 4},
		{chapter: domain.Chapter{Ref: chapterRef(5), Book: bookRef(2), Order: 2, PublishedOrder: &one, Title: "Смотритель",
			Paragraphs: paragraphs(5, "Смотритель ушёл в море.", "Его лодку нашли у скал.")}, card: `{"summary":"Смотритель пропал.","characters":[{"name":"Ойвин","aliases":["смотритель"]}]}`, version: 4},
		{chapter: domain.Chapter{Ref: chapterRef(6), Book: bookRef(3), Order: 1, Title: "Лёд",
			Paragraphs: paragraphs(6, "Лёд держал до весны.")}, card: `{"summary":"Зима держится.","characters":[{"name":"Тарен"}]}`, version: 4},
	}
}

func hexOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func coverPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewGray(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// fixtureOptions change the file of the saga for a test.
type fixtureOptions struct {
	name     string
	covers   map[int][]byte
	mime     map[int]string
	workshop bool
}

// northernLimit writes the file of the saga.
func northernLimit(t *testing.T, options fixtureOptions) []byte {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	writer := domain.NewWriter(&out)
	name := options.name
	if name == "" {
		name = "Северный предел"
	}
	must(writer.Saga(domain.Saga{Name: name, Description: "Сага о дороге на север.", BookKeyNumbers: 4}))
	books, err := writer.Books()
	must(err)
	subtitle, colour := "Книга первая", "#336699"
	shelf := []domain.Book{
		{Ref: bookRef(1), KeyNumber: 1, Order: 2, Title: "Перевал", Subtitle: &subtitle, CoverColor: &colour},
		{Ref: bookRef(2), KeyNumber: 2, Order: 1, Title: "Маяк"},
		{Ref: bookRef(3), KeyNumber: 4, Order: 3, Title: "Лёд"},
	}
	var files []string
	for index := range shelf {
		number := fmt.Sprint(shelf[index].Order)
		shelf[index].DisplayNumber = &number
		content, covered := options.covers[index+1]
		if !covered {
			continue
		}
		mime := "image/png"
		if declared, ok := options.mime[index+1]; ok {
			mime = declared
		}
		file, _ := domain.CoverFile(len(files)+1, mime)
		sum := sha256.Sum256(content)
		shelf[index].Cover = &domain.Cover{File: file, MIME: mime, SHA256: hex.EncodeToString(sum[:])}
		files = append(files, file)
	}
	for _, book := range shelf {
		must(books.Add(book))
	}
	must(books.Close())
	for index, book := range shelf {
		if book.Cover != nil {
			must(writer.File(book.Cover.File, options.covers[index+1]))
		}
	}
	chapters, err := writer.Chapters()
	must(err)
	fixture := fixtureChapters()
	for _, item := range fixture {
		must(chapters.Add(item.chapter))
	}
	must(chapters.Close())
	cards, err := writer.Cards()
	must(err)
	parsed := map[int]*sagamemory.Card{}
	hashes := map[string]string{}
	for index, item := range fixture {
		digest, _ := domain.ContentHash(item.chapter.Paragraphs)
		hashes[item.chapter.Ref] = digest
		card := domain.Card{Chapter: item.chapter.Ref, ContentHash: digest, Version: item.version, Model: "cards-model", Card: json.RawMessage(item.card)}
		if item.built != "" {
			built := item.built
			card.BuiltFrom = &built
		}
		must(cards.Add(card))
		value, err := sagamemory.ParseCard(item.card, len(item.chapter.Paragraphs))
		must(err)
		parsed[index] = &value
	}
	must(cards.Close())
	_, digest := sagamemory.BuildNameList(parsed)
	must(writer.Names(domain.Names{Digest: digest, Model: "names-model", Names: sagamemory.NameRegistry{
		Persons: []sagamemory.NamePerson{{Name: "мара", Names: []string{"марушка"}}}, Shared: []string{}, Wrong: []sagamemory.WrongAlias{},
	}}))
	// The layer was built in the order of reading — «Маяк» stands first on the shelf — and the card of «Застава» changed
	// since: the layer is one chapter behind, less than its rebuild needs.
	stamp := func(chapter int) domain.Stamp {
		item := fixture[chapter-1]
		filed := domain.Stamp{Chapter: item.chapter.Ref, H: hashes[item.chapter.Ref], CardVersion: item.version}
		if item.built != "" {
			built := item.built
			filed.K = &built
		}
		return filed
	}
	stamps := []domain.Stamp{stamp(4), stamp(5), stamp(1), stamp(2), stamp(3), stamp(6)}
	stamps[3].H = hexOf("Застава до правки")
	layerStamps := func(list []domain.Stamp) []sagalayer.Stamp {
		out := make([]sagalayer.Stamp, len(list))
		for index, item := range list {
			out[index] = sagalayer.Stamp{Chapter: item.Chapter, Hash: item.H, CardVersion: item.CardVersion}
			if item.K != nil {
				out[index].Card = *item.K
			}
		}
		return out
	}
	must(writer.Threads(domain.Threads{
		Model: "layer-model", Stamps: stamps,
		Books: []sagalayer.Book{
			{ID: bookRef(2), Order: 1, Digest: sagalayer.Digest(layerStamps(stamps[:2])), Threads: []sagalayer.Thread{{Text: "Кто зажигает маяк", First: []sagalayer.Ref{{Chapter: chapterRef(4), P: 1}}}}},
			{ID: bookRef(1), Order: 2, Digest: sagalayer.Digest(layerStamps(stamps[2:5])), Arcs: []sagalayer.Arc{{Title: "Дорога", From: sagalayer.Ref{Chapter: chapterRef(1)}, To: sagalayer.Ref{Chapter: chapterRef(3)}}}},
			{ID: bookRef(3), Order: 3, Digest: sagalayer.Digest(layerStamps(stamps[5:]))},
		},
		Threads: []sagalayer.Thread{{Text: "Кто ждёт Мару на заставе", First: []sagalayer.Ref{{Chapter: chapterRef(2), P: 1}}}},
	}))
	hidden, err := writer.MapHidden()
	must(err)
	must(hidden.Add(domain.MapHidden{NodeKey: "тарен", NodeName: "Тарен", CreatedAt: fixtureAt}))
	must(hidden.Close())
	materials, err := writer.Materials()
	must(err)
	must(materials.Add(domain.Material{Name: "Северный маяк", Kind: "place", Description: "Маяк на краю льдов.", Properties: []domain.Property{{Key: "Высота", Value: "сорок саженей"}},
		Section: "Места", SourceName: "мир.docx", SourceKind: "docx", CreatedAt: fixtureAt, UpdatedAt: fixtureAt}))
	must(materials.Add(domain.Material{Name: "Тарен", Kind: "character", Properties: []domain.Property{}, SourceKind: "manual", CreatedAt: fixtureAt, UpdatedAt: fixtureAt}))
	must(materials.Close())
	memory, err := writer.Memory()
	must(err)
	for index, origin := range []string{"manual", "chat", "draft", "drawing"} {
		record := domain.Memory{Kind: "correction", Body: fmt.Sprintf("Запись %d о Таренe.", index+1), Name: "Тарен", Origin: origin, CreatedAt: fixtureAt.Add(time.Duration(index) * time.Minute), UpdatedAt: fixtureAt}
		if index == 1 {
			record.Chapter = chapterRef(2)
		}
		must(memory.Add(record))
	}
	must(memory.Close())
	notes, err := writer.Notes()
	must(err)
	paragraph := paragraphID(2, 1)
	must(notes.Add(domain.Note{Title: "Застава", Body: "Сколько он ждал?", Source: &domain.NoteSource{Kind: domain.NoteSourceChapter, Chapter: chapterRef(2),
		Paragraph: paragraph, Range: &library.TextRange{StartOffset: 3, EndOffset: 10}, Quote: "заставе"}, CreatedAt: fixtureAt, UpdatedAt: fixtureAt}))
	must(notes.Add(domain.Note{Title: "Прежняя правка", Body: "", Source: &domain.NoteSource{Kind: domain.NoteSourceChapter, Chapter: chapterRef(1),
		Paragraph: paragraphID(1, 9), Quote: "перевалу"}, CreatedAt: fixtureAt, UpdatedAt: fixtureAt}))
	must(notes.Add(domain.Note{Title: "Из чата", Body: "Проверить.", Source: &domain.NoteSource{Kind: domain.NoteSourceChat, Quote: "Тарен — брат Мары."},
		CreatedAt: fixtureAt, UpdatedAt: fixtureAt}))
	must(notes.Close())
	key := "b1c1"
	must(writer.Transcript(domain.ChatFile(1), domain.ChatTranscript{Turns: []domain.ChatTurn{{AskedAt: fixtureAt, Question: "Кто ждёт Мару?",
		Answer: "Тарен, [Застава ¶1](source:" + key + "/1).", Outcome: domain.TurnAnswered, Attached: "Набросок главы"}}}))
	chats, err := writer.Chats()
	must(err)
	must(chats.Add(domain.ChatEntry{File: domain.ChatFile(1), Title: "Кто ждёт Мару", CreatedAt: fixtureAt, LastTurnAt: fixtureAt, Turns: 1, OthersTurns: 2}))
	must(chats.Close())
	must(writer.Close(domain.Manifest{CreatedBy: "canonkeeper.ru", CreatedAt: fixtureAt, Saga: domain.ManifestSaga{Name: name},
		LeftOut: domain.LeftOut{TrashedBooks: 1, OthersTurns: 2}}))
	return out.Bytes()
}
