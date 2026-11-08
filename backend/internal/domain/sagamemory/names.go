package sagamemory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The registry of names (specs/2026-11-01-index-foundation.md, UC5): one model call over the list of
// all names of a saga says who is who under which names, which the cards of single chapters cannot.

// NameInstructionsVersion is part of the digest: new instructions rebuild every registry.
const NameInstructionsVersion = 1

// NameInstructions are the instructions of the registry call, as measured on the saga.
const NameInstructions = `Дан список имён из карточек глав одной книги или книжной саги. Каждая строка: имя в нормализованном виде (строчными, ё→е, без ударений), тип, если это не персонаж, сколько глав и каких по порядку, сколько фактов, другие имена, которые ему дали карточки, и заметки разных глав. Заметки и «другие имена» написаны моделью по одной главе и могут ошибаться.

Собери реестр персонажей: кто в этой саге под какими именами. Персонаж — герой или существо с личностью.

1. persons — один элемент на персонажа, у которого в списке два и более имён. Проверь каждое из первых двухсот имён списка, главные персонажи должны быть собраны полностью.
 - name — главное имя, каким его чаще всего зовут.
 - names — настоящие имена: имя, фамилия, полное имя, прозвище, ник в игре, псевдоним, другое написание, опечатка.
 - roles — описательные обозначения вместо имени, если в этой саге они означают именно его и никого другого: «сестра героя», «капитан гвардейцев», «девушка с мечом».
 Включай только имена из списка, дословно, в нормализованном виде. Разных персонажей с похожими именами не объединяй. Роль персонажа, которую в книге на время играет другой (подмена, притворство), не делает их одним персонажем. Если не уверен — не объединяй.
2. shared — только имена, которые в тексте действительно обозначают разных персонажей: титулы и должности («император», «королева»), описания, тёзки. Для каждого — кто именно. Имя, которое подавляющее большинство глав относит к одному персонажу, не бывает shared, даже если похоже на животное, предмет или титул или один раз обозначило другого: такое имя относится к этому персонажу.
3. wrong — ошибки поля «другие имена»: пара имён, где alias на самом деле принадлежит другому персонажу, а не name.

Только JSON: {"persons":[{"name":"","names":[""],"roles":[""]}],"shared":[{"name":"","who":[""]}],"wrong":[{"name":"","alias":""}]}
`

// NameRegistry says who is who in one saga; every name is a glossary key.
type NameRegistry struct {
	Persons []NamePerson `json:"persons"`
	Shared  []string     `json:"shared"` // names of different characters: never merged
	Wrong   []WrongAlias `json:"wrong"`  // aliases the cards gave by mistake
}

// NamePerson is one character: the main name, the other real names and the roles, which mean only
// this character but are no names.
type NamePerson struct {
	Name  string   `json:"name"`
	Names []string `json:"names,omitempty"`
	Roles []string `json:"roles,omitempty"`
}

// WrongAlias is an alias the cards gave to a name it does not belong to.
type WrongAlias struct {
	Name  string `json:"name"`
	Alias string `json:"alias"`
}

