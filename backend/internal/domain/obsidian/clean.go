// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Dropped counts what of a note does not go into the text (OB3.2, OB4.2): the notes at the foot and the pictures — the
// import reads them as nothing —, the comments %%…%% and the blocks of plugins that the cleaning takes off, the tables
// a book does not take, and what stays text: a %% without its pair and the keys of a header nobody reads.
type Dropped struct {
	Footnotes        int
	Embeds           int
	Comments         int
	UnpairedComments int
	PluginBlocks     int
	Tables           int
	FrontKeys        int
}

// Add counts other into dropped.
func (dropped *Dropped) Add(other Dropped) {
	dropped.Footnotes += other.Footnotes
	dropped.Embeds += other.Embeds
	dropped.Comments += other.Comments
	dropped.UnpairedComments += other.UnpairedComments
	dropped.PluginBlocks += other.PluginBlocks
	dropped.Tables += other.Tables
	dropped.FrontKeys += other.FrontKeys
}

// pluginLanguages are the blocks of code of plugins: queries and pictures, not text of a book.
var pluginLanguages = map[string]bool{
	"dataview": true, "dataviewjs": true, "tasks": true, "query": true, "mermaid": true, "compressed-json": true,
}

// Cleaned is the text of a note after the cleaning, with what the cleaning took for a record: the tags, the fields
// «Ключ:: значение» and the rows of the tables of two columns.
type Cleaned struct {
	Text    string
	Dropped Dropped
	Tags    []string
	Fields  [][2]string
	Rows    [][2]string
}

var (
	calloutLabel    = regexp.MustCompile(`^(\s*(?:>\s*)+)\[![^\]\s]+\][+-]?[ \t]*`)
	footnoteLine    = regexp.MustCompile(`^\s*\[\^[^\]]+\]:`)
	embed           = regexp.MustCompile(`!\[\[[^\]]*\]\]|!\[[^\]]*\]\([^)]*\)`)
	blockID         = regexp.MustCompile(`\s+\^[A-Za-z0-9-]+\s*$`)
	inlineField     = regexp.MustCompile(`^\s*([^\s:\[\]][^:\[\]]*?)::\s*(.*)$`)
	tableSeparation = regexp.MustCompile(`^\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?$`)
)

// Clean cleans the text of a note for a book or for a record (OB3.2): the comments %%…%% with their pair, the blocks of
// code of plugins, the label of a callout [!…] — its words stay —, a line of tags alone and a row of tags at the end of a
// line go; a tag inside a sentence loses its «#»; a fence of code still open at the end is closed. The fences are read as
// the import of a book reads them (fenceLine), so that a note never leaves a fence open for the next one. A record takes
// the fields «Ключ:: значение» and the rows of a table of two columns out of the text; a book drops its tables.
func Clean(body string, record bool) Cleaned {
	var cleaned Cleaned
	text := dropComments(body, &cleaned.Dropped)
	var out []string
	fenced := false
	plugin := false
	table := false
	var tableRows [][2]string
	tableWide := false
	endTable := func() {
		if !table {
			return
		}
		table = false
		if record && !tableWide {
			cleaned.Rows = append(cleaned.Rows, tableRows...)
		} else {
			cleaned.Dropped.Tables++
		}
		tableRows, tableWide = nil, false
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if fenced {
			if fenceLine(trimmed) {
				fenced = false
				if plugin {
					plugin = false
					continue
				}
				out = append(out, line)
				continue
			}
			if !plugin {
				out = append(out, line)
			}
			continue
		}
		if fenceLine(trimmed) {
			endTable()
			fenced = true
			language := strings.ToLower(strings.Fields(strings.TrimLeft(trimmed, trimmed[:1]) + " x")[0])
			if plugin = pluginLanguages[language]; plugin {
				cleaned.Dropped.PluginBlocks++
				continue
			}
			out = append(out, line)
			continue
		}
		// A row of a table, in a callout too.
		if bare := strings.TrimLeft(trimmed, "> \t"); strings.HasPrefix(bare, "|") {
			table = true
			if !tableSeparation.MatchString(bare) {
				cells := tableCells(bare)
				if len(cells) == 2 {
					tableRows = append(tableRows, [2]string{cells[0], cells[1]})
				} else {
					tableWide = true
				}
			}
			continue
		}
		endTable()
		if footnoteLine.MatchString(line) {
			cleaned.Dropped.Footnotes++
		}
		cleaned.Dropped.Embeds += len(embed.FindAllStringIndex(line, -1))
		line = calloutLabel.ReplaceAllString(line, "$1")
		if record {
			if match := inlineField.FindStringSubmatch(line); match != nil && !strings.Contains(match[1], "://") {
				cleaned.Fields = append(cleaned.Fields, [2]string{strings.TrimSpace(match[1]), strings.TrimSpace(match[2])})
				continue
			}
		}
		line = blockID.ReplaceAllString(line, "")
		line, tags, alone := dropTags(line)
		cleaned.Tags = append(cleaned.Tags, tags...)
		if alone {
			continue
		}
		out = append(out, line)
	}
	endTable()
	if fenced && !plugin {
		out = append(out, "```")
	}
	cleaned.Text = strings.Join(out, "\n")
	return cleaned
}

