package sagadraft

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/textface"
)

// A place of the working text (specs/2026-11-04-writing-plain-path.md, P5.5): a quote of it, whatever its spaces, or its
// first and last phrases, never cutting a block of the model's text apart. A quote is found through the marks of faces:
// in the text without them, its place given back in the bytes of the text with the marks at its edges outside it
// (specs/2026-11-08-italic-bold-in-text.md, TF2.2). A refusal names the field of the call it came from, so that the
// helper corrects its call in the same turn.

// HelperError names what of a call of the helper is refused, at a path of its arguments.
type HelperError struct {
	Path    string
	Message string
	// Quote says the refusal is a quote that is not in its text (W6.7 quote_not_found).
	Quote bool
}

func (helper *HelperError) Error() string { return helper.Path + ": " + helper.Message }

// Refused is a refusal at a path of the arguments of a call.
func Refused(path, message string, args ...any) error {
	return &HelperError{Path: path, Message: fmt.Sprintf(message, args...)}
}

// Scene names the scene of the working text a variant takes the place of (specs/2026-11-04-writing-plain-path.md P5.5):
// a quote of a short place, or the first and the last phrases of a scene of any length.
type Scene struct {
	Place string
	From  string
	To    string
}

// SpanIn finds the scene in the text: its place as a note holds it — without the marks of faces — and where it stands.
func (scene Scene) SpanIn(text string) (string, int, int, error) {
	place, from, to := strings.TrimSpace(scene.Place), strings.TrimSpace(scene.From), strings.TrimSpace(scene.To)
	place, from, to = WithoutFaces(place), WithoutFaces(from), WithoutFaces(to)
	switch {
	case place != "" && (from != "" || to != ""):
		return "", 0, 0, Refused("$.place", "назовите сцену одним способом: дословной цитатой place или первой и последней фразами from и to")
	case place != "":
		if !ValidRunes(place, MaxQuoteRunes) {
			return "", 0, 0, Refused("$.place", "длинную сцену назовите первой и последней фразами from и to")
		}
		found := QuotesIn(text, place)
		if len(found) == 0 {
			return "", 0, 0, &HelperError{Path: "$.place", Message: "этой сцены нет в тексте: перечитайте его вызовом draft.state и цитируйте дословно", Quote: true}
		}
		return place, found[0][0], found[0][1], marksAround("$.place", "$.place", text, found[0][0], found[0][1])
	case from == "" && to == "":
		return "", 0, 0, Refused("$.place", "назовите сцену: дословной цитатой place или первой и последней фразами from и to")
	case to == "":
		return "", 0, 0, Refused("$.to", "назовите и последнюю фразу сцены")
	case from == "":
		return "", 0, 0, Refused("$.from", "назовите и первую фразу сцены")
	case !ValidRunes(from, MaxQuoteRunes):
		return "", 0, 0, Refused("$.from", "from — одна первая фраза сцены, а не большой кусок текста")
	case !ValidRunes(to, MaxQuoteRunes):
		return "", 0, 0, Refused("$.to", "to — одна последняя фраза сцены, а не большой кусок текста")
	}
	starts := QuotesIn(text, from)
	if len(starts) == 0 {
		return "", 0, 0, &HelperError{Path: "$.from", Message: "такого начала нет в тексте: перечитайте его вызовом draft.state и цитируйте дословно", Quote: true}
	}
	if len(starts) > 1 {
		return "", 0, 0, Refused("$.from", "это начало стоит в тексте %d раз: возьмите фразу длиннее, чтобы она была одна", len(starts))
	}
	start, startEnd := starts[0][0], starts[0][1]
	var ends []int
	for _, found := range QuotesIn(text[start:], to) {
		if start+found[1] >= startEnd {
			ends = append(ends, start+found[1])
		}
	}
	if len(ends) == 0 {
		return "", 0, 0, &HelperError{Path: "$.to", Message: "после начала сцены такого конца нет: перечитайте текст вызовом draft.state и цитируйте дословно", Quote: true}
	}
	if len(ends) > 1 {
		return "", 0, 0, Refused("$.to", "этот конец стоит после начала сцены %d раз: возьмите фразу длиннее, чтобы она была одна", len(ends))
	}
	return WithoutFaces(text[start:ends[0]]), start, ends[0], marksAround("$.from", "$.to", text, start, ends[0])
}

// marksAround refuses a place one of whose ends is inside a block of the helper and the other outside it: the
// replacement would leave a mark without its pair. A whole block inside the place, or the place inside a block, are fine.
func marksAround(fromPath, toPath, text string, start, end int) error {
	inside := func(at int) bool {
		return strings.LastIndex(text[:at], string(MarkOpen)) > strings.LastIndex(text[:at], string(MarkClose))
	}
	switch startInside, endInside := inside(start), inside(end); {
	case startInside && !endInside:
		return Refused(fromPath, "начало сцены стоит внутри дописанного, а конец снаружи: начните сцену с начала дописанного или кончите её внутри него")
	case !startInside && endInside:
		return Refused(toPath, "конец сцены стоит внутри дописанного, а начало снаружи: кончите сцену концом дописанного или начните её внутри него")
	case startInside && endInside && strings.ContainsRune(text[start:end], MarkClose):
		return Refused(fromPath, "сцена начинается в одном дописанном блоке и кончается в другом: назовите её внутри одного блока или целиком")
	}
	return nil
}

// Contains says the quote stands in the text after the spaces of both are made one.
func Contains(text, quote string) bool {
	return len(QuotesIn(text, quote)) > 0
}

// QuotesIn finds a quote in a text, each place once, whatever spaces stand between its words: a place of the size of a
// chapter, in a pasted text too, is found again as the note holds it (specs/2026-11-04-writing-plain-path.md P5.5). The
// marks of faces in the text and in the quote are not read; a place stands in the bytes of the text, the marks of faces
// at its edges outside it (TF2.2).
func QuotesIn(text, quote string) [][2]int {
	view := textface.Faceless(text)
	places := quotesIn(view.Text, WithoutFaces(quote))
	for i, place := range places {
		places[i][0], places[i][1] = view.Range(place[0], place[1])
	}
	return places
}

func quotesIn(text, quote string) [][2]int {
	words := strings.Fields(quote)
	if len(words) == 0 {
		return nil
	}
	var places [][2]int
	for from := 0; from <= len(text); {
		at := strings.Index(text[from:], words[0])
		if at < 0 {
			return places
		}
		start := from + at
		if end, whole := wordsFrom(text, start+len(words[0]), words[1:]); whole {
			places = append(places, [2]int{start, end})
			from = end
		} else {
			from = start + 1
		}
	}
	return places
}

// wordsFrom says the words stand in the text from an offset, each after its spaces, and where they end.
func wordsFrom(text string, end int, words []string) (int, bool) {
	for _, word := range words {
		rest := strings.TrimLeftFunc(text[end:], unicode.IsSpace)
		if len(rest) == len(text)-end || !strings.HasPrefix(rest, word) {
			return 0, false
		}
		end = len(text) - len(rest) + len(word)
	}
	return end, true
}

// ValidRunes says a text is not empty, is valid UTF-8 and has at most limit runes.
func ValidRunes(text string, limit int) bool {
	return text != "" && utf8.ValidString(text) && utf8.RuneCountInString(text) <= limit
}

// Span finds a place of the working text, by a quote or by its first and last phrases: its start and end.
func Span(text, quote, from, to string) (int, int, error) {
	_, start, end, err := Scene{Place: quote, From: from, To: to}.SpanIn(text)
	return start, end, err
}