// ParseNameRegistry extracts the registry JSON from model output and normalizes it: names become
// glossary keys, blank and repeated ones are dropped. An answer without persons is rejected; a
// registry with an empty list of them is a book where nobody has two names.
func ParseNameRegistry(output string) (NameRegistry, error) {
	start, end := strings.Index(output, "{"), strings.LastIndex(output, "}")
	if start < 0 || end <= start {
		return NameRegistry{}, fmt.Errorf("%w: no JSON object", ErrModelOutput)
	}
	var raw struct {
		Persons *[]NamePerson     `json:"persons"`
		Shared  []json.RawMessage `json:"shared"`
		Wrong   []WrongAlias      `json:"wrong"`
	}
	if err := json.Unmarshal([]byte(output[start:end+1]), &raw); err != nil {
		return NameRegistry{}, fmt.Errorf("%w: %v", ErrModelOutput, err)
	}
	if raw.Persons == nil {
		return NameRegistry{}, fmt.Errorf("%w: no persons", ErrModelOutput)
	}
	registry := NameRegistry{Persons: make([]NamePerson, 0, len(*raw.Persons))}
	for _, person := range *raw.Persons {
		names := nameKeys(append([]string{person.Name}, person.Names...))
		if len(names) == 0 {
			continue
		}
		// The first name is the main one when the model gave none.
		roles := nameKeys(append(append([]string{}, names...), person.Roles...))[len(names):]
		registry.Persons = append(registry.Persons, NamePerson{
			Name: names[0], Names: append([]string(nil), names[1:]...), Roles: append([]string(nil), roles...),
		})
	}
	var shared []string
	for _, item := range raw.Shared {
		var named struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(item, &named) != nil {
			_ = json.Unmarshal(item, &named.Name)
		}
		shared = append(shared, named.Name)
	}
	registry.Shared = nameKeys(shared)
	for _, pair := range raw.Wrong {
		name, alias := glossaryKey(pair.Name), glossaryKey(pair.Alias)
		if name != "" && alias != "" && name != alias {
			registry.Wrong = append(registry.Wrong, WrongAlias{Name: name, Alias: alias})
		}
	}
	return registry, nil
}