// fenceLine says a line opens or closes a fence of code as the import of a book reads Markdown (library, textLines.read):
// any line that starts with three «`» or «~», whatever opened the fence — not by the rule of CommonMark. The cleaning and
// the glue read the fences so: a fence the cleaning closes is closed for the import too, and a heading is on the same side
// of a fence for both (OB3.2, OB3.3).
func fenceLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func tableCells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	cells := strings.Split(row, "|")
	for index := range cells {
		cells[index] = strings.TrimSpace(cells[index])
	}
	return cells
}

// dropComments takes the comments %%…%% off a text outside its fences of code (OB3.2): a pair goes with what is between,
// across lines too; a %% without its pair stays text and is counted.
func dropComments(text string, dropped *Dropped) string {
	var out strings.Builder
	segments := splitFenced(text)
	for _, segment := range segments {
		if segment.fenced {
			out.WriteString(segment.text)
			continue
		}
		rest := segment.text
		for {
			open := strings.Index(rest, "%%")
			if open < 0 {
				out.WriteString(rest)
				break
			}
			end := strings.Index(rest[open+2:], "%%")
			if end < 0 {
				dropped.UnpairedComments++
				out.WriteString(rest)
				break
			}
			dropped.Comments++
			out.WriteString(rest[:open])
			rest = rest[open+2+end+2:]
		}
	}
	return out.String()
}

type segment struct {
	text   string
	fenced bool
}

// splitFenced splits a text into its parts outside and inside the fences of code, the lines of a fence with it.
func splitFenced(text string) []segment {
	var segments []segment
	var current strings.Builder
	fenced := false
	flush := func(fenced bool) {
		if current.Len() > 0 {
			segments = append(segments, segment{text: current.String(), fenced: fenced})
			current.Reset()
		}
	}
	lines := strings.SplitAfter(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case !fenced:
			if fenceLine(trimmed) {
				flush(false)
				fenced = true
			}
			current.WriteString(line)
		default:
			current.WriteString(line)
			if fenceLine(trimmed) {
				flush(true)
				fenced = false
			}
		}
	}
	flush(fenced)
	return segments
}

