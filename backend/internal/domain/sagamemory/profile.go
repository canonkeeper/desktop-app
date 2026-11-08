package sagamemory

import (
	"sort"
	"strings"
)

// The saga layer over chapter cards (specs/2026-10-29-chapter-cards-quality.md, UC4): the profile
// of a glossary entry gathers the facts the v3 cards state under any of its names.

// singleValueAttributes are the attributes whose later value replaces the earlier one, in the
// order a profile shows them; the vocabulary is the shared one of the card instructions.
var singleValueAttributes = []string{
	"возраст", "раса", "происхождение", "род занятий", "класс", "уровень", "ранг", "титул", "должность",
	"звание", "статус", "местонахождение", "здоровье",
}

// ChapterFact is a fact of the card of the chapter with that reading-order index.
type ChapterFact struct {
	Chapter int
	Fact    Fact
}

// ProfileValue is one value of an attribute: the value as last stated, the chapter (and
// paragraph) it was first stated in for this run, and the last chapter stating it.
type ProfileValue struct {
	Value   string
	Chapter int
	P       int
	Last    int
	LastP   int
	Past    bool
	Count   int
	order   int
}

// ProfileAttribute is one attribute of a profile. A single-valued one has its current value (the
// latest not about the past) and the history of its changes in story order; another one has its
// distinct values, the most recently stated first.
type ProfileAttribute struct {
	Name    string
	Single  bool
	Facts   int
	Current *ProfileValue
	History []ProfileValue
}

// Profile is what the cards state about a glossary entry.
type Profile struct {
	Entry      *GlossaryEntry
	Facts      int
	Chapters   int
	Attributes []ProfileAttribute
}

// Keys returns the glossary keys of the entry's name, aliases and roles: everything facts are stated under.
func (entry *GlossaryEntry) Keys() []string {
	seen := map[string]bool{}
	var keys []string
	for _, name := range append(append([]string{entry.Name}, entry.Aliases...), entry.Roles...) {
		if key := glossaryKey(name); key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
}

// BuildProfile gathers the facts whose subject is a name of the entry; facts come in story order
// (chapter, then the card's order).
func BuildProfile(entry *GlossaryEntry, facts []ChapterFact) Profile {
	names := map[string]bool{}
	for _, key := range entry.Keys() {
		names[key] = true
	}
	single := map[string]int{}
	for i, name := range singleValueAttributes {
		single[name] = i
	}
	profile := Profile{Entry: entry}
	byName := map[string]*ProfileAttribute{}
	groups := map[string]map[string]*ProfileValue{}
	chapters := map[int]bool{}
	current := map[string]int{} // 1 + the index of the current value in the history
	var order []string
	for i, item := range facts {
		if !names[glossaryKey(item.Fact.Subject)] {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(item.Fact.Attribute))
		attribute := byName[name]
		if attribute == nil {
			_, isSingle := single[name]
			attribute = &ProfileAttribute{Name: name, Single: isSingle}
			byName[name] = attribute
			groups[name] = map[string]*ProfileValue{}
			order = append(order, name)
		}
		profile.Facts++
		attribute.Facts++
		chapters[item.Chapter] = true
		fact := item.Fact
		if attribute.Single {
			last := len(attribute.History) - 1
			if last >= 0 && Normalize(attribute.History[last].Value) == Normalize(fact.Value) && attribute.History[last].Past == fact.Past {
				attribute.History[last].Last, attribute.History[last].LastP = item.Chapter, fact.P
				attribute.History[last].Count++
			} else {
				attribute.History = append(attribute.History, ProfileValue{
					Value: fact.Value, Chapter: item.Chapter, P: fact.P, Last: item.Chapter, LastP: fact.P, Past: fact.Past, Count: 1, order: i,
				})
			}
			if !fact.Past {
				current[name] = len(attribute.History)
			}
			continue
		}
		key := valueKey(fact.Value)
		value := groups[name][key]
		if value == nil {
			value = &ProfileValue{Chapter: item.Chapter, P: fact.P, Past: true}
			groups[name][key] = value
		}
		value.Count++
		if fact.Past && !value.Past {
			continue // a recollection does not replace what the story states at its time
		}
		value.Value, value.Last, value.LastP, value.Past, value.order = fact.Value, item.Chapter, fact.P, fact.Past, i
	}
	profile.Chapters = len(chapters)
	for _, name := range order {
		attribute := byName[name]
		if attribute.Single {
			if at := current[name]; at > 0 {
				value := attribute.History[at-1]
				attribute.Current = &value
			}
			continue
		}
		for _, value := range groups[name] {
			attribute.History = append(attribute.History, *value)
		}
		sort.Slice(attribute.History, func(i, j int) bool { return attribute.History[i].order > attribute.History[j].order })
	}
	for _, name := range order {
		profile.Attributes = append(profile.Attributes, *byName[name])
	}
	sort.SliceStable(profile.Attributes, func(i, j int) bool {
		a, b := profile.Attributes[i], profile.Attributes[j]
		if a.Single != b.Single {
			return a.Single
		}
		if a.Single {
			return single[a.Name] < single[b.Name]
		}
		return a.Facts > b.Facts
	})
	return profile
}

// valueKey groups the values of a many-valued attribute by what they name: «Взлом, ученик» and
// «Взлом (подмастерье)» are one skill, «Дарина: жена» is the relation with Дарина.
func valueKey(value string) string {
	value = Normalize(value)
	if cut := strings.IndexAny(value, "(:,—–"); cut > 0 {
		value = value[:cut]
	}
	return strings.Join(strings.Fields(strings.Trim(value, " .«»\"'")), " ")
}
