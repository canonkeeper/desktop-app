// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// The names of the properties a record from a note gets (OB3.4).
const (
	PropertyAliases = "Другие имена"
	PropertyTags    = "Теги"
)

// kindWords are the kinds of a record by the words of a header and of tags, in Russian and in English.
var kindWords = map[string]authormaterials.Kind{
	"персонаж": authormaterials.KindCharacter, "персонажи": authormaterials.KindCharacter, "герой": authormaterials.KindCharacter,
	"героиня": authormaterials.KindCharacter, "character": authormaterials.KindCharacter, "characters": authormaterials.KindCharacter,
	"person": authormaterials.KindCharacter, "npc": authormaterials.KindCharacter,
	"место": authormaterials.KindPlace, "места": authormaterials.KindPlace, "локация": authormaterials.KindPlace,
	"location": authormaterials.KindPlace, "locations": authormaterials.KindPlace, "place": authormaterials.KindPlace,
	"навык": authormaterials.KindSkill, "навыки": authormaterials.KindSkill, "умение": authormaterials.KindSkill,
	"способность": authormaterials.KindSkill, "skill": authormaterials.KindSkill, "skills": authormaterials.KindSkill,
	"ability": authormaterials.KindSkill,
	"предмет": authormaterials.KindItem, "предметы": authormaterials.KindItem, "артефакт": authormaterials.KindItem,
	"item": authormaterials.KindItem, "items": authormaterials.KindItem, "artifact": authormaterials.KindItem,
	"фракция": authormaterials.KindFaction, "фракции": authormaterials.KindFaction, "гильдия": authormaterials.KindFaction,
	"клан": authormaterials.KindFaction, "организация": authormaterials.KindFaction, "faction": authormaterials.KindFaction,
	"factions": authormaterials.KindFaction, "organization": authormaterials.KindFaction, "guild": authormaterials.KindFaction,
}

// kindKeys are the keys of a header that name the kind of a record.
var kindKeys = []string{"type", "kind", "категория"}

// serviceKeys are the keys of a header that are no property of a record: the kind, the name, the other names and the tags
// a record reads otherwise, and what Obsidian and its plugins keep for themselves.
var serviceKeys = map[string]bool{
	"type": true, "kind": true, "категория": true, "title": true, "name": true, "aliases": true, "alias": true,
	"tags": true, "tag": true, "cssclasses": true, "cssclass": true, "publish": true, "permalink": true, "created": true,
	"updated": true, "modified": true, "date": true, "banner": true, "icon": true, "position": true,
	"excalidraw-plugin": true, "kanban-plugin": true, "order": true, "chapter": true,
}

// Record is a record of the materials of the author a note gives (OB3.4), with what of it did not fit.
type Record struct {
	Path     string
	Draft    authormaterials.Draft
	Aliases  []string
	Warnings []Warning
	// Cut counts the values and the description cut to the limits of a record; Left counts the keys too long and the
	// properties over the limit.
	Cut, Left int
}

// Plain is how the import of a book reads the lines of Markdown as plain words (library.MarkdownPlainLines): the
// description of a record is the text of its note in the same projection as a paragraph of a book.
type Plain func(text string) []string

var (
	listItem  = regexp.MustCompile(`^\s*(?:[-*+]|\d{1,9}[.)])\s+`)
	linkInRow = regexp.MustCompile(`\[\[[^\]]+\]\]|\[[^\]]+\]\([^)]+\)`)
)

