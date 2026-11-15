// SPDX-License-Identifier: AGPL-3.0-only

// Package disclosure is what of the saga a sending gives a helper (specs/2026-11-14-desk-journal.md JR1.3, JR1.4): the
// chapters by their id with the paragraphs or the precision of what went, the attachments of the chat, the counts of the
// records of the author, the question and the correction — never a word of the text, a title or a name. Whoever gives the
// text names it: a tool of the chat in the row of its thread, a call of the core in its context, which the client of the app
// carries to the gateway in Header. The value has no owner of its storage: the journal of the app keeps it.
package disclosure

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
)

// Header carries the value of a call of the core to the local gateway of the app; the gateway takes it off before the
// helper. The client of the site sends no such header.
const Header = "X-CanonKeeper-Disclosed"

// Precision is how much of a chapter went: its title, what the helper learned of it from its card, excerpts of its
// paragraphs, paragraphs whole, its beginning, a page of an answer of several pages a program of the author read
// (specs/2026-11-15-desk-mcp.md MC4.2), or the whole chapter. A later precision is more of the text.
type Precision string

const (
	Titles     Precision = "titles"
	Cards      Precision = "cards"
	Excerpts   Precision = "excerpts"
	Paragraphs Precision = "paragraphs"
	Beginning  Precision = "beginning"
	Pages      Precision = "pages"
	Whole      Precision = "whole"
)

// precisions are the precisions from the least of the text to the most: a place given whole by an answer of one page and by
// pages of another went whole; given by pages and by paragraphs, it went by pages.
var precisions = []Precision{Titles, Cards, Excerpts, Paragraphs, Beginning, Pages, Whole}

func (precision Precision) rank() int { return slices.Index(precisions, precision) }

// Range is the paragraphs From..To of a chapter or an attachment, from one.
type Range struct {
	From int `json:"f"`
	To   int `json:"t"`
}

// Chapter is a chapter that went: its id, the precision and, for paragraphs, which.
type Chapter struct {
	ID         string    `json:"id"`
	Precision  Precision `json:"p"`
	Paragraphs []Range   `json:"r,omitempty"`
}

// Attachment is the chapter attached to a question of a chat, its paragraphs shown.
type Attachment struct {
	Chat       string  `json:"chat"`
	Number     int     `json:"n"`
	Paragraphs []Range `json:"r"`
}

