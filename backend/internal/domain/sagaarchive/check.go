// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/authormemory"
	"canonkeeper/backend/internal/domain/notes"
	"canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/domain/textface"
	"canonkeeper/backend/internal/governance"
)

// The checks of the records (SF3.1): the reader takes a record only when each of them holds, and the writer writes none
// it would not take (SF1.10). A check that needs other records — a ref, a repeat — is the reader's.

// PlainText is the plain text of a paragraph the product keeps beside its Markdown: the projection of the import and of the
// edit of a chapter, without the spaces at its edges (SF1.3, SF3.4).
func PlainText(markdown string) string { return strings.TrimSpace(textface.MarkdownPlain(markdown)) }

// ContentHash is the digest of a chapter by the ids, orders and plain texts of its paragraphs: the one the product binds a
// card to (governance.CanonicalChapterContentDigest), here over the paragraphs of the file.
func ContentHash(paragraphs []Paragraph) (string, bool) {
	canonical := make([]governance.CanonicalParagraphContent, len(paragraphs))
	for index, paragraph := range paragraphs {
		if paragraph.Order < 0 || paragraph.Order > math.MaxInt32 {
			return "", false
		}
		canonical[index] = governance.CanonicalParagraphContent{
			ParagraphID: paragraph.ID, Order: uint32(paragraph.Order), Content: PlainText(paragraph.Markdown),
		}
	}
	digest, err := governance.CanonicalChapterContentDigest(canonical)
	if err != nil {
		return "", false
	}
	return hex.EncodeToString(digest[:]), true
}

// ValidLine says a text is a line of a record: no control sign, no lost sign, within the limit, with words.
func ValidLine(value string, limit int) bool { return line(value, limit, true) }

// line is a short text of one line: no control signs, no lost signs, within its limit; required says it has words.
func line(value string, limit int, required bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit || required && strings.TrimSpace(value) == "" {
		return false
	}
	return !strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || r == utf8.RuneError })
}

// name is a name of a saga, a book or a chapter as its domain keeps it: words within the limit, when required, and no zero
// the base cannot keep; any other sign the product keeps in a name goes (SF3.1).
func name(value string, limit int, required bool) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0) && utf8.RuneCountInString(value) <= limit &&
		(!required || strings.TrimSpace(value) != "")
}

// free is a free text the model or the author wrote: any sign the base keeps, no lost sign.
func free(values ...string) bool {
	for _, value := range values {
		if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || strings.ContainsRune(value, utf8.RuneError) {
			return false
		}
	}
	return true
}

func model(value string) bool {
	return line(value, MaxModelRunes, true) && value == strings.TrimSpace(value)
}

func uuid(value string) bool { return uuidPattern.MatchString(value) }

func digestOf(value string) bool { return hashPattern.MatchString(value) }

func stamped(at time.Time) bool { return !at.IsZero() }

func int32Range(value, low int) bool { return value >= low && value <= math.MaxInt32 }

// validMarkdown is a paragraph of the book as the import of a book keeps it: no zero, no sign of control but the tab, no
// lost sign; the Markdown is of the subset of the faces (read and written back it is the same) and its words are not empty.
// The brackets ⟦⟧⟨⟩⟪⟫ are signs of the words of a book — every kind of file an import takes keeps them — and a draft makes
// them brackets when it takes the text of a chapter (textface.Marked), so they are carried as they are.
func validMarkdown(markdown string) bool {
	if !utf8.ValidString(markdown) || markdown == "" {
		return false
	}
	if strings.ContainsFunc(markdown, func(r rune) bool {
		return r == 0 || r != '\t' && unicode.IsControl(r) || r == utf8.RuneError
	}) {
		return false
	}
	return textface.Markdown(textface.FromMarkdown(markdown)) == markdown && PlainText(markdown) != ""
}

// chapterLocal checks a chapter by itself: its ref, place, title and paragraphs.
func chapterLocal(chapter Chapter) bool {
	if !uuid(chapter.Ref) || !uuid(chapter.Book) || !int32Range(chapter.Order, 1) ||
		chapter.PublishedOrder != nil && !int32Range(*chapter.PublishedOrder, 0) ||
		!name(chapter.Title, MaxChapterTitleRunes, true) || len(chapter.Paragraphs) == 0 {
		return false
	}
	ids := make(map[string]struct{}, len(chapter.Paragraphs))
	orders := make(map[int]struct{}, len(chapter.Paragraphs))
	runes := 0
	for _, paragraph := range chapter.Paragraphs {
		if !uuid(paragraph.ID) || !int32Range(paragraph.Order, 1) || !paragraphKind(paragraph.Kind) || !validMarkdown(paragraph.Markdown) {
			return false
		}
		if _, twice := ids[paragraph.ID]; twice {
			return false
		}
		if _, twice := orders[paragraph.Order]; twice {
			return false
		}
		ids[paragraph.ID], orders[paragraph.Order] = struct{}{}, struct{}{}
		if runes += utf8.RuneCountInString(paragraph.Markdown); runes > MaxChapterRunes {
			return false
		}
	}
	return true
}

