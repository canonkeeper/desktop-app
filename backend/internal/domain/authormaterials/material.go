// Package authormaterials holds what an author worked out apart from his saga: skills, places, characters and the
// like from his Excel and Word files (specs/2026-11-04-saga-map.md, UC9). Reading a file is deterministic here;
// a model only maps the columns of a table and the paragraphs of a text, and never makes a name up.
package authormaterials

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid    = errors.New("author materials: invalid")
	ErrNotFound   = errors.New("author materials: not found")
	ErrForbidden  = errors.New("author materials: forbidden")
	ErrConflict   = errors.New("author materials: conflict")
	ErrImportBusy = errors.New("author materials: an import is running")
	// ErrUnreadable is a file that is not the workbook or the document it claims to be.
	ErrUnreadable = errors.New("author materials: unreadable file")
	// ErrEmpty is a file with nothing to read.
	ErrEmpty = errors.New("author materials: empty file")
	// ErrTooLarge is a file over the limits of an import.
	ErrTooLarge = errors.New("author materials: file too large")
)

// Kind is what a record describes.
type Kind string

const (
	KindSkill     Kind = "skill"
	KindPlace     Kind = "place"
	KindCharacter Kind = "character"
	KindItem      Kind = "item"
	KindFaction   Kind = "faction"
	KindOther     Kind = "other"
)

// ParseKind reads a kind of the API or the model.
func ParseKind(value string) (Kind, bool) {
	switch kind := Kind(value); kind {
	case KindSkill, KindPlace, KindCharacter, KindItem, KindFaction, KindOther:
		return kind, true
	default:
		return "", false
	}
}

// SourceKind is what a record came from.
type SourceKind string

const (
	SourceXLSX SourceKind = "xlsx"
	SourceDOCX SourceKind = "docx"
	SourceText SourceKind = "text"
)

// The limits of a record (R9.1), the same as the checks of its table.
const (
	MaxNameRunes        = 200
	MaxDescriptionRunes = 8000
	MaxProperties       = 50
	MaxKeyRunes         = 80
	MaxValueRunes       = 2000
	MaxSectionRunes     = 200
	MaxSourceNameRunes  = 255
	// MaxQueryRunes bounds a search of the list.
	MaxQueryRunes = 200
	// MaxFileBytes is the largest file of an import (R9.2).
	MaxFileBytes = 10 << 20
	// MaxTextRunes is the largest pasted text of an import.
	MaxTextRunes = 200_000
)

// Property is one «key — value» of a record, in the order of the file.
type Property struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Draft is what a record says.
type Draft struct {
	Name        string     `json:"name"`
	Kind        Kind       `json:"kind"`
	Description string     `json:"description,omitempty"`
	Properties  []Property `json:"properties,omitempty"`
	Section     string     `json:"section,omitempty"`
}

func cleanLine(value string, limit int) (string, bool) {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > limit || !utf8.ValidString(value) {
		return "", false
	}
	for _, char := range value {
		if unicode.IsControl(char) && char != '\n' && char != '\t' {
			return "", false
		}
	}
	return value, true
}

// CleanDraft trims a record and refuses what its table would not hold.
func CleanDraft(draft Draft) (Draft, bool) {
	var ok bool
	if draft.Name, ok = cleanLine(draft.Name, MaxNameRunes); !ok || draft.Name == "" || strings.Contains(draft.Name, "\n") {
		return Draft{}, false
	}
	if _, ok := ParseKind(string(draft.Kind)); !ok {
		return Draft{}, false
	}
	if draft.Description, ok = cleanLine(draft.Description, MaxDescriptionRunes); !ok {
		return Draft{}, false
	}
	if draft.Section, ok = cleanLine(draft.Section, MaxSectionRunes); !ok {
		return Draft{}, false
	}
	if len(draft.Properties) > MaxProperties {
		return Draft{}, false
	}
	properties := make([]Property, len(draft.Properties))
	for index, property := range draft.Properties {
		if properties[index].Key, ok = cleanLine(property.Key, MaxKeyRunes); !ok || properties[index].Key == "" {
			return Draft{}, false
		}
		if properties[index].Value, ok = cleanLine(property.Value, MaxValueRunes); !ok {
			return Draft{}, false
		}
	}
	draft.Properties = properties
	return draft, true
}

// CleanQuery trims a search of the list.
func CleanQuery(query string) (string, bool) { return cleanLine(query, MaxQueryRunes) }

// CleanSourceName is the name of a file as a record keeps it: without a path, cut to the limit.
func CleanSourceName(name string) string {
	if index := strings.LastIndexAny(name, `/\`); index >= 0 {
		name = name[index+1:]
	}
	var kept strings.Builder
	for _, char := range strings.ToValidUTF8(strings.TrimSpace(name), "") {
		if !unicode.IsControl(char) {
			kept.WriteRune(char)
		}
	}
	return cut(kept.String(), MaxSourceNameRunes)
}

func cut(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return strings.TrimSpace(string([]rune(value)[:limit]))
}

// Record is a kept record of a project.
type Record struct {
	Draft
	ID         string
	ProjectID  string
	SourceName string
	SourceKind SourceKind
	Revision   int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Filter selects a page of the list; empty Kinds and Query select everything.
type Filter struct {
	Kinds    []Kind
	Query    string
	AfterKey string
	Limit    int
}

// Page is a page of the list by name.
type Page struct {
	Records []Record
	Total   int64
	NextKey string
}

// Import is what the author has not answered yet: JobStatus says how far its Job went, Candidates is nil until it ends.
type Import struct {
	ID         string
	JobID      string
	JobStatus  string
	SourceName string
	SourceKind SourceKind
	Candidates []Draft
	CreatedAt  time.Time
}

// Choice is one candidate the author keeps: its place in the list and, where he changed it, what he wrote.
type Choice struct {
	Index       int
	Name        *string
	Kind        *Kind
	Description *string
	Properties  *[]Property
}

// Chosen is what an answer keeps: the candidates by their place with the author's changes on them, one record per name.
func Chosen(candidates []Draft, choices []Choice) ([]Draft, error) {
	seen := make(map[int]bool, len(choices))
	drafts := make([]Draft, 0, len(choices))
	for _, choice := range choices {
		if choice.Index < 0 || choice.Index >= len(candidates) || seen[choice.Index] {
			return nil, ErrInvalid
		}
		seen[choice.Index] = true
		draft := candidates[choice.Index]
		if choice.Name != nil {
			draft.Name = *choice.Name
		}
		if choice.Kind != nil {
			draft.Kind = *choice.Kind
		}
		if choice.Description != nil {
			draft.Description = *choice.Description
		}
		if choice.Properties != nil {
			draft.Properties = *choice.Properties
		}
		clean, ok := CleanDraft(draft)
		if !ok {
			return nil, ErrInvalid
		}
		drafts = append(drafts, clean)
	}
	return drafts, nil
}
