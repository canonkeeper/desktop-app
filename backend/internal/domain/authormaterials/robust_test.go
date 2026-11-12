package authormaterials

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"
)

// workbookOf is a workbook of one sheet: its XML, and styles when given.
func workbookOf(t *testing.T, sheet, styles string) []byte {
	t.Helper()
	files := map[string]string{
		"xl/workbook.xml":            `<workbook><sheets><sheet name="Лист" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   sheet,
	}
	if styles != "" {
		files["xl/styles.xml"] = styles
	}
	return packed(t, files)
}

func row(number int, cells ...string) string {
	var text strings.Builder
	fmt.Fprintf(&text, `<row r="%d">`, number)
	for index, value := range cells {
		if value != "" {
			fmt.Fprintf(&text, `<c r="%c%d" t="inlineStr"><is><t>%s</t></is></c>`, 'A'+index, number, value)
		}
	}
	text.WriteString("</row>")
	return text.String()
}

func texts(rows [][]Cell) [][]string {
	out := make([][]string, len(rows))
	for index, cells := range rows {
		for _, cell := range cells {
			out[index] = append(out[index], cell.Text)
		}
	}
	return out
}

// cfb is the start of a compound file of the old Office formats; a file under a password keeps its workbook or document
// in a stream named EncryptedPackage.
func cfb(streams ...string) []byte {
	data := []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
	data = append(data, make([]byte, 504)...)
	for _, name := range streams {
		for _, unit := range utf16.Encode([]rune(name)) {
			data = append(data, byte(unit), byte(unit>>8))
		}
	}
	return data
}

// Any file the author may bring is told by what it holds, not by its name, and what the import does not read says what
// it is, so the page can tell the author what to do with it.
func TestParseTellsAFileByItsContent(t *testing.T) {
	workbook := workbookOf(t, `<worksheet><sheetData>`+row(1, "Имя", "Описание")+row(2, "Ворон", "Наёмник")+`</sheetData></worksheet>`, "")
	document := packed(t, map[string]string{"word/document.xml": `<w:document><w:body><w:p><w:r><w:t>Ворон — наёмник.</w:t></w:r></w:p></w:body></w:document>`})
	open := packed(t, map[string]string{"mimetype": "application/vnd.oasis.opendocument.spreadsheet", "content.xml": "<x/>"})
	for name, want := range map[string]struct {
		data []byte
		kind SourceKind
		err  error
	}{
		// MR2.4: a text file is read, not refused; refused stays only what cannot be read.
		"a workbook":                  {workbook, SourceXLSX, nil},
		"a document":                  {document, SourceDOCX, nil},
		"nothing at all":              {nil, "", ErrEmpty},
		"a workbook under a password": {cfb("EncryptedInfo", "EncryptedPackage"), "", ErrProtected},
		"an old workbook":             {cfb("Workbook"), "", ErrOldFormat},
		"an old document":             {cfb("WordDocument"), "", ErrOldFormat},
		"a rich text":                 {[]byte(`{\rtf1\ansi Ворон}`), "", ErrOldFormat},
		"a LibreOffice sheet":         {open, "", ErrOpenDocument},
		"a PDF":                       {[]byte("%PDF-1.7\n%âãÏÓ\n1 0 obj"), "", ErrPDF},
		"a PNG":                       {[]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), "", ErrPicture},
		"a JPEG":                      {[]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), "", ErrPicture},
		"a text":                      {[]byte("Ворон — наёмник.\nЛиса — воровка.\n"), SourceText, nil},
		"a text of Windows-1251":      {[]byte("\xc2\xee\xf0\xee\xed;\xef\xe5\xf0\xf1\xee\xed\xe0\xe6\r\n"), SourceText, nil},
		"a zip of something else":     {packed(t, map[string]string{"index.xml": "<x/>"}), "", ErrUnreadable},
		"bytes of nothing known":      {[]byte{0x00, 0x01, 0x02, 0xfe, 0x00, 0x13}, "", ErrUnreadable},
	} {
		t.Run(name, func(t *testing.T) {
			parsed, kind, err := Parse("materials", want.data)
			if !errors.Is(err, want.err) || kind != want.kind {
				t.Fatalf("Parse = %q, %v; want %q, %v", kind, err, want.kind, want.err)
			}
			if err == nil && len(parsed.Sections) == 0 {
				t.Fatal("nothing read")
			}
		})
	}
}

// A category merged down several rows belongs to each of them; a title merged across a row is not repeated.
func TestParseXLSXCarriesMergedCellsDown(t *testing.T) {
	data := workbookOf(t, `<worksheet><sheetData>`+
		row(1, "Школа", "Навык", "Ранг")+row(2, "Огонь", "Огненный шар", "B")+row(3, "", "Стена огня", "A")+row(4, "", "Пепельный след", "C")+
		row(5, "Лёд", "Иней", "D")+row(6, "Подпись на всю строку")+
		`</sheetData><mergeCells count="2"><mergeCell ref="A2:A4"/><mergeCell ref="A6:C6"/></mergeCells></worksheet>`, "")
	parsed, err := ParseXLSX(data)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"Школа", "Навык", "Ранг"}, {"Огонь", "Огненный шар", "B"}, {"Огонь", "Стена огня", "A"}, {"Огонь", "Пепельный след", "C"}, {"Лёд", "Иней", "D"}, {"Подпись на всю строку"}}
	if got := texts(parsed.Sections[0].Rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %q", got)
	}
}

// The lines an author writes above a table — its title, a date — are not its header: they go to the caption the model reads.
func TestParseXLSXPutsTheLinesAboveATableAside(t *testing.T) {
	data := workbookOf(t, `<worksheet><sheetData>`+
		row(1, "Персонажи второго тома")+row(2, "обновлено в марте")+row(4, "Имя", "Роль")+row(5, "Марта", "травница")+row(6, "Грег")+
		`</sheetData></worksheet>`, "")
	parsed, err := ParseXLSX(data)
	if err != nil {
		t.Fatal(err)
	}
	section := parsed.Sections[0]
	if section.Caption != "Персонажи второго тома\nобновлено в марте" || section.Title != "Лист" {
		t.Fatalf("caption %q, title %q", section.Caption, section.Title)
	}
	if got := texts(section.Rows); !reflect.DeepEqual(got, [][]string{{"Имя", "Роль"}, {"Марта", "травница"}, {"Грег"}}) {
		t.Fatalf("rows = %q", got)
	}
	if chunk := section.Chunks()[0]; !strings.Contains(chunk, "Над таблицей: Персонажи второго тома; обновлено в марте") || !strings.Contains(chunk, "строка 0: [0] Имя; [1] Роль;") {
		t.Fatalf("chunk = %q", chunk)
	}
	list := workbookOf(t, `<worksheet><sheetData>`+row(1, "Ворон")+row(2, "Лиса")+row(3, "Гильдия теней")+`</sheetData></worksheet>`, "")
	if parsed, err := ParseXLSX(list); err != nil || parsed.Sections[0].Caption != "" || len(parsed.Sections[0].Rows) != 3 {
		t.Fatalf("a list of one column is a table: %+v, %v", parsed, err)
	}
}

// A second table of other columns below the first, after an empty row, is a table of its own; rows after an empty row in the
// columns of the table go on with it.
func TestParseXLSXSplitsATableOfOtherColumns(t *testing.T) {
	data := workbookOf(t, `<worksheet><sheetData>`+
		row(1, "Навык", "Мана", "Описание")+row(2, "Щит света", "20", "Отражает атаку.")+row(4, "Луч", "35", "Бьёт далеко.")+
		row(7, "Места")+row(8, "Место", "Регион")+row(9, "Перевал Ястреба", "Север")+
		`</sheetData></worksheet>`, "")
	parsed, err := ParseXLSX(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sections) != 2 {
		t.Fatalf("sections = %+v", parsed.Sections)
	}
	first, second := parsed.Sections[0], parsed.Sections[1]
	if first.Title != "Лист" || len(first.Rows) != 3 {
		t.Fatalf("first = %+v", first)
	}
	if second.Title != "Лист · Места" || second.Caption != "Места" || !reflect.DeepEqual(texts(second.Rows), [][]string{{"Место", "Регион"}, {"Перевал Ястреба", "Север"}}) {
		t.Fatalf("second = %+v", second)
	}
}

// specs/2026-11-04-saga-map.md R9.4: a table with no line over it is called by the first names of its columns, not by its number.
func TestCandidatesNameAnUntitledTableByItsHeader(t *testing.T) {
	data := workbookOf(t, `<worksheet><sheetData>`+
		row(1, "Навык", "Мана", "Описание")+row(2, "Щит света", "20", "Отражает атаку.")+
		row(5, "Приём", "Школа", "Цена", "Ранг")+row(6, "Подсечка", "Улица", "5", "C")+
		row(9, "Предмет", "Вес")+row(10, "Ключ", "1")+
		`</sheetData></worksheet>`, "")
	parsed, err := ParseXLSX(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sections) != 3 {
		t.Fatalf("sections = %+v", parsed.Sections)
	}
	drafts := Candidates(parsed, []Mapping{
		{Kind: KindSkill, Header: true, Name: 0, Description: 2},
		{Kind: KindSkill, Header: true, Name: 0, Description: -1},
		{Kind: KindItem, Header: false, Name: 0, Description: -1},
	})
	want := map[string]string{"Щит света": "Лист", "Подсечка": "Лист · Приём, Школа, Цена", "Ключ": "Лист · таблица 3"}
	for name, section := range want {
		if got := draftNamed(t, drafts, name).Section; got != section {
			t.Errorf("%s is in %q, want %q", name, got, section)
		}
	}
	if chunk := parsed.Sections[1].Chunks()[0]; !strings.Contains(chunk, "Раздел: Лист · таблица 2\n") {
		t.Errorf("what the model reads = %q", chunk)
	}
}

// What Excel shows is what the author wrote: a date, a percent, «да»; an error of a formula says nothing.
func TestParseXLSXReadsValuesAsExcelShowsThem(t *testing.T) {
	styles := `<styleSheet><numFmts count="1"><numFmt numFmtId="164" formatCode="DD/MM/YYYY;@"/></numFmts>
		<cellXfs count="5"><xf numFmtId="0"/><xf numFmtId="14"/><xf numFmtId="164"/><xf numFmtId="9"/><xf numFmtId="22"/></cellXfs></styleSheet>`
	data := workbookOf(t, `<worksheet><sheetData>`+row(1, "Событие", "Когда", "Тоже", "Доля", "Важно", "Ошибка", "Число", "Миг")+
		`<row r="2"><c r="A2" t="inlineStr"><is><t>Падение столицы</t></is></c><c r="B2" s="1"><v>45055</v></c><c r="C2" s="2"><v>45306</v></c>`+
		`<c r="D2" s="3"><v>0.25</v></c><c r="E2" t="b"><v>1</v></c><c r="F2" t="e"><v>#DIV/0!</v></c><c r="G2"><v>0.30000000000000004</v></c><c r="H2" s="4"><v>45055.75</v></c></row>`+
		`</sheetData></worksheet>`, styles)
	parsed, err := ParseXLSX(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(parsed.Sections[0].Rows)[1]; !reflect.DeepEqual(got, []string{"Падение столицы", "09.05.2023", "15.01.2024", "25%", "да", "", "0.3", "09.05.2023 18:00"}) {
		t.Fatalf("values = %q", got)
	}
}

// A sheet formatted far below its data — a column coloured down to the bottom of the sheet — costs only the rows with something in them.
func TestParseXLSXReadsASheetOfEmptyRowsInLittleMemory(t *testing.T) {
	var sheet bytes.Buffer
	sheet.WriteString(`<worksheet><sheetData>` + row(1, "Предмет", "Описание") + row(2, "Посох", "Деревянный посох мага."))
	for number := 3; number <= 400_000; number++ {
		fmt.Fprintf(&sheet, `<row r="%d"><c r="A%d" s="1"/></row>`, number, number)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	data := workbookOf(t, sheet.String(), `<styleSheet><cellXfs count="2"><xf/><xf fillId="0"/></cellXfs></styleSheet>`)
	var parsed Parsed
	var err error
	top := peakHeap(func() { parsed, err = ParseXLSX(data) })
	if err != nil || len(parsed.Sections[0].Rows) != 2 {
		t.Fatalf("parsed = %+v, %v", parsed, err)
	}
	// Before it was read whole: about 150 MB for this sheet, 363 MB for a sheet of 1 048 576 rows (a file of 5 MB).
	if top > 48 {
		t.Fatalf("%d MB of heap at the peak for a sheet of two rows", top)
	}
}

// peakHeap runs a function and returns the most heap in use while it ran, in megabytes.
func peakHeap(run func()) uint64 {
	runtime.GC()
	var top atomic.Uint64
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		var stats runtime.MemStats
		for {
			runtime.ReadMemStats(&stats)
			if stats.HeapInuse > top.Load() {
				top.Store(stats.HeapInuse)
			}
			select {
			case <-done:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	run()
	close(done)
	<-stopped
	return top.Load() >> 20
}

func documentOf(t *testing.T, body string, parts map[string]string) []byte {
	t.Helper()
	files := map[string]string{"word/document.xml": `<w:document><w:body>` + body + `</w:body></w:document>`}
	for name, part := range parts {
		files[name] = part
	}
	return packed(t, files)
}

// A footnote or an endnote says something of the paragraph it stands in, as a comment does.
func TestParseDOCXReadsFootnotesAndEndnotes(t *testing.T) {
	data := documentOf(t, `<w:p><w:r><w:t>Студена — столица севера.</w:t></w:r><w:r><w:footnoteReference w:id="1"/></w:r></w:p>`+
		`<w:p><w:r><w:t>Башня Эха стоит у леса.</w:t></w:r><w:r><w:endnoteReference w:id="1"/></w:r></w:p>`, map[string]string{
		"word/footnotes.xml": `<w:footnotes><w:footnote w:type="separator" w:id="-1"><w:p><w:r><w:separator/></w:r></w:p></w:footnote>` +
			`<w:footnote w:id="1"><w:p><w:r><w:footnoteRef/></w:r><w:r><w:t xml:space="preserve"> Население: 40 тысяч</w:t></w:r></w:p></w:footnote></w:footnotes>`,
		"word/endnotes.xml": `<w:endnotes><w:endnote w:id="1"><w:p><w:r><w:t>Построена гномами.</w:t></w:r></w:p></w:endnote></w:endnotes>`,
	})
	parsed, err := ParseDOCX(data)
	if err != nil {
		t.Fatal(err)
	}
	paragraphs := parsed.Sections[0].Paragraphs
	if paragraphs[0].Note != "Население: 40 тысяч" || paragraphs[1].Note != "Построена гномами." {
		t.Fatalf("paragraphs = %+v", paragraphs)
	}
}

// Word writes a text box twice — the shape and its copy for old readers — inside the paragraph it stands in: it is read once,
// and the paragraph keeps its own text.
func TestParseDOCXReadsATextBoxOnce(t *testing.T) {
	box := `<w:txbxContent><w:p><w:r><w:t>Заметка на полях: меч поёт на морозе.</w:t></w:r></w:p></w:txbxContent>`
	data := documentOf(t, `<w:p><w:r><w:t>Клык Зимы — меч Ворона.</w:t></w:r><w:r><mc:AlternateContent><mc:Choice Requires="wps"><w:drawing><wps:txbx>`+box+
		`</wps:txbx></w:drawing></mc:Choice><mc:Fallback><w:pict><v:textbox>`+box+`</v:textbox></w:pict></mc:Fallback></mc:AlternateContent></w:r></w:p>`+
		`<w:p><w:r><w:t>Порт Альба — торговый город.</w:t></w:r></w:p>`, nil)
	parsed, err := ParseDOCX(data)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, paragraph := range parsed.Sections[0].Paragraphs {
		got = append(got, paragraph.Text)
	}
	if want := []string{"Клык Зимы — меч Ворона.", "Заметка на полях: меч поёт на морозе.", "Порт Альба — торговый город."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paragraphs = %q", got)
	}
}

// A file of pictures has nothing to read, and the import says so rather than failing.
func TestParseDOCXOfPicturesIsEmpty(t *testing.T) {
	data := documentOf(t, `<w:p><w:r><w:drawing><wp:inline><a:graphic/></wp:inline></w:drawing></w:r></w:p><w:sectPr/>`, nil)
	if _, err := ParseDOCX(data); !errors.Is(err, ErrEmpty) {
		t.Fatalf("err = %v", err)
	}
}

// Whatever an author brings, the reading of a file ends with records or with an error, never with a panic (the seeds run with
// the tests; go test -fuzz FuzzParse looks further).
func FuzzParse(f *testing.F) {
	for _, name := range []string{"testdata/materials.xlsx", "testdata/libre/materials.xlsx", "testdata/materials.docx"} {
		data, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add(cfb("EncryptedPackage"))
	f.Add([]byte("%PDF-1.7"))
	f.Add([]byte("Имя;Вид\n\"Ворон;\"\"x\";персонаж\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		parsed, kind, err := Parse("materials.csv", data)
		if err == nil && (kind == "" || len(parsed.Sections) == 0) {
			t.Fatalf("read %q without sections: %+v", kind, parsed)
		}
		for _, section := range parsed.Sections {
			_ = section.Chunks()
		}
	})
}
