package bookexport_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/bookexport"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/domain/textface"
)

// specs/2026-11-12-ux10-book-pictures.md BP2.1: a file without drawings is the file it was before them, to the byte.
func TestAFileWithoutPicturesIsTheSameToTheByte(t *testing.T) {
	want := map[bookexport.Format][2]string{
		bookexport.FormatDOCX: {"26c8144e4da699543065d1af9dc43210d67a61559ac1b0334ef54df22feec1c4", "0162911e85b8fbe6097cb23e86728d5bab142691cae4ce08ae83ddef2078a54c"},
		bookexport.FormatEPUB: {"10c2c0656772895f9dea4de76182327321b777824f420db69ae8317c40a41045", "70a61dd13ca4d10fcd0f2b0452709533decebc17ae158111975c749bd198aa4f"},
		bookexport.FormatFB2:  {"fa0026ec2d5d069076f9091d2fdad71c619729de984426095de0f4d15f01e8c7", "ed2a2350a4e42c1ed0f27a7270f825e9e9b8f3ef429789adfb29386fa76fb766"},
	}
	for _, format := range allFormats {
		for index, cover := range []bool{true, false} {
			sum := sha256.Sum256(written(t, format, testBook(cover, smallBook...)))
			if got := hex.EncodeToString(sum[:]); got != want[format][index] {
				t.Errorf("%s, cover %v: the file changed: %s", format, cover, got)
			}
		}
	}
}

// testImage is a picture of a drawing, JPEG or PNG of its size, in noise that does not compress.
func testImage(width, height int, asPNG bool) bookexport.Image {
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	seed := uint32(width*31 + height)
	for y := range height {
		for x := range width {
			seed = seed*1664525 + 1013904223
			picture.Set(x, y, color.RGBA{R: uint8(seed >> 24), G: uint8(seed >> 16), B: uint8(seed >> 8), A: 255})
		}
	}
	var content bytes.Buffer
	if asPNG {
		if err := png.Encode(&content, picture); err != nil {
			panic(err)
		}
		return bookexport.Image{MediaType: "image/png", Content: content.Bytes(), Width: width, Height: height}
	}
	if err := jpeg.Encode(&content, picture, nil); err != nil {
		panic(err)
	}
	return bookexport.Image{MediaType: "image/jpeg", Content: content.Bytes(), Width: width, Height: height}
}

func picture(chapter, after int, name string, content bookexport.Image) bookexport.Picture {
	return bookexport.Picture{Chapter: chapter, After: after, Name: name, Read: func() (bookexport.Image, error) { return content, nil }}
}

// The book of the pictures: the second chapter has a paragraph without words, which the file leaves out while the place
// of a drawing counts it, as the reader counts it (BP1.1).
var illustrated = []chapter{
	{"Глава 1", []string{"Первый.", "Второй.", "Третий."}},
	{"Глава 2", []string{"Начало.", "", "Абзац после пустого.", "Последний."}},
}

var (
	pier     = testImage(40, 30, false)
	fire     = testImage(30, 40, true)
	harbour  = testImage(64, 48, false)
	wantRead = []string{
		"H:Глава 1", "P:Первый.", "IMG:Мара у пристани=" + digest(pier.Content), "P:Второй.", "P:Третий.",
		"H:Глава 2", "P:Начало.", "P:Абзац после пустого.", "IMG:Костёр «у реки» & ночь=" + digest(fire.Content), "P:Последний.",
		"IMG:Порт=" + digest(harbour.Content),
	}
)

func pictureBook() bookexport.Book {
	book := testBook(true, illustrated...)
	book.Pictures = []bookexport.Picture{
		picture(0, 1, "Мара у пристани", pier),
		picture(1, 3, "Костёр «у реки» & ночь", fire),
		// A place beyond the end of the chapter is after its last paragraph.
		picture(1, 9, "Порт", harbour),
	}
	return book
}

// BP2.1, BP2.3: every drawing stands after its paragraph as the reader counts them — a paragraph without words counted,
// a place beyond the end of the chapter after its last paragraph —, alone, named for a screen reader, and its reference
// leads to its own bytes.
func TestPicturesStandAfterTheirParagraphs(t *testing.T) {
	for _, format := range allFormats {
		t.Run(string(format), func(t *testing.T) {
			if got := sequence(t, format, written(t, format, pictureBook())); !reflect.DeepEqual(got, wantRead) {
				t.Fatalf("the file reads\n%q\nwant\n%q", got, wantRead)
			}
		})
	}
}

