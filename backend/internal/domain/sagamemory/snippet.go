package sagamemory

import (
	"strings"
	"unicode/utf8"
)

// Snippet cuts a window of the original paragraph text around the first matched word.
func (index *Index) Snippet(hit Hit, width int) string {
	_, paragraph := index.Locate(hit.Para)
	original := []rune(paragraph.Text)
	if len(original) <= width {
		return paragraph.Text
	}
	normToOrig := make([]int, 0, len(original))
	for i, r := range original {
		if r == '́' || r == '̀' {
			continue
		}
		normToOrig = append(normToOrig, i)
	}
	best := -1
	for _, word := range hit.words {
		if word == "" {
			continue
		}
		if at := strings.Index(paragraph.Norm, word); at >= 0 {
			runeAt := utf8.RuneCountInString(paragraph.Norm[:at])
			if best < 0 || runeAt < best {
				best = runeAt
			}
		}
	}
	center := 0
	if best >= 0 && best < len(normToOrig) {
		center = normToOrig[best]
	}
	start := max(0, center-width/3)
	end := min(len(original), start+width)
	start = max(0, end-width)
	text := strings.TrimSpace(string(original[start:end]))
	if start > 0 {
		text = "…" + text
	}
	if end < len(original) {
		text += "…"
	}
	return text
}
