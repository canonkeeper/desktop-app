package sagamemory

import "slices"

// The first questions of a saga (specs/2026-11-10-first-questions.md, UC1): an empty chat offers questions with the names of the
// author's book, built from the prepared map after the decisions of the author, without a model. A name stands as the map has it,
// in the nominative, so the words of a question do not depend on its gender or number.

// FirstQuestionKind says what a first question is about.
type FirstQuestionKind string

const (
	// FirstQuestionHero is of the hero of the map.
	FirstQuestionHero FirstQuestionKind = "hero"
	// FirstQuestionPair is of the hero and his strongest neighbor of the characters.
	FirstQuestionPair FirstQuestionKind = "pair"
	// FirstQuestionPlace is of the place of most chapters or, without places, of an item or a faction.
	FirstQuestionPlace FirstQuestionKind = "place"
)

// FirstQuestion is a question an empty chat offers, in the shape of the API.
type FirstQuestion struct {
	Kind FirstQuestionKind `json:"kind"`
	Text string            `json:"text"`
}

// FirstQuestions are the questions of a saga by its map (FQ1.1–FQ1.3), each only when the map has what it asks of: the hero, the
// hero with his neighbor of the most shared events (then changes, then name), and the place of the most chapters (then name) or,
// without places, an item or a faction. A map without a hero gives none. A record of the author the book does not know is never
// asked of: the book will not answer. The document is the one the author sees — what he took off it is gone (MapDocument.Hide).
func FirstQuestions(document *MapDocument) []FirstQuestion {
	questions := []FirstQuestion{}
	hero := -1
	for i, node := range document.Nodes {
		if document.Hero != nil && node.Key == *document.Hero && node.Category == string(MapCharacter) && !node.Material {
			hero = i
		}
	}
	if hero < 0 {
		return questions
	}
	name := document.Nodes[hero].Name
	questions = append(questions, FirstQuestion{Kind: FirstQuestionHero, Text: name + ": кто это и что важного происходит с этим персонажем?"})
	if neighbor := strongestNeighbor(document, hero); neighbor >= 0 {
		questions = append(questions, FirstQuestion{Kind: FirstQuestionPair, Text: name + " и " + document.Nodes[neighbor].Name + ": что их связывает?"})
	}
	if place := mostNamed(document, MapPlace); place >= 0 {
		questions = append(questions, FirstQuestion{Kind: FirstQuestionPlace, Text: "Где впервые упоминается " + document.Nodes[place].Name + " и что там происходит?"})
	} else if thing := mostNamed(document, MapItem, MapFaction); thing >= 0 {
		questions = append(questions, FirstQuestion{Kind: FirstQuestionPlace, Text: "Где впервые упоминается " + document.Nodes[thing].Name + "?"})
	}
	return questions
}

// strongestNeighbor is the character linked to the hero by the most shared events, then the most changes naming both, then the
// first name; -1 without one. Every link of the document is honest (R11.1).
func strongestNeighbor(document *MapDocument, hero int) int {
	best, bestEvents, bestChanges := -1, 0, 0
	for _, link := range document.Links {
		other := -1
		switch hero {
		case link.Source:
			other = link.Target
		case link.Target:
			other = link.Source
		}
		if other < 0 || document.Nodes[other].Category != string(MapCharacter) || document.Nodes[other].Material {
			continue
		}
		events, changes := len(link.Events), len(link.Changes)
		if best < 0 || events > bestEvents || events == bestEvents && (changes > bestChanges ||
			changes == bestChanges && document.Nodes[other].Name < document.Nodes[best].Name) {
			best, bestEvents, bestChanges = other, events, changes
		}
	}
	return best
}

// mostNamed is the node of the categories named in the most chapters, then the first name; -1 without one.
func mostNamed(document *MapDocument, categories ...MapCategory) int {
	best := -1
	for i, node := range document.Nodes {
		if node.Material || !slices.Contains(categories, MapCategory(node.Category)) {
			continue
		}
		if best < 0 || len(node.Chapters) > len(document.Nodes[best].Chapters) ||
			len(node.Chapters) == len(document.Nodes[best].Chapters) && node.Name < document.Nodes[best].Name {
			best = i
		}
	}
	return best
}
