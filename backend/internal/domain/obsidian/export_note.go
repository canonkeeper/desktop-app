// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// ExportSaga is what an export writes of a saga, read once before the vault is touched (OE2.1–OE2.4): its map without what
// the author took off it, the card of every node of that map as the page of the map shows it, the chapters of its text with
// the cards that are fresh, and the way from a name of a card of a chapter to the key of its node.
type ExportSaga struct {
	ID    string
	Title string
	Map   sagamemory.MapDocument
	// Nodes are the cards of the nodes of the map by their keys.
	Nodes   map[string]sagamemory.MapCardDocument
	Version *sagamemory.Version
	// Cards are the fresh cards of the chapters by their indexes in the order of the reading; a chapter without one has none.
	Cards map[int]*sagamemory.Card
	// NodeOf is the key of the node a name of a card names, false for none.
	NodeOf func(name string) (string, bool)
}

// ExportFile is a reference a run writes (OE2.1): its kind and key, the folder it lies in from the folder of the export —
// the book of a chapter instead —, and the name it gets the first time, without .md.
type ExportFile struct {
	ID     FileID
	Folder string
	Book   string
	Name   string
}

// ExportBook is a book whose chapters have references: the folder of its chapters is named by its title.
type ExportBook struct {
	ID    string
	Title string
}

// ExportPaths are where the references lie: the folder of the export from the root of the vault and the path of each
// reference from that folder. A link goes by the full path from the root (OE2.4), so it never resolves into a note of the
// author with the same name.
type ExportPaths struct {
	Folder string
	Files  map[FileID]string
}

// target is the target of a link to a reference, "" for a reference the run does not write.
func (paths ExportPaths) target(id FileID) string {
	path, ok := paths.Files[id]
	if !ok {
		return ""
	}
	return Join(paths.Folder, strings.TrimSuffix(path, ".md"))
}

// Files are the references of the saga (OE2.1): Сага.md, one a node the map draws in the folder of its category, one a
// chapter with a fresh card in the folder of its book.
func (saga ExportSaga) Files() []ExportFile {
	files := []ExportFile{{ID: FileID{Kind: ExportIndex, Key: ExportIndexKey}, Name: wordIndex}}
	for _, node := range saga.Map.Nodes {
		folder, ok := categoryWords[sagamemory.MapCategory(node.Category)]
		if !ok {
			continue
		}
		files = append(files, ExportFile{ID: FileID{Kind: ExportNode, Key: node.Key}, Folder: folder, Name: ExportName(node.Name)})
	}
	for index := range saga.Version.Chapters {
		if saga.Cards[index] == nil {
			continue
		}
		chapter := &saga.Version.Chapters[index]
		number, name := sagamemory.NumberedTitle(chapter.Title, chapter.InBook)
		files = append(files, ExportFile{
			ID: FileID{Kind: ExportChapter, Key: chapter.ID}, Book: chapter.BookID, Name: ExportName(strings.TrimSpace(number + " " + name)),
		})
	}
	return files
}

// Stay are the chapters of the saga without a fresh card: a reference of one keeps what it says (OE2.7).
func (saga ExportSaga) Stay() []FileID {
	var stay []FileID
	for index := range saga.Version.Chapters {
		if saga.Cards[index] == nil {
			stay = append(stay, FileID{Kind: ExportChapter, Key: saga.Version.Chapters[index].ID})
		}
	}
	return stay
}

// Books are the books of the saga in their order.
func (saga ExportSaga) Books() []ExportBook {
	var books []ExportBook
	for _, book := range saga.Version.Books() {
		books = append(books, ExportBook{ID: book.ID, Title: book.Title})
	}
	return books
}

// Text is the text of a reference (OE2.2): its header, the lead line and what it says, its lines ended by "\n" and no time
// of the run in it, so a reference whose content did not change is not written again.
func (saga ExportSaga) Text(id FileID, paths ExportPaths) []byte {
	note := &noteText{saga: saga, paths: paths}
	switch id.Kind {
	case ExportIndex:
		note.index()
	case ExportNode:
		note.node(id.Key)
	case ExportChapter:
		note.chapter(id.Key)
	}
	return []byte(strings.TrimRight(note.String(), "\n") + "\n")
}

