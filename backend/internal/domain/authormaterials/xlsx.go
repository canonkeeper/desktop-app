package authormaterials

import (
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
)

type xlsxRels struct {
	List []struct {
		ID     string `xml:"Id,attr"`
		Type   string `xml:"Type,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

type xlsxWorkbook struct {
	Props struct {
		Date1904 string `xml:"date1904,attr"`
	} `xml:"workbookPr"`
	Sheets []struct {
		Name  string `xml:"name,attr"`
		State string `xml:"state,attr"`
		RID   string `xml:"id,attr"`
	} `xml:"sheets>sheet"`
}

type xlsxRun struct {
	T string `xml:"t"`
}

// xlsxText is a string with its formatted runs; the phonetic hints of rPh are not part of it.
type xlsxText struct {
	T string    `xml:"t"`
	R []xlsxRun `xml:"r"`
}

func (text xlsxText) String() string {
	var value strings.Builder
	value.WriteString(text.T)
	for _, run := range text.R {
		value.WriteString(run.T)
	}
	return value.String()
}

type xlsxComments struct {
	Authors []string `xml:"authors>author"`
	List    []struct {
		Ref  string   `xml:"ref,attr"`
		Text xlsxText `xml:"text"`
	} `xml:"commentList>comment"`
}

type xlsxThreaded struct {
	List []struct {
		Ref  string `xml:"ref,attr"`
		Text string `xml:"text"`
	} `xml:"threadedComment"`
}

type xlsxColor struct {
	RGB     string   `xml:"rgb,attr"`
	Theme   *int     `xml:"theme,attr"`
	Indexed *int     `xml:"indexed,attr"`
	Tint    *float64 `xml:"tint,attr"`
}

type xlsxStyles struct {
	Fonts []struct {
		Color xlsxColor `xml:"color"`
	} `xml:"fonts>font"`
	Fills []struct {
		Pattern struct {
			Type string    `xml:"patternType,attr"`
			Fg   xlsxColor `xml:"fgColor"`
		} `xml:"patternFill"`
	} `xml:"fills>fill"`
	NumberFormats []struct {
		ID   int    `xml:"numFmtId,attr"`
		Code string `xml:"formatCode,attr"`
	} `xml:"numFmts>numFmt"`
	Formats []struct {
		Font   int `xml:"fontId,attr"`
		Fill   int `xml:"fillId,attr"`
		Number int `xml:"numFmtId,attr"`
	} `xml:"cellXfs>xf"`
}

type xlsxThemeColor struct {
	SRGB struct {
		Val string `xml:"val,attr"`
	} `xml:"srgbClr"`
	Sys struct {
		Last string `xml:"lastClr,attr"`
	} `xml:"sysClr"`
}

func (color xlsxThemeColor) hex() string {
	if color.SRGB.Val != "" {
		return color.SRGB.Val
	}
	return color.Sys.Last
}

type xlsxTheme struct {
	Scheme struct {
		Dk1     xlsxThemeColor `xml:"dk1"`
		Lt1     xlsxThemeColor `xml:"lt1"`
		Dk2     xlsxThemeColor `xml:"dk2"`
		Lt2     xlsxThemeColor `xml:"lt2"`
		Accent1 xlsxThemeColor `xml:"accent1"`
		Accent2 xlsxThemeColor `xml:"accent2"`
		Accent3 xlsxThemeColor `xml:"accent3"`
		Accent4 xlsxThemeColor `xml:"accent4"`
		Accent5 xlsxThemeColor `xml:"accent5"`
		Accent6 xlsxThemeColor `xml:"accent6"`
		Link    xlsxThemeColor `xml:"hlink"`
		Visited xlsxThemeColor `xml:"folHlink"`
	} `xml:"themeElements>clrScheme"`
}

// decodeOptional unmarshals an entry that may be absent: an absent or broken part only loses what it adds.
func decodeOptional(source archive, name string, target any) error {
	body, err := source.read(name)
	if err != nil {
		return err
	}
	if body != nil {
		if xml.Unmarshal(body, target) != nil {
			return ErrUnreadable
		}
	}
	return nil
}

// ParseXLSX reads every visible sheet: rows, values, notes, comments, fills and font colours (R9.3).
func ParseXLSX(data []byte) (Parsed, error) {
	source, err := openArchive(data)
	if err != nil {
		return Parsed{}, err
	}
	const book = "xl/workbook.xml"
	var workbook xlsxWorkbook
	if _, ok := source.files[book]; !ok {
		return Parsed{}, ErrUnreadable
	}
	if err := decodeOptional(source, book, &workbook); err != nil {
		return Parsed{}, err
	}
	var bookRels xlsxRels
	if err := decodeOptional(source, "xl/_rels/workbook.xml.rels", &bookRels); err != nil {
		return Parsed{}, err
	}
	targets := make(map[string]string, len(bookRels.List))
	for _, rel := range bookRels.List {
		targets[rel.ID] = resolve(book, rel.Target)
	}
	strings_, err := sharedStrings(source)
	if err != nil {
		return Parsed{}, err
	}
	styles, err := cellStyles(source)
	if err != nil {
		return Parsed{}, err
	}
	values := valueReader{shared: strings_, styles: styles, date1904: workbook.Props.Date1904 == "1" || workbook.Props.Date1904 == "true"}
	var parsed Parsed
	rows := 0
	for place, sheet := range workbook.Sheets {
		target, ok := targets[sheet.RID]
		if !ok || sheet.State == "hidden" || sheet.State == "veryHidden" {
			continue
		}
		read, err := readSheet(source, target, values, maxRows-rows)
		if err != nil {
			return Parsed{}, err
		}
		rows += len(read)
		parsed.Sections = append(parsed.Sections, sheetSections(sheetTitle(sheet.Name, place), read)...)
	}
	if len(parsed.Sections) == 0 {
		return Parsed{}, ErrEmpty
	}
	return parsed, nil
}

func sharedStrings(source archive) ([]string, error) {
	var table struct {
		Items []xlsxText `xml:"si"`
	}
	if err := decodeOptional(source, "xl/sharedStrings.xml", &table); err != nil {
		return nil, err
	}
	values := make([]string, len(table.Items))
	for index, item := range table.Items {
		values[index] = item.String()
	}
	return values, nil
}

// sheetRow is a row with something in it, its number on the sheet and whether empty rows stand before it.
type sheetRow struct {
	number int
	gap    bool
	cells  []Cell
}

// xlsxCell is one cell as a sheet writes it.
type xlsxCell struct {
	Ref    string   `xml:"r,attr"`
	Style  int      `xml:"s,attr"`
	Type   string   `xml:"t,attr"`
	Value  string   `xml:"v"`
	Inline xlsxText `xml:"is"`
}

// readSheet reads the rows of a sheet with something in them, a cell at a time: a sheet formatted down to its bottom costs
// only the rows it has. A value merged down several rows is put in each of them; more than limit rows is ErrTooLarge.
func readSheet(source archive, name string, values valueReader, limit int) ([]sheetRow, error) {
	entry, err := source.open(name)
	if err != nil || entry == nil {
		return nil, err
	}
	defer entry.Close()
	notes, err := sheetNotes(source, name)
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(entry)
	var rows []sheetRow
	var merges []cellRange
	var cells []Cell
	var raw xlsxCell
	var text *strings.Builder // the value or the inline text being read, nil outside them
	var value, inline strings.Builder
	number, previous := 0, 0
	inCell, hidden, filled := false, false, false
	for {
		// RawToken: a sheet is read token by token, without the namespaces it does not need.
		token, err := decoder.RawToken()
		if err == io.EOF {
			break
		}
		if errors.Is(err, ErrTooLarge) {
			return nil, ErrTooLarge
		}
		if err != nil {
			return nil, ErrUnreadable
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "row":
				number++
				if found, err := strconv.Atoi(attribute(element, "r")); err == nil && found > 0 {
					number = found
				}
				hidden = attribute(element, "hidden") == "1" || attribute(element, "hidden") == "true"
				cells, filled = cells[:0:0], false
			case "c":
				raw, inCell = xlsxCell{Ref: attribute(element, "r"), Type: attribute(element, "t")}, true
				raw.Style, _ = strconv.Atoi(attribute(element, "s"))
				value.Reset()
				inline.Reset()
			case "v":
				if inCell {
					text = &value
				}
			case "t":
				if inCell {
					text = &inline
				}
			case "rPh":
				// The phonetic hints of a string are not its text.
				if decoder.Skip() != nil {
					return nil, ErrUnreadable
				}
			case "mergeCell":
				if merged, ok := parseRange(attribute(element, "ref")); ok {
					merges = append(merges, merged)
				}
			}
		case xml.CharData:
			if text != nil {
				text.Write(element)
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "v", "t":
				text = nil
			case "c":
				inCell = false
				if value.Len() == 0 && inline.Len() == 0 && notes[raw.Ref] == "" {
					continue
				}
				raw.Value, raw.Inline = value.String(), xlsxText{T: inline.String()}
				column := len(cells)
				if raw.Ref != "" {
					column = columnOf(raw.Ref)
				}
				if column < 0 || column >= 512 {
					continue
				}
				for len(cells) <= column {
					cells = append(cells, Cell{})
				}
				cell := Cell{Text: values.text(raw), Note: notes[raw.Ref]}
				if raw.Style >= 0 && raw.Style < len(values.styles) && cell.Text != "" {
					cell.Look = values.styles[raw.Style].look
				}
				cells[column] = cell
				filled = filled || cell.Text != "" || cell.Note != ""
			case "row":
				if !filled {
					continue
				}
				if !hidden {
					rows = append(rows, sheetRow{number: number, gap: len(rows) > 0 && number > previous+1, cells: cells})
					if len(rows) > limit {
						return nil, ErrTooLarge
					}
				}
				previous = number
			}
		}
	}
	mergeDown(rows, merges)
	return rows, nil
}

// cellRange is a range of cells, rows and columns from 0.
type cellRange struct{ top, left, bottom, right int }

func parseRange(ref string) (cellRange, bool) {
	from, to, ok := strings.Cut(ref, ":")
	if !ok {
		return cellRange{}, false
	}
	top, left := cellAt(from)
	bottom, right := cellAt(to)
	if top < 0 || left < 0 || bottom < top || right < left {
		return cellRange{}, false
	}
	return cellRange{top, left, bottom, right}, true
}

// cellAt is the row and the column of a reference, from 0: B3 is 2, 1.
func cellAt(ref string) (int, int) {
	column := columnOf(ref)
	digits := strings.TrimLeft(ref, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	number, err := strconv.Atoi(digits)
	if err != nil || number < 1 || column < 0 {
		return -1, -1
	}
	return number - 1, column
}

// mergeDown gives each row of a merged range the value of its first cell: a category merged down belongs to every row of
// it. A range merged across a row is left as it is, so a title over a table stays one cell.
func mergeDown(rows []sheetRow, merges []cellRange) {
	if len(merges) == 0 {
		return
	}
	index := make(map[int]int, len(rows))
	for at, kept := range rows {
		index[kept.number-1] = at
	}
	for _, merged := range merges {
		first, ok := index[merged.top]
		if !ok || merged.bottom == merged.top || merged.left >= len(rows[first].cells) {
			continue
		}
		value := rows[first].cells[merged.left]
		for number := merged.top + 1; number <= merged.bottom; number++ {
			at, ok := index[number]
			if !ok {
				continue
			}
			kept := &rows[at]
			for len(kept.cells) <= merged.left {
				kept.cells = append(kept.cells, Cell{})
			}
			if kept.cells[merged.left].Text == "" {
				kept.cells[merged.left].Text, kept.cells[merged.left].Look = value.Text, value.Look
			}
		}
	}
}

// maxTablesPerSheet is how many tables a sheet is cut into at most: more is a table with empty rows in it, read whole.
const maxTablesPerSheet = 8

// sheetSections cuts a sheet into its tables. A table ends at empty rows when the next row has other columns: a second table
// of other columns, or a line over it. The lines over a table — its title, a date — are its caption, not its rows.
func sheetSections(title string, rows []sheetRow) []Section {
	if len(rows) == 0 {
		return nil
	}
	var blocks [][]sheetRow
	var shape []int
	for _, kept := range rows {
		columns := filledColumns(kept.cells)
		if len(blocks) > 0 && kept.gap && shape != nil && !sameColumns(columns, shape) {
			blocks, shape = append(blocks, nil), nil
		}
		if len(blocks) == 0 {
			blocks = append(blocks, nil)
		}
		blocks[len(blocks)-1] = append(blocks[len(blocks)-1], kept)
		if shape == nil && len(columns) > 1 {
			shape = columns
		}
	}
	if len(blocks) > maxTablesPerSheet {
		blocks = [][]sheetRow{rows}
	}
	sections := make([]Section, 0, len(blocks))
	for at, block := range blocks {
		section := tableOf(block)
		section.Title = title
		if at > 0 {
			if line, _, _ := strings.Cut(section.Caption, "\n"); line != "" {
				section.Title += " · " + cut(line, MaxSectionRunes)
			} else {
				section.Table = at + 1
			}
		}
		if len(section.Rows) > 0 {
			sections = append(sections, section)
		}
	}
	return sections
}

// tableOf is a table of rows; the rows of one cell above the first row of several are its caption, when it has such a row.
func tableOf(block []sheetRow) Section {
	var section Section
	start := 0
	for start < len(block) && len(filledColumns(block[start].cells)) == 1 {
		start++
	}
	if start == len(block) {
		start = 0
	}
	var caption []string
	for _, kept := range block[:start] {
		for _, cell := range kept.cells {
			if line := strings.TrimSpace(strings.Join(nonEmpty(cell.Text, cell.Note), " — ")); line != "" {
				caption = append(caption, line)
			}
		}
	}
	section.Caption = strings.Join(caption, "\n")
	for _, kept := range block[start:] {
		section.Rows = append(section.Rows, kept.cells)
	}
	return section
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func filledColumns(cells []Cell) []int {
	var columns []int
	for column, cell := range cells {
		if cell.Text != "" || cell.Note != "" {
			columns = append(columns, column)
		}
	}
	return columns
}

func sameColumns(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// columnOf is the column of a reference, from 0: A1 is 0, AB7 is 27.
func columnOf(ref string) int {
	column := 0
	for _, char := range ref {
		if char < 'A' || char > 'Z' {
			break
		}
		column = column*26 + int(char-'A') + 1
	}
	return column - 1
}

// sheetNotes reads the notes and the threaded comments of a sheet by the reference of their cell. A threaded
// comment wins: Excel also writes a stand-in for older versions under the same reference.
func sheetNotes(source archive, sheet string) (map[string]string, error) {
	rels := path2rels(sheet)
	var list xlsxRels
	if err := decodeOptional(source, rels, &list); err != nil {
		return nil, err
	}
	notes := map[string]string{}
	threaded := map[string]string{}
	for _, rel := range list.List {
		target := resolve(sheet, rel.Target)
		switch {
		case strings.HasSuffix(rel.Type, "/comments"):
			var comments xlsxComments
			if err := decodeOptional(source, target, &comments); err != nil {
				return nil, err
			}
			for _, comment := range comments.List {
				notes[comment.Ref] = withoutAuthor(comment.Text.String(), comments.Authors)
			}
		case strings.HasSuffix(rel.Type, "/threadedComment"):
			var comments xlsxThreaded
			if err := decodeOptional(source, target, &comments); err != nil {
				return nil, err
			}
			for _, comment := range comments.List {
				text := strings.TrimSpace(comment.Text)
				if earlier := threaded[comment.Ref]; earlier != "" {
					text = earlier + "\n" + text
				}
				threaded[comment.Ref] = text
			}
		}
	}
	for ref, text := range threaded {
		notes[ref] = text
	}
	return notes, nil
}

func path2rels(sheet string) string {
	index := strings.LastIndex(sheet, "/")
	return sheet[:index] + "/_rels" + sheet[index:] + ".rels"
}

// withoutAuthor drops the line Excel starts a note with: the name of its author and a colon.
func withoutAuthor(text string, authors []string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	for _, author := range authors {
		prefix := strings.TrimSpace(author) + ":"
		if author != "" && strings.HasPrefix(text, prefix) {
			return strings.TrimSpace(text[len(prefix):])
		}
	}
	return text
}

// cellStyle is what a cell format says of its cell: how it looks and how its number reads.
type cellStyle struct {
	look   string
	number numberFormat
}

// cellStyles names the look of each cell format — its fill and the colour of its font — and the kind of its numbers.
func cellStyles(source archive) ([]cellStyle, error) {
	var styles xlsxStyles
	if err := decodeOptional(source, "xl/styles.xml", &styles); err != nil {
		return nil, err
	}
	var theme xlsxTheme
	if err := decodeOptional(source, "xl/theme/theme1.xml", &theme); err != nil {
		return nil, err
	}
	scheme := theme.Scheme
	// The order of theme indexes in a workbook: light before dark in each pair.
	palette := []string{
		scheme.Lt1.hex(), scheme.Dk1.hex(), scheme.Lt2.hex(), scheme.Dk2.hex(), scheme.Accent1.hex(), scheme.Accent2.hex(),
		scheme.Accent3.hex(), scheme.Accent4.hex(), scheme.Accent5.hex(), scheme.Accent6.hex(), scheme.Link.hex(), scheme.Visited.hex(),
	}
	codes := make(map[int]string, len(styles.NumberFormats))
	for _, format := range styles.NumberFormats {
		codes[format.ID] = format.Code
	}
	looks := make([]cellStyle, len(styles.Formats))
	for index, format := range styles.Formats {
		var parts []string
		if format.Fill >= 0 && format.Fill < len(styles.Fills) {
			fill := styles.Fills[format.Fill].Pattern
			if fill.Type != "" && fill.Type != "none" && fill.Type != "gray125" {
				if name := hueOf(fill.Fg, palette); name.fill != "" {
					parts = append(parts, name.fill+" заливка")
				}
			}
		}
		if format.Font >= 0 && format.Font < len(styles.Fonts) {
			if name := hueOf(styles.Fonts[format.Font].Color, palette); name.font != "" {
				parts = append(parts, name.font+" текст")
			}
		}
		looks[index] = cellStyle{look: strings.Join(parts, ", "), number: numberFormatOf(format.Number, codes[format.Number])}
	}
	return looks, nil
}

// hue is the name of a colour for a fill and for a font; empty where it says nothing: a white fill, a black font.
type hue struct{ fill, font string }

func hueOf(color xlsxColor, palette []string) hue {
	value := color.RGB
	switch {
	case value != "":
	case color.Theme != nil && *color.Theme >= 0 && *color.Theme < len(palette):
		value = palette[*color.Theme]
	case color.Indexed != nil && *color.Indexed >= 0 && *color.Indexed < len(indexedColors):
		value = indexedColors[*color.Indexed]
	default:
		return hue{}
	}
	if len(value) == 8 {
		value = value[2:]
	}
	number, err := strconv.ParseUint(value, 16, 32)
	if err != nil || len(value) != 6 {
		return hue{}
	}
	tint := 0.0
	if color.Tint != nil {
		tint = *color.Tint
	}
	return nameOf(float64(number>>16&255)/255, float64(number>>8&255)/255, float64(number&255)/255, tint)
}

// nameOf names a colour by its hue, lightness and saturation; the tint of a theme colour moves its lightness.
func nameOf(red, green, blue, tint float64) hue {
	high := math.Max(red, math.Max(green, blue))
	low := math.Min(red, math.Min(green, blue))
	light := (high + low) / 2
	saturation := 0.0
	if high != low {
		saturation = (high - low) / (1 - math.Abs(2*light-1))
	}
	if tint < 0 {
		light *= 1 + tint
	} else {
		light = light*(1-tint) + tint
	}
	switch {
	case light > 0.95:
		return hue{}
	case saturation < 0.12 && light < 0.25:
		return hue{fill: "чёрная"}
	case saturation < 0.12:
		return hue{fill: "серая", font: "серый"}
	}
	degrees := 0.0
	switch high {
	case red:
		degrees = math.Mod((green-blue)/(high-low)+6, 6) * 60
	case green:
		degrees = ((blue-red)/(high-low) + 2) * 60
	default:
		degrees = ((red-green)/(high-low) + 4) * 60
	}
	switch {
	case degrees < 15 || degrees >= 340:
		return hue{"красная", "красный"}
	case degrees < 48:
		return hue{"оранжевая", "оранжевый"}
	case degrees < 70:
		return hue{"жёлтая", "жёлтый"}
	case degrees < 170:
		return hue{"зелёная", "зелёный"}
	case degrees < 200:
		return hue{"голубая", "голубой"}
	case degrees < 260:
		return hue{"синяя", "синий"}
	case degrees < 300:
		return hue{"фиолетовая", "фиолетовый"}
	default:
		return hue{"розовая", "розовый"}
	}
}

// indexedColors is the standard palette of the indexed colours of a workbook.
var indexedColors = [...]string{
	"000000", "FFFFFF", "FF0000", "00FF00", "0000FF", "FFFF00", "FF00FF", "00FFFF",
	"000000", "FFFFFF", "FF0000", "00FF00", "0000FF", "FFFF00", "FF00FF", "00FFFF",
	"800000", "008000", "000080", "808000", "800080", "008080", "C0C0C0", "808080",
	"9999FF", "993366", "FFFFCC", "CCFFFF", "660066", "FF8080", "0066CC", "CCCCFF",
	"000080", "FF00FF", "FFFF00", "00FFFF", "800080", "800000", "008080", "0000FF",
	"00CCFF", "CCFFFF", "CCFFCC", "FFFF99", "99CCFF", "FF99CC", "CC99FF", "FFCC99",
	"3366FF", "33CCCC", "99CC00", "FFCC00", "FF9900", "FF6600", "666699", "969696",
	"003366", "339966", "003300", "333300", "993300", "993366", "333399", "333333",
}
