package authormaterials

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

// The limits of what one file may unpack to, so a small archive cannot take the server's memory.
const (
	maxEntryBytes = 64 << 20
	maxRows       = 5000
	maxParagraphs = 5000
	// sampleRows is how many rows of a table the model sees: enough to name its columns.
	sampleRows = 5
	// sampleCellRunes bounds one cell of the sample.
	sampleCellRunes = 200
	// chunkRunes bounds the text of one call about a section of paragraphs.
	chunkRunes = 12000
)

// Cell is one cell of a table: its text, the note or comment on it and how it looks («оранжевая заливка»).
type Cell struct {
	Text string `json:"text,omitempty"`
	Note string `json:"note,omitempty"`
	Look string `json:"look,omitempty"`
}

// Paragraph is one paragraph of a text with the comment on it; Level is its heading level, 0 for plain text.
type Paragraph struct {
	Text  string `json:"text"`
	Note  string `json:"note,omitempty"`
	Level int    `json:"level,omitempty"`
}

// Section is a sheet, a table or a part of a text under a heading. Caption is what the author wrote above a table — its
// title, a date — which the model reads and no record takes. Table is the place of a table with no such line among the tables
// of its sheet or document: the author sees it by the names of its columns (label).
type Section struct {
	Title      string      `json:"title,omitempty"`
	Caption    string      `json:"caption,omitempty"`
	Table      int         `json:"table,omitempty"`
	Rows       [][]Cell    `json:"rows,omitempty"`
	Paragraphs []Paragraph `json:"paragraphs,omitempty"`
}

// maxLabelColumns and maxLabelColumnRunes bound the names of columns that name a table (R9.4).
const (
	maxLabelColumns     = 3
	maxLabelColumnRunes = 40
)

// label is the name the author sees of a section: a table with no line over it goes by the first names of its columns, by its
// number when it has no header.
func (section Section) label(header []Cell) string {
	if section.Table == 0 {
		return section.Title
	}
	var names []string
	for _, cell := range header {
		if text := strings.TrimSpace(cell.Text); text != "" && len(names) < maxLabelColumns {
			names = append(names, cut(text, maxLabelColumnRunes))
		}
	}
	switch {
	case len(names) > 0 && section.Title != "":
		return section.Title + " · " + strings.Join(names, ", ")
	case len(names) > 0:
		return strings.Join(names, ", ")
	case section.Title != "":
		return fmt.Sprintf("%s · таблица %d", section.Title, section.Table)
	}
	return fmt.Sprintf("Таблица %d", section.Table)
}

// Parsed is what a file holds, before anything is decided about it.
type Parsed struct {
	Sections []Section `json:"sections"`
}

// Chunks is what the model sees of a section: the first rows of a table or the numbered paragraphs of a text, in
// parts of a bounded size. The notes never go to it: they are read without a model (R9.3).
func (section Section) Chunks() []string {
	if len(section.Rows) > 0 {
		var text strings.Builder
		fmt.Fprintf(&text, "Раздел: %s\n", section.label(nil))
		if section.Caption != "" {
			fmt.Fprintf(&text, "Над таблицей: %s\n", cut(strings.ReplaceAll(section.Caption, "\n", "; "), sampleCellRunes))
		}
		for index, row := range section.Rows {
			if index == sampleRows+1 {
				break
			}
			fmt.Fprintf(&text, "строка %d:", index)
			for column, cell := range row {
				fmt.Fprintf(&text, " [%d] %s;", column, cut(cell.Text, sampleCellRunes))
			}
			text.WriteByte('\n')
		}
		fmt.Fprintf(&text, "всего строк: %d\n", len(section.Rows))
		return []string{text.String()}
	}
	var chunks []string
	var text strings.Builder
	start := func() {
		text.Reset()
		fmt.Fprintf(&text, "Раздел: %s\n", section.Title)
	}
	start()
	size := 0
	for index, paragraph := range section.Paragraphs {
		length := utf8.RuneCountInString(paragraph.Text)
		if size > 0 && size+length > chunkRunes {
			chunks = append(chunks, text.String())
			start()
			size = 0
		}
		heading := ""
		if paragraph.Level > 0 {
			heading = " (заголовок)"
		}
		fmt.Fprintf(&text, "абзац %d%s: %s\n", index, heading, cut(paragraph.Text, chunkRunes))
		size += length
	}
	return append(chunks, text.String())
}

// archive is a zip of a workbook or a document, read within the limits.
type archive struct {
	files map[string]*zip.File
}

func openArchive(data []byte) (archive, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return archive{}, ErrUnreadable
	}
	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		files[file.Name] = file
	}
	return archive{files: files}, nil
}

// read returns an entry, nil without one.
func (source archive) read(name string) ([]byte, error) {
	file, ok := source.files[name]
	if !ok {
		return nil, nil
	}
	if file.UncompressedSize64 > maxEntryBytes {
		return nil, ErrTooLarge
	}
	reader, err := file.Open()
	if err != nil {
		return nil, ErrUnreadable
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, maxEntryBytes+1))
	if err != nil {
		return nil, ErrUnreadable
	}
	if len(body) > maxEntryBytes {
		return nil, ErrTooLarge
	}
	return body, nil
}

// open streams an entry, nil without one; past maxEntryBytes its reader fails with ErrTooLarge.
func (source archive) open(name string) (io.ReadCloser, error) {
	file, ok := source.files[name]
	if !ok {
		return nil, nil
	}
	if file.UncompressedSize64 > maxEntryBytes {
		return nil, ErrTooLarge
	}
	reader, err := file.Open()
	if err != nil {
		return nil, ErrUnreadable
	}
	return &boundedEntry{ReadCloser: reader, left: maxEntryBytes}, nil
}

// boundedEntry is an entry read within the limit: a zip may say less than it unpacks to.
type boundedEntry struct {
	io.ReadCloser
	left int64
}

func (entry *boundedEntry) Read(buffer []byte) (int, error) {
	if entry.left <= 0 {
		return 0, ErrTooLarge
	}
	if int64(len(buffer)) > entry.left {
		buffer = buffer[:entry.left]
	}
	read, err := entry.ReadCloser.Read(buffer)
	entry.left -= int64(read)
	return read, err
}

// resolve is the entry a relationship of base points to.
func resolve(base, target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(target, "/")
	}
	return path.Join(path.Dir(base), target)
}