// noteText writes a reference.
type noteText struct {
	strings.Builder
	saga  ExportSaga
	paths ExportPaths
}

// front writes the header (OE2.2): the flat keys of the app and the names of the reference, every value in double quotes.
func (note *noteText) front(id FileID, aliases []string) {
	note.WriteString("---\n")
	fmt.Fprintf(note, "canonkeeper-saga: %s\ncanonkeeper-key: %s\ncanonkeeper-kind: %s\n",
		strconv.Quote(note.saga.ID), strconv.Quote(id.Key), strconv.Quote(string(id.Kind)))
	if len(aliases) > 0 {
		note.WriteString("aliases:\n")
		for _, alias := range aliases {
			fmt.Fprintf(note, "  - %s\n", strconv.Quote(alias))
		}
	}
	note.WriteString("---\n" + wordLead + "\n")
}

// heading writes a heading after one empty line and before another.
func (note *noteText) heading(level int, text string) {
	note.blank()
	fmt.Fprintf(note, "%s %s\n\n", strings.Repeat("#", level), plain(text))
}

// blank ends what is written with one empty line, never two.
func (note *noteText) blank() {
	if text := note.String(); !strings.HasSuffix(text, "\n\n") {
		note.WriteString("\n")
	}
}

func (note *noteText) line(text string) {
	note.WriteString(text + "\n")
}

// link is a link to a reference with its caption, the caption alone as text for a reference the run does not write (OE2.4).
func (note *noteText) link(id FileID, caption string) string {
	if target := note.paths.target(id); target != "" {
		return "[[" + target + "|" + cleanCaption(caption) + "]]"
	}
	return plain(caption)
}

// nodeLink is a link to the reference of a node by its key.
func (note *noteText) nodeLink(key, caption string) string {
	return note.link(FileID{Kind: ExportNode, Key: key}, caption)
}

// nameLink is a link to the reference of the node a name names, the name alone for none.
func (note *noteText) nameLink(name string) string {
	if note.saga.NodeOf != nil {
		if key, ok := note.saga.NodeOf(name); ok {
			return note.nodeLink(key, name)
		}
	}
	return plain(name)
}

// place is a place of the book: a link to the reference of its chapter with its place as the caption, and «абзац N».
func (note *noteText) place(chapter sagamemory.MapChapterRef, paragraph int) string {
	place := note.link(FileID{Kind: ExportChapter, Key: chapter.ChapterID}, chapter.Label)
	if paragraph > 0 {
		place += ", " + note.paragraph(chapter.ChapterID, paragraph)
	}
	return place
}

// paragraph is «абзац N» of a chapter: a link that opens the app at that paragraph (LW1.1), the words alone for values that
// are not of a link.
func (note *noteText) paragraph(chapter string, paragraph int) string {
	words := fmt.Sprintf("%s %d", wordParagraph, paragraph)
	if link, ok := openLink(note.saga.ID, chapter, paragraph); ok {
		return "[" + words + "](" + link + ")"
	}
	return words
}

// index writes Сага.md (OE2.1): the books and their chapters in their order, the categories of the map and their nodes.
func (note *noteText) index() {
	saga := note.saga
	note.front(FileID{Kind: ExportIndex, Key: ExportIndexKey}, nil)
	note.heading(1, saga.Title)
	// Books of one title are named as their folders are: the second with « (2)».
	titles := map[string]int{}
	for _, book := range saga.Version.Books() {
		title := exportBookTitle(book.Title)
		titles[FoldKey(ExportName(title))]++
		note.heading(2, Numbered(title, titles[FoldKey(ExportName(title))]))
		for index := book.First; index <= book.Last; index++ {
			chapter := &saga.Version.Chapters[index]
			note.line("- " + note.link(FileID{Kind: ExportChapter, Key: chapter.ID}, chapter.Place()))
		}
	}
	for _, category := range categoryOrder {
		first := true
		for _, node := range saga.Map.Nodes {
			if sagamemory.MapCategory(node.Category) != category {
				continue
			}
			if first {
				note.heading(2, categoryWords[category])
				first = false
			}
			note.line("- " + note.nodeLink(node.Key, node.Name))
		}
	}
}

