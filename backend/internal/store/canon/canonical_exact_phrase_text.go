package canonstore

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type canonicalExactRawSpan struct{ start, end uint32 }

type canonicalExactNormalizedText struct {
	value string
	spans []canonicalExactRawSpan
}

func normalizeCanonicalExactText(content string, base uint32) canonicalExactNormalizedText {
	result := canonicalExactNormalizedText{spans: make([]canonicalExactRawSpan, 0, len(content))}
	var normalized strings.Builder
	normalized.Grow(len(content))
	var iterator norm.Iter
	iterator.InitString(norm.NFC, content)
	fold := cases.Fold()
	var pendingWhitespace *canonicalExactRawSpan
	appendValue := func(value string, span canonicalExactRawSpan) {
		normalized.WriteString(value)
		for range len(value) {
			result.spans = append(result.spans, span)
		}
	}
	emitRune := func(runeValue rune, span canonicalExactRawSpan) {
		if unicode.IsSpace(runeValue) {
			if pendingWhitespace == nil {
				copy := span
				pendingWhitespace = &copy
			} else {
				pendingWhitespace.end = span.end
			}
			return
		}
		if pendingWhitespace != nil && normalized.Len() != 0 {
			appendValue(" ", *pendingWhitespace)
		}
		pendingWhitespace = nil
		appendValue(string(runeValue), span)
	}
	for !iterator.Done() {
		start := base + uint32(iterator.Pos())
		segment := iterator.Next()
		span := canonicalExactRawSpan{start: start, end: base + uint32(iterator.Pos())}
		if len(segment) == 1 && segment[0] < utf8.RuneSelf {
			value := rune(segment[0])
			if value >= 'A' && value <= 'Z' {
				value += 'a' - 'A'
			}
			emitRune(value, span)
			continue
		}
		for _, runeValue := range fold.String(string(segment)) {
			emitRune(runeValue, span)
		}
	}
	result.value = normalized.String()
	return result
}
