package authormaterials

import (
	"archive/zip"
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
)

func packed(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// skillsWorkbook is what Excel writes for the author's sheet (R9.3): the names and the rarity on the sheet, the main
// text of a skill in the note of its cell, the rarity in an orange fill, a second sheet of places and a hidden one.
func skillsWorkbook(t *testing.T) []byte {
	t.Helper()
	return packed(t, map[string]string{
		"xl/workbook.xml": `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>
			<sheet name="Навыки" sheetId="1" r:id="rId1"/><sheet name="Места" sheetId="2" r:id="rId2"/>
			<sheet name="Скрытый" sheetId="3" state="hidden" r:id="rId3"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships>
			<Relationship Id="rId1" Type="http://x/worksheet" Target="worksheets/sheet1.xml"/>
			<Relationship Id="rId2" Type="http://x/worksheet" Target="/xl/worksheets/sheet2.xml"/>
			<Relationship Id="rId3" Type="http://x/worksheet" Target="worksheets/sheet3.xml"/></Relationships>`,
		"xl/sharedStrings.xml": `<sst><si><t>Навык</t></si><si><t>Редкость</t></si><si><t>омут снов</t></si>
			<si><r><t>эпи</t></r><r><t>ческий</t></r><rPh><t>фонетика</t></rPh></si><si><t>Миг назад</t></si>
			<si><t>уникальный</t></si><si><t>Название</t></si><si><t>Белый порт</t></si><si><t>Секрет</t></si></sst>`,
		"xl/styles.xml": `<styleSheet>
			<fonts count="2"><font><color theme="1"/></font><font><color rgb="FFC00000"/></font></fonts>
			<fills count="4"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill>
				<fill><patternFill patternType="solid"><fgColor rgb="FFFFC000"/></patternFill></fill>
				<fill><patternFill patternType="solid"><fgColor theme="5"/></patternFill></fill></fills>
			<cellXfs count="4"><xf fontId="0" fillId="0"/><xf fontId="0" fillId="2"/><xf fontId="1" fillId="0"/><xf fontId="0" fillId="3"/></cellXfs>
			</styleSheet>`,
		"xl/theme/theme1.xml": `<a:theme xmlns:a="a"><a:themeElements><a:clrScheme>
			<a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1><a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1>
			<a:dk2><a:srgbClr val="44546A"/></a:dk2><a:lt2><a:srgbClr val="E7E6E6"/></a:lt2>
			<a:accent1><a:srgbClr val="4472C4"/></a:accent1><a:accent2><a:srgbClr val="ED7D31"/></a:accent2>
			</a:clrScheme></a:themeElements></a:theme>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData>
			<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
			<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2" s="1" t="s"><v>3</v></c></row>
			<row r="4" hidden="1"><c r="A4" t="inlineStr"><is><t>скрытая строка</t></is></c></row>
			<row r="5"><c r="A5" t="s"><v>4</v></c><c r="C5" s="2"><v>12</v></c></row>
			<row r="6"><c r="B6" t="s"><v>5</v></c></row>
			</sheetData></worksheet>`,
		"xl/worksheets/_rels/sheet1.xml.rels": `<Relationships>
			<Relationship Id="rId1" Type="http://x/comments" Target="../comments1.xml"/>
			<Relationship Id="rId2" Type="http://x/threadedComment" Target="../threadedComments/threadedComment1.xml"/></Relationships>`,
		"xl/comments1.xml": `<comments><authors><author>Мирон</author></authors><commentList>
			<comment ref="A2" authorId="0"><text><r><t>Мирон:
</t></r><r><t>Улучшить навык: [ужас] → [омут снов]
Ранг: Уникальный (вне уровней)
Описание: Сны становятся явью.
Затраты эфира: 40</t></r></text></comment>
			<comment ref="A5" authorId="0"><text><t>[Threaded comment] Your version of Excel allows you to read this threaded comment</t></text></comment>
			</commentList></comments>`,
		"xl/threadedComments/threadedComment1.xml": `<ThreadedComments>
			<threadedComment ref="A5" id="1"><text>Возвращает бой на миг назад.</text></threadedComment>
			<threadedComment ref="A5" id="2" parentId="1"><text>Только раз в бою.</text></threadedComment></ThreadedComments>`,
		"xl/worksheets/sheet2.xml": `<worksheet><sheetData>
			<row r="1"><c r="A1" t="s"><v>6</v></c></row><row r="2"><c r="A2" s="3" t="s"><v>7</v></c></row>
			</sheetData></worksheet>`,
		"xl/worksheets/sheet3.xml": `<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>8</v></c></row></sheetData></worksheet>`,
	})
}

func TestParseXLSXReadsSheetsNotesAndFills(t *testing.T) {
	parsed, err := ParseXLSX(skillsWorkbook(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sections) != 2 || parsed.Sections[0].Title != "Навыки" || parsed.Sections[1].Title != "Места" {
		t.Fatalf("sections = %+v", parsed.Sections)
	}
	skills := parsed.Sections[0]
	if len(skills.Rows) != 4 {
		t.Fatalf("rows = %+v", skills.Rows)
	}
	nightmare := skills.Rows[1]
	if nightmare[0].Text != "омут снов" || nightmare[1].Text != "эпический" || nightmare[1].Look != "оранжевая заливка" {
		t.Fatalf("row = %+v", nightmare)
	}
	if !strings.HasPrefix(nightmare[0].Note, "Улучшить навык: [ужас]") {
		t.Fatalf("the note keeps the author's name: %q", nightmare[0].Note)
	}
	moment := skills.Rows[2]
	if moment[0].Note != "Возвращает бой на миг назад.\nТолько раз в бою." {
		t.Fatalf("a threaded comment wins over its legacy stand-in: %q", moment[0].Note)
	}
	if moment[2].Text != "12" || moment[2].Look != "красный текст" {
		t.Fatalf("a number with a coloured font: %+v", moment[2])
	}
	if got := parsed.Sections[1].Rows[1][0]; got.Text != "Белый порт" || got.Look != "оранжевая заливка" {
		t.Fatalf("a fill of the theme: %+v", got)
	}
}

func TestParseXLSXRefusesWhatIsNotAWorkbook(t *testing.T) {
	if _, err := ParseXLSX([]byte("not a zip")); err != ErrUnreadable {
		t.Fatalf("err = %v", err)
	}
	if _, err := ParseXLSX(packed(t, map[string]string{"word/document.xml": "<document/>"})); err != ErrUnreadable {
		t.Fatalf("err = %v", err)
	}
	empty := packed(t, map[string]string{
		"xl/workbook.xml":            `<workbook><sheets><sheet name="Пусто" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   `<worksheet><sheetData/></worksheet>`,
	})
	if _, err := ParseXLSX(empty); err != ErrEmpty {
		t.Fatalf("err = %v", err)
	}
}

func TestParseDOCXReadsHeadingsTablesAndComments(t *testing.T) {
	data := packed(t, map[string]string{
		"word/document.xml": `<w:document xmlns:w="w"><w:body>
			<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Места</w:t></w:r></w:p>
			<w:p><w:pPr><w:pStyle w:val="2"/></w:pPr><w:r><w:t>Студена</w:t></w:r></w:p>
			<w:p><w:commentRangeStart w:id="0"/><w:r><w:t xml:space="preserve">Столица </w:t></w:r><w:r><w:t>севера.</w:t></w:r><w:commentRangeEnd w:id="0"/><w:r><w:commentReference w:id="0"/></w:r></w:p>
			<w:p><w:r><w:t></w:t></w:r></w:p>
			<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Навыки</w:t></w:r></w:p>
			<w:tbl>
				<w:tr><w:tc><w:p><w:r><w:t>Навык</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Ранг</w:t></w:r></w:p></w:tc></w:tr>
				<w:tr><w:tc><w:p><w:commentRangeStart w:id="1"/><w:r><w:t>одушевление</w:t></w:r><w:r><w:commentReference w:id="1"/></w:r></w:p></w:tc>
				<w:tc><w:p><w:r><w:t>эпический</w:t></w:r></w:p><w:p><w:r><w:t>второй абзац</w:t></w:r></w:p></w:tc></w:tr>
			</w:tbl>
			</w:body></w:document>`,
		"word/comments.xml": `<w:comments xmlns:w="w">
			<w:comment w:id="0" w:author="Д"><w:p><w:r><w:t>Население: 40 тысяч</w:t></w:r></w:p></w:comment>
			<w:comment w:id="1" w:author="Д"><w:p><w:r><w:t>Оживляет предмет.</w:t></w:r></w:p></w:comment></w:comments>`,
	})
	parsed, err := ParseDOCX(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sections) != 2 {
		t.Fatalf("sections = %+v", parsed.Sections)
	}
	places := parsed.Sections[0]
	if places.Title != "Места" || len(places.Paragraphs) != 2 {
		t.Fatalf("places = %+v", places)
	}
	if places.Paragraphs[0].Text != "Студена" || places.Paragraphs[0].Level != 2 {
		t.Fatalf("heading = %+v", places.Paragraphs[0])
	}
	if places.Paragraphs[1].Text != "Столица севера." || places.Paragraphs[1].Note != "Население: 40 тысяч" {
		t.Fatalf("paragraph = %+v", places.Paragraphs[1])
	}
	skills := parsed.Sections[1]
	if skills.Title != "Навыки" || len(skills.Rows) != 2 {
		t.Fatalf("skills = %+v", skills)
	}
	if got := skills.Rows[1]; got[0].Text != "одушевление" || got[0].Note != "Оживляет предмет." || got[1].Text != "эпический\nвторой абзац" {
		t.Fatalf("row = %+v", got)
	}
}

func TestParseTextIsParagraphs(t *testing.T) {
	parsed, err := ParseText("  Студена — столица.\n\n  Белый порт: город на берегу \r\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sections) != 1 || len(parsed.Sections[0].Paragraphs) != 2 || parsed.Sections[0].Paragraphs[1].Text != "Белый порт: город на берегу" {
		t.Fatalf("parsed = %+v", parsed)
	}
	if _, err := ParseText(" \n "); err != ErrEmpty {
		t.Fatalf("err = %v", err)
	}
}

func TestChunksOfASectionLeaveTheNotesOut(t *testing.T) {
	parsed, err := ParseXLSX(skillsWorkbook(t))
	if err != nil {
		t.Fatal(err)
	}
	sample := strings.Join(parsed.Sections[0].Chunks(), "\n")
	if !strings.Contains(sample, "омут снов") || !strings.Contains(sample, "эпический") {
		t.Fatalf("sample = %s", sample)
	}
	if strings.Contains(sample, "Сны становятся явью") || strings.Contains(sample, "Возвращает") {
		t.Fatalf("a note reached the model: %s", sample)
	}
}

func TestChunksOfProseKeepTheNumbersOfTheParagraphs(t *testing.T) {
	section := Section{Paragraphs: make([]Paragraph, 0, 40)}
	for range 40 {
		section.Paragraphs = append(section.Paragraphs, Paragraph{Text: strings.Repeat("слово ", 100)})
	}
	chunks := section.Chunks()
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d", len(chunks))
	}
	if !strings.Contains(chunks[1], "абзац ") || strings.Contains(chunks[1], "абзац 0:") {
		t.Fatalf("second chunk = %.80s", chunks[1])
	}
}

// The files of testdata are written by the programs an author's files come from (testdata/make_fixtures.py): openpyxl and,
// through it, LibreOffice for the workbook, python-docx for the document.
func TestParseFilesOfOtherWriters(t *testing.T) {
	for _, name := range []string{"testdata/materials.xlsx", "testdata/libre/materials.xlsx"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := ParseXLSX(data)
			if err != nil {
				t.Fatal(err)
			}
			titles := make([]string, 0, len(parsed.Sections))
			for _, section := range parsed.Sections {
				titles = append(titles, section.Title)
			}
			if !reflect.DeepEqual(titles, []string{"Навыки", "Места", "Персонажи"}) {
				t.Fatalf("a hidden sheet is left out: %v", titles)
			}
			drafts := Candidates(parsed, []Mapping{
				{Kind: KindSkill, Header: true, Name: 0, Description: -1},
				{Kind: KindPlace, Header: true, Name: 0, Description: 1},
				{Kind: KindCharacter, Header: true, Name: 0, Description: 1},
			})
			nightmare := draftNamed(t, drafts, "омут снов")
			if nightmare.Description != "Сны становятся явью." {
				t.Fatalf("description = %q", nightmare.Description)
			}
			want := []Property{
				{Key: "Редкость", Value: "эпический (оранжевая заливка)"},
				{Key: "Улучшить навык", Value: "[ужас] → [омут снов]"},
				{Key: "Ранг", Value: "Уникальный (вне уровней)"},
				{Key: "Затраты эфира", Value: "40"},
				{Key: "Примечание", Value: "только ночью"},
			}
			if !reflect.DeepEqual(nightmare.Properties, want) {
				t.Fatalf("properties = %+v", nightmare.Properties)
			}
			if got := draftNamed(t, drafts, "пустота в ладонях"); got.Description != "Гасит чужое заклинание." || got.Properties[0].Value != "наследие (фиолетовая заливка)" {
				t.Fatalf("a fill of another colour: %+v", got)
			}
			tower := draftNamed(t, drafts, "Башня Эха")
			if tower.Kind != KindPlace || tower.Description != "Построена гномами." || !reflect.DeepEqual(tower.Properties, []Property{{Key: "Этаж", Value: "12 (красный текст)"}}) {
				t.Fatalf("a coloured number: %+v", tower)
			}
			if got := draftNamed(t, drafts, "Мирон"); got.Kind != KindCharacter || got.Description != "Главный герой, уже есть в книге." {
				t.Fatalf("a name the book knows is a candidate like any other: %+v", got)
			}
			if len(drafts) != 8 {
				t.Fatalf("drafts = %+v", drafts)
			}
		})
	}
	data, err := os.ReadFile("testdata/materials.docx")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseDOCX(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sections) != 2 || parsed.Sections[0].Title != "Места" || parsed.Sections[1].Title != "Навыки" {
		t.Fatalf("sections = %+v", parsed.Sections)
	}
	places := parsed.Sections[0].Paragraphs
	if len(places) != 3 || places[0].Text != "Студена" || places[0].Level != 2 || places[1].Note != "Население: 40 тысяч" {
		t.Fatalf("places = %+v", places)
	}
	if got := parsed.Sections[1].Rows[2][0]; got.Text != "омут снов" || got.Note != "Сны становятся явью." {
		t.Fatalf("a comment in a table: %+v", got)
	}
}