// Material reads a record from a note (OB3.4): its kind by the header, then by the tags, then by its folder; its name by
// title or name of the header, otherwise the name of its file; its other names as «Другие имена»; the other keys of the
// header, the fields «Ключ:: значение» and the rows of a table of two columns as its properties, the tags left as «Теги»;
// its text after the cleaning in the plain projection of a book as its description; the path of its folder as its
// section. A note without a name or with a name over the limit of a record is skipped by its reason.
func Material(note Note, folderKind authormaterials.Kind, plain Plain) (Record, Skip) {
	record := Record{Path: note.Path}
	name := frontText(note, "title", "name")
	if name == "" {
		name = note.Name
	}
	name = cleanTitle(name)
	switch {
	case name == "":
		return Record{}, SkipNoName
	case utf8.RuneCountInString(name) > authormaterials.MaxNameRunes:
		return Record{}, SkipLongName
	}
	record.Draft.Name = name
	cleaned := Clean(note.Body, true)
	tags := append(frontList(note, "tags", "tag"), cleaned.Tags...)
	record.Draft.Kind = materialKind(note, tags, folderKind)

	properties := newProperties(&record)
	for _, alias := range frontList(note, "aliases", "alias") {
		if alias = cleanTitle(alias); alias != "" && alias != name {
			record.Aliases = append(record.Aliases, alias)
		}
	}
	if len(record.Aliases) > 0 {
		properties.add(PropertyAliases, strings.Join(record.Aliases, ", "))
	}
	for _, key := range note.Front.Keys() {
		if serviceKeys[strings.ToLower(key)] {
			continue
		}
		value, _ := note.Front.Get(key)
		properties.add(key, strings.Join(value.Texts(), ", "))
	}
	for _, field := range cleaned.Fields {
		properties.add(field[0], field[1])
	}
	for _, row := range cleaned.Rows {
		if genericHeader(row) {
			continue
		}
		properties.add(row[0], row[1])
	}
	if left := otherTags(tags); len(left) > 0 {
		properties.add(PropertyTags, strings.Join(left, ", "))
	}
	record.Draft.Properties = properties.list

	var paragraphs []string
	var current []string
	for _, line := range plain(descriptionLinks(cleaned.Text, plain)) {
		if strings.TrimSpace(line) == "" {
			if len(current) > 0 {
				paragraphs = append(paragraphs, strings.Join(current, "\n"))
				current = nil
			}
			continue
		}
		current = append(current, strings.TrimSpace(line))
	}
	if len(current) > 0 {
		paragraphs = append(paragraphs, strings.Join(current, "\n"))
	}
	record.Draft.Description = byParagraphs(paragraphs, authormaterials.MaxDescriptionRunes, &record.Cut)
	if folder := parentOf(note.Path); folder != Root {
		record.Draft.Section = sectionOf(folder)
	}
	record.Warnings = noteWarnings(cleaned.Text)
	// specs/2026-11-15-desk-obsidian-notes.md ON1.6: the record is what the rules of a record keep — a name of no key is no
	// name, a record they refuse holds signs a record does not keep.
	clean, ok := authormaterials.CleanDraft(record.Draft)
	switch {
	case !ok:
		return Record{}, SkipEncoding
	case sagamemory.GlossaryKey(clean.Name) == "":
		return Record{}, SkipNoName
	}
	record.Draft = clean
	return record, ""
}

func frontText(note Note, keys ...string) string {
	for _, key := range keys {
		if value, ok := note.Front.Get(key); ok {
			if texts := value.Texts(); len(texts) > 0 {
				return linkCaption(texts[0])
			}
		}
	}
	return ""
}

func frontList(note Note, keys ...string) []string {
	var list []string
	for _, key := range keys {
		value, ok := note.Front.Get(key)
		if !ok {
			continue
		}
		for _, text := range value.Texts() {
			for _, item := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || !value.IsList && r == ' ' }) {
				if item = strings.TrimPrefix(strings.TrimSpace(item), "#"); item != "" {
					list = append(list, item)
				}
			}
		}
	}
	return list
}

// materialKind is the kind of a record: the header, then the tags, then the folder, otherwise «other».
func materialKind(note Note, tags []string, folderKind authormaterials.Kind) authormaterials.Kind {
	for _, key := range kindKeys {
		if value, ok := note.Front.Get(key); ok {
			for _, text := range value.Texts() {
				if kind, ok := kindWords[strings.ToLower(cleanTitle(linkCaption(text)))]; ok {
					return kind
				}
			}
		}
	}
	for _, tag := range tags {
		if kind, ok := tagKind(tag); ok {
			return kind
		}
	}
	if folderKind != "" {
		return folderKind
	}
	return authormaterials.KindOther
}

func tagKind(tag string) (authormaterials.Kind, bool) {
	first, _, _ := strings.Cut(strings.ToLower(tag), "/")
	kind, ok := kindWords[first]
	return kind, ok
}

// otherTags are the tags that do not name a kind, once each.
func otherTags(tags []string) []string {
	seen := map[string]bool{}
	var left []string
	for _, tag := range tags {
		if _, kind := tagKind(tag); kind || seen[strings.ToLower(tag)] {
			continue
		}
		seen[strings.ToLower(tag)] = true
		left = append(left, tag)
	}
	return left
}

// linkCaption is a link of the wiki as its caption or its name; any other value is itself.
func linkCaption(value string) string {
	return wikiValue.ReplaceAllStringFunc(value, func(link string) string {
		inner := strings.TrimSuffix(strings.TrimPrefix(link, "[["), "]]")
		if bar := strings.LastIndexByte(inner, '|'); bar >= 0 {
			return inner[bar+1:]
		}
		if hash := strings.IndexAny(inner, "#^"); hash >= 0 {
			inner = inner[:hash]
		}
		return inner
	})
}

var wikiValue = regexp.MustCompile(`\[\[[^\]]*\]\]`)

