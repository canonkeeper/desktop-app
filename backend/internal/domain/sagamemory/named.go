package sagamemory

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// What a saga tool is asked about, for the author's words about it (specs/2026-11-11-chat-corrections.md CR1.2): the words
// of the author are about a hero, a place or a thing when the name they are about is one of their names by the glossary,
// which the name registry builds, or when one of those names stands in their text as whole words in any of their cases;
// for a search the words of its queries are named. The cases are those of the author's text on the map (namedText): the
// author's words are not the book's, so the stems of the book's vocabulary do not know them.

// minNamedWordRunes is the shortest word of a query that is named without being a name of the glossary: «кто», «где» and
// «она» bring nothing.
const minNamedWordRunes = 4

// Named is what one answer of a saga tool is asked about, as it was asked: names, or the texts of a search.
type Named struct {
	names []string
	texts []string
}

// NamedNames is the names asked for: «Варвара», «Белый порт».
func NamedNames(names ...string) Named { return Named{names: nonBlank(names)} }

// NamedWords is the texts of a search, each word of which is named.
func NamedWords(texts ...string) Named { return Named{texts: nonBlank(texts)} }

// And is what both name.
func (named Named) And(other Named) Named {
	return Named{names: append(slices.Clip(named.names), other.names...), texts: append(slices.Clip(named.texts), other.texts...)}
}

// Asked says something is asked about.
func (named Named) Asked() bool { return len(named.names) > 0 || len(named.texts) > 0 }

func nonBlank(values []string) []string {
	var kept []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			kept = append(kept, value)
		}
	}
	return kept
}

// Subject is what is named with every name of the entries the glossary knows it by.
type Subject struct {
	keys  map[string]bool
	names []string
	// asked is what was asked as written: the words of the author whose own name stands in it are about it.
	asked []namedText
}

// Subject resolves what is named through the glossary: a name the glossary knows brings every name and role of its entry,
// a name it does not know stands alone; a word of a search is named when it is long enough and no function word, or when
// the glossary knows it as a name. A nil glossary knows no name.
func (named Named) Subject(glossary *Glossary) Subject {
	subject := Subject{keys: map[string]bool{}}
	add := func(name string) {
		if key := glossaryKey(name); key != "" && !subject.keys[key] {
			subject.keys[key] = true
			subject.names = append(subject.names, key)
		}
	}
	known := func(name string) bool {
		if glossary == nil {
			return false
		}
		entry, ok := glossary.Lookup(name)
		if ok {
			for _, key := range entry.Keys() {
				add(key)
			}
		}
		return ok
	}
	for _, name := range named.names {
		add(name)
		known(name)
		subject.asked = append(subject.asked, newNamedText(name))
	}
	for _, text := range named.texts {
		known(text)
		for _, word := range strings.FieldsFunc(Normalize(text), func(char rune) bool { return !unicode.IsLetter(char) && !unicode.IsDigit(char) }) {
			if known(word) || utf8.RuneCountInString(word) >= minNamedWordRunes && !slices.Contains(FunctionWords, word) {
				add(word)
			}
		}
		subject.asked = append(subject.asked, newNamedText(text))
	}
	return subject
}

// Empty says nothing is named.
func (subject Subject) Empty() bool { return len(subject.names) == 0 }

// About says whether the words of the author — the name they are about, empty without one, and their text — are about the
// subject: the name is one of its names, one of its names stands in the name or the text, or the name stands in what was
// asked.
func (subject Subject) About(name, text string) bool {
	if subject.Empty() {
		return false
	}
	if name = strings.TrimSpace(name); name != "" {
		if subject.keys[glossaryKey(name)] {
			return true
		}
		for _, asked := range subject.asked {
			if asked.hasName(name) {
				return true
			}
		}
	}
	own, said := newNamedText(name), newNamedText(text)
	for _, named := range subject.names {
		if own.hasName(named) || said.hasName(named) {
			return true
		}
	}
	return false
}