func bookLocal(book Book) bool {
	if !uuid(book.Ref) || !int32Range(book.KeyNumber, 1) || book.KeyNumber > MaxKeyNumbers || !int32Range(book.Order, 1) ||
		!name(book.Title, MaxBookTitleRunes, true) {
		return false
	}
	// The number of a book has no limit of its own in the product: the line of its record bounds it.
	if book.Subtitle != nil && !name(*book.Subtitle, MaxBookSubtitleRunes, false) ||
		book.DisplayNumber != nil && !name(*book.DisplayNumber, MaxLineBytes, true) ||
		book.CoverColor != nil && !colorPattern.MatchString(*book.CoverColor) {
		return false
	}
	if cover := book.Cover; cover != nil {
		extension, known := coverExtensions[cover.MIME]
		if !known || !coverName.MatchString(cover.File) || !strings.HasSuffix(cover.File, "."+extension) || !digestOf(cover.SHA256) {
			return false
		}
	}
	return true
}

func sagaLocal(saga Saga) bool {
	return name(saga.Name, MaxSagaNameRunes, true) && name(saga.Description, MaxSagaDescriptionRunes, false) &&
		saga.BookKeyNumbers >= 0 && saga.BookKeyNumbers <= MaxKeyNumbers
}

// cardLocal checks a card by itself: a card is built from another text than its own or from none (the rule of the table of
// cards), and every text in it — a value or a name, escaped or not — is one the base keeps.
func cardLocal(card Card) bool {
	return uuid(card.Chapter) && digestOf(card.ContentHash) && card.Version >= 1 && card.Version <= math.MaxInt32 &&
		model(card.Model) && (card.BuiltFrom == nil || digestOf(*card.BuiltFrom) && *card.BuiltFrom != card.ContentHash) &&
		len(card.Card) > 1 && card.Card[0] == '{' && free(string(card.Card)) && freeJSON(card.Card)
}

// freeJSON says every string of a JSON value, decoded, is a free text: an escaped zero is a zero all the same.
func freeJSON(value []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return true
		}
		if err != nil {
			return false
		}
		if text, ok := token.(string); ok && !free(text) {
			return false
		}
	}
}

func namesLocal(names Names) bool {
	if !digestOf(names.Digest) || !model(names.Model) {
		return false
	}
	registry := names.Names
	for _, person := range registry.Persons {
		if !free(person.Name) || !free(person.Names...) || !free(person.Roles...) {
			return false
		}
	}
	for _, wrong := range registry.Wrong {
		if !free(wrong.Name, wrong.Alias) {
			return false
		}
	}
	return free(registry.Shared...)
}

func threadLocal(thread sagalayer.Thread) bool {
	return free(thread.Text, thread.Kind, thread.Weight, thread.ClosedHow)
}

func threadsLocal(threads Threads) bool {
	if !model(threads.Model) {
		return false
	}
	for _, stamp := range threads.Stamps {
		if !uuid(stamp.Chapter) || !digestOf(stamp.H) || stamp.K != nil && !digestOf(*stamp.K) || stamp.CardVersion < 0 || stamp.CardVersion > math.MaxInt32 {
			return false
		}
	}
	for _, book := range threads.Books {
		if !uuid(book.ID) || !digestOf(book.Digest) || book.Order < 0 {
			return false
		}
		for _, arc := range book.Arcs {
			if !free(arc.Title, arc.Gist, arc.Outcome) {
				return false
			}
		}
		for _, thread := range book.Threads {
			if !threadLocal(thread) {
				return false
			}
		}
	}
	for _, thread := range threads.Threads {
		if !threadLocal(thread) {
			return false
		}
	}
	return true
}

func mapHiddenLocal(item MapHidden) bool {
	return line(item.NodeKey, MaxMapKeyRunes, true) && line(item.OtherKey, MaxMapKeyRunes, false) && item.OtherKey != item.NodeKey &&
		line(item.NodeName, MaxMapKeyRunes, true) && line(item.OtherName, MaxMapKeyRunes, false) && stamped(item.CreatedAt)
}

// MaterialDraft is the draft of the domain a material of the file names.
func MaterialDraft(material Material) authormaterials.Draft {
	draft := authormaterials.Draft{
		Name: material.Name, Kind: authormaterials.Kind(material.Kind), Description: material.Description, Section: material.Section,
	}
	for _, property := range material.Properties {
		draft.Properties = append(draft.Properties, authormaterials.Property{Key: property.Key, Value: property.Value})
	}
	return draft
}

func materialLocal(material Material) bool {
	draft := MaterialDraft(material)
	clean, ok := authormaterials.CleanDraft(draft)
	if !ok || clean.Name != draft.Name || clean.Description != draft.Description || clean.Section != draft.Section ||
		len(clean.Properties) != len(draft.Properties) || sagamemory.GlossaryKey(clean.Name) == "" {
		return false
	}
	for index := range clean.Properties {
		if clean.Properties[index] != draft.Properties[index] {
			return false
		}
	}
	return line(material.SourceName, authormaterials.MaxSourceNameRunes, false) && materialSource(material.SourceKind) &&
		stamped(material.CreatedAt) && stamped(material.UpdatedAt)
}