// Named is what a sending gives that went with none before it. Memory and Materials are the ids of the records of the
// author shown; Names is how many names the list of the saga had; Sections are the numbers of the sections of the file or
// text of the author's materials, from one. Protocol marks the probe of the protocol of an address: nothing of the author.
// Sagas is how many titles of sagas a program of the author read (specs/2026-11-15-desk-mcp.md MC4.2).
type Named struct {
	Question    bool         `json:"question,omitempty"`
	Correction  bool         `json:"correction,omitempty"`
	Chapters    []Chapter    `json:"chapters,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Threads     bool         `json:"threads,omitempty"`
	Memory      []string     `json:"memory,omitempty"`
	Materials   []string     `json:"materials,omitempty"`
	Names       int          `json:"names,omitempty"`
	Sections    []int        `json:"sections,omitempty"`
	Protocol    bool         `json:"protocol,omitempty"`
	Sagas       int          `json:"sagas,omitempty"`
}

// Empty says nothing of the author is named.
func (named Named) Empty() bool {
	return !named.Question && !named.Correction && len(named.Chapters) == 0 && len(named.Attachments) == 0 && !named.Threads &&
		len(named.Memory) == 0 && len(named.Materials) == 0 && named.Names == 0 && len(named.Sections) == 0 && named.Sagas == 0
}

// InPages is what an answer of several pages named: the text of every chapter — excerpts, paragraphs, the beginning, the whole
// — at the precision Pages, since which paragraphs went on a page the door that cut the answer does not know; the titles and
// the cards of chapters keep their own precision (specs/2026-11-15-desk-mcp.md MC4.2).
func (named Named) InPages() Named {
	paged := Merge(named)
	for index, chapter := range paged.Chapters {
		if chapter.Precision == Titles || chapter.Precision == Cards {
			continue
		}
		paged.Chapters[index].Precision, paged.Chapters[index].Paragraphs = Pages, nil
	}
	return paged
}

// Chapter adds a chapter with its precision; paragraphs from..to, when the precision is Paragraphs.
func (named *Named) Chapter(id string, precision Precision, from, to int) {
	chapter := Chapter{ID: id, Precision: precision}
	if precision == Paragraphs {
		chapter.Paragraphs = []Range{{From: from, To: to}}
	}
	*named = Merge(*named, Named{Chapters: []Chapter{chapter}})
}

// Merge is what the namings give together, in one pass over them: a place once, at the larger precision with the
// paragraphs of every naming at that precision; a record once; the largest list of names.
func Merge(all ...Named) Named {
	var merged Named
	chapters := map[string]int{}
	type attachmentKey struct {
		chat   string
		number int
	}
	attachments := map[attachmentKey]int{}
	memory, materials, sections := map[string]bool{}, map[string]bool{}, map[int]bool{}
	for _, named := range all {
		merged.Question = merged.Question || named.Question
		merged.Correction = merged.Correction || named.Correction
		merged.Threads = merged.Threads || named.Threads
		merged.Protocol = merged.Protocol || named.Protocol
		merged.Names = max(merged.Names, named.Names)
		merged.Sagas = max(merged.Sagas, named.Sagas)
		merged.Memory = appendNew(merged.Memory, memory, named.Memory)
		merged.Materials = appendNew(merged.Materials, materials, named.Materials)
		merged.Sections = appendNew(merged.Sections, sections, named.Sections)
		for _, chapter := range named.Chapters {
			at, known := chapters[chapter.ID]
			if !known {
				chapters[chapter.ID] = len(merged.Chapters)
				chapter.Paragraphs = slices.Clone(chapter.Paragraphs)
				merged.Chapters = append(merged.Chapters, chapter)
				continue
			}
			current := &merged.Chapters[at]
			switch {
			case chapter.Precision.rank() > current.Precision.rank():
				current.Precision, current.Paragraphs = chapter.Precision, slices.Clone(chapter.Paragraphs)
			case chapter.Precision == current.Precision:
				current.Paragraphs = append(current.Paragraphs, chapter.Paragraphs...)
			}
		}
		for _, attachment := range named.Attachments {
			key := attachmentKey{attachment.Chat, attachment.Number}
			if at, known := attachments[key]; known {
				merged.Attachments[at].Paragraphs = append(merged.Attachments[at].Paragraphs, attachment.Paragraphs...)
				continue
			}
			attachments[key] = len(merged.Attachments)
			attachment.Paragraphs = slices.Clone(attachment.Paragraphs)
			merged.Attachments = append(merged.Attachments, attachment)
		}
	}
	for index := range merged.Chapters {
		merged.Chapters[index].Paragraphs = joined(merged.Chapters[index].Paragraphs)
	}
	for index := range merged.Attachments {
		merged.Attachments[index].Paragraphs = joined(merged.Attachments[index].Paragraphs)
	}
	return merged
}

// appendNew appends the values not seen yet, in their order.
func appendNew[T comparable](list []T, seen map[T]bool, values []T) []T {
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			list = append(list, value)
		}
	}
	return list
}

// joined are the ranges in order, the touching and the overlapping ones made one.
func joined(all []Range) []Range {
	if len(all) == 0 {
		return nil
	}
	slices.SortFunc(all, func(x, y Range) int { return x.From - y.From })
	out := []Range{all[0]}
	for _, next := range all[1:] {
		last := &out[len(out)-1]
		if next.From <= last.To+1 {
			last.To = max(last.To, next.To)
			continue
		}
		out = append(out, next)
	}
	return out
}

// The bounds of a value the gateway takes: a book of the layer names hundreds of chapters, the header of the gateway takes
// a mebibyte.
const (
	maxChapters = 20000
	maxRanges   = 2000
	maxRecords  = 1000
)

var identifier = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ErrShape is a value of a form the journal does not keep.
var ErrShape = errors.New("disclosure: the value is not of its form")

// Valid says the value is of the form the journal keeps: ids of the database, known precisions, paragraphs only with
// Paragraphs and in order, counts in their bounds.
func (named Named) Valid() error {
	if len(named.Chapters) > maxChapters || len(named.Attachments) > maxRecords || len(named.Memory) > maxRecords ||
		len(named.Materials) > maxRecords || len(named.Sections) > maxRecords || named.Names < 0 || named.Sagas < 0 {
		return ErrShape
	}
	for _, chapter := range named.Chapters {
		if !identifier.MatchString(chapter.ID) || chapter.Precision.rank() < 0 ||
			(chapter.Precision == Paragraphs) != (len(chapter.Paragraphs) > 0) || !validRanges(chapter.Paragraphs) {
			return ErrShape
		}
	}
	for _, attachment := range named.Attachments {
		if !identifier.MatchString(attachment.Chat) || attachment.Number < 1 || len(attachment.Paragraphs) == 0 || !validRanges(attachment.Paragraphs) {
			return ErrShape
		}
	}
	for _, id := range append(append([]string(nil), named.Memory...), named.Materials...) {
		if !identifier.MatchString(id) {
			return ErrShape
		}
	}
	for _, section := range named.Sections {
		if section < 1 {
			return ErrShape
		}
	}
	return nil
}

func validRanges(ranges []Range) bool {
	if len(ranges) > maxRanges {
		return false
	}
	for _, paragraphs := range ranges {
		if paragraphs.From < 1 || paragraphs.To < paragraphs.From {
			return false
		}
	}
	return true
}

// Encode is the value in one line of ASCII, the form of Header and of the journal.
func (named Named) Encode() (string, error) {
	if err := named.Valid(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(named)
	if err != nil {
		return "", err
	}
	for _, letter := range encoded {
		if letter < ' ' || letter > '~' {
			return "", ErrShape
		}
	}
	return string(encoded), nil
}

// Parse reads a value of Header: one line of ASCII, the fields of the form and nothing else, valid.
func Parse(header string) (Named, error) {
	for _, letter := range []byte(header) {
		if letter < ' ' || letter > '~' {
			return Named{}, ErrShape
		}
	}
	decoder := json.NewDecoder(strings.NewReader(header))
	decoder.DisallowUnknownFields()
	var named Named
	if err := decoder.Decode(&named); err != nil || decoder.More() {
		return Named{}, ErrShape
	}
	if err := named.Valid(); err != nil {
		return Named{}, err
	}
	return named, nil
}

type contextKey struct{}

// With is ctx carrying what the call made under it gives; Of reads it.
func With(ctx context.Context, named Named) context.Context {
	return context.WithValue(ctx, contextKey{}, named)
}

func Of(ctx context.Context) (Named, bool) {
	named, ok := ctx.Value(contextKey{}).(Named)
	return named, ok
}

// The codes of the refusals of the journal (JR1.1, JR1.4): the record of a sending did not lie in the journal — or the
// journal is not there —, or a call of the core named nothing it gives.
const (
	CodeUnavailable = "journal.unavailable"
	CodeUnnamed     = "journal.unnamed"
)

// ErrJournal is a request the gateway did not send for its journal (JR1.6): no pause of the reading — the consent of the
// author has nothing to do with it —, a failure of the step the Job repeats.
var ErrJournal = errors.New("disclosure: the journal of the sendings refused the request")

// JournalError is the refusal of the journal the client of the app hands its caller, with its code.
type JournalError struct{ Code string }

func (refused *JournalError) Error() string { return ErrJournal.Error() + ": " + refused.Code }

func (refused *JournalError) Is(target error) bool { return target == ErrJournal }

// SafeErrorCode is the code a Job ends with when the journal refused its request.
func (refused *JournalError) SafeErrorCode() string { return refused.Code }

// CodeOf is the code of a refusal of the journal of an error; "" when it is none.
func CodeOf(err error) string {
	var refused *JournalError
	if errors.As(err, &refused) {
		return refused.Code
	}
	return ""
}
