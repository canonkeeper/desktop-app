// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

// ChaptersDiffer compares the chapters the notes of a book are to give with the chapters the import read of their text
// (OB4.2) and names the notes that do not give what they should: a note the import split — a line «Глава N», «Пролог»,
// «Эпилог» or «Интерлюдия» inside it starts a chapter — and a note whose chapter the import did not read or joined to the
// one before. Nothing is named when they agree.
func ChaptersDiffer(notes []GluedNote, parsed []string) []string {
	type expected struct{ path, title string }
	var titles []expected
	for _, note := range notes {
		for _, title := range note.Chapters {
			titles = append(titles, expected{path: note.Path, title: title})
		}
	}
	var named []string
	name := func(notePath string) {
		if notePath != "" && (len(named) == 0 || named[len(named)-1] != notePath) {
			for _, held := range named {
				if held == notePath {
					return
				}
			}
			named = append(named, notePath)
		}
	}
	next, last := 0, ""
	for _, chapter := range parsed {
		found := -1
		for index := next; index < len(titles); index++ {
			if sameTitle(chapter, titles[index].title) {
				found = index
				break
			}
		}
		if found < 0 {
			// A chapter no note names: the note before it was split — or, before the first chapter of a note, the note
			// whose text it came out of.
			if last == "" && next < len(titles) {
				name(titles[next].path)
			} else {
				name(last)
			}
			continue
		}
		for _, missed := range titles[next:found] {
			name(missed.path)
		}
		last, next = titles[found].path, found+1
	}
	for _, missed := range titles[next:] {
		name(missed.path)
	}
	return named
}
