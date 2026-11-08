package authormaterials

import (
	"fmt"
	"regexp"
	"strings"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// ProseItem is what the model found in a section of paragraphs: a name and the paragraphs, From to To inclusive,
// that speak of it.
type ProseItem struct {
	Name string
	Kind Kind
	From int
	To   int
}

// Mapping is what the model says of a section. For a table: its kind, whether the first row is a header and which
// columns hold the name and the description (-1 for none). For paragraphs: Items.
type Mapping struct {
	Kind        Kind
	Header      bool
	Name        int
	Description int
	Items       []ProseItem
}

// noteKey reads a line «Ключ: значение»; a key is short and starts with a letter.
var noteKey = regexp.MustCompile(`^(\p{L}[^:]{0,79}):\s*(.*)$`)

// maxKeyWords is the longest key of a property in words: «Затраты эфира», «Улучшить навык»; a longer one is a sentence with a colon.
const maxKeyWords = 4

const descriptionKey = "описание"

// readNote splits a note into its description and properties: «Ключ: значение» lines are properties, a line «Описание:
// …» and free text are the description, and a line without a key goes on the value before it (R9.3).
func readNote(note string) (string, []Property) {
	var description []string
	var properties []Property
	inDescription := true
	for _, line := range strings.Split(strings.ReplaceAll(note, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if match := noteKey.FindStringSubmatch(line); match != nil && !strings.Contains(match[1], ". ") && len(strings.Fields(match[1])) <= maxKeyWords {
			if strings.EqualFold(strings.TrimSpace(match[1]), descriptionKey) {
				description, inDescription = append(description, match[2]), true
				continue
			}
			properties, inDescription = append(properties, Property{Key: strings.TrimSpace(match[1]), Value: match[2]}), false
			continue
		}
		if inDescription || len(properties) == 0 {
			description = append(description, line)
			continue
		}
		last := &properties[len(properties)-1]
		last.Value = strings.TrimSpace(last.Value + " " + line)
	}
	return strings.Join(description, "\n"), properties
}

// collector gathers the records of a file: one name of a file is one record.
type collector struct {
	drafts []Draft
	byKey  map[string]int
}

func (found *collector) add(draft Draft) {
	draft.Name = cut(strings.TrimSpace(draft.Name), MaxNameRunes)
	key := sagamemory.GlossaryKey(draft.Name)
	if key == "" || strings.Contains(draft.Name, "\n") {
		return
	}
	if index, ok := found.byKey[key]; ok {
		kept := &found.drafts[index]
		if draft.Description != "" && !strings.Contains(kept.Description, draft.Description) {
			kept.Description = strings.TrimSpace(kept.Description + "\n" + draft.Description)
		}
		kept.Properties = merged(kept.Properties, draft.Properties)
		kept.Description = cut(kept.Description, MaxDescriptionRunes)
		return
	}
	found.byKey[key] = len(found.drafts)
	draft.Description = cut(strings.TrimSpace(draft.Description), MaxDescriptionRunes)
	draft.Properties = merged(nil, draft.Properties)
	draft.Section = cut(strings.TrimSpace(draft.Section), MaxSectionRunes)
	found.drafts = append(found.drafts, draft)
}

// merged adds new properties within the limits of a record; a pair it has is not repeated.
func merged(kept, added []Property) []Property {
	for _, property := range added {
		property = Property{Key: cut(strings.TrimSpace(property.Key), MaxKeyRunes), Value: cut(strings.TrimSpace(property.Value), MaxValueRunes)}
		if property.Key == "" || len(kept) == MaxProperties || containsProperty(kept, property) {
			continue
		}
		kept = append(kept, property)
	}
	return kept
}

func containsProperty(list []Property, property Property) bool {
	for _, kept := range list {
		if kept == property {
			return true
		}
	}
	return false
}

// Candidates builds the records of a file by what the model said of its sections, one mapping per section. A row or
// an item without a name is not a record, and an item whose name is not in the text of its paragraphs is dropped.
func Candidates(parsed Parsed, mappings []Mapping) []Draft {
	found := collector{byKey: map[string]int{}}
	for index, section := range parsed.Sections {
		if index >= len(mappings) {
			break
		}
		if len(section.Rows) > 0 {
			tableCandidates(&found, section, mappings[index])
		} else {
			proseCandidates(&found, section, mappings[index])
		}
	}
	return found.drafts
}

func tableCandidates(found *collector, section Section, mapping Mapping) {
	rows := section.Rows
	var header []Cell
	if mapping.Header && len(rows) > 0 {
		header, rows = rows[0], rows[1:]
	}
	columnName := func(column int) string {
		if column < len(header) && header[column].Text != "" {
			return header[column].Text
		}
		return fmt.Sprintf("Столбец %d", column+1)
	}
	for _, row := range rows {
		if mapping.Name < 0 || mapping.Name >= len(row) || row[mapping.Name].Text == "" {
			continue
		}
		draft := Draft{Name: row[mapping.Name].Text, Kind: mapping.Kind, Section: section.label(header)}
		var description []string
		var fromNotes []Property
		for column, cell := range row {
			if cell.Note != "" {
				text, properties := readNote(cell.Note)
				if text != "" {
					description = append(description, text)
				}
				fromNotes = append(fromNotes, properties...)
			}
			switch {
			case column == mapping.Name:
				if cell.Look != "" {
					draft.Properties = append(draft.Properties, Property{Key: "Оформление", Value: cell.Look})
				}
			case column == mapping.Description:
				if cell.Text != "" {
					description = append([]string{cell.Text}, description...)
				}
			case cell.Text != "":
				value := cell.Text
				if cell.Look != "" {
					value += " (" + cell.Look + ")"
				}
				draft.Properties = append(draft.Properties, Property{Key: columnName(column), Value: value})
			}
		}
		draft.Properties = append(draft.Properties, fromNotes...)
		draft.Description = strings.Join(description, "\n")
		found.add(draft)
	}
}

func proseCandidates(found *collector, section Section, mapping Mapping) {
	for _, item := range mapping.Items {
		last := min(item.To, len(section.Paragraphs)-1)
		if item.From < 0 || item.From > last {
			continue
		}
		kind := item.Kind
		if kind == "" {
			kind = mapping.Kind
		}
		draft := Draft{Name: item.Name, Kind: kind, Section: section.Title}
		var description []string
		named := false
		wanted := sagamemory.Normalize(strings.TrimSpace(item.Name))
		for _, paragraph := range section.Paragraphs[item.From : last+1] {
			named = named || strings.Contains(sagamemory.Normalize(paragraph.Text), wanted)
			if paragraph.Level > 0 && sagamemory.GlossaryKey(paragraph.Text) == sagamemory.GlossaryKey(item.Name) {
				continue
			}
			text, properties := readNote(withoutName(paragraph.Text, item.Name))
			if text != "" {
				description = append(description, text)
			}
			draft.Properties = append(draft.Properties, properties...)
			if paragraph.Note != "" {
				text, properties := readNote(paragraph.Note)
				if text != "" {
					description = append(description, text)
				}
				draft.Properties = append(draft.Properties, properties...)
			}
		}
		if !named || wanted == "" {
			continue
		}
		draft.Description = strings.Join(description, "\n")
		found.add(draft)
	}
}

// withoutName drops the name a paragraph starts with before a dash or a colon: «Порт Альба: торговый город» says
// «торговый город» of Порт Альба.
func withoutName(text, name string) string {
	runes, own := []rune(text), []rune(strings.TrimSpace(name))
	if len(own) == 0 || len(runes) <= len(own) || sagamemory.Normalize(string(runes[:len(own)])) != sagamemory.Normalize(string(own)) {
		return text
	}
	rest := strings.TrimSpace(string(runes[len(own):]))
	for _, mark := range []string{"—", "–", "-", ":"} {
		if after, found := strings.CutPrefix(rest, mark); found {
			if after = strings.TrimSpace(after); after != "" {
				return after
			}
		}
	}
	return text
}

var bracketName = regexp.MustCompile(`\[([^\[\]\n]{1,200})\]`)

// BracketNames are the names a record cites in square brackets, as in «[ужас] → [омут снов]», without its own.
func BracketNames(draft Draft) []string {
	own := sagamemory.GlossaryKey(draft.Name)
	seen := map[string]bool{own: true}
	var names []string
	add := func(text string) {
		for _, match := range bracketName.FindAllStringSubmatch(text, -1) {
			key := sagamemory.GlossaryKey(match[1])
			if key != "" && !seen[key] {
				seen[key] = true
				names = append(names, strings.TrimSpace(match[1]))
			}
		}
	}
	add(draft.Description)
	for _, property := range draft.Properties {
		add(property.Value)
	}
	return names
}