// BP2.3: the parts of a file with drawings are what the format wants — Word: the type of every picture and a relation to
// each of its parts, the ids of its drawings once each; EPUB 3: every picture in the manifest with its type, nothing in
// the manifest the file has not, the mimetype first; FB2: every binary after the body and named once.
func TestAFileWithPicturesIsProper(t *testing.T) {
	t.Run("docx", func(t *testing.T) {
		contents, names := parts(t, written(t, bookexport.FormatDOCX, pictureBook()))
		for _, name := range names {
			if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
				wellFormed(t, name, contents[name])
			}
		}
		if names[0] != "[Content_Types].xml" {
			t.Fatalf("first part = %q", names[0])
		}
		contains(t, "types", contents["[Content_Types].xml"], `Extension="jpg"`, `Extension="png"`)
		var relationships struct {
			Items []struct {
				Target string `xml:"Target,attr"`
			} `xml:"Relationship"`
		}
		if err := xml.Unmarshal([]byte(contents["word/_rels/document.xml.rels"]), &relationships); err != nil {
			t.Fatal(err)
		}
		for _, item := range relationships.Items {
			if _, found := contents["word/"+item.Target]; !found {
				t.Fatalf("a relation leads to %q, which the file has not", item.Target)
			}
		}
		ids := []string{}
		decoder := xml.NewDecoder(strings.NewReader(contents["word/document.xml"]))
		for {
			token, err := decoder.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "docPr" {
				ids = append(ids, attribute(start, "id"))
			}
		}
		if len(ids) != 4 || len(slices.Compact(slices.Sorted(slices.Values(ids)))) != 4 {
			t.Fatalf("the ids of the drawings: %v", ids)
		}
	})
	t.Run("epub", func(t *testing.T) {
		file := written(t, bookexport.FormatEPUB, pictureBook())
		if !bytes.Equal(file[30:38], []byte("mimetype")) {
			t.Fatalf("the file starts with %q", file[:64])
		}
		contents, names := parts(t, file)
		for _, name := range names {
			if strings.HasSuffix(name, ".xhtml") || strings.HasSuffix(name, ".opf") {
				wellFormed(t, name, contents[name])
			}
		}
		var manifest struct {
			Items []struct {
				Href string `xml:"href,attr"`
				Type string `xml:"media-type,attr"`
			} `xml:"manifest>item"`
		}
		if err := xml.Unmarshal([]byte(contents["OEBPS/content.opf"]), &manifest); err != nil {
			t.Fatal(err)
		}
		listed := map[string]string{}
		for _, item := range manifest.Items {
			if _, found := contents["OEBPS/"+item.Href]; !found {
				t.Fatalf("the manifest lists %q, which the file has not", item.Href)
			}
			listed["OEBPS/"+item.Href] = item.Type
		}
		pictures := 0
		for _, name := range names {
			if strings.HasPrefix(name, "OEBPS/pictures/") {
				pictures++
				if want := map[string]string{".jpg": "image/jpeg", ".png": "image/png"}[path.Ext(name)]; listed[name] != want {
					t.Fatalf("%s is listed as %q", name, listed[name])
				}
			}
		}
		if pictures != 3 {
			t.Fatalf("%d pictures in the file", pictures)
		}
		contains(t, "style", contents["OEBPS/style.css"], `.picture`)
	})
	t.Run("fb2", func(t *testing.T) {
		raw := written(t, bookexport.FormatFB2, pictureBook())
		wellFormed(t, "fb2", string(raw))
		body := bytes.Index(raw, []byte("</body>"))
		if first := bytes.Index(raw, []byte("<binary")); first < body {
			t.Fatalf("a binary at %d before the end of the body at %d", first, body)
		}
		if count := bytes.Count(raw, []byte("<binary")); count != 4 {
			t.Fatalf("%d binaries", count)
		}
	})
}