// exportBookTitle is the heading of a book of the index.
func exportBookTitle(title string) string {
	if oneLine(title) == "" {
		return wordNoName
	}
	return title
}

// node writes the reference of a node (OE2.3): what its card on the page of the map shows, in its words and in its order.
func (note *noteText) node(key string) {
	card := note.saga.Nodes[key]
	var aliases []string
	for _, alias := range card.Aliases {
		if alias = oneLine(alias); alias != "" {
			aliases = append(aliases, alias)
		}
	}
	// A note of the author links the node by its name: a file named otherwise has the name first among its aliases.
	header := aliases
	if name, file := oneLine(card.Name), note.paths.Files[FileID{Kind: ExportNode, Key: key}]; name != "" && file != "" && NoteStem(path.Base(file)) != name {
		header = append([]string{name}, aliases...)
	}
	note.front(FileID{Kind: ExportNode, Key: key}, header)
	note.heading(1, card.Name)
	kind := plain(card.Type)
	if kind == "" {
		kind = wordNoType
	}
	note.line(kind)
	if len(aliases) > 0 {
		also := make([]string, 0, len(aliases))
		for _, alias := range aliases {
			also = append(also, plain(alias))
		}
		note.blank()
		note.line(wordAlso + " " + strings.Join(also, ", "))
	}
	if card.Note != nil && plain(*card.Note) != "" {
		note.blank()
		note.line(plain(*card.Note))
	}
	onlyMaterial := card.Material != nil && card.First == nil && card.Mentions == 0
	if !onlyMaterial {
		note.blank()
		if card.First != nil {
			note.line("- " + wordFirst + ": " + note.place(card.First.Chapter, card.First.Paragraph))
		}
		note.line(fmt.Sprintf("- %s: в %d %s", wordMet, card.Mentions, plural(card.Mentions, chapterForms)))
	}
	if card.Material != nil {
		note.material(*card.Material, onlyMaterial)
	}
	note.links(card)
	if len(card.NamedIn) > 0 {
		note.heading(2, wordNamedIn)
		for _, record := range card.NamedIn {
			note.line("- " + note.nameLink(record.Name) + " · " + kindWord(record.Kind))
		}
	}
	if onlyMaterial {
		return
	}
	if len(card.Events) > 0 {
		note.heading(2, counted(wordEvents, card.EventsTotal))
		for _, event := range card.Events {
			note.line("- " + note.place(event.Chapter, event.Paragraph) + " — " + plain(event.Event))
		}
		note.part(card.EventsTotal > len(card.Events))
	}
	if len(card.Changes) > 0 {
		note.heading(2, counted(wordChanges, card.ChangesTotal))
		for _, change := range card.Changes {
			note.line("- " + note.change(change))
		}
		note.part(card.ChangesTotal > len(card.Changes))
	}
}

// counted is a heading with the number of all its items, as the page shows it beside.
func counted(heading string, total int) string {
	if total <= 0 {
		return heading
	}
	return fmt.Sprintf("%s (%d)", heading, total)
}

// part says a list is cut, where the card cuts it.
func (note *noteText) part(cut bool) {
	if cut {
		note.blank()
		note.line(wordPart)
	}
}

func (note *noteText) change(change sagamemory.CardChange) string {
	return note.place(change.Chapter, change.Paragraph) + " — " + plain(change.Subject) + " — " + plain(change.Change)
}

// material writes «По вашим материалам»: what the record of the author of this name says, apart from the book.
func (note *noteText) material(record sagamemory.CardMaterial, plan bool) {
	note.heading(2, wordMaterial)
	sign := wordRecord
	if plan {
		sign = wordPlan
	}
	note.line(sign + " · " + kindWord(record.Kind))
	// The page shows the description with its lines (white-space: pre-wrap).
	if description := paragraphs(record.Description); description != "" {
		note.blank()
		note.line(description)
	}
	if len(record.Properties) > 0 {
		note.blank()
		for _, property := range record.Properties {
			note.line("- " + plain(property.Key) + ": " + plain(property.Value))
		}
	}
	switch {
	case record.SourceKind == "obsidian" && record.SourceName != "":
		note.blank()
		note.line(wordFromObsidian + " " + plain(record.SourceName))
	case record.SourceName != "":
		origin := wordFromFile + " «" + plain(record.SourceName) + "»"
		if section := plain(record.Section); section != "" {
			origin += ", " + section
		}
		note.blank()
		note.line(origin)
	}
}

