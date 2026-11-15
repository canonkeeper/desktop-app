// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"strconv"
	"strings"
)

// Value is the value of a key of the header of a note: a text or a list of texts.
type Value struct {
	Text   string
	List   []string
	IsList bool
}

// Texts are the texts of a value: the items of a list, or the text alone.
func (value Value) Texts() []string {
	if value.IsList {
		return value.List
	}
	if value.Text == "" {
		return nil
	}
	return []string{value.Text}
}

// Front is the header of a note (OB3.1): its keys in their order and their values. Skipped counts what the reader did
// not take — a nested object, a key written twice, a line that is not «ключ: значение».
type Front struct {
	keys    []string
	values  map[string]Value
	Skipped int
}

// Keys are the keys of the header in their order, as the note writes them.
func (front Front) Keys() []string { return front.keys }

// Get is the value of a key, its case ignored.
func (front Front) Get(key string) (Value, bool) {
	value, ok := front.values[strings.ToLower(key)]
	return value, ok
}

// splitFront takes the header off a text whose lines end with "\n" (OB3.1): a header is the block from «---» on the
// first line to «---» or «...» not further than MaxFrontLines lines. Without its end there is no header and the text is as
// it is — a note may begin with the break of a scene.
func splitFront(text string) ([]string, string, bool) {
	if !strings.HasPrefix(text, "---\n") && text != "---" {
		return nil, text, false
	}
	lines := strings.Split(text, "\n")
	for index := 1; index < len(lines) && index <= MaxFrontLines; index++ {
		if line := strings.TrimRight(lines[index], " \t"); line == "---" || line == "..." {
			return lines[1:index], strings.Join(lines[index+1:], "\n"), true
		}
	}
	return nil, text, false
}

// readFront reads the lines of a header with its own linear reader (OB3.1): «ключ: значение», a value in quotes, a list
// in one line «[a, b]» and a list of lines «- a». A nested object, a flow object and anything else are skipped by their
// key and counted; a key written twice keeps its first value.
func readFront(lines []string) Front {
	front := Front{values: map[string]Value{}}
	for index := 0; index < len(lines); {
		line := lines[index]
		index++
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' || strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			// A line of something the reader skipped.
			continue
		}
		key, rest, ok := splitKey(trimmed)
		if !ok {
			front.Skipped++
			continue
		}
		// The lines that belong to this key: indented ones and the items of a list.
		var nested []string
		for index < len(lines) {
			next := lines[index]
			nextTrimmed := strings.TrimSpace(next)
			if nextTrimmed != "" && next[0] != ' ' && next[0] != '\t' && !strings.HasPrefix(nextTrimmed, "- ") && nextTrimmed != "-" {
				break
			}
			if nextTrimmed != "" {
				nested = append(nested, nextTrimmed)
			}
			index++
		}
		value, ok := readValue(rest, nested)
		lower := strings.ToLower(key)
		if _, taken := front.values[lower]; !ok || taken {
			front.Skipped++
			continue
		}
		front.keys = append(front.keys, key)
		front.values[lower] = value
	}
	return front
}

// splitKey reads «ключ: значение»: a key, in quotes or not, then a colon and a space or the end.
func splitKey(line string) (string, string, bool) {
	if line[0] == '"' || line[0] == '\'' {
		end := strings.IndexByte(line[1:], line[0])
		if end < 0 || !strings.HasPrefix(line[end+2:], ":") {
			return "", "", false
		}
		rest := line[end+3:]
		if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
			return "", "", false
		}
		return line[1 : end+1], strings.TrimSpace(rest), line[1:end+1] != ""
	}
	for at := 0; at < len(line); at++ {
		if line[at] == ':' && (at+1 == len(line) || line[at+1] == ' ' || line[at+1] == '\t') {
			key := strings.TrimSpace(line[:at])
			return key, strings.TrimSpace(line[at+1:]), key != ""
		}
	}
	return "", "", false
}

// readValue reads the value of a key from the rest of its line and the lines under it.
func readValue(rest string, nested []string) (Value, bool) {
	rest = stripComment(rest)
	switch {
	case rest == "" && len(nested) == 0:
		return Value{}, true
	case rest == "":
		var list []string
		for _, line := range nested {
			if !strings.HasPrefix(line, "- ") && line != "-" {
				// A nested object.
				return Value{}, false
			}
			if item := unquote(strings.TrimSpace(strings.TrimPrefix(line, "-"))); item != "" {
				list = append(list, item)
			}
		}
		return Value{List: list, IsList: true}, true
	case len(nested) > 0 && (rest == "|" || rest == ">" || strings.HasPrefix(rest, "|") || strings.HasPrefix(rest, ">")) && len(rest) <= 2:
		separator := "\n"
		if rest[0] == '>' {
			separator = " "
		}
		return Value{Text: strings.Join(nested, separator)}, true
	case len(nested) > 0:
		// A value that goes on the next lines: a plain text broken by width.
		return Value{Text: unquote(rest + " " + strings.Join(nested, " "))}, true
	case strings.HasPrefix(rest, "[[") && strings.HasSuffix(rest, "]]") && !strings.Contains(rest[2:len(rest)-2], "]]"):
		// A link of the wiki written without quotes: what the author meant is the link.
		return Value{Text: rest}, true
	case strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]"):
		return Value{List: flowItems(rest[1 : len(rest)-1]), IsList: true}, true
	case strings.HasPrefix(rest, "{"):
		return Value{}, false
	}
	return Value{Text: unquote(rest)}, true
}

// stripComment takes « # …» off a value that is not in quotes.
func stripComment(value string) string {
	if value == "" || value[0] == '"' || value[0] == '\'' {
		return value
	}
	if at := strings.Index(value, " #"); at >= 0 {
		return strings.TrimSpace(value[:at])
	}
	return value
}

// flowItems splits a list in one line by its commas, a comma in quotes or in a link kept.
func flowItems(text string) []string {
	var items []string
	var current strings.Builder
	quote, links := byte(0), 0
	flush := func() {
		if item := unquote(strings.TrimSpace(current.String())); item != "" {
			items = append(items, item)
		}
		current.Reset()
	}
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch {
		case quote != 0:
			if character == quote {
				quote = 0
			}
		case character == '"' || character == '\'':
			quote = character
		case strings.HasPrefix(text[index:], "[["):
			links++
		case strings.HasPrefix(text[index:], "]]") && links > 0:
			links--
		case character == ',' && links == 0:
			flush()
			continue
		}
		current.WriteByte(character)
	}
	flush()
	return items
}

// unquote takes the quotes off a value: in double quotes the escapes of YAML, in single ones a doubled quote.
func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
		return value[1 : len(value)-1]
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	return value
}