// dropTags reads the tags of a line by the rule of Obsidian (OB3.2): «#» after the start of the line or a space, then the
// letters, digits, «_», «-» and «/» of the tag with at least one sign that is not a digit; inside code, links and
// addresses there are no tags. A line of tags alone goes (alone), a row of tags at the end of a line goes, a tag inside a
// sentence keeps its word without «#».
func dropTags(line string) (string, []string, bool) {
	if !strings.Contains(line, "#") {
		return line, nil, false
	}
	type found struct{ start, end int }
	var tags []found
	protected := protectedSpans(line)
	span := 0
	for index := 0; index < len(line); index++ {
		if line[index] != '#' {
			continue
		}
		// The spans are sorted and apart, and index only grows: the line is read once.
		for span < len(protected) && protected[span][1] <= index {
			span++
		}
		if span < len(protected) && protected[span][0] <= index {
			continue
		}
		if index > 0 {
			before, _ := utf8.DecodeLastRuneInString(line[:index])
			if !unicode.IsSpace(before) {
				continue
			}
		}
		end := index + 1
		digitsOnly := true
		for end < len(line) {
			r, size := utf8.DecodeRuneInString(line[end:])
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '/' {
				break
			}
			if !unicode.IsDigit(r) {
				digitsOnly = false
			}
			end += size
		}
		if end == index+1 || digitsOnly {
			continue
		}
		tags = append(tags, found{index, end})
		index = end - 1
	}
	if len(tags) == 0 {
		return line, nil, false
	}
	names := make([]string, len(tags))
	for index, tag := range tags {
		names[index] = line[tag.start+1 : tag.end]
	}
	// The row of tags at the end of the line: tags with only spaces between them and after the last.
	tail := len(tags)
	end := len(strings.TrimRightFunc(line, unicode.IsSpace))
	for tail > 0 && tags[tail-1].end == end {
		tail--
		end = len(strings.TrimRightFunc(line[:tags[tail].start], unicode.IsSpace))
	}
	// Nothing but the marks of a quote or of an item of a list stays before the row: the line was tags alone.
	if strings.Trim(line[:end], " \t>-*+") == "" {
		return "", names, true
	}
	var out strings.Builder
	last := 0
	for _, tag := range tags[:tail] {
		out.WriteString(line[last:tag.start])
		out.WriteString(line[tag.start+1 : tag.end])
		last = tag.end
	}
	out.WriteString(line[last:end])
	return out.String(), names, false
}

// protectedSpans are the parts of a line where a «#» is not a tag: code in backticks, links of the wiki and of Markdown —
// their words and their addresses —, addresses. They come sorted and apart; the line is read once.
func protectedSpans(line string) [][2]int {
	var spans [][2]int
	add := func(start, end int) {
		if last := len(spans) - 1; last >= 0 && start <= spans[last][1] {
			spans[last][1] = max(spans[last][1], end)
			return
		}
		spans = append(spans, [2]int{start, end})
	}
	// links says a «]» and a «)» may still stand further on: once one is missing, no later «[» starts a link.
	links := true
	for index := 0; index < len(line); {
		switch {
		case line[index] == '`':
			end := strings.IndexByte(line[index+1:], '`')
			if end < 0 {
				add(index, len(line))
				return spans
			}
			add(index, index+1+end+1)
			index += end + 2
		case strings.HasPrefix(line[index:], "[["):
			end := strings.Index(line[index:], "]]")
			if end < 0 {
				add(index, len(line))
				return spans
			}
			add(index, index+end+2)
			index += end + 2
		case line[index] == '[' && links:
			// A link of Markdown: its words up to the first «]», then «(» and its address up to «)». A «[» before that
			// «]» shares it, so the reading goes on after it.
			label := strings.IndexByte(line[index+1:], ']')
			if label < 0 {
				links = false
				index++
				continue
			}
			close := index + 1 + label
			if close+1 >= len(line) || line[close+1] != '(' {
				index = close + 1
				continue
			}
			end := strings.IndexByte(line[close+1:], ')')
			if end < 0 {
				links = false
				index++
				continue
			}
			add(index, close+1+end+1)
			index = close + 1 + end + 1
		case strings.HasPrefix(line[index:], "://"):
			start := strings.LastIndexFunc(line[:index], unicode.IsSpace) + 1
			end := strings.IndexFunc(line[index:], unicode.IsSpace)
			if end < 0 {
				end = len(line) - index
			}
			add(start, index+end)
			index += end
		default:
			index++
		}
	}
	return spans
}