func memoryLocal(record Memory) bool {
	if _, ok := authormemory.ParseKind(record.Kind); !ok {
		return false
	}
	if body, ok := authormemory.CleanText(record.Body); !ok || body != record.Body {
		return false
	}
	if name, ok := authormemory.CleanName(record.Name); !ok || name != record.Name {
		return false
	}
	if _, ok := authormemory.ParseCarried(record.Origin); !ok {
		return false
	}
	return (record.Chapter == "" || uuid(record.Chapter)) && stamped(record.CreatedAt) && stamped(record.UpdatedAt)
}

func noteLocal(note Note) bool {
	if notes.ValidateTitle(note.Title) != nil || notes.ValidateThought(note.Body, note.Source != nil) != nil ||
		!stamped(note.CreatedAt) || !stamped(note.UpdatedAt) {
		return false
	}
	source := note.Source
	if source == nil {
		return true
	}
	if notes.ValidateText(source.Quote) != nil {
		return false
	}
	switch source.Kind {
	case NoteSourceChat:
		return source.Chapter == "" && source.Paragraph == "" && source.Range == nil
	case NoteSourceChapter:
		if !uuid(source.Chapter) || source.Paragraph != "" && !uuid(source.Paragraph) {
			return false
		}
		if selected := source.Range; selected != nil {
			return source.Paragraph != "" && selected.StartOffset >= 0 && selected.EndOffset >= selected.StartOffset &&
				(selected.Quote == nil || free(*selected.Quote) && utf8.RuneCountInString(*selected.Quote) <= 500)
		}
		return true
	default:
		return false
	}
}

func chatEntryLocal(entry ChatEntry) bool {
	return chatName.MatchString(entry.File) && line(entry.Title, MaxChatTitleRunes, true) && stamped(entry.CreatedAt) &&
		!entry.LastTurnAt.Before(entry.CreatedAt) && entry.Turns >= 1 && entry.Turns <= MaxTranscriptTurns && int32Range(entry.OthersTurns, 0)
}

func transcriptLocal(transcript ChatTranscript) bool {
	if len(transcript.Turns) == 0 || len(transcript.Turns) > MaxTranscriptTurns {
		return false
	}
	for _, turn := range transcript.Turns {
		if !stamped(turn.AskedAt) || !free(turn.Question, turn.Answer) || strings.TrimSpace(turn.Question) == "" ||
			utf8.RuneCountInString(turn.Question) > MaxQuestionRunes || utf8.RuneCountInString(turn.Answer) > MaxAnswerRunes ||
			!outcome(turn.Outcome) || !line(turn.Attached, MaxAttachedRunes, false) {
			return false
		}
	}
	return true
}

func manifestLocal(manifest Manifest) bool {
	if !line(manifest.CreatedBy, MaxCreatedByRunes, true) || !stamped(manifest.CreatedAt) ||
		!name(manifest.Saga.Name, MaxSagaNameRunes, true) || manifest.Files == nil {
		return false
	}
	counts := manifest.Counts
	left := manifest.LeftOut
	for _, value := range []int{counts.Books, counts.Chapters, counts.Cards, counts.Materials, counts.Memory, counts.Notes,
		counts.Chats, left.Drafts, left.Drawings, left.TrashedBooks, left.TrashedChapters, left.OthersTurns} {
		if value < 0 {
			return false
		}
	}
	for _, title := range manifest.ChatsNotRead {
		if !line(title, MaxChatTitleRunes, true) {
			return false
		}
	}
	for _, section := range manifest.Sections {
		if !line(section, 80, true) {
			return false
		}
	}
	for name, sum := range manifest.Files {
		if name == EntryManifest || !knownName(name) || !digestOf(sum) {
			return false
		}
	}
	return true
}

// knownName is a record of the table or of the sections of the workshops.
func knownName(name string) bool {
	if _, fixed := fixedEntries[name]; fixed {
		return true
	}
	return coverName.MatchString(name) || chatName.MatchString(name) || workshopName.MatchString(name) && !strings.Contains(name, "..")
}

// paragraphKind is a kind of a paragraph of the book.
func paragraphKind(kind string) bool {
	switch kind {
	case "paragraph", "heading", "quote", "list_item", "scene_break":
		return true
	}
	return false
}

// materialSource is what a material came from: a file, a pasted text, the page, a vault.
func materialSource(kind string) bool {
	switch kind {
	case "xlsx", "docx", "text", "obsidian", "csv", "manual":
		return true
	}
	return false
}

// outcome is how a turn ended.
func outcome(value string) bool {
	switch value {
	case TurnAnswered, TurnInterrupted, TurnFailed, TurnInProgress:
		return true
	}
	return false
}

// ValidMarkdown says a paragraph of the book passes the checks of the text of a saga file (SF3.1).
func ValidMarkdown(markdown string) bool { return validMarkdown(markdown) }