// BP2.4: a file with drawings, downloaded and given back as «Новая версия из файла», changes nothing: the same chapters
// with the same titles and the same paragraphs in their italic and bold, no paragraph more.
func TestAFileWithPicturesComesBackUnchanged(t *testing.T) {
	faced := []chapter{
		{"Глава 1", []string{"Он *пришёл* и **сел**.", "Второй абзац."}},
		{"Глава 2", []string{"Третий.", "Четвёртый, *последний*."}},
	}
	book := testBook(true, faced...)
	book.Pictures = []bookexport.Picture{picture(0, 1, "Мара у пристани", pier), picture(0, 2, "Костёр", fire), picture(1, 2, "Порт", harbour)}
	// The book compares its plain text with the plain text of the file, as the preview of an update does.
	stored := make([]domainlibrary.BookChapterText, len(faced))
	for index, chapter := range faced {
		stored[index].Title = chapter.title
		for _, paragraph := range chapter.paragraphs {
			stored[index].Paragraphs = append(stored[index].Paragraphs, textface.MarkdownPlain(paragraph))
		}
	}
	for _, format := range allFormats {
		t.Run(string(format), func(t *testing.T) {
			back := readBack(t, format, written(t, format, book))
			if got := chaptersOf(back); !reflect.DeepEqual(got, faced) {
				t.Fatalf("chapters = %#v", got)
			}
			file := make([]domainlibrary.BookChapterText, len(back.Chapters))
			for index, chapter := range back.Chapters {
				file[index].Title = chapter.Title
				for _, paragraph := range chapter.Paragraphs {
					file[index].Paragraphs = append(file[index].Paragraphs, paragraph.Text())
				}
			}
			plan := domainlibrary.PlanBookUpdate(stored, file)
			for index, pairing := range plan.Chapters {
				if pairing.Book != index || pairing.Change != domainlibrary.TextSame || pairing.Renamed {
					t.Fatalf("chapter %d: %+v", index, pairing)
				}
			}
			if len(plan.Removed) != 0 {
				t.Fatalf("removed: %v", plan.Removed)
			}
		})
	}
}

// BP2.4: a picture that cannot be read is left out and the file is written to its end; nothing in it leads to the picture.
func TestAPictureThatCannotBeReadIsLeftOut(t *testing.T) {
	for _, format := range allFormats {
		t.Run(string(format), func(t *testing.T) {
			book := pictureBook()
			book.Pictures[1].Read = func() (bookexport.Image, error) { return bookexport.Image{}, errors.New("the file is gone") }
			want := slices.DeleteFunc(slices.Clone(wantRead), func(entry string) bool { return strings.HasPrefix(entry, "IMG:Костёр") })
			file := written(t, format, book)
			if got := sequence(t, format, file); !reflect.DeepEqual(got, want) {
				t.Fatalf("the file reads\n%q\nwant\n%q", got, want)
			}
			whole := string(file)
			if format != bookexport.FormatFB2 {
				contents, names := parts(t, file)
				for _, name := range names {
					whole += name + contents[name]
				}
			}
			if strings.Contains(whole, "picture-0002") || strings.Contains(whole, "picture-2.") {
				t.Fatal("the file leads to the picture it has not")
			}
		})
	}
}

// BP2.2: the pictures are read one at a time as the file is written: the bytes of a picture are gone out before the next
// one is read.
func TestPicturesGoOutOneByOne(t *testing.T) {
	for _, format := range allFormats {
		t.Run(string(format), func(t *testing.T) {
			var sink countingWriter
			book := testBook(false, illustrated...)
			large := testImage(400, 300, true)
			var seen []int
			for index := range 3 {
				book.Pictures = append(book.Pictures, bookexport.Picture{Chapter: 0, After: index + 1, Name: "Рисунок", Read: func() (bookexport.Image, error) {
					seen = append(seen, sink.total)
					return large, nil
				}})
			}
			if err := bookexport.Write(&sink, format, book); err != nil {
				t.Fatal(err)
			}
			reads := len(seen)
			if reads != 3 && !(format == bookexport.FormatFB2 && reads == 6) {
				t.Fatalf("%d reads", reads)
			}
			// The last reads write the bytes: each comes after the one before it has gone out.
			last := seen[reads-3:]
			if last[1]-last[0] < len(large.Content) || last[2]-last[1] < len(large.Content) {
				t.Fatalf("written before each picture: %v", seen)
			}
		})
	}
}