// cardBeside is how many names the card of a node of the map shows as mentioned in the same chapters at most; it gives no
// total of them, so a list that reached the bound is a part.
const cardBeside = 8

// drawnLinks is how many links of a node the map draws: the card shows the strongest of them.
func (saga ExportSaga) drawnLinks(key string) int {
	at := slices.IndexFunc(saga.Map.Nodes, func(node sagamemory.MapDocumentNode) bool { return node.Key == key })
	if at < 0 {
		return 0
	}
	count := 0
	for _, link := range saga.Map.Links {
		if link.Source == at || link.Target == at {
			count++
		}
	}
	return count
}

// links writes «Связи»: each link with why the two are linked and its reason, then the names beside; a list the card cut says
// so (Data «обрезанный список»).
func (note *noteText) links(card sagamemory.MapCardDocument) {
	if len(card.Links) == 0 && len(card.Beside) == 0 {
		return
	}
	note.heading(2, wordLinks)
	for _, link := range card.Links {
		why := wordBothChanged
		switch {
		case link.FromMaterials:
			why = wordFromMaterials
		case link.Together > 0:
			why = fmt.Sprintf("%s %d %s", wordTogether, link.Together, plural(link.Together, eventForms))
		}
		note.line("- " + note.nodeLink(link.Key, link.Name) + " — " + why)
		switch {
		case len(link.Shared) > 0:
			for _, event := range link.Shared {
				note.line("  - " + note.place(event.Chapter, event.Paragraph) + " — " + plain(event.Event))
			}
		case len(link.Changes) > 0:
			note.line("  - " + note.change(link.Changes[len(link.Changes)-1]))
		}
	}
	note.part(note.saga.drawnLinks(card.Key) > len(card.Links))
	if len(card.Beside) > 0 {
		names := make([]string, 0, len(card.Beside))
		for _, neighbor := range card.Beside {
			names = append(names, note.nodeLink(neighbor.Key, neighbor.Name))
		}
		note.blank()
		note.line(wordBeside + " " + strings.Join(names, ", "))
		note.part(len(card.Beside) >= cardBeside)
	}
}

// chapter writes the reference of a chapter (Data «справка главы»): what happens, who is in it, where, what else is named and
// what changed; an empty part is not written, and the parts of the card the table does not name are not either.
func (note *noteText) chapter(id string) {
	version := note.saga.Version
	index := -1
	for at := range version.Chapters {
		if version.Chapters[at].ID == id {
			index = at
			break
		}
	}
	if index < 0 || note.saga.Cards[index] == nil {
		return
	}
	chapter, card := &version.Chapters[index], note.saga.Cards[index]
	place := chapter.Place()
	note.front(FileID{Kind: ExportChapter, Key: id}, []string{place})
	// Under the first line, a link that opens the chapter in the app (LW1.2); a chapter whose values are not of a link has no
	// such line — its words alone would open nothing.
	if link, ok := openLink(note.saga.ID, id, 0); ok {
		note.blank()
		note.line("[" + wordOpenChapter + "](" + link + ")")
	}
	note.heading(1, place)
	if summary := paragraphs(card.Summary); summary != "" || len(card.Events) > 0 {
		note.heading(2, wordHappens)
		if summary != "" {
			note.line(summary)
			note.blank()
		}
		for _, event := range card.Events {
			text := plain(event.Event)
			if len(event.Who) > 0 {
				who := make([]string, 0, len(event.Who))
				for _, name := range event.Who {
					who = append(who, note.nameLink(name))
				}
				text += " — " + strings.Join(who, ", ")
			}
			note.line("- " + text + note.atParagraph(id, event.P))
		}
	}
	note.mentions(wordWho, card.Characters)
	var places, others []sagamemory.Mention
	for _, entity := range card.Entities {
		if category, ok := sagamemory.MapCategoryOf(entity.Type); ok && category == sagamemory.MapPlace {
			places = append(places, entity)
		} else {
			others = append(others, entity)
		}
	}
	note.mentions(wordWhere, places)
	note.mentions(wordNamed, others)
	if len(card.Changes) > 0 {
		note.heading(2, wordChanges)
		for _, change := range card.Changes {
			note.line("- " + note.nameLink(change.Subject) + " — " + plain(change.Change) + note.atParagraph(id, change.P))
		}
	}
}

