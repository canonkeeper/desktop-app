package sagamemory

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Normalize prepares text for matching: lower case, "ё" as "е", stress marks removed.
// Only the combining acute and grave accents are dropped so that "й" keeps its breve.
func Normalize(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, r := range text {
		switch r {
		case '́', '̀':
			continue
		case 'ё', 'Ё':
			builder.WriteRune('е')
			continue
		}
		builder.WriteRune(unicode.ToLower(r))
	}
	return builder.String()
}

// Token is one word of normalized text with its word position.
type Token struct {
	Word string
	Pos  int
}

// Tokenize splits normalized text into words. A hyphenated word yields its parts and
// the joined form at the same position, so "мини-босс" also matches "минибосс".
func Tokenize(norm string) []Token {
	tokens := make([]Token, 0, len(norm)/6)
	position := 0
	var word []rune
	var parts []string
	flush := func() {
		if len(word) > 0 {
			parts = append(parts, string(word))
			word = word[:0]
		}
	}
	emit := func() {
		flush()
		if len(parts) == 0 {
			return
		}
		for _, part := range parts {
			tokens = append(tokens, Token{Word: part, Pos: position})
		}
		if len(parts) > 1 {
			tokens = append(tokens, Token{Word: strings.Join(parts, ""), Pos: position})
		}
		parts = parts[:0]
		position++
	}
	runes := []rune(norm)
	for index, r := range runes {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word = append(word, r)
		case r == '-' && len(word) > 0 && index+1 < len(runes) && unicode.IsLetter(runes[index+1]):
			flush()
		default:
			emit()
		}
	}
	emit()
	return tokens
}

// wordHyphen reports whether the hyphen at byte offset i joins two parts of one word.
func wordHyphen(text string, i int) bool {
	before, _ := utf8.DecodeLastRuneInString(text[:i])
	after, _ := utf8.DecodeRuneInString(text[i+1:])
	return (unicode.IsLetter(before) || unicode.IsDigit(before)) && unicode.IsLetter(after)
}

// joinWordHyphens drops the hyphens inside words: "мини-контроллер" becomes "миниконтроллер".
func joinWordHyphens(text string) string {
	if !strings.Contains(text, "-") {
		return text
	}
	var builder strings.Builder
	builder.Grow(len(text))
	for i, r := range text {
		if r == '-' && wordHyphen(text, i) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

// countIgnoringWordHyphens counts a needle without word hyphens in text whose words may be
// hyphenated, and returns the first matched span of the text.
func countIgnoringWordHyphens(text, needle string) (int, string) {
	if !strings.Contains(text, "-") {
		return strings.Count(text, needle), needle
	}
	count, first := 0, ""
	_, firstSize := utf8.DecodeRuneInString(needle)
	for offset := 0; offset < len(text); {
		at := strings.Index(text[offset:], needle[:firstSize])
		if at < 0 {
			break
		}
		start := offset + at
		end, ok := matchIgnoringWordHyphens(text, start, needle)
		if !ok {
			offset = start + firstSize
			continue
		}
		if count == 0 {
			first = text[start:end]
		}
		count++
		offset = end
	}
	return count, first
}

func matchIgnoringWordHyphens(text string, start int, needle string) (int, bool) {
	i := start
	for j := 0; j < len(needle); {
		if i >= len(text) {
			return 0, false
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		want, wantSize := utf8.DecodeRuneInString(needle[j:])
		switch {
		case r == want:
			i, j = i+size, j+wantSize
		case r == '-' && j > 0 && wordHyphen(text, i):
			i += size
		default:
			return 0, false
		}
	}
	return i, true
}