// linkTarget is the note a link of the wiki leads to and the caption of the link (specs/2026-11-15-desk-wiki-target.md
// WT1.3): the target is the last name of the path of the link before its first «|», «#» or «^», in NFC, without .md — a
// name of a note has no backslash (readableName), so an escaped «\|» of a table ends the target too. A link without a
// caption, a link to a heading of its own note and a link to a file that is not a note (fileName) have no target.
func linkTarget(inner string) (target, caption string) {
	bar := strings.LastIndexByte(inner, '|')
	if bar < 0 {
		return "", ""
	}
	caption, target = inner[bar+1:], inner
	if end := strings.IndexAny(target, "|#^"); end >= 0 {
		target = target[:end]
	}
	target = cleanTitle(strings.TrimSuffix(target[strings.LastIndexByte(target, '/')+1:], `\`))
	switch {
	case IsNoteName(target):
		target = cleanTitle(NoteStem(target))
	case fileName(target):
		return "", caption
	}
	return target, caption
}

// fileName says a name ends with the extension of a file: a dot and up to ten Latin letters and digits, a letter among
// them — «Зимогорск.pdf», «План.canvas»; «Глава 1.2» and «Т. Иванов» are names of notes.
func fileName(name string) bool {
	extension := name[strings.LastIndexByte(name, '.')+1:]
	if len(extension) == len(name) || extension == "" || len(extension) > 10 {
		return false
	}
	letter := false
	for _, r := range extension {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			letter = true
		case r < '0' || r > '9':
			return false
		}
	}
	return letter
}

// unnamed is the target of a link a record writes after its caption (WT1.1, WT1.2): the target when the caption, as the map
// reads it, neither is its name nor names it — by the rule by which the map finds a name in the text of a record
// (sagamemory: the words of the name in a row, in any of their cases) —, so the map links a record with the note of its
// link; otherwise nothing.
func unnamed(target, read string) string {
	key := sagamemory.GlossaryKey(target)
	if key == "" || sagamemory.GlossaryKey(read) == key || sagamemory.NamedNames(target).Subject(nil).About("", read) {
		return ""
	}
	return target
}

// linkValue is a value of a property of a record with its links of the wiki as their captions (linkCaption) and the note of
// a link after a caption that does not name it, in brackets (WT1.1): «город у гор (Зимогорск)». The map reads a value as it
// is, so the caption names its note as it stands. An embed and a link in code are as linkCaption makes them.
func linkValue(value string) string {
	return valueLink.ReplaceAllStringFunc(value, func(link string) string {
		caption := linkCaption(link)
		if !strings.HasPrefix(link, "[[") {
			return caption
		}
		if target, read := linkTarget(link[2 : len(link)-2]); target != "" {
			if target = unnamed(target, read); target != "" {
				return caption + " (" + target + ")"
			}
		}
		return caption
	})
}

// valueLink finds the links of the wiki of a value and its code in a line — from a «`» to the next one of its line, without
// one to the end of the line.
var valueLink = regexp.MustCompile("`[^`\\n]*`?|!?\\[\\[[^\\]]*\\]\\]")

// descriptionLinks writes into the caption of a link of the wiki of the text of a record the note of the link, where the
// caption does not name it (WT1.1): [[Зимогорск|к городу у гор]] is [[Зимогорск|к городу у гор (Зимогорск)]], which the plain
// projection reads as «к городу у гор (Зимогорск)»; the name of the note goes escaped as a title is (escapeTitle), so it
// reads as its letters. The caption names its note as the map reads it — in the plain projection, the signs of its faces
// gone. A link is found as the import reads one (library, markdownLink): a sign after a backslash is no sign, a link runs
// within its line to the first «]]»; an embed ![[…]] is left — the projection reads it as nothing —, and so is a link in a
// fence of code (splitFenced, the fences as the import reads them) and in code in a line. The glue of a book does not come
// here: a chapter keeps the caption alone (WT1.4).
func descriptionLinks(text string, plain Plain) string {
	if !strings.Contains(text, "[[") {
		return text
	}
	var out strings.Builder
	for _, part := range splitFenced(text) {
		if part.fenced {
			out.WriteString(part.text)
			continue
		}
		out.WriteString(descriptionLink.ReplaceAllStringFunc(part.text, func(link string) string {
			if !strings.HasPrefix(link, "[[") {
				return link
			}
			inner := link[2 : len(link)-2]
			if target, caption := linkTarget(inner); target != "" {
				if target = unnamed(target, strings.Join(plain(caption), " ")); target != "" {
					return "[[" + inner + " (" + escapeTitle(target) + ")]]"
				}
			}
			return link
		}))
	}
	return out.String()
}

// descriptionLink finds in a text out of its fences a sign after a backslash, code in a line as valueLink finds it, an embed
// and a link of the wiki.
var descriptionLink = regexp.MustCompile("\\\\.|`[^`\\n]*`?|!?\\[\\[(?:[^\\]\\n]|\\][^\\]\\n])*\\]\\]")

// genericHeader says a row of a table names its columns, not a property.
func genericHeader(row [2]string) bool {
	generic := map[string]bool{"ключ": true, "значение": true, "свойство": true, "key": true, "value": true, "property": true, "field": true, "поле": true}
	return generic[strings.ToLower(row[0])] && generic[strings.ToLower(row[1])]
}

// properties gathers the properties of a record within its limits.
type properties struct {
	record *Record
	list   []authormaterials.Property
}

func newProperties(record *Record) *properties { return &properties{record: record} }

func (gathered *properties) add(key, value string) {
	key = cleanTitle(linkCaption(key))
	value = cleanTitle(linkValue(value))
	if key == "" || value == "" {
		return
	}
	for _, held := range gathered.list {
		if strings.EqualFold(held.Key, key) {
			return
		}
	}
	if utf8.RuneCountInString(key) > authormaterials.MaxKeyRunes || len(gathered.list) >= authormaterials.MaxProperties {
		gathered.record.Left++
		return
	}
	if cut, ok := cutRunes(value, authormaterials.MaxValueRunes); ok {
		value = cut
		gathered.record.Cut++
	}
	gathered.list = append(gathered.list, authormaterials.Property{Key: key, Value: value})
}

// byParagraphs joins paragraphs up to limit runes, cut at the end of a paragraph; a first paragraph longer than the limit
// is cut at a word.
func byParagraphs(paragraphs []string, limit int, cut *int) string {
	var out strings.Builder
	runes := 0
	for index, paragraph := range paragraphs {
		length := utf8.RuneCountInString(paragraph)
		separator := 0
		if index > 0 {
			separator = 2
		}
		if runes+separator+length > limit {
			*cut++
			if index == 0 {
				shortened, _ := cutRunes(paragraph, limit)
				return shortened
			}
			break
		}
		if index > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(paragraph)
		runes += separator + length
	}
	return out.String()
}

// sectionOf is the section of a record: the path of its folder, a long one cut from its start.
func sectionOf(folder string) string {
	if utf8.RuneCountInString(folder) <= authormaterials.MaxSectionRunes {
		return folder
	}
	runes := []rune(folder)
	return "…" + string(runes[len(runes)-authormaterials.MaxSectionRunes+1:])
}

// noteWarnings are what a record says of its note (OB4.3): a note that is a list of links rather than one record, and a
// note with several sections of one level — several heroes in one note.
func noteWarnings(text string) []Warning {
	var warnings []Warning
	lines, items, linked := 0, 0, 0
	levels := map[int]int{}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if match := headingLine.FindStringSubmatch(trimmed); match != nil && strings.TrimSpace(match[2]) != "" {
			levels[len(match[1])]++
			continue
		}
		lines++
		if listItem.MatchString(trimmed) {
			items++
			if linkInRow.MatchString(trimmed) {
				linked++
			}
		}
	}
	if linked >= 3 && items*3 >= lines*2 {
		warnings = append(warnings, WarningListNote)
	}
	for level := 1; level <= 6; level++ {
		if levels[level] > 0 {
			if levels[level] >= 2 {
				warnings = append(warnings, WarningSeveralSections)
			}
			break
		}
	}
	return warnings
}

// RecordsFingerprint is the fingerprint of the records a preview shows (OB4.3): SHA-256 of what they are to write — the
// note, the name, the kind, the description, the section, the properties and the other names — in the order of their
// notes, so the order in which the system walks a vault does not change it.
func RecordsFingerprint(records []Record) string {
	type written struct {
		Path        string                     `json:"path"`
		Name        string                     `json:"name"`
		Kind        authormaterials.Kind       `json:"kind"`
		Description string                     `json:"description"`
		Section     string                     `json:"section"`
		Properties  []authormaterials.Property `json:"properties"`
		Aliases     []string                   `json:"aliases"`
	}
	sorted := make([]written, 0, len(records))
	for _, record := range records {
		sorted = append(sorted, written{
			Path: record.Path, Name: record.Draft.Name, Kind: record.Draft.Kind, Description: record.Draft.Description,
			Section: record.Draft.Section, Properties: record.Draft.Properties, Aliases: record.Aliases,
		})
	}
	slices.SortFunc(sorted, func(left, right written) int { return strings.Compare(left.Path, right.Path) })
	encoded, _ := json.Marshal(sorted)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