// mentions writes a part of the names of a chapter: each name as a link to its node, and what the card notes of it.
func (note *noteText) mentions(heading string, mentions []sagamemory.Mention) {
	if len(mentions) == 0 {
		return
	}
	note.heading(2, heading)
	for _, mention := range mentions {
		text := note.nameLink(mention.Name)
		if remark := plain(mention.Note); remark != "" {
			text += " — " + remark
		}
		note.line("- " + text)
	}
}

// atParagraph is « · абзац N» of an item of a chapter, nothing for no paragraph.
func (note *noteText) atParagraph(chapter string, paragraph int) string {
	if paragraph <= 0 {
		return ""
	}
	return " · " + note.paragraph(chapter, paragraph)
}

// kindWord is the word of a kind of a record, the kind itself for one the page does not name.
func kindWord(kind string) string {
	if word, ok := exportKindWords[kind]; ok {
		return word
	}
	return plain(kind)
}

// oneLine is a text on one line: its spaces and ends of lines one space.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// plain is a text of a card, of a record of the author or of the model on one line of a reference (OE2.3): its spaces and
// ends of lines one space, as Markdown that reads as the text itself (literal).
func plain(text string) string {
	return literal(oneLine(text))
}

// paragraphs is a text of many lines — the description of a record of the author, which the page shows with its lines; the
// summary of a chapter — as lines of a reference: each line plain, one empty line between paragraphs. Obsidian shows a line
// break as a line break.
func paragraphs(text string) string {
	var lines []string
	for line := range strings.Lines(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")) {
		switch line = plain(line); {
		case line != "":
			lines = append(lines, line)
		case len(lines) > 0 && lines[len(lines)-1] != "":
			lines = append(lines, "")
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// markupSigns are the signs Markdown or Obsidian read as markup wherever they stand: a tag, a link and an embed of the wiki,
// a block id, a formula, emphasis, code, HTML and an entity, a cell of a table, the escape itself.
const markupSigns = "\\`*_[]<>#|$^&"

// doubledSigns are markup only twice in a row: a comment, a highlight, a strikethrough.
const doubledSigns = "%=~"

// literal is one line of a text that came from a card, a record of the author or the model as Markdown that reads as the
// text itself (OE2.3): every sign of markup escaped by a backslash, and a line that would start a heading, a quote, a list or
// a rule starts with an escaped sign. The page shows these texts as they are; so does Obsidian.
func literal(line string) string {
	runes := []rune(line)
	var escaped strings.Builder
	for index, sign := range runes {
		doubled := strings.ContainsRune(doubledSigns, sign) &&
			(index > 0 && runes[index-1] == sign || index+1 < len(runes) && runes[index+1] == sign)
		if strings.ContainsRune(markupSigns, sign) || doubled {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(sign)
	}
	text := escaped.String()
	switch {
	case strings.HasPrefix(text, "-"), strings.HasPrefix(text, "+"), strings.HasPrefix(text, "="):
		return "\\" + text
	}
	// «12. …» and «12) …» start a list.
	digits := strings.IndexFunc(text, func(sign rune) bool { return sign < '0' || sign > '9' })
	if digits > 0 && (text[digits] == '.' || text[digits] == ')') {
		return text[:digits] + "\\" + text[digits:]
	}
	return text
}

// cleanCaption is a caption of a link (OE2.4): no [, ], | and no end of a line, which would break the link.
func cleanCaption(caption string) string {
	return oneLine(strings.Map(func(sign rune) rune {
		if sign == '[' || sign == ']' || sign == '|' {
			return ' '
		}
		return sign
	}, caption))
}