// nameKeys returns the glossary keys of the names without blanks and repeats, in their order.
func nameKeys(names []string) []string {
	seen := make(map[string]bool, len(names))
	var keys []string
	for _, name := range names {
		if key := glossaryKey(bounded(name, maxCardTextRunes)); key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
}

const (
	// maxNameLines bounds the model input: the measured saga of 644 chapters gives 2274 lines and
	// ≈ 150 thousand tokens, so a saga at the bound stays under ≈ 270 thousand. Only the rarest
	// names fall out, and they follow the rules of the glossary without a registry.
	maxNameLines = 4000
	// nameFactsAlone is how many facts bring a name into the list without a character or knowledge.
	nameFactsAlone = 20
	nameListNotes  = 110 // runes of one note
	nameListOthers = 6   // most frequent other names of one line
)

// nameLine is what the cards say about one name key.
type nameLine struct {
	key            string
	chapters       int
	first, last    int
	facts, knows   int
	character      bool
	kinds, aliases map[string]int
	notes          []string
}

// BuildNameList renders the input of the registry call: one line per name that is a character of
// a card, knows something or has many facts, the heaviest first. The digest covers only the names
// and their other names, with the instructions version: an edit that brings no new name does not
// call the model (R5.2).
func BuildNameList(cards map[int]*Card) (list, digest string) {
	lines := map[string]*nameLine{}
	line := func(name string) *nameLine {
		key := glossaryKey(name)
		if key == "" {
			return nil
		}
		item := lines[key]
		if item == nil {
			item = &nameLine{key: key, kinds: map[string]int{}, aliases: map[string]int{}}
			lines[key] = item
		}
		return item
	}
	addMention := func(chapter int, mention Mention, character bool) {
		item := line(mention.Name)
		if item == nil {
			return
		}
		if item.chapters == 0 {
			item.first = chapter
		}
		if item.chapters == 0 || item.last != chapter {
			item.chapters++
			item.last = chapter
		}
		if character {
			item.character = true
		} else if kind := strings.ToLower(strings.TrimSpace(mention.Type)); kind != "" {
			item.kinds[kind]++
		}
		for _, alias := range mention.Aliases {
			if key := glossaryKey(alias); key != "" && key != item.key {
				item.aliases[strings.TrimSpace(alias)]++
			}
		}
		if note := strings.TrimSpace(mention.Note); note != "" {
			item.notes = append(item.notes, note)
		}
	}
	chapters := make([]int, 0, len(cards))
	for chapter := range cards {
		chapters = append(chapters, chapter)
	}
	sort.Ints(chapters)
	for _, chapter := range chapters {
		card := cards[chapter]
		for _, item := range card.Characters {
			addMention(chapter, item, true)
		}
		for _, item := range card.Entities {
			addMention(chapter, item, false)
		}
		for _, fact := range card.Facts {
			if item := line(fact.Subject); item != nil {
				item.facts++
			}
		}
		for _, knowledge := range card.Knows {
			if item := line(knowledge.Who); item != nil {
				item.knows++
			}
		}
	}
	picked := make([]*nameLine, 0, len(lines))
	for _, item := range lines {
		if item.character || item.knows >= 1 || item.facts >= nameFactsAlone {
			picked = append(picked, item)
		}
	}
	sort.Slice(picked, func(i, j int) bool {
		a, b := picked[i], picked[j]
		if weightA, weightB := a.facts+a.knows+a.chapters, b.facts+b.knows+b.chapters; weightA != weightB {
			return weightA > weightB
		}
		return a.key < b.key
	})
	if len(picked) > maxNameLines {
		picked = picked[:maxNameLines]
	}
	rendered := make([]string, len(picked))
	for i, item := range picked {
		rendered[i] = item.render()
	}
	return strings.Join(rendered, "\n"), nameDigest(picked)
}

// render writes one line of the list as the lab measured it.
func (item *nameLine) render() string {
	kind := ""
	if !item.character {
		kind = " тип:нет"
		if best := mostFrequent(item.kinds); best != "" {
			kind = " тип:" + best
		}
	}
	first, last := "-", "-"
	if item.chapters > 0 {
		first, last = strconv.Itoa(item.first), strconv.Itoa(item.last)
	}
	others := byFrequency(item.aliases)
	if len(others) > nameListOthers {
		others = others[:nameListOthers]
	}
	return fmt.Sprintf("%s%s | глав %d (%s–%s), фактов %d | также: %s | %s",
		item.key, kind, item.chapters, first, last, item.facts, strings.Join(others, ", "), strings.Join(item.shownNotes(), " / "))
}

// shownNotes picks the first, the middle and the last note, each cut.
func (item *nameLine) shownNotes() []string {
	count := len(item.notes)
	var notes []string
	if count > 0 {
		notes = append(notes, item.notes[0])
	}
	if count > 2 {
		notes = append(notes, item.notes[count/2])
	}
	if count > 1 {
		notes = append(notes, item.notes[count-1])
	}
	for i, note := range notes {
		if runes := []rune(note); len(runes) > nameListNotes {
			notes[i] = string(runes[:nameListNotes])
		}
	}
	return notes
}

// nameDigest hashes the sorted names with their sorted other names and the instructions version.
func nameDigest(lines []*nameLine) string {
	entries := make([]string, len(lines))
	for i, item := range lines {
		aliases := make([]string, 0, len(item.aliases))
		for alias := range item.aliases {
			aliases = append(aliases, alias)
		}
		keys := nameKeys(aliases)
		sort.Strings(keys)
		entries[i] = item.key + "\x00" + strings.Join(keys, "\x00")
	}
	sort.Strings(entries)
	hash := sha256.New()
	fmt.Fprintf(hash, "%d\n", NameInstructionsVersion)
	for _, entry := range entries {
		fmt.Fprintln(hash, entry)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// byFrequency lists the keys of the counts, most frequent first, then in order.
func byFrequency(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}

func mostFrequent(counts map[string]int) string {
	if keys := byFrequency(counts); len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// nameIndex is a registry as the glossary reads it; without a registry it is empty, so the
// glossary has one path. A name belongs to one person at most.
type nameIndex struct {
	persons []registeredPerson
	of      map[string]int // the person of every name and role
	shared  map[string]bool
	wrong   map[[2]string]bool
}

// registeredPerson holds glossary keys; names start with the main one.
type registeredPerson struct {
	names []string
	roles []string
}

// newNameIndex decides what a registry leaves open: a main name is never shared and never another
// person's; any other name or role in Shared is shared; a name given twice stays with the first
// person, and a name beats a role.
func newNameIndex(registry *NameRegistry) *nameIndex {
	index := &nameIndex{of: map[string]int{}, shared: map[string]bool{}, wrong: map[[2]string]bool{}}
	if registry == nil {
		return index
	}
	for _, name := range registry.Shared {
		index.shared[glossaryKey(name)] = true
	}
	mainOf := make([]int, len(registry.Persons))
	for i, person := range registry.Persons {
		mainOf[i] = -1
		main := glossaryKey(person.Name)
		if main == "" {
			continue
		}
		delete(index.shared, main)
		at, taken := index.of[main]
		if !taken {
			at = len(index.persons)
			index.persons = append(index.persons, registeredPerson{names: []string{main}})
			index.of[main] = at
		}
		mainOf[i] = at
	}
	claim := func(person int, name string) bool {
		key := glossaryKey(name)
		if _, taken := index.of[key]; key == "" || taken || index.shared[key] {
			return false
		}
		index.of[key] = person
		return true
	}
	for i, person := range registry.Persons {
		for _, name := range person.Names {
			if mainOf[i] >= 0 && claim(mainOf[i], name) {
				target := &index.persons[mainOf[i]]
				target.names = append(target.names, glossaryKey(name))
			}
		}
	}
	for i, person := range registry.Persons {
		for _, role := range person.Roles {
			if mainOf[i] >= 0 && claim(mainOf[i], role) {
				target := &index.persons[mainOf[i]]
				target.roles = append(target.roles, glossaryKey(role))
			}
		}
	}
	for _, pair := range registry.Wrong {
		index.wrong[[2]string{glossaryKey(pair.Name), glossaryKey(pair.Alias)}] = true
	}
	return index
}

// allows reports whether a mention of the name may use the alias at all.
func (index *nameIndex) allows(nameKey, aliasKey string) bool {
	return !index.shared[aliasKey] && !index.wrong[[2]string{nameKey, aliasKey}]
}

// knows reports whether the registry says anything about the name.
func (index *nameIndex) knows(key string) bool {
	_, person := index.of[key]
	return person || index.shared[key]
}

// spellings counts how the mentions of the cards write the registered names.
func (index *nameIndex) spellings(cards map[int]*Card) map[string]map[string]int {
	found := map[string]map[string]int{}
	count := func(name string) {
		key := glossaryKey(name)
		if _, registered := index.of[key]; !registered {
			return
		}
		if found[key] == nil {
			found[key] = map[string]int{}
		}
		found[key][strings.TrimSpace(name)]++
	}
	for _, card := range cards {
		for _, group := range [][]Mention{card.Characters, card.Entities} {
			for _, mention := range group {
				count(mention.Name)
				for _, alias := range mention.Aliases {
					count(alias)
				}
			}
		}
	}
	return found
}

// shown splits a person's names by the given cards: the name of the entry and the aliases are
// spelled as the cards write them, most used first after the main name; a name the cards do not
// write stays a key only, so that a glossary of one book shows no later name and facts stated
// under it still reach the person.
func (person registeredPerson) shown(spellings map[string]map[string]int) (name string, aliases, keysOnly []string) {
	uses := func(key string) int {
		total := 0
		for _, count := range spellings[key] {
			total += count
		}
		return total
	}
	others := append([]string{}, person.names[1:]...)
	sort.SliceStable(others, func(i, j int) bool { return uses(others[i]) > uses(others[j]) })
	for _, key := range append([]string{person.names[0]}, others...) {
		switch spelled := mostFrequent(spellings[key]); {
		case spelled == "":
			keysOnly = append(keysOnly, key)
		case name == "":
			name = spelled
		default:
			aliases = append(aliases, spelled)
		}
	}
	return name, aliases, keysOnly
}
